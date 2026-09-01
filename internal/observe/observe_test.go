package observe

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ProjectAILeap/ompinyin/internal/catalog"
	"github.com/ProjectAILeap/ompinyin/internal/deploy"
	"github.com/ProjectAILeap/ompinyin/internal/patches"
	"github.com/ProjectAILeap/ompinyin/internal/pkgs"
	"github.com/ProjectAILeap/ompinyin/internal/service"
	"github.com/ProjectAILeap/ompinyin/internal/state"
	"github.com/ProjectAILeap/ompinyin/internal/tray"
)

// fakeHost points every exec seam at in-memory answers and redirects $HOME, so
// Collect() probes only the fixture tree (T0 hermeticity, §15).
func fakeHost(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMPINYIN_TEST_HOME", home)
	service.SystemUnitDirs = nil
	orig := []func(){
		func() { pkgs.Run = origPkgsRun },
		func() { service.Run = origServiceRun },
		func() { tray.ShellRunning = origShellRunning },
	}
	pkgs.Run = func(string, ...string) error { return nil }
	service.Run = func(string, ...string) error { return errors.New("inactive") }
	tray.ShellRunning = func() bool { return false }
	t.Cleanup(func() {
		service.SystemUnitDirs = []string{"/etc/systemd/user", "/usr/lib/systemd/user"}
		for _, r := range orig {
			r()
		}
	})
	return home
}

var (
	origPkgsRun      = pkgs.Run
	origServiceRun   = service.Run
	origShellRunning = tray.ShellRunning
)

// TestCollectFreshHost: everything absent, so plan.Diff sees work.
func TestCollectFreshHost(t *testing.T) {
	home := fakeHost(t)
	d := catalog.DefaultDesired()
	c := Collect(d, state.New())

	if c.RimeDir != filepath.Join(home, ".local", "share", "fcitx5", "rime") {
		t.Errorf("rime dir wrong: %s", c.RimeDir)
	}
	if c.RimeDataExists || c.GramFileExists {
		t.Error("empty fixture must not look populated")
	}
	if c.DropInExists || c.DropInOK || c.PinnedHasFc || c.ProfileHasRime || c.HotkeyOK {
		t.Errorf("empty fixture must not look converged: %+v", c)
	}
	if len(c.Managed) != len(patches.ManagedFiles(d)) {
		t.Errorf("managed map must cover every desired file: %d != %d", len(c.Managed), len(patches.ManagedFiles(d)))
	}
	for rel, ok := range c.ContentEqual {
		if ok {
			t.Errorf("%s reported byte-equal on a fresh host", rel)
		}
	}
}

// TestCollectConvergedHost writes the real target files and expects the
// snapshot to say "nothing to do" — this is the predicate behind the
// "re-run is a no-op" guarantee (§3).
func TestCollectConvergedHost(t *testing.T) {
	home := fakeHost(t)
	d := catalog.DefaultDesired()
	dir := DataDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range patches.ManagedFiles(d) {
		if err := os.WriteFile(filepath.Join(dir, f.RelPath), []byte(f.Content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"default.yaml", "rime_ice.schema.yaml", catalog.GrammarLanguage + ".gram"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(FcitxConfigDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ProfilePath(), []byte("[Groups/0]\nDefaultIM=rime\n\n[Groups/0/Items/0]\nName=rime\nLayout=\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath(), []byte("[Hotkey/TriggerKeys]\n0=Alt+space\n1=Control+Shift_L\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tray.WriteDropIn(home, "omarchy-fcitx5.service", tray.DropInContent); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(tray.ShellJSONPath(home)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := tray.SetPinned(tray.ShellJSONPath(home), []string{"Fcitx"}); err != nil {
		t.Fatal(err)
	}
	for _, s := range d.SchemaList() {
		p := filepath.Join(dir, "build", s+".schema.yaml")
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("compiled"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	st := state.New()
	for _, f := range patches.ManagedFiles(d) {
		st.ManagedFiles[f.RelPath] = state.HashBytes([]byte(f.Content))
	}
	c := Collect(d, st)

	if !c.RimeDataExists || !c.GramFileExists {
		t.Errorf("L2 anchors not detected: %+v", c)
	}
	if !c.ProfileHasRime || !c.HotkeyOK || !c.DropInOK || !c.PinnedHasFc {
		t.Errorf("L4 not detected as converged: %+v", c)
	}
	if len(c.BuildMissing) != 0 {
		t.Errorf("build artifacts not detected: %v", c.BuildMissing)
	}
	for rel, ok := range c.ContentEqual {
		if !ok {
			t.Errorf("%s should be byte-equal to the desired content", rel)
		}
	}
	if len(c.Orphans) != 0 {
		t.Errorf("no orphans expected, got %v", c.Orphans)
	}
}

// TestCollectDetectsOrphansAndForeign: a grammar file from a previous layout
// and a hand-edited managed file must both surface in the snapshot.
func TestCollectDetectsOrphansAndForeign(t *testing.T) {
	fakeHost(t)
	d := catalog.DefaultDesired()
	dir := DataDir()
	os.MkdirAll(dir, 0o755)

	def := patches.ManagedFiles(d)[0]
	if err := os.WriteFile(filepath.Join(dir, def.RelPath), []byte("patch:\n  menu/page_size: 5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := state.New()
	st.ManagedFiles[def.RelPath] = state.HashBytes([]byte(def.Content)) // ledger says otherwise → user-modified
	st.ManagedFiles["double_pinyin.custom.yaml"] = "hash"               // previous layout, no longer desired

	c := Collect(d, st)
	if c.ContentEqual[def.RelPath] {
		t.Error("hand-edited file reported as byte-equal")
	}
	if len(c.Orphans) != 1 || c.Orphans[0] != "double_pinyin.custom.yaml" {
		t.Errorf("orphans = %v", c.Orphans)
	}
	// nil ledger must not crash (status on a host with no state.json)
	c2 := Collect(d, nil)
	if len(c2.Orphans) != 0 {
		t.Errorf("nil ledger should yield no orphans, got %v", c2.Orphans)
	}
}

// TestL2AnchorsRequireBothFiles: a half-extracted data dir (upstream zip ever
// gaining a top-level directory) must not be reported as "data in place".
func TestL2AnchorsRequireBothFiles(t *testing.T) {
	fakeHost(t)
	dir := DataDir()
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "default.yaml"), []byte("x"), 0o644)

	c := Collect(catalog.DefaultDesired(), state.New())
	if c.RimeDataExists {
		t.Error("default.yaml alone must not count as populated rime data")
	}
	os.WriteFile(filepath.Join(dir, "rime_ice.schema.yaml"), []byte("x"), 0o644)
	if c = Collect(catalog.DefaultDesired(), state.New()); !c.RimeDataExists {
		t.Error("both anchors present must count as populated")
	}
}

// TestBuildArtifactsProbe keeps deploy's read-only probe honest.
func TestBuildArtifactsProbe(t *testing.T) {
	dir := t.TempDir()
	if got := deploy.BuildArtifactsExist(dir, []string{"rime_ice"}); len(got) != 1 {
		t.Errorf("missing artifact should be reported: %v", got)
	}
	p := filepath.Join(dir, "build", "rime_ice.schema.yaml")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("x"), 0o644)
	if got := deploy.BuildArtifactsExist(dir, []string{"rime_ice"}); len(got) != 0 {
		t.Errorf("present artifact should not be reported missing: %v", got)
	}
}
