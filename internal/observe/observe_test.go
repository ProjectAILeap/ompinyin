package observe

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProjectAILeap/ompinyin/internal/catalog"
	"github.com/ProjectAILeap/ompinyin/internal/deploy"
	"github.com/ProjectAILeap/ompinyin/internal/hidpi"
	"github.com/ProjectAILeap/ompinyin/internal/patches"
	"github.com/ProjectAILeap/ompinyin/internal/pkgs"
	"github.com/ProjectAILeap/ompinyin/internal/service"
	"github.com/ProjectAILeap/ompinyin/internal/state"
	"github.com/ProjectAILeap/ompinyin/internal/theme"
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
		func() { service.FcitxRunning = origFcitxRunning },
		func() { service.FcitxCount = origFcitxCount },
		func() { pkgs.Run = origPkgsRun },
		func() { service.Run = origServiceRun },
		func() { tray.ShellRunning = origShellRunning },
		func() { theme.CurrentFont = origThemeFont },
	}
	service.FcitxRunning = func() bool { return false }
	service.FcitxCount = func() int { return 0 }
	pkgs.Run = func(string, ...string) error { return nil }
	service.Run = func(string, ...string) error { return errors.New("inactive") }
	tray.ShellRunning = func() bool { return false }
	theme.CurrentFont = func() string { return "TestFont" }
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
	origFcitxRunning = service.FcitxRunning
	origFcitxCount   = service.FcitxCount
	origShellRunning = tray.ShellRunning
	origThemeFont    = theme.CurrentFont
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
	if c.ThemeConfOK || c.ThemeHookOK || c.ThemeDirOK || c.ThemeEqual {
		t.Errorf("empty fixture must not look theme-converged: %+v", c)
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
	// L4 candidate-window theming (§6.6)
	for rel, content := range map[string]string{
		theme.ConfRelPath: theme.ConfContent("TestFont"),
		theme.HookRelPath: theme.HookContent(),
	} {
		abs := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(theme.ThemeDir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(theme.ThemeDir(home), "theme.conf"), []byte("[Metadata]\nName=Omarchy\n"), 0o644); err != nil {
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
	st.ManagedFiles[theme.ConfRelPath] = state.HashBytes([]byte(theme.ConfContent("TestFont")))
	st.ManagedFiles[theme.HookRelPath] = state.HashBytes([]byte(theme.HookContent()))
	c := Collect(d, st)

	if !c.RimeDataExists || !c.GramFileExists {
		t.Errorf("L2 anchors not detected: %+v", c)
	}
	if !c.ProfileHasRime || !c.HotkeyOK || !c.DropInOK || !c.PinnedHasFc {
		t.Errorf("L4 not detected as converged: %+v", c)
	}
	if !c.ThemeConfOK || !c.ThemeHookOK || !c.ThemeDirOK || !c.ThemeEqual {
		t.Errorf("theme not detected as converged: %+v", c)
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

// TestCollectStrayFcitx: a live fcitx5 with an inactive unit is the unmanaged
// instance that blocks the unit from ever starting (its own fcitx5 exits with
// "another fcitx already running"). The probe is a plain pgrep; a
// fcitx5-remote/bus probe would create the very stray it reports.
func TestCollectStrayFcitx(t *testing.T) {
	home := fakeHost(t)
	orig := service.FcitxRunning
	defer func() { service.FcitxRunning = orig }()

	// no unit discovered + a running fcitx5 → stray
	service.FcitxRunning = func() bool { return true }
	if c := Collect(catalog.DefaultDesired(), state.New()); !c.StrayFcitx {
		t.Errorf("a running fcitx5 with no active unit must read as stray: %+v", c)
	}
	service.FcitxRunning = func() bool { return false }
	if c := Collect(catalog.DefaultDesired(), state.New()); c.StrayFcitx {
		t.Error("no fcitx5 process must not read as stray")
	}
	_ = home
}

// TestCollectCountsFcitxProcs: the process count is what exposes a
// Restart=always flap — the unit reads "active" for the few hundred ms its
// doomed instance lives while a stray owns the bus name.
func TestCollectCountsFcitxProcs(t *testing.T) {
	fakeHost(t)
	orig := service.FcitxCount
	defer func() { service.FcitxCount = orig }()

	service.FcitxCount = func() int { return 2 }
	if c := Collect(catalog.DefaultDesired(), state.New()); c.FcitxCount != 2 {
		t.Errorf("FcitxCount must be observed, got %d", c.FcitxCount)
	}
}

// TestCollectX11UnitExecMissing: the publisher unit bakes the binary path at
// opt-in time; if it disappears the watcher fails to exec silently, so the
// observation must flag it.
func TestCollectX11UnitExecMissing(t *testing.T) {
	home := fakeHost(t)
	unitDir := hidpi.UnitDir(home)
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	svc, _ := hidpi.UnitContent()
	writeUnit := func(body string) {
		if err := os.WriteFile(filepath.Join(unitDir, hidpi.ServiceName), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeUnit(strings.Replace(svc, hidpi.ExecPath(), "/nonexistent/ompinyin", 1))
	c := Collect(catalog.DefaultDesired(), state.New())
	if !c.X11UnitExecMissing || c.X11UnitExec != "/nonexistent/ompinyin" {
		t.Errorf("missing publisher binary not detected: exec=%q missing=%v", c.X11UnitExec, c.X11UnitExecMissing)
	}
	if !strings.Contains(c.X11UnitExecNote(), "不存在") {
		t.Errorf("note must explain the missing binary: %q", c.X11UnitExecNote())
	}

	writeUnit(svc) // the real path (os.Executable at test time) does exist
	c = Collect(catalog.DefaultDesired(), state.New())
	if c.X11UnitExecMissing {
		t.Errorf("existing publisher binary wrongly flagged missing: %+v", c)
	}
	if c.X11UnitExecNote() != "" {
		t.Errorf("no note expected when the binary exists: %q", c.X11UnitExecNote())
	}
}
