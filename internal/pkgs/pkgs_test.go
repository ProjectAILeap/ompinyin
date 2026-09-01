package pkgs

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// TestInstalledUsesExitStatus documents the contract the implementation
// relies on: `pacman -Qq a b c` exits 0 IFF every name is installed, so the
// exit status alone answers the question (the old code fabricated "output" by
// echoing the args back, which read like a parsing bug).
func TestInstalledUsesExitStatus(t *testing.T) {
	orig := Run
	defer func() { Run = orig }()

	var queries [][]string
	Run = func(name string, args ...string) error {
		queries = append(queries, append([]string{name}, args...))
		return nil // everything installed
	}
	inst, err := Installed("fcitx5", "fcitx5-rime")
	if err != nil {
		t.Fatal(err)
	}
	if !inst["fcitx5"] || !inst["fcitx5-rime"] {
		t.Errorf("all installed: %v", inst)
	}
	if len(queries) != 1 {
		t.Errorf("want one batched query, got %d", len(queries))
	}

	// partial install → the batched query fails, per-package fallback decides
	// each one. Model real pacman semantics: -Qq exits non-zero if ANY queried
	// name is absent.
	installed := map[string]bool{"fcitx5": true, "opencc": true}
	Run = func(name string, args ...string) error {
		queries = append(queries, append([]string{name}, args...))
		for _, a := range args[1:] {
			if !installed[a] {
				return errors.New("package not found: " + a)
			}
		}
		return nil
	}
	missing, err := Missing("fcitx5", "fcitx5-rime", "opencc")
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || missing[0] != "fcitx5-rime" {
		t.Errorf("missing = %v, want [fcitx5-rime]", missing)
	}
	if len(queries) < 2 {
		t.Errorf("failed batch query must be followed by per-package probes, got %d calls", len(queries))
	}
}

// TestInstallRefreshesSyncDBOnlyWhenMissing: the ISO ships without sync DBs,
// and `pacman -S` then fails with "target not found"; refreshing only when the
// repos the L1 closure actually needs (core+extra) are absent keeps a real
// mirror problem from being masked behind a slow -Sy.
func TestInstallRefreshesSyncDBOnlyWhenMissing(t *testing.T) {
	origRun, origInteractive, origDBs := Run, runInteractive, syncDBPaths
	defer func() { Run, runInteractive, syncDBPaths = origRun, origInteractive, origDBs }()

	needed := []string{t.TempDir() + "/core.db", t.TempDir() + "/extra.db"}
	present := []string{t.TempDir() + "/core.db"}
	if err := writeStub(present[0]); err != nil {
		t.Fatal(err)
	}

	var calls []string
	runInteractive = func(name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if strings.Contains(strings.Join(args, " "), "-Sy") {
			// simulate a successful sync of whichever DBs are configured
			for _, p := range syncDBPaths {
				_ = os.WriteFile(p, []byte("stub db"), 0o644)
			}
		}
		return nil
	}

	syncDBPaths = needed
	if err := Install([]string{"fcitx5"}, true); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || !strings.Contains(calls[0], "-Sy") || !strings.Contains(calls[1], "-S --needed --noconfirm fcitx5") {
		t.Errorf("absent DB must be refreshed first, then install: %v", calls)
	}

	calls = nil
	syncDBPaths = present
	if err := Install([]string{"fcitx5", "opencc"}, false); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || strings.Contains(calls[0], "-Sy") || strings.Contains(calls[0], "--noconfirm") {
		t.Errorf("present DB must not trigger -Sy; --noconfirm only with --yes: %v", calls)
	}

	// no names → no pacman invocation at all
	calls = nil
	if err := Install(nil, true); err != nil || len(calls) != 0 {
		t.Errorf("empty install should be a no-op: %v %v", calls, err)
	}
}

// TestInstallErrorCarriesManualCommand: L1 runs interactively (sudo prompt),
// so the failure message must hand the user the exact command to re-run.
func TestInstallErrorCarriesManualCommand(t *testing.T) {
	origInteractive, origDBs := runInteractive, syncDBPaths
	defer func() { runInteractive, syncDBPaths = origInteractive, origDBs }()
	db := t.TempDir() + "/core.db"
	syncDBPaths = []string{db}
	if err := writeStub(db); err != nil {
		t.Fatal(err)
	}
	runInteractive = func(name string, args ...string) error { return errors.New("exit status 1") }
	err := Install([]string{"fcitx5-rime"}, true)
	if err == nil {
		t.Fatal("must report the pacman failure")
	}
	if !strings.Contains(err.Error(), "sudo pacman -S --needed fcitx5-rime") {
		t.Errorf("error must carry the manual command: %v", err)
	}
}

