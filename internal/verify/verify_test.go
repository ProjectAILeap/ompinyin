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

// convergedHost is a converged pre-4.5 Omarchy: there the tray drop-in + pin and
// the trigger keys are still ompinyin's. managesHost() is the 4.5 counterpart.
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
		GroupIMs:       catalog.DefaultIMs(),
		AltTriggerOK:   true,
		ThemeConfOK:    true,
	}
}

// managesHost is a converged Omarchy 4.5+ host: Omarchy owns the indicator and
// the hotkeys, ompinyin only owns classicui.conf and the group shape.
func managesHost() *observe.Current {
	c := convergedHost()
	c.OmarchyManagesIME = true
	c.BarIndicator = true
	c.ShellRunning = true
	c.DropInExists, c.DropInOK = false, false
	c.PinnedHasFc = false
	return c
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

// TestIMCheckRejectsAFlappingService locks the false-success fix: a unit under
// Restart=always reads "active" for the few hundred ms its doomed instance
// lives, so the ServiceActive snapshot alone passed L5 while a stray owned the
// bus name. A second fcitx5 process must fail the check instead.
func TestIMCheckRejectsAFlappingService(t *testing.T) {
	d := catalog.DefaultDesired()
	orig := service.RunOutput
	defer func() { service.RunOutput = orig }()
	service.RunOutput = func(string, ...string) ([]byte, error) { return []byte("2"), nil }

	c := convergedHost()
	c.ServiceActive = true
	c.FcitxCount = 2 // the unit's dying instance + the stray
	got := find(t, TerminalState(d, c), "IM 三态")
	if got.OK {
		t.Error("a unit flapping beside a stray must not satisfy the IM check")
	}
	for _, want := range []string{"2 个 fcitx5", "pkill -x fcitx5", "Restart=always"} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("detail must mention %q, got %q", want, got.Detail)
		}
	}

	c.FcitxCount = 1 // a single instance is the healthy case
	if got := find(t, TerminalState(d, c), "IM 三态"); !got.OK {
		t.Errorf("one fcitx5 process is healthy: %s", got.Detail)
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
	for _, want := range []string{"服务", "环境变量红线", "触发键", "AltTriggerKeys", "遗留目录", "build 产物"} {
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

// TestX11ToolkitNote: the doctor line must name a stacking toolkit factor, or
// §6.7's "一个框架只能有一个缩放权" never reaches the read-only path (Omarchy's
// monitors.lua ships GDK_SCALE=2 by default).
func TestX11ToolkitNote(t *testing.T) {
	c := &observe.Current{}
	if got := c.X11ToolkitNote(); got != "" {
		t.Errorf("no factors must stay silent, got %q", got)
	}
	c.X11ToolkitFactors = []string{"GDK_SCALE=2"}
	got := c.X11ToolkitNote()
	for _, want := range []string{"GDK_SCALE=2", "4×"} {
		if !strings.Contains(got, want) {
			t.Errorf("note %q missing %q", got, want)
		}
	}
}

func TestSwitchNames(t *testing.T) {
	b := []byte("schema: rime_ice\nswitches:\n  - name: ascii_mode\n    states: [中, Ａ]\n  - name: ascii_punct\n  - name: full_shape\n    reset: 0\nengine:\n  processors:\n    - name: should_not_count\n")
	got := switchNames(b)
	want := []string{"ascii_mode", "ascii_punct", "full_shape"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("switchNames = %v, want %v", got, want)
	}
}

// TestCheckFullShapeIndex: the full_shape reset is patched by INDEX. If upstream
// reorders the switches, the patch silently resets a different switch, so the
// doctor must read the compiled schema instead of trusting the constant.
func TestCheckFullShapeIndex(t *testing.T) {
	const good = "switches:\n  - name: ascii_mode\n  - name: ascii_punct\n  - name: traditionalization\n  - name: emoji\n  - name: full_shape\n  - name: search_single_char\n"
	cases := []struct {
		name  string
		body  string
		write bool
		ok    bool
	}{
		{"index points at full_shape", good, true, true},
		{"upstream reordered", "switches:\n  - name: ascii_mode\n  - name: full_shape\n", true, false},
		{"not compiled yet is not a failure", "", false, true},
	}
	for _, c := range cases {
		rimeDir := t.TempDir()
		if c.write {
			dir := filepath.Join(rimeDir, "build")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "rime_ice.schema.yaml"), []byte(c.body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		d := catalog.Desired{Primary: "quanpin", Channel: "stable"}
		got := checkFullShapeIndex(d, &observe.Current{RimeDir: rimeDir})
		if got.OK != c.ok {
			t.Errorf("%s: OK=%v want %v (detail: %s)", c.name, got.OK, c.ok, got.Detail)
		}
	}
}

// TestGroupShapeIsTerminalState: fcitx5 gates every IM hotkey on
// len(items) > 1, so a stale second IM is a real L5 failure — it is what turned
// left Shift into a second Ctrl+Space on an upgraded host.
func TestGroupShapeIsTerminalState(t *testing.T) {
	d := catalog.DefaultDesired()

	c := convergedHost()
	if got := find(t, TerminalState(d, c), "输入法组"); !got.OK {
		t.Errorf("matching group shape must pass: %+v", got)
	}

	stale := convergedHost()
	stale.GroupIMs = []string{catalog.IMRime, catalog.IMEnglish, "pinyin-custom"}
	got := find(t, TerminalState(d, stale), "输入法组")
	if got.OK {
		t.Fatalf("extra IM must fail L5: %+v", got)
	}
	for _, want := range []string{"pinyin-custom", "移出组", "omarchy setup input pinyin-custom"} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("detail must explain %q: %s", want, got.Detail)
		}
	}

	// The single-IM terminal state is legitimate: it just means no IM hotkey can
	// act (fcitx5 canTrigger() == false), which the detail must say.
	single := convergedHost()
	single.GroupIMs = []string{catalog.IMRime}
	singleDesired := d
	singleDesired.IMs = []string{catalog.IMRime}
	one := find(t, TerminalState(singleDesired, single), "输入法组")
	if !one.OK || !strings.Contains(one.Detail, "canTrigger") {
		t.Errorf("single-IM shape must pass with the canTrigger caveat: %+v", one)
	}
}

// TestAltTriggerCheck: fcitx5 ships AltTriggerKeys=Shift_L; with >1 IM it fires
// the same toggle() as the trigger key. "Empty" is a present-but-childless
// section — an absent one falls back to Shift_L again.
func TestAltTriggerCheck(t *testing.T) {
	d := catalog.DefaultDesired()
	c := convergedHost()
	if got := find(t, Doctor(d, c), "AltTriggerKeys"); !got.OK {
		t.Errorf("empty section must pass: %+v", got)
	}

	bound := convergedHost()
	bound.AltTriggerOK = false
	bound.AltTriggerKeys = []string{"Shift_L"}
	got := find(t, Doctor(d, bound), "AltTriggerKeys")
	if got.OK {
		t.Fatal("bound Shift_L must fail doctor")
	}
	if !strings.Contains(got.Detail, "Shift_L") || !strings.Contains(got.Detail, "Ctrl+Space") {
		t.Errorf("detail must name the key and the symptom: %s", got.Detail)
	}
}

// TestTriggerDetailIsHonestAboutSingleIM: with one IM fcitx5 cannot act on the
// trigger key at all (canTrigger gates it), so doctor must not print
// "开/停用输入法" — the old text claimed a switch that never happened.
func TestTriggerDetailIsHonestAboutSingleIM(t *testing.T) {
	d := catalog.DefaultDesired()
	d.IMs = []string{catalog.IMRime}
	c := convergedHost()
	c.GroupIMs = []string{catalog.IMRime}
	got := find(t, Doctor(d, c), "触发键")
	if !got.OK {
		t.Fatalf("configured trigger keys are still OK: %+v", got)
	}
	if !strings.Contains(got.Detail, "不生效") || !strings.Contains(got.Detail, "canTrigger") {
		t.Errorf("single-IM detail must state the key is inert: %s", got.Detail)
	}

	two := convergedHost()
	got2 := find(t, Doctor(d2WithTwoIMs(), two), "触发键")
	if !strings.Contains(got2.Detail, "在 ") || !strings.Contains(got2.Detail, "间切换") {
		t.Errorf("two-IM detail must describe the switch: %s", got2.Detail)
	}
}

func d2WithTwoIMs() catalog.Desired {
	d := catalog.DefaultDesired()
	d.IMs = catalog.DefaultIMs()
	return d
}

// TestBarIndicatorCheck: 4.5+ the terminal state is Omarchy's own widget on the
// bar — the check must pass on the widget and name the exact fix when it is
// missing, plus flag leftovers from the pre-4.5 tray.
func TestBarIndicatorCheck(t *testing.T) {
	d := catalog.DefaultDesired()
	got := find(t, TerminalState(d, managesHost()), "顶栏输入法指示器")
	if !got.OK {
		t.Fatalf("widget on the bar must pass: %s", got.Detail)
	}
	// the 4.5 audit has no drop-in/pin rows any more (they are residue, not state)
	for _, name := range []string{"托盘 drop-in", "托盘 pin"} {
		for _, chk := range TerminalState(d, managesHost()) {
			if chk.Name == name {
				t.Errorf("4.5+ must not report %q as terminal state", name)
			}
		}
	}

	// present + shell running = green; absent + shell running = real drift
	missing := managesHost()
	missing.BarIndicator = false
	got = find(t, TerminalState(d, missing), "顶栏输入法指示器")
	if got.OK {
		t.Fatal("a missing widget on a running shell must fail")
	}
	if !strings.Contains(got.Detail, "omarchy bar put omarchy.keyboard-layout") {
		t.Errorf("detail must give the exact fix: %s", got.Detail)
	}

	// absent + shell NOT running: nothing can put the widget there, and Omarchy's
	// default config already carries it — a red here is a false alarm that no run
	// could clear before login.
	noShell := managesHost()
	noShell.BarIndicator = false
	noShell.ShellRunning = false
	got = find(t, TerminalState(d, noShell), "顶栏输入法指示器")
	if !got.OK {
		t.Fatalf("absent widget without a running shell must not fail: %s", got.Detail)
	}
	for _, want := range []string{"外壳未运行", "无法确认", "omarchy bar put"} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("detail must explain %q: %s", want, got.Detail)
		}
	}

	stale := managesHost()
	stale.StaleTray = true
	if got := find(t, TerminalState(d, stale), "顶栏输入法指示器"); !strings.Contains(got.Detail, "旧产物") {
		t.Errorf("leftovers must be named: %s", got.Detail)
	}
}

