package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ProjectAILeap/ompinyin/internal/catalog"
)

func TestStateRoundtrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMPINYIN_TEST_HOME", home)

	s := New()
	s.Desired.Primary = "flypy"
	s.Desired.Extra = []string{"quanpin"}
	s.SchemaList = []string{"double_pinyin_flypy", "rime_ice"}
	s.Assets["wanxiang"] = AssetRecord{Tag: "LTS", SHA256: "abc123"}
	s.ManagedFiles["default.custom.yaml"] = "deadbeef"

	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(Path()); err != nil {
		t.Fatalf("state.json not written: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Desired.Primary != "flypy" || len(loaded.Desired.Extra) != 1 {
		t.Errorf("desired roundtrip failed: %+v", loaded.Desired)
	}
	if loaded.Assets["wanxiang"].Tag != "LTS" {
		t.Errorf("assets roundtrip failed: %+v", loaded.Assets)
	}
	if loaded.ManagedFiles["default.custom.yaml"] != "deadbeef" {
		t.Errorf("ledger roundtrip failed: %+v", loaded.ManagedFiles)
	}
}

func TestLoadMissingFileYieldsDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMPINYIN_TEST_HOME", home)
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if s.Desired.Primary != "quanpin" || !s.Desired.Model {
		t.Errorf("default desired wrong: %+v", s.Desired)
	}
}

func TestAtomicWriteLeavesNoTemp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMPINYIN_TEST_HOME", home)
	p := filepath.Join(Dir(), "sub", "f.txt")
	if err := WriteAtomic(p, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	for _, e := range entries {
		if len(e.Name()) > 1 && e.Name()[0] == '.' && e.Name()[1] == 'o' {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestLockExclusionAndStaleBreak(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMPINYIN_TEST_HOME", home)

	l1, err := Acquire()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(); err == nil {
		t.Fatal("second lock must fail")
	}
	l1.Release()
	if _, err := Acquire(); err != nil {
		t.Fatalf("lock must be reusable after release: %v", err)
	}
}

func TestLockStaleBroken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMPINYIN_TEST_HOME", home)
	os.MkdirAll(Dir(), 0o755)
	// a pid that certainly does not exist
	os.WriteFile(filepath.Join(Dir(), "lock"), []byte("999999999\n"), 0o644)
	l, err := Acquire()
	if err != nil {
		t.Fatalf("stale lock must be broken: %v", err)
	}
	l.Release()
}

// TestSaveLedgerKeepsDesired locks 评审 P0-5: the ownership/asset facts may be
// persisted mid-run, but the terminal state must only advance on success.
func TestSaveLedgerKeepsDesired(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMPINYIN_TEST_HOME", home)

	s := New()
	s.Desired = catalog.DefaultDesired()
	s.diskDesired = s.Desired
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	// a run moves Desired forward and writes managed bytes
	s.Desired = catalog.Desired{Primary: "flypy", Extra: []string{"quanpin"}, Model: true, Channel: "nightly"}
	s.SchemaList = []string{"double_pinyin_flypy", "rime_ice"}
	s.ManagedFiles["default.custom.yaml"] = "hash1"
	s.Assets["rime_ice"] = AssetRecord{Tag: "2026.06.30", SHA256: "sha1"}
	if err := s.SaveLedger(); err != nil {
		t.Fatal(err)
	}

	onDisk, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.ManagedFiles["default.custom.yaml"] != "hash1" {
		t.Error("SaveLedger did not persist the ownership ledger")
	}
	if onDisk.Assets["rime_ice"].Tag != "2026.06.30" {
		t.Error("SaveLedger did not persist the asset record")
	}
	if onDisk.Desired.Primary != "quanpin" || len(onDisk.SchemaList) != 0 {
		t.Errorf("SaveLedger advanced the terminal state: %+v schema_list=%v", onDisk.Desired, onDisk.SchemaList)
	}
	// the in-memory desired survives the swap-back for the caller
	if s.Desired.Primary != "flypy" {
		t.Errorf("SaveLedger clobbered the caller's in-memory desired: %+v", s.Desired)
	}
}

// TestPruneBackups keeps only the newest N backup dirs (names sort by time).
func TestPruneBackups(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMPINYIN_TEST_HOME", home)
	names := []string{
		"backup-20260101-000001", "backup-20260102-000002", "backup-20260103-000003",
		"backup-20260104-000004", "backup-20260105-000005", "backup-20260106-000006",
	}
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(Dir(), n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := PruneBackups(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 3 {
		t.Fatalf("removed = %v, want 3", removed)
	}
	for _, n := range names[:3] {
		if _, err := os.Stat(filepath.Join(Dir(), n)); !os.IsNotExist(err) {
			t.Errorf("oldest backup %s survived pruning", n)
		}
	}
	for _, n := range names[3:] {
		if _, err := os.Stat(filepath.Join(Dir(), n)); err != nil {
			t.Errorf("newest backup %s was removed: %v", n, err)
		}
	}
}

// TestHomeNeverExits: a getter that calls os.Exit would skip the deferred
// instance-lock release and defeat the §7 exit-code contract (评审 新增#3).
func TestHomeNeverExits(t *testing.T) {
	t.Setenv("OMPINYIN_TEST_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	if h := Home(); h != "" {
		t.Errorf("Home() = %q, want \"\" when undeterminable", h)
	}
	if d := Dir(); d != "" {
		t.Errorf("Dir() = %q, want \"\" when there is no home", d)
	}
	if err := CheckHome(); err == nil {
		t.Error("CheckHome must report the missing home")
	}
	if _, err := Acquire(); err == nil {
		t.Error("Acquire must refuse without a writable state dir")
	}
}
