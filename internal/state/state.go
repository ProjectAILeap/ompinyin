// Package state implements the persistent state ledger (~/.local/state/
// ompinyin/state.json), the instance lock, and atomic file writes (§8).
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ProjectAILeap/ompinyin/internal/catalog"
)

// StateVersion is the schema version of state.json. Bump it together with a
// migration in Load; an unknown NEWER version is refused rather than silently
// rewritten (which would drop fields a newer tool depends on).
const StateVersion = 1

// AssetRecord pins the resolved version/tag and checksum of one asset.
type AssetRecord struct {
	Tag    string `json:"tag,omitempty"`
	SHA256 string `json:"sha256"`
}

// State is the on-disk ledger.
type State struct {
	Version      int                    `json:"version"`
	Desired      catalog.Desired        `json:"desired"`
	SchemaList   []string               `json:"schema_list"`
	Assets       map[string]AssetRecord `json:"assets"`
	ManagedFiles map[string]string      `json:"managed_files"` // relpath -> sha256 of last tool write
	UpdatedAt    time.Time              `json:"updated_at"`

	// diskDesired/diskSchemaList mirror what is currently persisted. They are
	// not serialized. SaveLedger writes the ledger facts WITHOUT advancing the
	// desired state, so a convergence that fails half-way still remembers which
	// bytes it owns (otherwise the next run would classify its own files as
	// "外来" and ask to overwrite them — 评审 P0-5).
	diskDesired    catalog.Desired
	diskSchemaList []string
}

// LedgerKeepBackups is how many backup-<ts>/ directories are retained.
const LedgerKeepBackups = 5

// New returns an empty ledger.
func New() *State {
	return &State{
		Version:      StateVersion,
		Assets:       map[string]AssetRecord{},
		ManagedFiles: map[string]string{},
	}
}

// ErrNoHome is returned when the user home directory cannot be determined.
var ErrNoHome = errors.New("ompinyin: cannot determine home directory (set $HOME or $OMPINYIN_TEST_HOME)")

// Home returns the user home directory (respects $OMPINYIN_TEST_HOME for
// tests), or "" when it cannot be determined. It never terminates the process:
// a getter that calls os.Exit would skip the deferred instance-lock release and
// defeat the exit-code contract (§7). Callers that must have a home check
// CheckHome() once at the CLI boundary.
func Home() string {
	if h := os.Getenv("OMPINYIN_TEST_HOME"); h != "" {
		return h
	}
	h, err := os.UserHomeDir()
	if err != nil || h == "" || h == "/" {
		return ""
	}
	return h
}

// CheckHome validates the home/XDG roots up front (exit 3 material).
func CheckHome() error {
	if Home() == "" {
		return ErrNoHome
	}
	if Dir() == "" {
		return ErrNoHome
	}
	return nil
}

// Dir is ~/.local/state/ompinyin (respects XDG_STATE_HOME; OMPINYIN_TEST_HOME
// takes precedence for hermetic tests). Returns "" when no usable root exists —
// an undeterminable home must not silently redirect every write under "/".
func Dir() string {
	if th := os.Getenv("OMPINYIN_TEST_HOME"); th != "" {
		return filepath.Join(th, ".local", "state", "ompinyin")
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		h := Home()
		if h == "" {
			return ""
		}
		base = filepath.Join(h, ".local", "state")
	}
	return filepath.Join(base, "ompinyin")
}

// Path is the state.json path.
func Path() string {
	d := Dir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "state.json")
}