// TestInstallToleratesUnrelatedRepoSyncFailure: a `pacman -Sy` that exits
// non-zero because an unrelated repo (multilib / [omarchy]) failed must NOT
// block L1, as long as the DBs the closure needs (core+extra) got written.
func TestInstallToleratesUnrelatedRepoSyncFailure(t *testing.T) {
	origInteractive, origDBs := runInteractive, syncDBPaths
	defer func() { runInteractive, syncDBPaths = origInteractive, origDBs }()
	syncDBPaths = []string{t.TempDir() + "/core.db", t.TempDir() + "/extra.db"}

	runInteractive = func(name string, args ...string) error {
		if strings.Contains(strings.Join(args, " "), "-Sy") {
			// -Sy "failed" (non-zero) but the needed DBs got written anyway
			for _, p := range syncDBPaths {
				_ = os.WriteFile(p, []byte("stub db"), 0o644)
			}
			return errors.New("exit status 1: multilib failed")
		}
		return nil
	}
	if err := Install([]string{"fcitx5-rime"}, true); err != nil {
		t.Fatalf("unrelated sync failure must not block L1: %v", err)
	}
}

// TestInstallFailsWhenNeededDBsStillMissing: if core/extra cannot be synced at
// all (a real mirror problem), L1 must fail — a genuine fault must not be hidden.
func TestInstallFailsWhenNeededDBsStillMissing(t *testing.T) {
	origInteractive, origDBs := runInteractive, syncDBPaths
	defer func() { runInteractive, syncDBPaths = origInteractive, origDBs }()
	syncDBPaths = []string{t.TempDir() + "/core.db", t.TempDir() + "/extra.db"}

	runInteractive = func(name string, args ...string) error {
		return errors.New("exit status 1") // -Sy fails and writes nothing
	}
	err := Install([]string{"fcitx5-rime"}, true)
	if err == nil {
		t.Fatal("still-missing needed DBs must be fatal")
	}
	if !strings.Contains(err.Error(), "sync DBs still missing") {
		t.Errorf("error should point at the missing DBs: %v", err)
	}
}

func writeStub(path string) error {
	return os.WriteFile(path, []byte("stub db"), 0o644)
}

// TestSudoArgv: without a controlling terminal (agent/CI) sudo must get -n so
// it fails fast instead of hanging on a password prompt nobody answers.
func TestSudoArgv(t *testing.T) {
	orig := hasTTY
	defer func() { hasTTY = orig }()
	hasTTY = func() bool { return false }
	if got := strings.Join(sudoArgv("pacman", "-S", "fcitx5"), " "); got != "sudo -n pacman -S fcitx5" {
		t.Errorf("no tty: want 'sudo -n pacman -S fcitx5', got %q", got)
	}
	hasTTY = func() bool { return true }
	if got := strings.Join(sudoArgv("pacman", "-S", "fcitx5"), " "); got != "sudo pacman -S fcitx5" {
		t.Errorf("tty: want 'sudo pacman -S fcitx5', got %q", got)
	}
}

// TestInstallPrefersSudoNWithoutTTY: the L1 pacman step forwards the -n flag
// end-to-end when there is no controlling terminal (headless agent run).
func TestInstallPrefersSudoNWithoutTTY(t *testing.T) {
	origTTY, origInteractive, origDBs := hasTTY, runInteractive, syncDBPaths
	defer func() { hasTTY, runInteractive, syncDBPaths = origTTY, origInteractive, origDBs }()
	db := t.TempDir() + "/core.db"
	syncDBPaths = []string{db}
	if err := writeStub(db); err != nil {
		t.Fatal(err)
	}
	hasTTY = func() bool { return false }
	var calls []string
	runInteractive = func(name string, args ...string) error {
		calls = append([]string{name}, args...)
		return nil
	}
	if err := Install([]string{"fcitx5-rime"}, true); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, " "); got != "sudo -n pacman -S --needed --noconfirm fcitx5-rime" {
		t.Errorf("no tty install must use sudo -n, got %q", got)
	}
}

// TestInstallSudoErrorHintsNopasswd: when there is no tty and sudo fails, the
// error must tell an agent how to fix it (NOPASSWD sudoers), not just dump a
// generic message.
func TestInstallSudoErrorHintsNopasswd(t *testing.T) {
	origTTY, origInteractive, origDBs := hasTTY, runInteractive, syncDBPaths
	defer func() { hasTTY, runInteractive, syncDBPaths = origTTY, origInteractive, origDBs }()
	db := t.TempDir() + "/core.db"
	syncDBPaths = []string{db}
	if err := writeStub(db); err != nil {
		t.Fatal(err)
	}
	hasTTY = func() bool { return false }
	runInteractive = func(name string, args ...string) error { return errors.New("a password is required") }
	err := Install([]string{"fcitx5-rime"}, true)
	if err == nil {
		t.Fatal("must report the sudo failure")
	}
	if !strings.Contains(err.Error(), "NOPASSWD") {
		t.Errorf("headless sudo failure must hint at NOPASSWD sudoers: %v", err)
	}
}
