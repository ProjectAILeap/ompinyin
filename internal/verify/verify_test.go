package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProjectAILeap/ompinyin/internal/catalog"
	"github.com/ProjectAILeap/ompinyin/internal/observe"
	"github.com/ProjectAILeap/ompinyin/internal/service"
)

func convergedHost() *observe.Current {
	return &observe.Current{
		RimeDir:        "/tmp/rime",
		Unit:           "omarchy-fcitx5.service",
		ServiceActive:  true,
		DropInExists:   true,
		DropInOK:       true,
		PinnedHasFc:    true,
		HotkeyOK:       true,
		ProfileHasRime: true,
	}
}

func find(t *testing.T, checks []Check, name string) Check {
	t.Helper()
	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("check %q missing from %v", name, checks)
	return Check{}
}

// TestBuildArtifactsAreAHardFailure locks 评审 P0-3: a missing build artifact
// is exactly the condition that makes the input method type nothing at all, so
// L5 must fail it. The old lenient "懒编译" pass-through made the whole audit
// unable to detect the tool's own worst failure mode.
func TestBuildArtifactsAreAHardFailure(t *testing.T) {
	d := catalog.DefaultDesired()

	c := convergedHost()
	if got := find(t, TerminalState(d, c), "build 产物"); !got.OK {
		t.Errorf("complete artifacts must pass: %s", got.Detail)
	}

	c = convergedHost()
	c.BuildMissing = []string{"rime_ice"}
	got := find(t, TerminalState(d, c), "build 产物")
	if got.OK {
		t.Error("missing build artifact reported as OK — L5 can no longer detect a useless install")
	}
	if got.Detail == "" {
		t.Error("failure must explain itself")
	}
}