// Load reads the ledger; a missing file yields a fresh ledger with the
// default desired state and nil error.
func Load() (*State, error) {
	if Path() == "" {
		return nil, ErrNoHome
	}
	s := New()
	b, err := os.ReadFile(Path())
	if err != nil {
		if os.IsNotExist(err) {
			s.Desired = catalog.DefaultDesired()
			s.diskDesired = s.Desired
			return s, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("state.json corrupt: %w", err)
	}
	if s.Version > StateVersion {
		return nil, fmt.Errorf("state.json was written by ompinyin with a newer ledger schema (v%d > v%d); upgrade ompinyin before converging",
			s.Version, StateVersion)
	}
	if s.Version < StateVersion {
		// migrations run in order here; none exist yet at v1, so the ledger is
		// simply upgraded on the next Save
		s.Version = StateVersion
	}
	if s.Assets == nil {
		s.Assets = map[string]AssetRecord{}
	}
	if s.ManagedFiles == nil {
		s.ManagedFiles = map[string]string{}
	}
	s.diskDesired = s.Desired
	s.diskSchemaList = append([]string{}, s.SchemaList...)
	return s, nil
}

// Save atomically persists the whole ledger, including the desired terminal
// state. Only a completed convergence calls it.
func (s *State) Save() error {
	s.UpdatedAt = time.Now().UTC()
	if err := s.write(); err != nil {
		return err
	}
	s.diskDesired = s.Desired
	s.diskSchemaList = append([]string{}, s.SchemaList...)
	return nil
}

// SaveLedger persists the ownership/asset facts (ManagedFiles, Assets) while
// leaving the recorded terminal state at whatever was last achieved. Call it
// right after a layer that put bytes on disk: if a later layer fails, the next
// run still recognises its own files instead of classifying them as 外来
// (评审 P0-5).
func (s *State) SaveLedger() error {
	savedDesired, savedList := s.Desired, s.SchemaList
	s.Desired = s.diskDesired
	s.SchemaList = s.diskSchemaList
	err := s.Save()
	s.Desired, s.SchemaList = savedDesired, savedList
	return err
}

func (s *State) write() error {
	dir := Dir()
	if dir == "" {
		return ErrNoHome
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return WriteAtomic(Path(), b)
}

// Remove deletes state.json (uninstall).
func Remove() error {
	p := Path()
	if p == "" {
		return ErrNoHome
	}
	err := os.Remove(p)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// PruneBackups keeps only the newest keep backup-<ts>/ directories (names sort
// chronologically) and returns the removed ones. Backups are taken on every
// convergence that writes, so without rotation a -b full backup (~1GB) would
// pile up forever (评审 P2).
func PruneBackups(keep int) ([]string, error) {
	dir := Dir()
	if dir == "" || keep <= 0 {
		return nil, nil
	}
	matches, err := filepath.Glob(filepath.Join(dir, "backup-*"))
	if err != nil || len(matches) <= keep {
		return nil, err
	}
	sort.Strings(matches) // backup-YYYYMMDD-HHMMSS[-n] sorts chronologically
	var removed []string
	for _, old := range matches[:len(matches)-keep] {
		if err := os.RemoveAll(old); err != nil {
			return removed, err
		}
		removed = append(removed, old)
	}
	return removed, nil
}

// ---------------------------------------------------------------------------
// Lock (§8)
// ---------------------------------------------------------------------------

// Lock is the single-instance lock guarding the stop/start window.
type Lock struct {
	path string
}

// Acquire creates the lock file exclusively. A stale lock (dead pid) is
// broken automatically.
func Acquire() (*Lock, error) {
	dir := Dir()
	if dir == "" {
		return nil, ErrNoHome
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "lock")
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
			return &Lock{path: path}, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		// Stale? If the recorded pid is not alive, break it.
		if b, err := os.ReadFile(path); err == nil {
			pid, perr := strconv.Atoi(strings.TrimSpace(string(b)))
			if perr == nil && !pidAlive(pid) {
				os.Remove(path)
				continue
			}
		}
		return nil, fmt.Errorf("another ompinyin instance is running (lock: %s)", path)
	}
	return nil, fmt.Errorf("cannot acquire lock %s", path)
}

// Release removes the lock.
func (l *Lock) Release() {
	if l == nil {
		return
	}
	os.Remove(l.path)
}

func pidAlive(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	return err == nil && len(b) > 0
}

// ---------------------------------------------------------------------------
// Atomic write + hashing (§8)
// ---------------------------------------------------------------------------

// WriteAtomic writes b to path via a temp file + rename.
func WriteAtomic(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".ompinyin-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// HashFile returns the sha256 hex of the file at path, or "" if missing.
func HashFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return HashBytes(b)
}

// HashBytes returns the sha256 hex of b.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