// TestThemeCheckAfterHandover: classicui is ours, the hook is residue — a host
// that still carries the retired hook is NOT converged even though the file it
// used to generate is fine.
func TestThemeCheckAfterHandover(t *testing.T) {
	d := catalog.DefaultDesired()
	if got := find(t, TerminalState(d, managesHost()), "候选框主题"); !got.OK {
		t.Errorf("converged theming must pass: %s", got.Detail)
	}
	hook := managesHost()
	hook.ThemeHookOK = true
	got := find(t, TerminalState(d, hook), "候选框主题")
	if got.OK {
		t.Fatal("the retired hook must read as residual work")
	}
	if !strings.Contains(got.Detail, "omarchy-theme-set-fcitx5") {
		t.Errorf("detail must name the single source of colors: %s", got.Detail)
	}
	conf := managesHost()
	conf.ThemeConfOK = false
	if got := find(t, TerminalState(d, conf), "候选框主题"); got.OK {
		t.Error("classicui.conf not pointing at the theme must fail")
	}
}

// TestDoctorHotkeysAfterHandover: 4.5+ doctor reports the Omarchy hotkeys and
// must NOT print an AltTriggerKeys row (Omarchy writes that section itself, so
// the row could only ever pass by accident).
func TestDoctorHotkeysAfterHandover(t *testing.T) {
	d := catalog.DefaultDesired()
	checks := Doctor(d, managesHost())
	got := find(t, checks, "触发键")
	if !got.OK {
		t.Fatalf("Omarchy's hotkeys are the terminal state: %s", got.Detail)
	}
	for _, want := range []string{"Super+I", "左 Shift", "Ctrl+Space"} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("detail must state the hand-over (%q): %s", want, got.Detail)
		}
	}
	for _, chk := range checks {
		if chk.Name == "AltTriggerKeys" {
			t.Error("4.5+ doctor must not assert AltTriggerKeys (Omarchy's migration owns it)")
		}
	}

	// a leftover Control+space is the one hotkey drift we still own
	residue := managesHost()
	residue.ControlSpace = true
	if got := find(t, Doctor(d, residue), "触发键"); got.OK {
		t.Error("a leftover Control+space must fail doctor")
	}
}