// TestGrammarCompiledCheck reads the real compiled schema.
func TestGrammarCompiledCheck(t *testing.T) {
	d := catalog.DefaultDesired()
	dir := t.TempDir()
	build := filepath.Join(dir, "build")
	if err := os.MkdirAll(build, 0o755); err != nil {
		t.Fatal(err)
	}
	c := convergedHost()
	c.RimeDir = dir

	// absent artifact → hard failure (paired with the build check above)
	if got := find(t, TerminalState(d, c), "grammar 编入"); got.OK {
		t.Error("missing compiled schema must not pass the grammar check")
	}

	write := func(body string) {
		if err := os.WriteFile(filepath.Join(build, "rime_ice.schema.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// rime quotes negative values in compiled output — the probe must tolerate it
	write("grammar:\n  language: " + catalog.GrammarLanguage + "\n  collocation_penalty: \"-14\"\n")
	if got := find(t, TerminalState(d, c), "grammar 编入"); !got.OK {
		t.Errorf("quoted penalty must be accepted: %s", got.Detail)
	}
	// model present but non-official penalties → fail (community installer shape)
	write("grammar:\n  language: " + catalog.GrammarLanguage + "\n  collocation_penalty: -7\n")
	if got := find(t, TerminalState(d, c), "grammar 编入"); got.OK {
		t.Error("non-official grammar constants must be flagged")
	}
	// no model at all → fail
	write("grammar:\n  language: other\n")
	if got := find(t, TerminalState(d, c), "grammar 编入"); got.OK {
		t.Error("missing wanxiang grammar must be flagged")
	}
	// Model=false → the check is not part of the terminal state
	noModel := catalog.Desired{Primary: "quanpin", Model: false, Channel: "stable"}
	for _, chk := range TerminalState(noModel, c) {
		if chk.Name == "grammar 编入" {
			t.Error("grammar check must not run when Model=false")
		}
	}
}

// TestIMTriStateRequiresService covers the fcitx5-remote probe and its
// dependency on a running unit.
func TestIMTriStateRequiresService(t *testing.T) {
	d := catalog.DefaultDesired()
	orig := service.RunOutput
	defer func() { service.RunOutput = orig }()

	c := convergedHost()
	c.ServiceActive = false
	c.Unit = ""
	if got := find(t, TerminalState(d, c), "IM 三态"); got.OK {
		t.Error("no running service cannot satisfy the IM check")
	}

	c = convergedHost()
	service.RunOutput = func(string, ...string) ([]byte, error) { return []byte("1"), nil }
	if got := find(t, TerminalState(d, c), "IM 三态"); !got.OK {
		t.Errorf("english state is a valid tri-state reading: %s", got.Detail)
	}
	service.RunOutput = func(string, ...string) ([]byte, error) { return nil, os.ErrClosed }
	if got := find(t, TerminalState(d, c), "IM 三态"); got.OK {
		t.Error("unusable fcitx5-remote must fail the check")
	}
}

// TestDropInCheckRequiresEnabledContent locks 评审 P1-4: presence is not
// enough — the file must actually enable notificationitem.
func TestDropInCheckRequiresEnabledContent(t *testing.T) {
	d := catalog.DefaultDesired()

	c := convergedHost()
	c.DropInOK = false
	c.DropInExists = true
	c.DropInPath = "/home/x/.config/systemd/user/omarchy-fcitx5.service.d/ompinyin-notificationitem.conf"
	got := find(t, TerminalState(d, c), "托盘 drop-in")
	if got.OK {
		t.Error("a drop-in that still disables notificationitem must fail")
	}
	if got.Detail == "" {
		t.Error("detail must explain the content problem")
	}

	c.DropInExists = false
	c.DropInPath = ""
	if got := find(t, TerminalState(d, c), "托盘 drop-in"); got.OK {
		t.Error("missing drop-in must fail")
	}
}

// TestDoctorAddsHostChecks: doctor is the superset (service, red line, trigger
// keys, legacy dir) on top of the terminal-state audit.
func TestDoctorAddsHostChecks(t *testing.T) {
	d := catalog.DefaultDesired()
	c := convergedHost()
	c.LegacyDirExists = true
	c.HotkeyOK = false

	checks := Doctor(d, c)
	names := map[string]bool{}
	for _, chk := range checks {
		names[chk.Name] = true
	}
	for _, want := range []string{"服务", "环境变量红线", "触发键", "遗留目录", "build 产物"} {
		if !names[want] {
			t.Errorf("doctor is missing the %q check", want)
		}
	}
	if got := find(t, checks, "遗留目录"); got.OK {
		t.Error("~/.config/fcitx/rime present must be reported (§6.5 duplicate data)")
	}
	if got := find(t, checks, "触发键"); got.OK {
		t.Error("wrong trigger keys must be reported")
	}
}

// TestX11HiDPIOptionalCheckIsDiagnostic: when the compat mode is off, doctor
// must stay OK (it is optional) but still report the observed facts instead of
// a static sentence — that is what "default = diagnose only" promises.
func TestX11HiDPIOptionalCheckIsDiagnostic(t *testing.T) {
	d := catalog.DefaultDesired() // X11HiDPI = false
	c := convergedHost()
	c.X11Scale = 2
	c.X11DPIDesired = 192
	c.X11DPIActual = 96
	c.X11Available = true

	got := find(t, TerminalState(d, c), "X11 HiDPI（可选）")
	if !got.OK {
		t.Fatalf("optional check must not fail a default install: %s", got.Detail)
	}
	for _, want := range []string{"scale=2.00", "期望 Xft.dpi=192", "实际=96"} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("detail missing %q: %s", want, got.Detail)
		}
	}

	// left-over artifacts are flagged even though the check stays OK
	c2 := convergedHost()
	c2.X11DPIDesired = 192
	c2.X11UnitsPresent = true
	if got := find(t, TerminalState(d, c2), "X11 HiDPI（可选）"); !strings.Contains(got.Detail, "历史产物") {
		t.Errorf("residual artifacts must be flagged: %s", got.Detail)
	}
}

// TestX11HiDPICompositorScalingIsNotAFailure: when Hyprland scales X11 itself
// (force_zero_scaling=false) the opt-in is intentionally inert, so doctor must
// stay OK and say why — never report a missing Xft.dpi as drift.
func TestX11HiDPICompositorScalingIsNotAFailure(t *testing.T) {
	d := catalog.DefaultDesired()
	d.X11HiDPI = true
	c := convergedHost()
	c.X11DPIDesired = 192
	c.X11DPIActual = 0
	c.X11Available = true
	c.X11ForceZeroScaling = false

	got := find(t, TerminalState(d, c), "X11 HiDPI")
	if !got.OK {
		t.Fatalf("inert mode must not fail L5: %s", got.Detail)
	}
	if !strings.Contains(got.Detail, "force_zero_scaling") {
		t.Errorf("detail must explain the inert mode: %s", got.Detail)
	}
}

// TestX11HiDPIForeignAssignmentIsSurfaced: a competing Xft.dpi line outside
// ompinyin's block is never removed (line-scoped ownership); doctor must point
// at it so the ordering-dependent outcome is explained.
func TestX11HiDPIForeignAssignmentIsSurfaced(t *testing.T) {
	d := catalog.DefaultDesired()
	d.X11HiDPI = true
	c := convergedHost()
	c.X11ForceZeroScaling = true
	c.X11DPIDesired = 192
	c.X11DPIActual = 192
	c.X11Available = true
	c.X11ForeignDPI = true

	got := find(t, TerminalState(d, c), "X11 HiDPI")
	if !strings.Contains(got.Detail, "块外 Xft.dpi") {
		t.Errorf("foreign assignment must be surfaced: %s", got.Detail)
	}
}

// TestGrammarCompiledCoversEverySchema: every enabled schema must carry the
// model (invariant 5). Probing only schema_list[0] would pass a host whose
// second (double-pinyin) schema lost it.
func TestGrammarCompiledCoversEverySchema(t *testing.T) {
	d := catalog.Desired{Primary: "quanpin", Extra: []string{"zrm"}, Model: true, Channel: "stable"}
	dir := t.TempDir()
	build := filepath.Join(dir, "build")
	if err := os.MkdirAll(build, 0o755); err != nil {
		t.Fatal(err)
	}
	c := convergedHost()
	c.RimeDir = dir

	good := "grammar:\n  language: " + catalog.GrammarLanguage + "\n  collocation_penalty: -14\n"
	for _, s := range d.SchemaList() {
		if err := os.WriteFile(filepath.Join(build, s+".schema.yaml"), []byte(good), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := find(t, TerminalState(d, c), "grammar 编入"); !got.OK {
		t.Errorf("both schemas compiled must pass: %s", got.Detail)
	}

	// schema_list[0] stays good; the double-pinyin schema lost the model
	if err := os.WriteFile(filepath.Join(build, "double_pinyin.schema.yaml"), []byte("grammar:\n  language: other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := find(t, TerminalState(d, c), "grammar 编入"); got.OK {
		t.Error("a second schema without the model must fail the check")
	}
}

// TestFindFileBounded: the exact candidates win, and the fallback search is
// depth-bounded (it used to walk the whole /usr/share/omarchy tree on every
// doctor).
func TestFindFileBounded(t *testing.T) {
	root := t.TempDir()
	// shallow hit: root/default/environment.d/<name>
	shallow := filepath.Join(root, "default", "environment.d")
	if err := os.MkdirAll(shallow, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shallow, "10-omarchy-fcitx.conf"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !findFileBounded(nil, root, "10-omarchy-fcitx.conf", 4) {
		t.Error("a file within the depth bound must be found")
	}

	// deep hit: root/a/b/c/d/e/<name> is beyond depth 4 and must be ignored
	deep := filepath.Join(root, "a", "b", "c", "d", "e")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "10-omarchy-fcitx.conf"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir()
	if err := os.MkdirAll(filepath.Join(empty, "a", "b", "c", "d", "e"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(empty, "a", "b", "c", "d", "e", "10-omarchy-fcitx.conf"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if findFileBounded(nil, empty, "10-omarchy-fcitx.conf", 4) {
		t.Error("a file beyond the depth bound must not be found")
	}

	// an exact candidate path is honored regardless of depth
	exact := filepath.Join(empty, "a", "b", "c", "d", "e", "10-omarchy-fcitx.conf")
	if !findFileBounded([]string{exact}, t.TempDir(), "10-omarchy-fcitx.conf", 1) {
		t.Error("an exact candidate path must be honored")
	}
}

// TestServiceDownDetail names the way out: a stray (non-unit) fcitx5 owns
// org.fcitx.Fcitx5, so the unit cannot start until it is gone, and the remedy
// clears systemd's rate limit first (Restart=always trips it).
func TestServiceDownDetail(t *testing.T) {
	stray := serviceDownDetail(&observe.Current{Unit: "omarchy-fcitx5.service", StrayFcitx: true})
	for _, want := range []string{"pkill -x fcitx5", "reset-failed", "org.fcitx.Fcitx5"} {
		if !strings.Contains(stray, want) {
			t.Errorf("stray detail must mention %q, got %q", want, stray)
		}
	}
	plain := serviceDownDetail(&observe.Current{Unit: "omarchy-fcitx5.service"})
	if strings.Contains(plain, "pkill") {
		t.Errorf("without a stray there is nothing to kill: %q", plain)
	}
	if !strings.Contains(plain, "reset-failed") || !strings.Contains(plain, "omarchy-fcitx5.service") {
		t.Errorf("plain detail must name the unit and reset-failed: %q", plain)
	}
}
