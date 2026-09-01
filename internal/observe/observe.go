// Package observe collects the current host state cheaply so that
// plan.Diff can compare desired vs current (§3).
package observe

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ProjectAILeap/ompinyin/internal/assets"
	"github.com/ProjectAILeap/ompinyin/internal/catalog"
	"github.com/ProjectAILeap/ompinyin/internal/deploy"
	"github.com/ProjectAILeap/ompinyin/internal/hotkey"
	"github.com/ProjectAILeap/ompinyin/internal/patches"
	"github.com/ProjectAILeap/ompinyin/internal/pkgs"
	"github.com/ProjectAILeap/ompinyin/internal/service"
	"github.com/ProjectAILeap/ompinyin/internal/state"
	"github.com/ProjectAILeap/ompinyin/internal/tray"
)

// DataDir is ~/.local/share/fcitx5/rime (the only rime dir we write, §6.5).
func DataDir() string {
	if th := os.Getenv("OMPINYIN_TEST_HOME"); th != "" {
		return filepath.Join(th, ".local", "share", "fcitx5", "rime")
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		base = filepath.Join(state.Home(), ".local", "share")
	}
	return filepath.Join(base, "fcitx5", "rime")
}

// FcitxConfigDir is ~/.config/fcitx5.
func FcitxConfigDir() string {
	return filepath.Join(state.Home(), ".config", "fcitx5")
}

// ProfilePath is ~/.config/fcitx5/profile.
func ProfilePath() string { return filepath.Join(FcitxConfigDir(), "profile") }

// ConfigPath is ~/.config/fcitx5/config.
func ConfigPath() string { return filepath.Join(FcitxConfigDir(), "config") }

// Current is the observed snapshot.
type Current struct {
	PackagesMissing []string
	Unit            string
	ServiceActive   bool

	RimeDir string

	// L3 managed file classification (keyed by RelPath):
	// Status = ownership class; ContentEqual = disk already matches the
	// desired generated content (rewriting unnecessary).
	Managed      map[string]patches.Status
	ContentEqual map[string]bool
	// Orphans are ledger-recorded managed files the desired state no longer
	// includes (previous layout's grammar, Model=false).
	Orphans []string

	GramFileExists bool // wanxiang .gram in place
	RimeDataExists bool // rime-ice data dir populated (anchor files present)

	ProfileHasRime bool
	HotkeyOK       bool
	// DropInExists is the file presence; DropInOK additionally requires the
	// content to really enable notificationitem for the DISCOVERED unit.
	DropInExists bool
	DropInOK     bool
	DropInPath   string

	Pinned       []string
	PinnedHasFc  bool
	ShellRunning bool

	BuildMissing []string

	LegacyDirExists bool // ~/.config/fcitx/rime (§6.5)
}

// Collect probes the host. Best-effort: probe errors are surfaced as
// "absent" states; the convergence run re-checks with hard errors.
func Collect(d catalog.Desired, st *state.State) *Current {
	c := &Current{RimeDir: DataDir(), Managed: map[string]patches.Status{}, ContentEqual: map[string]bool{}}

	missing, err := pkgs.Missing(pkgs.Needed...)
	if err == nil {
		c.PackagesMissing = missing
	}
	c.Unit = service.FindUnit(state.Home())
	c.ServiceActive = c.Unit != "" && service.IsActive(c.Unit)

	// L2 assets
	// RimeDataExists requires BOTH anchor files so a nested/wrong-layout
	// extraction (upstream zip gains a top-level dir someday) is detected
	// instead of silently reported as "data in place" (P1-2b).
	c.RimeDataExists = probeAll(filepath.Join(c.RimeDir, "default.yaml"),
		filepath.Join(c.RimeDir, "rime_ice.schema.yaml"))
	if _, err := os.Stat(filepath.Join(c.RimeDir, catalog.GrammarLanguage+".gram")); err == nil {
		c.GramFileExists = true
	}

	// L3
	for _, f := range patches.ManagedFiles(d) {
		abs := filepath.Join(c.RimeDir, f.RelPath)
		var ledger string
		if st != nil {
			ledger = st.ManagedFiles[filepath.Base(f.RelPath)]
		}
		c.Managed[f.RelPath] = patches.Classify(abs, ledger)
		if b, err := os.ReadFile(abs); err == nil {
			c.ContentEqual[f.RelPath] = string(b) == f.Content
		} else {
			c.ContentEqual[f.RelPath] = false
		}
	}
	if st != nil {
		c.Orphans = patches.OrphanFiles(st, d)
	}

	// L4
	if b, err := os.ReadFile(ProfilePath()); err == nil {
		c.ProfileHasRime = profileHasRime(string(b))
	}
	if b, err := os.ReadFile(ConfigPath()); err == nil {
		c.HotkeyOK = hotkey.HasTrigger(string(b), hotkey.DefaultKeys)
	}
	c.DropInPath = tray.DropInPath(state.Home(), c.Unit)
	if b, err := os.ReadFile(c.DropInPath); err == nil {
		c.DropInExists = true
		c.DropInOK = tray.DropInEnabled(string(b))
	}
	if b, err := os.ReadFile(tray.ShellJSONPath(state.Home())); err == nil {
		if pinned, perr := tray.ReadPinned(b); perr == nil {
			c.Pinned = pinned
			c.PinnedHasFc = tray.HasPin(pinned)
		}
	}
	c.ShellRunning = tray.ShellRunning()

	// build artifacts (only meaningful when data dir exists)
	if _, err := os.Stat(c.RimeDir); err == nil {
		c.BuildMissing = deploy.BuildArtifactsExist(c.RimeDir, d.SchemaList())
	}

	if _, err := os.Stat(filepath.Join(state.Home(), ".config", "fcitx", "rime")); err == nil {
		c.LegacyDirExists = true
	}
	return c
}

func profileHasRime(content string) bool {
	for _, name := range strings.FieldsFunc(content, func(r rune) bool { return r == '\n' }) {
		if strings.TrimSpace(name) == "Name=rime" {
			return true
		}
	}
	return false
}

// probeAll reports whether every path exists.
func probeAll(paths ...string) bool {
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			return false
		}
	}
	return true
}

// CacheDir exposes the asset cache for clean/status.
func CacheDir() string { return assets.CacheDir() }