// TestExecStartCheck: the duplicate-icon root cause can come from ANY drop-in, so
// the check reads the EFFECTIVE ExecStart instead of trusting our own file names.
// It only runs on 4.5+ — the pre-4.5 path removes that flag on purpose.
func TestExecStartCheck(t *testing.T) {
	d := catalog.DefaultDesired()
	c := managesHost()
	c.ExecStartOK = true
	if got := find(t, TerminalState(d, c), "fcitx5 启动参数"); !got.OK {
		t.Errorf("Omarchy's own ExecStart must pass: %s", got.Detail)
	}
	if !strings.Contains(find(t, TerminalState(d, c), "fcitx5 启动参数").Detail, "--disable notificationitem") {
		t.Error("detail must name the flag that keeps the duplicate tray item away")
	}

	bad := managesHost()
	bad.ExecStartOK = false
	bad.EffectiveExecStart = "{ argv[]=/usr/bin/fcitx5 --replace }"
	got := find(t, TerminalState(d, bad), "fcitx5 启动参数")
	if got.OK {
		t.Fatal("an overridden ExecStart must fail L5")
	}
	for _, want := range []string{"drop-in", "--disable notificationitem", "两个图标", ".config/systemd/user/", "override.conf"} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("detail must explain %q: %s", want, got.Detail)
		}
	}
	if !strings.Contains(got.Detail, bad.Unit) {
		t.Errorf("detail must name the unit's drop-in dir: %s", got.Detail)
	}

	// pre-4.5 hosts deliberately strip the flag: this row must not exist there
	for _, chk := range TerminalState(d, convergedHost()) {
		if chk.Name == "fcitx5 启动参数" {
			t.Error("the ExecStart check is 4.5+ only (the old path removes the flag on purpose)")
		}
	}
}
