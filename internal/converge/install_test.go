package converge

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProjectAILeap/ompinyin/internal/assets"
	"github.com/ProjectAILeap/ompinyin/internal/bar"
	"github.com/ProjectAILeap/ompinyin/internal/catalog"
	"github.com/ProjectAILeap/ompinyin/internal/deploy"
	"github.com/ProjectAILeap/ompinyin/internal/facts"
	"github.com/ProjectAILeap/ompinyin/internal/hidpi"
	"github.com/ProjectAILeap/ompinyin/internal/hotkey"
	"github.com/ProjectAILeap/ompinyin/internal/observe"
	"github.com/ProjectAILeap/ompinyin/internal/pkgs"
	"github.com/ProjectAILeap/ompinyin/internal/plan"
	"github.com/ProjectAILeap/ompinyin/internal/profile"
	"github.com/ProjectAILeap/ompinyin/internal/selfup"
	"github.com/ProjectAILeap/ompinyin/internal/service"
	"github.com/ProjectAILeap/ompinyin/internal/state"
	"github.com/ProjectAILeap/ompinyin/internal/theme"
	"github.com/ProjectAILeap/ompinyin/internal/tray"
	"github.com/ProjectAILeap/ompinyin/internal/verify"
)

func realCmd(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }

// assetsResolveStableTagProd preserves the production resolver across tests.
var assetsResolveStableTagProd = assets.ResolveStableTag

// factsLookPathProd preserves the production PATH lookup across tests: the
// precheck (facts.Collect) gate runs before any layer, so setupFakeHost stubs
// LookPath to keep T0 hermetic and independent of the host (CI ubuntu has no
// omarchy/fcitx5-remote/rime_deployer).
var factsLookPathProd = facts.LookPath

// factsOctagramProbeProd preserves the production octagram probe: CI has no
// librime, and the probe now checks plugin FILES (so Collect would fail the
// precheck there without this stub).
var factsOctagramProbeProd = facts.OctagramProbe

// hidpiRunProd preserves the production hyprctl/xrdb runner across tests.
var hidpiRunProd = hidpi.Run

// hidpiImportTimeoutProd preserves the production env-import budget: the login
// publisher may legitimately poll for DISPLAY, but no T0 test should pay 10s.
var hidpiImportTimeoutProd = hidpi.ImportTimeout

// hidpiShowEnvironmentProd preserves the production `systemctl --user
// show-environment` seam, which the fixtures replace with a quiet stub.
var hidpiShowEnvironmentProd = hidpi.ShowEnvironment

// serviceFcitxRunningProd preserves the production stray-instance probe: the
// fixtures stub it so T0 never shells out to the real pgrep.
var serviceFcitxRunningProd = service.FcitxRunning

// observeInputMethodsPathProd / themeLookPathProd preserve the 4.5 capability
// probe and the upstream-theme-script probe; both are stubbed so T0 models a
// 4.5 host on any machine (AGENTS.md 规则 7: new seam ⇒ sync setupFakeHost).
var (
	observeInputMethodsPathProd = observe.OmarchyInputMethodsPath
	observeUnitExecStartProd    = observe.UnitExecStart
	themeLookPathProd           = theme.LookPath
)

// serviceStraySeamsProd preserves the production stray-cleanup seams for the
// same reason: a bare `pkill -x fcitx5` from a test would kill the developer's
// own input method.
var (
	serviceFcitxCountProd = service.FcitxCount
	serviceKillStrayProd  = service.KillStray
)

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

func jsonMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func jsonSlice(s string) []any {
	var out []any
	json.Unmarshal([]byte(s), &out)
	return out
}

// fakeHost wires all exec seams to an in-memory fake host (T0, §15).
type fakeHost struct {
	unitActive  bool
	barPuts     int // `omarchy bar put omarchy.keyboard-layout` calls
	themeApply  int // omarchy-theme-set-fcitx5 --activate calls
	barSets     [][]string
	buildRuns   int
	themeReload int
	xrdb        []byte // RESOURCE_MANAGER state served by fake `xrdb -query`
	forceZero   bool   // Hyprland xwayland:force_zero_scaling (Omarchy default true)
}

func setupFakeHost(t *testing.T, home string) *fakeHost {
	t.Helper()
	t.Setenv("OMPINYIN_TEST_HOME", home)

	// fake os-release: ID=omarchy
	osRel := filepath.Join(home, "os-release")
	os.WriteFile(osRel, []byte("ID=omarchy\nBUILD_ID=4.0.1\n"), 0o644)
	facts.OSReleasePath = osRel

	h := &fakeHost{forceZero: true}

	// fake pacman: everything installed
	facts.Run = func(name string, args ...string) error { return nil }
	pkgs.Run = func(name string, args ...string) error { return nil }

	// fake PATH lookup: every required tool "exists", so the precheck is hermetic
	// and never depends on the host having omarchy/fcitx5-remote/rime_deployer.
	facts.LookPath = func(string) (string, error) { return "/usr/bin/omarchy", nil }
	// octagram probe: CI has no librime; the real probe checks plugin files.
	facts.OctagramProbe = func() bool { return true }

	// fake fcitx5 user unit: FindUnit/UnitFilePath/ExecStartLine must resolve on
	// ANY host. CI (ubuntu) has no /usr/lib/systemd/user/omarchy-fcitx5.service,
	// so the old fixture silently used the local Omarchy machine's real unit and
	// L5 failed with "fcitx5 服务未运行". The home unit is checked first, so this
	// stub wins on dev and CI alike; SystemUnitDirs=nil keeps it fully hermetic.
	service.SystemUnitDirs = nil
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	os.MkdirAll(unitDir, 0o755)
	os.WriteFile(filepath.Join(unitDir, "omarchy-fcitx5.service"), []byte(
		"[Unit]\nDescription=fcitx5\n[Service]\nExecStart=/usr/bin/fcitx5 --disable notificationitem\n[Install]\nWantedBy=default.target\n"), 0o644)

	// process probe: never shell out to the real pgrep in T0 (on a dev machine
	// with fcitx5 running it would report a stray instance and change plan/L5).
	service.FcitxRunning = func() bool { return false }
	service.FcitxCount = func() int { return 0 }
	service.KillStray = func() error { return nil }

	// fake systemctl: stateful is-active
	service.Run = func(name string, args ...string) error {
		if len(args) >= 3 && args[0] == "--user" && args[1] == "is-active" {
			if h.unitActive {
				return nil
			}
			return errors.New("inactive")
		}
		if len(args) >= 3 && args[0] == "--user" && args[1] == "start" {
			h.unitActive = true
			return nil
		}
		if len(args) >= 3 && args[0] == "--user" && args[1] == "stop" {
			h.unitActive = false
			return nil
		}
		return nil // daemon-reload etc.
	}
	service.RunOutput = func(name string, args ...string) ([]byte, error) {
		return []byte("2"), nil // 中文 IM state
	}

	// fake rime_deployer --build: Build() runs it for the build_info/scaffolding;
	// the schemas are synthesized below by CompileSchemas (fcitx5-rime lazy deploy).
	deploy.Run = func(dir, name string, args ...string) error {
		return nil
	}

	// fake hyprctl + xrdb: focused monitor scale=2 (→ Xft.dpi 192); `xrdb -query`
	// echoes the last merged file, so the published-value diff is observable.
	// Omarchy ships xwayland force_zero_scaling=true (X11 apps self-scale).
	//
	// The session-environment import is stubbed to "the manager knows nothing":
	// that matches these fixtures (they never set DISPLAY) and makes the poll exit
	// on its first iteration instead of sleeping out the production budget.
	hidpi.ImportTimeout = 0
	hidpi.ShowEnvironment = func() ([]byte, error) { return nil, nil }
	hidpi.Run = func(name string, args ...string) ([]byte, error) {
		switch {
		case name == "hyprctl" && len(args) >= 1 && args[0] == "getoption":
			return []byte(fmt.Sprintf("bool: %v\nset: true\n", h.forceZero)), nil
		case name == "hyprctl":
			return []byte(`[{"focused":true,"scale":2}]`), nil
		case name == "xrdb" && len(args) >= 1 && args[0] == "-query":
			return h.xrdb, nil
		case name == "xrdb" && len(args) == 2 && args[0] == "-merge":
			if b, err := os.ReadFile(args[1]); err == nil {
				h.xrdb = b
			}
			return nil, nil
		}
		return nil, nil
	}

	// fake rime lazy deploy: synthesize the grammar-compiled build schemas
	deploy.CompileSchemas = func(rimeDir string, schemas []string) error {
		h.buildRuns++
		for _, s := range schemas {
			// The switches list mirrors rime-ice's order, so doctor's index probe
			// (switches[@4] == full_shape) is exercised by a converged fixture.
			body := fmt.Sprintf("schema: %s\ngrammar:\n  language: %s\n  collocation_penalty: %d\ntranslator/max_homophones: %d\n"+
				"switches:\n  - name: ascii_mode\n  - name: ascii_punct\n  - name: traditionalization\n"+
				"  - name: emoji\n  - name: full_shape\n  - name: search_single_char\n",
				s, catalog.GrammarLanguage, catalog.GrammarCollocationPenalty, catalog.GrammarMaxHomophones)
			os.MkdirAll(filepath.Join(rimeDir, "build"), 0o755)
			if werr := os.WriteFile(filepath.Join(rimeDir, "build", s+".schema.yaml"), []byte(body), 0o644); werr != nil {
				return werr
			}
		}
		return nil
	}

	// fake omarchy-shell: alive
	tray.ShellRunning = func() bool { return true }

	// Omarchy 4.5 capability probe: point it at a marker INSIDE the fake home so
	// T0 is hermetic and the default fixture is a 4.5 host. Tests that model an
	// upgraded 4.0 machine remove the file (useLegacyOmarchy).
	observe.OmarchyInputMethodsPath = filepath.Join(home, "omarchy", "input-methods", "indicator.py")
	os.MkdirAll(filepath.Dir(observe.OmarchyInputMethodsPath), 0o755)
	os.WriteFile(observe.OmarchyInputMethodsPath, []byte("# Omarchy's own IM indicator\n"), 0o644)

	// fake `systemctl show -p ExecStart --value`: systemd applies the .d/ drop-ins,
	// so the EFFECTIVE value loses Omarchy's `--disable notificationitem` as soon
	// as any drop-in or override.conf resets ExecStart (that is the whole bug).
	observe.UnitExecStart = func(unit string) (string, error) {
		if dropInRewritesExecStart(home, unit) {
			return "{ path=/usr/bin/fcitx5 ; argv[]=/usr/bin/fcitx5 --replace }", nil
		}
		return "{ path=/usr/bin/fcitx5 ; argv[]=/usr/bin/fcitx5 --replace --disable notificationitem }", nil
	}

	// fake `omarchy bar put omarchy.keyboard-layout`: persists the widget the same
	// way the shell does (into shell.json) and counts the call.
	bar.Run = func(name string, args ...string) error {
		if name == "omarchy" && len(args) >= 2 && args[0] == "bar" && args[1] == "put" {
			h.barPuts++
			if len(args) >= 3 {
				putBarWidget(home, args[2])
			}
		}
		return nil
	}

	// fake candidate-window theming: Omarchy owns the colors now, so theme.Run is
	// the upstream generator (counted, not executed); Reload counts hot reloads;
	// the UI font is deterministic so desired-byte comparisons stay stable.
	theme.LookPath = func(string) (string, error) { return "/usr/bin/omarchy-theme-set-fcitx5", nil }
	theme.Run = func(name string, args ...string) error {
		h.themeApply++
		return nil
	}
	theme.Reload = func() error { h.themeReload++; return nil }
	theme.CurrentFont = func() string { return "TestFont" }

	// tag resolution: never touch the network in T0 (stub = resolution
	// unavailable → unpinned releases/latest fallback, cache-seeded)
	assets.ResolveStableTag = func(ctx context.Context) string { return "" }

	// fake omarchy bar set: persists into shell.json like persistShellConfig
	tray.Run = func(name string, args ...string) error {
		if name == "omarchy" && len(args) >= 5 && args[0] == "bar" && args[1] == "set" && args[2] == "omarchy.tray" {
			h.barSets = append(h.barSets, args)
			p := tray.ShellJSONPath(home)
			var root map[string]any
			if b, err := os.ReadFile(p); err == nil {
				if jsonErr := jsonUnmarshal(b, &root); jsonErr != nil {
					root = map[string]any{}
				}
			} else {
				root = map[string]any{}
			}
			bar, _ := root["bar"].(map[string]any)
			if bar == nil {
				bar = map[string]any{}
				root["bar"] = bar
			}
			layout, _ := bar["layout"].(map[string]any)
			if layout == nil {
				layout = map[string]any{}
				bar["layout"] = layout
			}
			right, _ := layout["right"].([]any)
			found := false
			for i, e := range right {
				if em, ok := e.(map[string]any); ok && em["id"] == "omarchy.tray" {
					em["pinned"] = jsonSlice(args[4])
					right[i] = em
					found = true
				}
			}
			if !found {
				right = append(right, map[string]any{"id": "omarchy.tray", "pinned": jsonSlice(args[4])})
			}
			layout["right"] = right
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, jsonMarshal(root), 0o644)
			return nil
		}
		return nil
	}

	t.Cleanup(func() {
		facts.Run = nil
		facts.LookPath = factsLookPathProd
		facts.OctagramProbe = factsOctagramProbeProd
		facts.OSReleasePath = "/etc/os-release"
		pkgs.Run = nil
		service.SystemUnitDirs = []string{"/etc/systemd/user", "/usr/lib/systemd/user"}
		service.Run = func(name string, args ...string) error {
			c := realCmd(name, args...)
			return c.Run()
		}
		service.FcitxRunning = serviceFcitxRunningProd
		service.FcitxCount = serviceFcitxCountProd
		service.KillStray = serviceKillStrayProd
		service.RunOutput = nil
		deploy.Run = nil
		deploy.CompileSchemas = nil
		hidpi.Run = hidpiRunProd
		hidpi.ImportTimeout = hidpiImportTimeoutProd
		hidpi.ShowEnvironment = hidpiShowEnvironmentProd
		tray.ShellRunning = nil
		tray.Run = nil
		bar.Run = nil
		theme.Run = nil
		theme.LookPath = themeLookPathProd
		observe.OmarchyInputMethodsPath = observeInputMethodsPathProd
		observe.UnitExecStart = observeUnitExecStartProd
		theme.Reload = nil
		theme.CurrentFont = nil
		assets.ResolveStableTag = assetsResolveStableTagProd
	})
	return h
}

// putBarWidget appends one bar widget to shell.json's right section (what
// `omarchy bar put <id>` does, idempotently).
func putBarWidget(home, id string) {
	p := tray.ShellJSONPath(home)
	var root map[string]any
	if b, err := os.ReadFile(p); err == nil {
		if jsonErr := jsonUnmarshal(b, &root); jsonErr != nil {
			root = map[string]any{}
		}
	} else {
		root = map[string]any{}
	}
	barSection, _ := root["bar"].(map[string]any)
	if barSection == nil {
		barSection = map[string]any{}
		root["bar"] = barSection
	}
	layout, _ := barSection["layout"].(map[string]any)
	if layout == nil {
		layout = map[string]any{}
		barSection["layout"] = layout
	}
	right, _ := layout["right"].([]any)
	for _, e := range right {
		if em, ok := e.(map[string]any); ok && em["id"] == id {
			return
		}
	}
	layout["right"] = append(right, map[string]any{"id": id})
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, jsonMarshal(root), 0o644)
}

// dropInRewritesExecStart models systemd's drop-in precedence for the fake host:
// any file in the unit's .d/ (ours, v1.0's override.conf, or a hand-written one)
// that assigns ExecStart replaces the unit's own line — which is how the
// `--disable notificationitem` flag disappears and fcitx5 starts publishing its
// own tray item again.
func dropInRewritesExecStart(home, unit string) bool {
	for _, rel := range []string{tray.LegacyOverrideRelPath(unit), tray.DropInRelPath(unit), tray.DropInRelPathLegacy} {
		b, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "ExecStart="); ok && strings.TrimSpace(v) != "" {
				return true
			}
		}
	}
	return false
}

// useLegacyOmarchy turns the fake host into a pre-4.5 Omarchy (no
// default/input-methods/indicator.py), i.e. the frozen old code path (D3).
func useLegacyOmarchy(t *testing.T) {
	t.Helper()
	if err := os.Remove(observe.OmarchyInputMethodsPath); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// seedCache pre-seeds the asset cache so no network is touched. Every cache
// name the resolver can pick is written: the plain stable/nightly names and
// the per-tag key of a pinned stable build.
func seedCache(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".cache", "ompinyin")
	os.MkdirAll(dir, 0o755)

	// rime-ice full.zip: the two anchor files observe() looks for
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"default.yaml", "rime_ice.schema.yaml"} {
		f, _ := zw.Create(name)
		f.Write([]byte("# rime-ice data: " + name + "\n"))
	}
	zw.Close()
	for _, name := range []string{
		"rime-ice-full-stable.zip",
		"rime-ice-full-nightly.zip",
		"rime-ice-full-stable.zip@" + stableTestTag,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// wanxiang gram
	os.WriteFile(filepath.Join(dir, catalog.GrammarLanguage+".gram"), []byte("fake-gram-bytes"), 0o644)
}

// stableTestTag is the tag the stubbed resolver reports in T0.
const stableTestTag = "2026.06.30"

func newTestOpts() (Options, *bytes.Buffer) {
	out := &bytes.Buffer{}
	return Options{Stdout: out, Stderr: out, Yes: true}, out
}

// TestInstallConvergesDefaultTerminalState is the T0 end-to-end check:
// the default terminal state (雾凇全拼 × 万象 × 顶栏图标) is reached.
func TestInstallConvergesDefaultTerminalState(t *testing.T) {
	home := t.TempDir()
	h := setupFakeHost(t, home)
	seedCache(t, home)

	opts, out := newTestOpts()
	code := Install(catalog.DefaultDesired(), false, opts)
	if code != ExitOK {
		t.Fatalf("install exit=%d\noutput:\n%s", code, out.String())
	}

	// L3 managed files with managed header
	rimeDir := observe.DataDir()
	for _, name := range []string{"default.custom.yaml", "rime_ice.custom.yaml", "radical_pinyin.custom.yaml", "melt_eng.custom.yaml"} {
		b, err := os.ReadFile(filepath.Join(rimeDir, name))
		if err != nil {
			t.Fatalf("managed file %s missing: %v\noutput:\n%s", name, err, out.String())
		}
		if !strings.HasPrefix(string(b), "# managed by ompinyin") {
			t.Errorf("%s missing managed header", name)
		}
	}
	// grammar applied to the enabled schema with official constants (ADR 9)
	b, _ := os.ReadFile(filepath.Join(rimeDir, "rime_ice.custom.yaml"))
	for _, want := range []string{"wanxiang-lts-zh-hans", "collocation_penalty: -14", "weak_collocation_penalty: -100"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("rime_ice.custom.yaml missing %q", want)
		}
	}
	// double pinyin NOT installed by default (§1.2)
	if _, err := os.Stat(filepath.Join(rimeDir, "double_pinyin.custom.yaml")); err == nil {
		t.Error("double pinyin grammar must not exist without --dsp")
	}

	// L4 profile registered rime AND carries the declared group shape: two items
	// are what lets Ctrl+Space act at all (fcitx5 gates IM hotkeys on >1 IM).
	pb, _ := os.ReadFile(observe.ProfilePath())
	if !strings.Contains(string(pb), "DefaultIM=rime") || !strings.Contains(string(pb), "Name=rime") {
		t.Errorf("profile not converged:\n%s", pb)
	}
	if got := profile.GroupIMs(string(pb)); !catalog.SameIMs(got, catalog.DefaultIMs()) {
		t.Errorf("group shape = %v; want %v\n%s", got, catalog.DefaultIMs(), pb)
	}
	// L4 hotkeys belong to Omarchy: nothing of ours is written, and the
	// AltTriggerKeys section is NEVER touched (upstream rewrites it).
	cb, _ := os.ReadFile(observe.ConfigPath())
	if strings.Contains(strings.ToLower(string(cb)), "control+space") {
		t.Errorf("4.5 must not write Control+space:\n%s", cb)
	}
	if hotkey.HasEmptyAltTrigger(string(cb)) {
		t.Errorf("4.5 must not create an empty [Hotkey/AltTriggerKeys] section:\n%s", cb)
	}
	// L4 tray: no drop-in, no SNI pin — Omarchy's widget is on the bar instead
	if _, err := os.Stat(tray.DropInPath(home, "omarchy-fcitx5.service")); !os.IsNotExist(err) {
		t.Error("4.5 must not install the notificationitem drop-in")
	}
	sj, _ := os.ReadFile(tray.ShellJSONPath(home))
	if pinned, perr := tray.ReadPinned(sj); perr != nil || len(pinned) != 0 {
		t.Errorf("4.5 must not pin Fcitx: %v err=%v\n%s", pinned, perr, sj)
	}
	if !bar.IndicatorPresent(sj) {
		t.Errorf("bar indicator missing after install:\n%s", sj)
	}
	if h.barPuts != 1 {
		t.Errorf("omarchy bar put ran %d times, want 1", h.barPuts)
	}
	// L4 candidate-window theming (§6.6): classicui.conf is ours, the colors are
	// Omarchy's generator's, and our old hook is NOT installed
	cb2, _ := os.ReadFile(theme.ConfPath(home))
	if !strings.HasPrefix(string(cb2), "# managed by ompinyin") || !strings.Contains(string(cb2), "Theme=omarchy") {
		t.Errorf("classicui.conf not converged:\n%s", cb2)
	}
	if !strings.Contains(string(cb2), "Font=TestFont 12") {
		t.Errorf("classicui.conf missing the Omarchy font:\n%s", cb2)
	}
	if _, err := os.Stat(theme.HookPath(home)); !os.IsNotExist(err) {
		t.Error("the retired theme-set hook must not be (re)installed")
	}
	if h.themeApply != 1 {
		t.Errorf("omarchy-theme-set-fcitx5 ran %d times, want 1", h.themeApply)
	}
	if h.themeReload != 1 {
		t.Errorf("fcitx5 theme reload ran %d times, want 1", h.themeReload)
	}
	// deploy ran once for --build only (no per-schema --compile)
	if h.buildRuns != 1 {
		t.Errorf("rime lazy deploy ran %d times, want 1", h.buildRuns)
	}

	// state ledger written
	st, err := state.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Desired.Primary != "quanpin" || !st.Desired.Model {
		t.Errorf("state desired wrong: %+v", st.Desired)
	}
	if _, ok := st.Assets["wanxiang"]; !ok {
		t.Error("wanxiang asset not recorded")
	}
	if st.ManagedFiles["default.custom.yaml"] == "" {
		t.Error("ownership ledger empty")
	}
	_ = service.RunOutput
}

// TestInstallIdempotent: re-running converges with all skips (T4 前置逻辑).
func TestInstallIdempotent(t *testing.T) {
	home := t.TempDir()
	h := setupFakeHost(t, home)
	seedCache(t, home)

	opts, out := newTestOpts()
	if code := Install(catalog.DefaultDesired(), false, opts); code != ExitOK {
		t.Fatalf("first install exit=%d\n%s", code, out.String())
	}
	if code := Install(catalog.DefaultDesired(), false, opts); code != ExitOK {
		t.Fatalf("second install exit=%d\n%s", code, out.String())
	}
	// P1-1a: the second (idempotent) run must NOT rebuild — the stop window
	// stays closed, deploy runs exactly once across both runs.
	if !strings.Contains(out.String(), "[跳过] L4 stop 窗口未开启") {
		t.Errorf("second run must keep the stop window closed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "已是目标内容") {
		t.Errorf("second run must skip byte-identical managed files:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "[跳过] L4 候选框已跟随 Omarchy 主题") {
		t.Errorf("second run must skip the candidate-window theme:\n%s", out.String())
	}
	if h.buildRuns != 1 {
		t.Errorf("rime deploy ran %d times, want 1 (second run must skip the stop window)", h.buildRuns)
	}
	// the theming reload must not re-fire on an idempotent run
	if h.themeReload != 1 {
		t.Errorf("theme hot reload ran %d times, want 1", h.themeReload)
	}
	// neither the bar put nor the upstream generator may re-run
	if h.barPuts != 1 || h.themeApply != 1 {
		t.Errorf("idempotent re-run re-did work: barPuts=%d themeApply=%d", h.barPuts, h.themeApply)
	}
}

// TestInstallDSPConverges: --dsp flypy --dsp-default writes grammar for both
// schemas and points radical/melt algebra at the primary (§2.2).
func TestInstallDSPConverges(t *testing.T) {
	home := t.TempDir()
	setupFakeHost(t, home)
	seedCache(t, home)

	d := catalog.Desired{Primary: "flypy", Extra: []string{"quanpin"}, Model: true, Channel: "stable"}
	opts, out := newTestOpts()
	if code := Install(d, false, opts); code != ExitOK {
		t.Fatalf("install exit=%d\n%s", code, out.String())
	}
	rimeDir := observe.DataDir()
	for _, schema := range []string{"double_pinyin_flypy", "rime_ice"} {
		b, err := os.ReadFile(filepath.Join(rimeDir, schema+".custom.yaml"))
		if err != nil {
			t.Fatalf("%s.custom.yaml missing: %v\n%s", schema, err, out.String())
		}
		if !strings.Contains(string(b), "wanxiang-lts-zh-hans") {
			t.Errorf("schema %s grammar missing", schema)
		}
	}
	rb, _ := os.ReadFile(filepath.Join(rimeDir, "radical_pinyin.custom.yaml"))
	if !strings.Contains(string(rb), "algebra_double_pinyin_flypy") {
		t.Errorf("radical algebra not following primary:\n%s", rb)
	}
	// default.custom.yaml lists flypy first (F4 default)
	def, _ := os.ReadFile(filepath.Join(rimeDir, "default.custom.yaml"))
	if strings.Index(string(def), "double_pinyin_flypy") > strings.Index(string(def), "rime_ice") {
		t.Errorf("flypy should be schema_list[0]:\n%s", def)
	}
}

// TestInstallUserModifiedManagedFileRequiresYes: the ownership protocol must
// not silently overwrite hand-edited managed files (§5.1).
func TestInstallUserModifiedManagedFile(t *testing.T) {
	home := t.TempDir()
	setupFakeHost(t, home)
	seedCache(t, home)

	// first install establishes the ledger
	opts, out := newTestOpts()
	if code := Install(catalog.DefaultDesired(), false, opts); code != ExitOK {
		t.Fatalf("first install exit=%d\n%s", code, out.String())
	}

	// user hand-edits a managed file
	rimeDir := observe.DataDir()
	os.WriteFile(filepath.Join(rimeDir, "default.custom.yaml"), []byte("patch:\n  menu/page_size: 9\n"), 0o644)

	// refusal (empty stdin, no --yes) must abort with exit 1
	abort := Options{Stdout: out, Stderr: out, Stdin: strings.NewReader("n\n")}
	if code := Install(catalog.DefaultDesired(), false, abort); code != ExitExecFail {
		t.Fatalf("refused overwrite exit=%d, want 1\n%s", code, out.String())
	}
	if b, _ := os.ReadFile(filepath.Join(rimeDir, "default.custom.yaml")); string(b) != "patch:\n  menu/page_size: 9\n" {
		t.Error("user edit was clobbered without consent")
	}

	// consent (--yes) overwrites after backup
	opts2, out2 := newTestOpts()
	if code := Install(catalog.DefaultDesired(), false, opts2); code != ExitOK {
		t.Fatalf("consented install exit=%d\n%s", code, out2.String())
	}
	// backup must contain the user edit
	matches, _ := filepath.Glob(filepath.Join(home, ".local", "state", "ompinyin", "backup-*", ".local", "share", "fcitx5", "rime", "default.custom.yaml"))
	if len(matches) == 0 {
		t.Fatal("no backup of user-edited managed file")
	}
	if b, _ := os.ReadFile(matches[len(matches)-1]); string(b) != "patch:\n  menu/page_size: 9\n" {
		t.Errorf("backup content wrong: %s", b)
	}
}

// TestSwitchDSPKeepsQuanpin locks the switch->desired mapping to mirror install:
// `switch --dsp X --dsp-default` keeps quanpin in Extra (§2.2), a bare `--dsp X`
// keeps quanpin primary, and `--dsp X --no-quanpin` yields only the dsp layout
// (these used to be inconsistent with install and silently dropped quanpin).
func TestSwitchDSPKeepsQuanpin(t *testing.T) {
	cases := []struct {
		name           string
		args           SwitchArgs
		wantPrimary    string
		wantExtra      []string
		wantSchemaList []string
	}{
		{
			name:           "dsp-default keeps quanpin in Extra",
			args:           SwitchArgs{DSP: "zrm", DSPDefault: true},
			wantPrimary:    "zrm",
			wantExtra:      []string{"quanpin"},
			wantSchemaList: []string{"double_pinyin", "rime_ice"},
		},
		{
			name:           "bare dsp keeps quanpin primary",
			args:           SwitchArgs{DSP: "zrm"},
			wantPrimary:    "quanpin",
			wantExtra:      []string{"zrm"},
			wantSchemaList: []string{"rime_ice", "double_pinyin"},
		},
		{
			name:           "dsp no-quanpin drops quanpin",
			args:           SwitchArgs{DSP: "zrm", NoQuanpin: true},
			wantPrimary:    "zrm",
			wantExtra:      nil,
			wantSchemaList: []string{"double_pinyin"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			setupFakeHost(t, home)
			seedCache(t, home)

			opts, out := newTestOpts()
			if code := Switch(tc.args, opts); code != ExitOK {
				t.Fatalf("switch exit=%d\n%s", code, out.String())
			}
			st, err := state.Load()
			if err != nil {
				t.Fatal(err)
			}
			if st.Desired.Primary != tc.wantPrimary {
				t.Errorf("Primary=%q want %q", st.Desired.Primary, tc.wantPrimary)
			}
			if len(st.Desired.Extra) != len(tc.wantExtra) {
				t.Errorf("Extra=%v want %v", st.Desired.Extra, tc.wantExtra)
			} else {
				for i := range tc.wantExtra {
					if st.Desired.Extra[i] != tc.wantExtra[i] {
						t.Errorf("Extra=%v want %v", st.Desired.Extra, tc.wantExtra)
						break
					}
				}
			}
			if len(st.SchemaList) != len(tc.wantSchemaList) {
				t.Errorf("SchemaList=%v want %v", st.SchemaList, tc.wantSchemaList)
			} else {
				for i := range tc.wantSchemaList {
					if st.SchemaList[i] != tc.wantSchemaList[i] {
						t.Errorf("SchemaList=%v want %v", st.SchemaList, tc.wantSchemaList)
						break
					}
				}
			}
		})
	}
}

// backupDirs lists the backup-<ts>/ directories under the state dir.
func backupDirs(t *testing.T, home string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(home, ".local", "state", "ompinyin", "backup-*"))
	return m
}

// TestConvergedRunCreatesNoBackup locks 评审 P0-9 tail: once the host is
// converged, a re-run must be a true no-op — no backup directory, no stop
// window, no L2 re-download, and every mutating layer reports [跳过].
func TestConvergedRunCreatesNoBackup(t *testing.T) {
	home := t.TempDir()
	setupFakeHost(t, home)
	seedCache(t, home)

	o1, out1 := newTestOpts()
	if c := Install(catalog.DefaultDesired(), false, o1); c != ExitOK {
		t.Fatalf("first install: %d\n%s", c, out1)
	}
	before := len(backupDirs(t, home))

	o2, out2 := newTestOpts()
	if c := Install(catalog.DefaultDesired(), false, o2); c != ExitOK {
		t.Fatalf("second install: %d\n%s", c, out2)
	}
	s := out2.String()
	if got := len(backupDirs(t, home)); got != before {
		t.Errorf("converged re-run created a backup dir (%d -> %d):\n%s", before, got, s)
	}
	for _, want := range []string{
		"[跳过] L2 数据资产已就位",
		"[跳过] L3 受管配置与终态一致",
		"[跳过] L4 stop 窗口未开启",
		"[跳过] L4 无旧 Fcitx pin 残留",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("re-run must print %q:\n%s", want, s)
		}
	}
	// the plan and the execution must agree: nothing may be [计划] except L5
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "[计划]") && !strings.Contains(line, "L5") {
			t.Errorf("plan promised work that a converged host does not need: %s", line)
		}
	}
}

// TestLedgerSurvivesLaterFailure locks 评审 P0-5: L3 wrote managed files and
// then a later layer failed. The ownership ledger must already be on disk, or
// the next run classifies ompinyin's own files as 外来 and asks to overwrite
// them.
func TestLedgerSurvivesLaterFailure(t *testing.T) {
	home := t.TempDir()
	setupFakeHost(t, home)
	seedCache(t, home)
	orig := service.Run
	service.Run = func(name string, args ...string) error {
		if len(args) > 1 && args[1] == "start" {
			return errors.New("start failed")
		}
		return orig(name, args...)
	}

	opts, out := newTestOpts()
	// a non-default desired, so "did not advance" is observable
	d := catalog.Desired{Primary: "quanpin", Extra: []string{"zrm"}, Model: true, Channel: "stable"}
	if c := Install(d, false, opts); c == ExitOK {
		t.Fatalf("run should have failed on start:\n%s", out)
	}
	st, err := state.Load()
	if err != nil {
		t.Fatalf("state.json must exist after a partial run: %v\n%s", err, out)
	}
	for _, rel := range []string{"default.custom.yaml", "rime_ice.custom.yaml", "double_pinyin.custom.yaml", "radical_pinyin.custom.yaml", "melt_eng.custom.yaml"} {
		if st.ManagedFiles[rel] == "" {
			t.Errorf("ownership ledger lost %s", rel)
		}
	}
	// the desired state must NOT have advanced past a failed run (§8)
	if len(st.SchemaList) != 0 || len(st.Desired.Extra) != 0 {
		t.Errorf("failed run persisted the new terminal state: desired=%+v schema_list=%v", st.Desired, st.SchemaList)
	}
}

// TestDaemonReloadFailureRestartsFcitx locks 评审 P0-4: an error inside the
// stop window must never leave the user without an input method.
func TestDaemonReloadFailureRestartsFcitx(t *testing.T) {
	home := t.TempDir()
	h := setupFakeHost(t, home)
	seedCache(t, home)
	o1, out1 := newTestOpts()
	if c := Install(catalog.DefaultDesired(), false, o1); c != ExitOK {
		t.Fatalf("warmup: %d\n%s", c, out1)
	}
	// force work INSIDE the window that reaches daemon-reload: a leftover
	// Control+space is 4.5 host work, so the stop window (and the reload) opens
	if err := os.MkdirAll(filepath.Dir(observe.ConfigPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(observe.ConfigPath(), []byte("[Hotkey/TriggerKeys]\n0=Control+space\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := service.Run
	service.Run = func(name string, args ...string) error {
		if len(args) > 1 && args[1] == "daemon-reload" {
			return errors.New("boom")
		}
		return orig(name, args...)
	}
	o2, out2 := newTestOpts()
	if c := Install(catalog.DefaultDesired(), false, o2); c == ExitOK {
		t.Fatalf("daemon-reload failure must surface as an exec failure:\n%s", out2)
	}
	if !h.unitActive {
		t.Error("fcitx5 left STOPPED after a failed convergence — user has no input method")
	}
}

// TestInstallPinsStableTag exercises the pinned-stable path at the converge
// layer (T0 used to stub the resolver to "" so the whole branch was dead):
// the resolved tag must reach the ledger and the per-tag cache key.
func TestInstallPinsStableTag(t *testing.T) {
	home := t.TempDir()
	setupFakeHost(t, home)
	seedCache(t, home)
	assets.ResolveStableTag = func(context.Context) string { return stableTestTag }

	opts, out := newTestOpts()
	if c := Install(catalog.DefaultDesired(), false, opts); c != ExitOK {
		t.Fatalf("install: %d\n%s", c, out)
	}
	st, err := state.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Assets["rime_ice"].Tag; got != stableTestTag {
		t.Errorf("recorded tag = %q, want %q", got, stableTestTag)
	}
	if st.Assets["rime_ice"].SHA256 == "" {
		t.Error("asset sha256 not recorded")
	}
	// the pinned key must have been used, and no unpinned download may occur
	if _, err := os.Stat(filepath.Join(home, ".cache", "ompinyin", "rime-ice-full-stable.zip@"+stableTestTag)); err != nil {
		t.Errorf("per-tag cache key not used: %v", err)
	}
	if !strings.Contains(out.String(), "[跳过] L2 rime-ice-full-stable.zip 缓存命中") {
		t.Logf("L2 lines:\n%s", out.String())
	}
}

// TestChannelChangeForcesL2: --channel is part of Desired, so switching it on
// an already-converged host must re-run L2 instead of silently keeping the old
// build's bytes while recording the new channel.
func TestChannelChangeForcesL2(t *testing.T) {
	home := t.TempDir()
	setupFakeHost(t, home)
	seedCache(t, home)
	o1, out1 := newTestOpts()
	if c := Install(catalog.DefaultDesired(), false, o1); c != ExitOK {
		t.Fatalf("stable install: %d\n%s", c, out1)
	}

	d := catalog.DefaultDesired()
	d.Channel = "nightly"
	o2, out2 := newTestOpts()
	if c := Install(d, false, o2); c != ExitOK {
		t.Fatalf("nightly install: %d\n%s", c, out2)
	}
	s := out2.String()
	if !strings.Contains(s, "[计划] L2") || strings.Contains(s, "[跳过] L2 数据资产已就位") {
		t.Errorf("channel change must force an L2 refresh:\n%s", s)
	}
	st, _ := state.Load()
	if st.Desired.Channel != "nightly" {
		t.Errorf("channel not persisted: %+v", st.Desired)
	}
}

// TestDropInFollowsGenericUnit locks 评审 P1-4 end to end: on a host whose
// unit is the generic fcitx5.service, the notificationitem drop-in must be
// written under fcitx5.service.d/ — not the hardcoded omarchy path.
func TestDropInFollowsGenericUnit(t *testing.T) {
	home := t.TempDir()
	setupFakeHost(t, home)
	useLegacyOmarchy(t) // the drop-in only exists on the pre-4.5 path
	seedCache(t, home)
	// make FindUnit discover the generic unit (hermetic: no real system units)
	service.SystemUnitDirs = nil
	t.Cleanup(func() { service.SystemUnitDirs = []string{"/etc/systemd/user", "/usr/lib/systemd/user"} })
	// setupFakeHost stubs an omarchy-fcitx5.service home unit; remove it so the
	// generic fcitx5.service below is the one FindUnit discovers (P1-4).
	os.Remove(filepath.Join(home, ".config", "systemd", "user", "omarchy-fcitx5.service"))
	unitFile := filepath.Join(home, ".config", "systemd", "user", "fcitx5.service")
	if err := os.MkdirAll(filepath.Dir(unitFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unitFile, []byte("[Service]\nExecStart=/usr/bin/fcitx5 --disable notificationitem --other-flag\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	opts, out := newTestOpts()
	if c := Install(catalog.DefaultDesired(), false, opts); c != ExitOK {
		t.Fatalf("install: %d\n%s", c, out)
	}
	got := tray.DropInPath(home, "fcitx5.service")
	b, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("drop-in must live under the discovered unit (%s): %v\n%s", got, err, out)
	}
	if _, err := os.Stat(tray.DropInPath(home, "omarchy-fcitx5.service")); err == nil {
		t.Error("drop-in written to a unit that does not exist on this host")
	}
	// derived from the real ExecStart: flag stripped, other flags preserved
	if !strings.Contains(string(b), "ExecStart=/usr/bin/fcitx5 --other-flag") {
		t.Errorf("ExecStart not derived from the unit file:\n%s", b)
	}
	if strings.Contains(string(b), "--disable notificationitem") {
		t.Errorf("notificationitem still disabled:\n%s", b)
	}
	// and L5 must consider it good
	if strings.Contains(out.String(), "[失败] L5 托盘 drop-in") {
		t.Errorf("L5 rejects a valid generic-unit drop-in:\n%s", out)
	}
}

// TestLayoutSwitchRemovesOrphanGrammar locks 评审 P1-5: dropping the double
// pinyin must delete the previous schema's grammar file (the old code only
// cleaned orphans when Model=false).
func TestLayoutSwitchRemovesOrphanGrammar(t *testing.T) {
	home := t.TempDir()
	setupFakeHost(t, home)
	seedCache(t, home)
	rimeDir := observe.DataDir()

	withDSP := catalog.Desired{Primary: "quanpin", Extra: []string{"zrm"}, Model: true, Channel: "stable"}
	o1, out1 := newTestOpts()
	if c := Install(withDSP, false, o1); c != ExitOK {
		t.Fatalf("dsp install: %d\n%s", c, out1)
	}
	orphanned := filepath.Join(rimeDir, "double_pinyin.custom.yaml")
	if _, err := os.Stat(orphanned); err != nil {
		t.Fatalf("precondition: grammar file should exist: %v", err)
	}

	o2, out2 := newTestOpts()
	if c := Install(catalog.DefaultDesired(), false, o2); c != ExitOK {
		t.Fatalf("install without dsp: %d\n%s", c, out2)
	}
	if _, err := os.Stat(orphanned); !os.IsNotExist(err) {
		t.Errorf("orphaned grammar left behind after dropping --dsp:\n%s", out2)
	}
	if !strings.Contains(out2.String(), "删除孤儿受管文件 double_pinyin.custom.yaml") {
		t.Errorf("orphan removal must be reported:\n%s", out2)
	}
	// rime_ice.custom.yaml is still desired and must survive
	if _, err := os.Stat(filepath.Join(rimeDir, "rime_ice.custom.yaml")); err != nil {
		t.Errorf("still-desired managed file was deleted: %v", err)
	}
}

// TestStatusJSON / TestDoctorJSON: the machine-readable face must parse and
// must carry the typed plan predicates.
func TestStatusJSON(t *testing.T) {
	home := t.TempDir()
	setupFakeHost(t, home)
	seedCache(t, home)
	o1, out1 := newTestOpts()
	if c := Install(catalog.DefaultDesired(), false, o1); c != ExitOK {
		t.Fatalf("install: %d\n%s", c, out1)
	}
	var buf bytes.Buffer
	opts := Options{Stdout: &buf, Stderr: &buf, Yes: true, JSON: true}
	if c := Status(opts); c != ExitOK {
		t.Fatalf("status exit=%d", c)
	}
	var rep StatusReport
	if err := json.Unmarshal(buf.Bytes(), &rep); err != nil {
		t.Fatalf("status --json is not valid JSON: %v\n%s", err, buf.String())
	}
	if rep.Desired.Primary != "quanpin" || !rep.Desired.Model {
		t.Errorf("desired wrong: %+v", rep.Desired)
	}
	if rep.Plan.NeedsApply {
		t.Errorf("converged host must report needsApply=false: %+v", rep.Plan)
	}
	if !rep.Host.BarIndicator || !rep.Host.ThemeOK || rep.Host.StaleTrayArtifacts {
		t.Errorf("host report wrong: %+v", rep.Host)
	}
	if rep.Version == "" {
		t.Error("version missing from the report")
	}
}

func TestDoctorJSON(t *testing.T) {
	home := t.TempDir()
	setupFakeHost(t, home)
	seedCache(t, home)
	o1, out1 := newTestOpts()
	if c := Install(catalog.DefaultDesired(), false, o1); c != ExitOK {
		t.Fatalf("install: %d\n%s", c, out1)
	}
	var buf bytes.Buffer
	opts := Options{Stdout: &buf, Stderr: &buf, Yes: true, JSON: true}
	code := Doctor(opts)
	var rep DoctorReport
	if err := json.Unmarshal(buf.Bytes(), &rep); err != nil {
		t.Fatalf("doctor --json is not valid JSON: %v\n%s", err, buf.String())
	}
	if len(rep.Checks) == 0 {
		t.Fatal("no checks in the doctor report")
	}
	if rep.OK != (code == ExitOK) {
		t.Errorf("report ok=%v but exit=%d", rep.OK, code)
	}
}

// TestInstallDryRunJSON: `install --dry-run --json` must emit a machine-readable
// plan (desired + per-layer predicates + steps) that an agent can gate on,
// with NO human dry-run text mixed in.
func TestInstallDryRunJSON(t *testing.T) {
	home := t.TempDir()
	setupFakeHost(t, home)
	var out, errb bytes.Buffer
	opts := Options{Stdout: &out, Stderr: &errb, Yes: true, DryRun: true, JSON: true}
	if c := Install(catalog.DefaultDesired(), false, opts); c != ExitOK {
		t.Fatalf("install --dry-run --json exit=%d\n%s", c, errb.String())
	}
	var rep DryRunReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("install --dry-run --json is not valid JSON: %v\n%s", err, out.String())
	}
	if rep.Tool != "ompinyin" || rep.Command != "install --dry-run" {
		t.Errorf("report meta wrong: %+v", rep)
	}
	if rep.Desired.Primary != "quanpin" || !rep.Desired.Model || rep.Desired.Channel != "stable" {
		t.Errorf("desired wrong: %+v", rep.Desired)
	}
	if !rep.Plan.NeedsApply {
		t.Errorf("fresh host must need apply: %+v", rep.Plan)
	}
	if len(rep.Plan.Steps) < 5 {
		t.Errorf("plan must carry L1..L5 steps, got %d", len(rep.Plan.Steps))
	}
	for _, s := range rep.Plan.Steps {
		if s.Layer == "" || s.Title == "" {
			t.Errorf("step missing fields: %+v", s)
		}
	}
	if strings.Contains(out.String(), "dry-run: no changes") {
		t.Errorf("json output should not include human dry-run text:\n%s", out.String())
	}
}

// TestUpdateDryRunJSON: `update --dry-run --json` must report the command name
// and a fresh-host plan that needs apply.
func TestUpdateDryRunJSON(t *testing.T) {
	home := t.TempDir()
	setupFakeHost(t, home)
	var out, errb bytes.Buffer
	opts := Options{Stdout: &out, Stderr: &errb, Yes: true, DryRun: true, JSON: true, Command: "update"}
	if c := Update(opts); c != ExitOK {
		t.Fatalf("update --dry-run --json exit=%d\n%s", c, errb.String())
	}
	var rep DryRunReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("update --dry-run --json not valid JSON: %v\n%s", err, out.String())
	}
	if rep.Command != "update --dry-run" {
		t.Errorf("command=%q, want update --dry-run", rep.Command)
	}
	if !rep.Plan.NeedsApply {
		t.Errorf("fresh host must need apply")
	}
}

// TestSwitchDryRunJSON: `switch --dsp zrm --dry-run --json` must report the
// switch command and the resulting desired.extra=[zrm] plan.
func TestSwitchDryRunJSON(t *testing.T) {
	home := t.TempDir()
	setupFakeHost(t, home)
	var out, errb bytes.Buffer
	opts := Options{Stdout: &out, Stderr: &errb, Yes: true, DryRun: true, JSON: true, Command: "switch"}
	if c := Switch(SwitchArgs{DSP: "zrm"}, opts); c != ExitOK {
		t.Fatalf("switch --dry-run --json exit=%d\n%s", c, errb.String())
	}
	var rep DryRunReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("switch --dry-run --json not valid JSON: %v\n%s", err, out.String())
	}
	if rep.Command != "switch --dry-run" {
		t.Errorf("command=%q, want switch --dry-run", rep.Command)
	}
	if len(rep.Desired.Extra) != 1 || rep.Desired.Extra[0] != "zrm" {
		t.Errorf("desired.extra=%v, want [zrm]", rep.Desired.Extra)
	}
}

// TestUpdateSelfDryRun: `update --self --dry-run` must report the self-upgrade
// plan without touching the network (selfup.Version is stubbed).
func TestUpdateSelfDryRun(t *testing.T) {
	home := t.TempDir()
	setupFakeHost(t, home)
	seedCache(t, home)
	origV := selfup.Version
	defer func() { selfup.Version = origV }()
	selfup.Version = func() (string, error) { return "v1.9.9", nil }

	var out, errb bytes.Buffer
	opts := Options{Stdout: &out, Stderr: &errb, Yes: true, DryRun: true, Self: true}
	if c := Update(opts); c != ExitOK {
		t.Fatalf("update --self --dry-run exit=%d\n%s", c, errb.String())
	}
	if !strings.Contains(out.String(), "自升级") {
		t.Errorf("self dry-run not reported:\n%s", out.String())
	}
}

// TestThemeOwnershipProtocol: a hand-edited classicui.conf must trip the §5.1
// protocol (refusal aborts, consent overwrites after backup) exactly like a
// hand-edited rime managed file.
func TestThemeOwnershipProtocol(t *testing.T) {
	home := t.TempDir()
	setupFakeHost(t, home)
	seedCache(t, home)

	opts, out := newTestOpts()
	if c := Install(catalog.DefaultDesired(), false, opts); c != ExitOK {
		t.Fatalf("first install exit=%d\n%s", c, out.String())
	}

	// user hand-edits classicui.conf
	confPath := theme.ConfPath(home)
	if err := os.WriteFile(confPath, []byte("Theme=default-dark\nFont=Sans 10\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// refusal (empty stdin, no --yes) must abort with exit 1
	abort := Options{Stdout: out, Stderr: out, Stdin: strings.NewReader("n\n")}
	if c := Install(catalog.DefaultDesired(), false, abort); c != ExitExecFail {
		t.Fatalf("refused overwrite exit=%d, want 1\n%s", c, out.String())
	}
	if b, _ := os.ReadFile(confPath); string(b) != "Theme=default-dark\nFont=Sans 10\n" {
		t.Error("user edit was clobbered without consent")
	}

	// consent (--yes) overwrites after backup
	opts2, out2 := newTestOpts()
	if c := Install(catalog.DefaultDesired(), false, opts2); c != ExitOK {
		t.Fatalf("consented install exit=%d\n%s", c, out2.String())
	}
	b, _ := os.ReadFile(confPath)
	if !strings.Contains(string(b), "Theme=omarchy") {
		t.Errorf("classicui.conf not restored:\n%s", b)
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".local", "state", "ompinyin", "backup-*", ".config", "fcitx5", "conf", "classicui.conf"))
	if len(matches) == 0 {
		t.Fatal("no backup of user-edited classicui.conf")
	}
	if b, _ := os.ReadFile(matches[len(matches)-1]); string(b) != "Theme=default-dark\nFont=Sans 10\n" {
		t.Errorf("backup content wrong: %s", b)
	}
}

// TestUninstallRemovesThemeFootprint: uninstall must delete classicui.conf and
// retire the pre-4.5 artifacts (hook + our two SVGs) while leaving Omarchy's
// generated theme in place — the theme directory is no longer ours.
func TestUninstallRemovesThemeFootprint(t *testing.T) {
	home := t.TempDir()
	h := setupFakeHost(t, home)
	seedCache(t, home)

	opts, out := newTestOpts()
	if c := Install(catalog.DefaultDesired(), false, opts); c != ExitOK {
		t.Fatalf("install exit=%d\n%s", c, out.String())
	}
	// a host upgraded from <=1.3 still carries the hook and the SVGs
	themeDir := filepath.Join(home, ".local", "share", "fcitx5", "themes", "omarchy")
	if err := os.MkdirAll(themeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := []string{
		theme.HookPath(home),
		filepath.Join(themeDir, "background.svg"),
		filepath.Join(themeDir, "highlight.svg"),
	}
	if err := os.MkdirAll(filepath.Dir(theme.HookPath(home)), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range append(legacy, filepath.Join(themeDir, "theme.conf")) {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(theme.ConfPath(home)); err != nil {
		t.Fatalf("precondition: classicui.conf missing: %v", err)
	}

	if c := Uninstall(opts); c != ExitOK {
		t.Fatalf("uninstall exit=%d\n%s", c, out.String())
	}
	if _, err := os.Stat(theme.ConfPath(home)); !os.IsNotExist(err) {
		t.Errorf("classicui.conf left behind")
	}
	for _, p := range legacy {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("pre-4.5 artifact %s left behind", p)
		}
	}
	if _, err := os.Stat(filepath.Join(themeDir, "theme.conf")); err != nil {
		t.Errorf("Omarchy's own theme.conf must not be deleted: %v", err)
	}
	if _, err := os.Stat(state.Path()); !os.IsNotExist(err) {
		t.Error("state.json not removed by uninstall")
	}
	// a running fcitx5 must be told to drop the theme (would otherwise keep it
	// in memory until restart)
	if h.themeReload == 0 {
		t.Error("uninstall must hot-reload fcitx5 to drop the themed candidate window")
	}
}

// TestInstallX11HiDPIDiagnosticsOnlyByDefault: a default install must NOT
// touch any global XWayland resource — no ~/.Xresources file, no user units.
func TestInstallX11HiDPIDiagnosticsOnlyByDefault(t *testing.T) {
	home := t.TempDir()
	h := setupFakeHost(t, home)
	seedCache(t, home)

	opts, out := newTestOpts()
	if code := Install(catalog.DefaultDesired(), false, opts); code != ExitOK {
		t.Fatalf("install exit=%d\noutput:\n%s", code, out.String())
	}
	if _, err := os.Stat(hidpi.XresourcesPath(home)); err == nil {
		t.Error("default install must not create ~/.Xresources")
	}
	for _, p := range []string{hidpi.ServicePath(home), hidpi.PathPath(home)} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("default install must not install %s", p)
		}
	}
	if len(h.xrdb) != 0 {
		t.Errorf("default install must not publish RESOURCE_MANAGER, got %q", h.xrdb)
	}
}

// TestInstallX11HiDPIOptInConvergesAndOptOutUndoes: the full opt-in /
// opt-out cycle. Opt-in writes the managed block and the two units and
// publishes the value; a re-run is a no-op; --no-x11-hidpi withdraws all
// three artifacts again.
func TestInstallX11HiDPIOptInConvergesAndOptOutUndoes(t *testing.T) {
	home := t.TempDir()
	h := setupFakeHost(t, home)
	seedCache(t, home)

	opts, out := newTestOpts()
	d := catalog.DefaultDesired()
	d.X11HiDPI = true

	if code := Install(d, false, opts); code != ExitOK {
		t.Fatalf("opt-in install exit=%d\noutput:\n%s", code, out.String())
	}
	b, err := os.ReadFile(hidpi.XresourcesPath(home))
	if err != nil {
		t.Fatalf("managed Xresources missing: %v", err)
	}
	if dpi, ok := hidpi.ParseXftDPI(b); !ok || dpi != 192 {
		t.Errorf("managed Xft.dpi=%d,%v, want 192 (scale 2.0)", dpi, ok)
	}
	for _, p := range []string{hidpi.ServicePath(home), hidpi.PathPath(home)} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("unit %s not installed: %v", p, err)
		}
	}
	if !bytes.Contains(h.xrdb, []byte("Xft.dpi: 192")) {
		t.Errorf("published RESOURCE_MANAGER missing Xft.dpi: %q", h.xrdb)
	}
	if st, err := state.Load(); err != nil || !st.Desired.X11HiDPI {
		t.Errorf("opt-in must be recorded in state.json (err=%v)", err)
	}

	// re-run must converge to a no-op (idempotent, one managed block)
	opts2, out2 := newTestOpts()
	if code := Install(d, false, opts2); code != ExitOK {
		t.Fatalf("re-run exit=%d\noutput:\n%s", code, out2.String())
	}
	b2, _ := os.ReadFile(hidpi.XresourcesPath(home))
	if dpi, ok := hidpi.ParseXftDPI(b2); !ok || dpi != 192 {
		t.Errorf("re-run broke Xft.dpi=%d,%v", dpi, ok)
	}

	// opt-out withdraws everything ompinyin owned
	opts3, out3 := newTestOpts()
	d3 := catalog.DefaultDesired() // X11HiDPI = false
	if code := Install(d3, false, opts3); code != ExitOK {
		t.Fatalf("opt-out install exit=%d\noutput:\n%s", code, out3.String())
	}
	if _, err := os.Stat(hidpi.XresourcesPath(home)); err == nil {
		t.Error("~/.Xresources should be removed after opt-out (only the managed block was in it)")
	}
	for _, p := range []string{hidpi.ServicePath(home), hidpi.PathPath(home)} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("unit %s still present after opt-out", p)
		}
	}
	if st, err := state.Load(); err != nil || st.Desired.X11HiDPI {
		t.Errorf("opt-out must clear the recorded mode (err=%v)", err)
	}
}

// TestApplyX11HiDPINoOpWithoutOptIn: the systemd hook must never publish the
// global resource when the mode is not recorded as opted-in (stale enable
// link or manual re-enable must not start writing behind the user's back).
func TestApplyX11HiDPINoOpWithoutOptIn(t *testing.T) {
	home := t.TempDir()
	h := setupFakeHost(t, home)
	seedCache(t, home)

	opts, _ := newTestOpts()
	if code := Install(catalog.DefaultDesired(), false, opts); code != ExitOK {
		t.Fatalf("default install exit=%d", code)
	}
	if _, err := os.Stat(hidpi.XresourcesPath(home)); err == nil {
		t.Fatal("precondition broken: default install created Xresources")
	}
	opts2, out2 := newTestOpts()
	if code := ApplyX11HiDPI(opts2); code != ExitOK {
		t.Fatalf("ApplyX11HiDPI exit=%d\n%s", code, out2.String())
	}
	if _, err := os.Stat(hidpi.XresourcesPath(home)); err == nil {
		t.Error("ApplyX11HiDPI must not write Xresources when not opted in")
	}
	if len(h.xrdb) != 0 {
		t.Errorf("ApplyX11HiDPI must not publish RESOURCE_MANAGER when not opted in: %q", h.xrdb)
	}
}

// TestInstallX11HiDPIFirstOptInPublishesWhenXrdbAppearsMidRun: on a host where
// xorg-xrdb is missing at observe time, L1 installs it and the publish must not
// be gated on the stale X11Available=false snapshot. That snapshot made the very
// first `install --x11-hidpi` skip the publish and then fail L5 with
// "期望=192 实际=0", needing a second run.
func TestInstallX11HiDPIFirstOptInPublishesWhenXrdbAppearsMidRun(t *testing.T) {
	home := t.TempDir()
	h := setupFakeHost(t, home)
	seedCache(t, home)

	// Only the first `xrdb -query` (the pre-L1 observe) fails, as if the binary
	// were installed by L1; everything afterwards succeeds.
	queries := 0
	hidpi.Run = func(name string, args ...string) ([]byte, error) {
		switch {
		case name == "hyprctl":
			return []byte(`[{"focused":true,"scale":2}]`), nil
		case name == "xrdb" && len(args) >= 1 && args[0] == "-query":
			queries++
			if queries == 1 {
				return nil, errors.New("xrdb: command not found")
			}
			return h.xrdb, nil
		case name == "xrdb" && len(args) == 2 && args[0] == "-merge":
			if b, err := os.ReadFile(args[1]); err == nil {
				h.xrdb = b
			}
			return nil, nil
		}
		return nil, nil
	}

	opts, out := newTestOpts()
	d := catalog.DefaultDesired()
	d.X11HiDPI = true
	if code := Install(d, false, opts); code != ExitOK {
		t.Fatalf("first opt-in exit=%d (must publish, not fail L5)\noutput:\n%s", code, out.String())
	}
	if !bytes.Contains(h.xrdb, []byte("Xft.dpi: 192")) {
		t.Errorf("first opt-in must publish Xft.dpi=192, got %q", h.xrdb)
	}
}

// TestInstallX11HiDPIRecordsIntentBeforeL5: when the opt-in applies its
// artifacts but a later read-only check (L5) fails, the intent must already be
// on disk. Otherwise the next bare `install` (whose baseline is state.json)
// would read X11HiDPI=false and silently withdraw what the user asked for.
func TestInstallX11HiDPIRecordsIntentBeforeL5(t *testing.T) {
	home := t.TempDir()
	setupFakeHost(t, home)
	seedCache(t, home)

	// `xrdb -merge` is a no-op and `-query` never reports Xft.dpi, so L5 must
	// detect 期望=192 / 实际=0 and fail — while the block + units are on disk.
	hidpi.Run = func(name string, args ...string) ([]byte, error) {
		if name == "hyprctl" {
			return []byte(`[{"focused":true,"scale":2}]`), nil
		}
		return []byte{}, nil
	}

	opts, out := newTestOpts()
	d := catalog.DefaultDesired()
	d.X11HiDPI = true
	if code := Install(d, false, opts); code == ExitOK {
		t.Fatalf("expected L5 to fail with the broken fake\noutput:\n%s", out.String())
	}
	st, err := state.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if !st.Desired.X11HiDPI {
		t.Fatal("opt-in intent must be persisted before L5 so the next bare install does not tear it down")
	}
	b, err := os.ReadFile(hidpi.XresourcesPath(home))
	if err != nil || !hidpi.ManagedBlockPresent(string(b)) {
		t.Fatalf("artifacts must remain after the failed run (err=%v)", err)
	}
}

// TestInstallWithdrawsStaleHidpiUnits: a leftover unit from an older build (or
// a half-removed one) has no matching managed block and stale content. The undo
// predicate is presence-based, so it must still be withdrawn.
func TestInstallWithdrawsStaleHidpiUnits(t *testing.T) {
	home := t.TempDir()
	setupFakeHost(t, home)
	seedCache(t, home)

	if err := os.MkdirAll(hidpi.UnitDir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hidpi.PathPath(home), []byte("[Unit]\nDescription=old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	opts, out := newTestOpts()
	if code := Install(catalog.DefaultDesired(), false, opts); code != ExitOK {
		t.Fatalf("install exit=%d\noutput:\n%s", code, out.String())
	}
	if _, err := os.Stat(hidpi.PathPath(home)); err == nil {
		t.Error("a stale publisher unit must be withdrawn on a default install")
	}
}

// TestInstallX11HiDPIDryRunDoesNotPersistIntent guards the ordering: intent is
// recorded after the dry-run guard, so `--dry-run` remains side-effect free.
func TestInstallX11HiDPIDryRunDoesNotPersistIntent(t *testing.T) {
	home := t.TempDir()
	setupFakeHost(t, home)
	seedCache(t, home)

	opts, out := newTestOpts()
	opts.DryRun = true
	d := catalog.DefaultDesired()
	d.X11HiDPI = true
	if code := Install(d, false, opts); code != ExitOK {
		t.Fatalf("dry-run exit=%d\n%s", code, out.String())
	}
	st, err := state.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Desired.X11HiDPI {
		t.Error("--dry-run must not persist the opt-in intent")
	}
	if _, err := os.Stat(hidpi.XresourcesPath(home)); err == nil {
		t.Error("--dry-run must not write ~/.Xresources")
	}
}

// TestInstallX11HiDPISkipsWhenCompositorScales: with
// xwayland:force_zero_scaling=false Hyprland scales X11 windows itself, so an
// opt-in must stay inert — no block, no units, no global Xft.dpi (which would
// double-scale every X11 client).
func TestInstallX11HiDPISkipsWhenCompositorScales(t *testing.T) {
	home := t.TempDir()
	h := setupFakeHost(t, home)
	h.forceZero = false
	seedCache(t, home)

	opts, out := newTestOpts()
	d := catalog.DefaultDesired()
	d.X11HiDPI = true
	if code := Install(d, false, opts); code != ExitOK {
		t.Fatalf("install exit=%d\noutput:\n%s", code, out.String())
	}
	if _, err := os.Stat(hidpi.XresourcesPath(home)); err == nil {
		t.Error("must not write ~/.Xresources when the compositor scales X11")
	}
	for _, p := range []string{hidpi.ServicePath(home), hidpi.PathPath(home)} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("must not install %s when the compositor scales X11", p)
		}
	}
	if len(h.xrdb) != 0 {
		t.Errorf("must not publish RESOURCE_MANAGER when the compositor scales X11: %q", h.xrdb)
	}
}

// writeLocalRimeIce seeds a --mirror <dir> with the rime-ice archive name. The
// zip is padded past the 1 MiB shape gate (with a stored entry, so compression
// cannot shrink it back) so the offline path exercises the same integrity
// checks as a real download. Callers use Model=false: the 420MB wanxiang gram
// deliberately fails that size gate in a fixture.
func writeLocalRimeIce(t *testing.T, dir string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"default.yaml", "rime_ice.schema.yaml"} {
		f, _ := zw.Create(name)
		f.Write([]byte("# rime-ice data: " + name + "\n"))
	}
	big, _ := zw.CreateHeader(&zip.FileHeader{Name: "pad.bin", Method: zip.Store})
	big.Write(bytes.Repeat([]byte("x"), 2<<20))
	zw.Close()
	for _, name := range []string{"rime-ice-full-stable.zip", "rime-ice-full-nightly.zip"} {
		if err := os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestInstallRepairsDisabledDropIn locks invariant 19: a drop-in that EXISTS
// but still disables notificationitem must be rewritten. The execution path
// used to gate on file existence while the plan and L5 gated on content, so a
// stale drop-in was reported as work, then skipped — and no number of installs
// could repair it.
func TestInstallRepairsDisabledDropIn(t *testing.T) {
	home := t.TempDir()
	setupFakeHost(t, home)
	useLegacyOmarchy(t) // the drop-in only exists on the pre-4.5 path
	seedCache(t, home)

	o1, out1 := newTestOpts()
	if c := Install(catalog.DefaultDesired(), false, o1); c != ExitOK {
		t.Fatalf("install: %d\n%s", c, out1)
	}
	p := tray.DropInPath(home, "omarchy-fcitx5.service")
	if err := os.WriteFile(p, []byte("[Service]\nExecStart=/usr/bin/fcitx5 --disable notificationitem\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	o2, out2 := newTestOpts()
	if c := Install(catalog.DefaultDesired(), false, o2); c != ExitOK {
		t.Fatalf("install must repair a disabled drop-in, exit=%d\n%s", c, out2)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !tray.DropInEnabled(string(b)) {
		t.Errorf("drop-in not repaired:\n%s", b)
	}
}

// TestInstallRestartsStoppedService: "fcitx5 is running" is part of the L4
// terminal state. A host whose unit was stopped by hand has no other layer's
// work, so the stop window — the only place service.Start runs — used to stay
// closed and L5 failed forever.
func TestInstallRestartsStoppedService(t *testing.T) {
	home := t.TempDir()
	h := setupFakeHost(t, home)
	seedCache(t, home)

	o1, out1 := newTestOpts()
	if c := Install(catalog.DefaultDesired(), false, o1); c != ExitOK {
		t.Fatalf("install: %d\n%s", c, out1)
	}
	h.unitActive = false // user stopped fcitx5 (or it crashed)

	o2, out2 := newTestOpts()
	if c := Install(catalog.DefaultDesired(), false, o2); c != ExitOK {
		t.Fatalf("install must restart the unit, exit=%d\n%s", c, out2)
	}
	if !h.unitActive {
		t.Error("fcitx5 left STOPPED — user has no input method")
	}
}

// TestInstallClearsStrayBeforeStart locks the reported false-success: a unit
// that is down (or flapping under Restart=always) while an unmanaged fcitx5
// holds org.fcitx.Fcitx5. A bare start only spawns another instance that exits
// 0 at once; `systemctl start` still returns 0, so install printed
// "[完成] L4 start" and exited 0 while doctor showed the service down. install
// must end the stray instead.
func TestInstallClearsStrayBeforeStart(t *testing.T) {
	for _, tc := range []struct {
		name        string
		unitActive  bool // what `systemctl is-active` reports at plan time
		wantPlanSay string
	}{
		{"unit down, stray owns the bus name", false, "先清理再启动"},
		{"transient active from the Restart=always flap", true, "抖动"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			h := setupFakeHost(t, home)
			seedCache(t, home)

			o1, out1 := newTestOpts()
			if c := Install(catalog.DefaultDesired(), false, o1); c != ExitOK {
				t.Fatalf("install: %d\n%s", c, out1)
			}

			// A D-Bus-activated stray takes the bus name; the unit's own instances
			// exit immediately, so Restart=always loops it and `is-active` reads
			// active for the few hundred ms one lives (hence count 2).
			h.unitActive = tc.unitActive
			stray := true
			service.FcitxRunning = func() bool { return stray }
			service.FcitxCount = func() int {
				if stray {
					return 2 // doomed unit instance + stray
				}
				return 1
			}
			killed := 0
			service.KillStray = func() error {
				killed++
				stray = false
				return nil
			}

			o2, out2 := newTestOpts()
			if c := Install(catalog.DefaultDesired(), false, o2); c != ExitOK {
				t.Fatalf("install must clear the stray and converge, exit=%d\n%s", c, out2)
			}
			if killed == 0 {
				t.Errorf("install reported success without ending the stray:\n%s", out2)
			}
			if !h.unitActive {
				t.Error("fcitx5 left STOPPED — user has no input method")
			}
			if !strings.Contains(out2.String(), tc.wantPlanSay) {
				t.Errorf("plan must tell the user what happens (%q):\n%s", tc.wantPlanSay, out2)
			}
		})
	}
}

// TestUpdateRepinsStableTag locks the documented contract that `update` is the
// entry point which re-pins the stable channel to the newest release. It used
// to keep serving the previously recorded tag forever.
func TestUpdateRepinsStableTag(t *testing.T) {
	home := t.TempDir()
	setupFakeHost(t, home)
	local := t.TempDir()
	writeLocalRimeIce(t, local)
	assets.ResolveStableTag = func(context.Context) string { return "2026.06.30" }

	o1, out1 := newTestOpts()
	o1.LocalDir = local
	o1.MirrorSource = catalog.MirrorChina
	d0 := catalog.DefaultDesired()
	d0.Model = false // the fixture gram is not 420MB; this test is about the tag
	if c := Install(d0, false, o1); c != ExitOK {
		t.Fatalf("install: %d\n%s", c, out1)
	}
	st, err := state.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Assets["rime_ice"].Tag != "2026.06.30" {
		t.Fatalf("precondition: tag=%q", st.Assets["rime_ice"].Tag)
	}

	// upstream publishes a newer stable release
	assets.ResolveStableTag = func(context.Context) string { return "2099.01.01" }
	o2, out2 := newTestOpts()
	o2.LocalDir = local
	o2.MirrorSource = catalog.MirrorChina
	if c := Update(o2); c != ExitOK {
		t.Fatalf("update: %d\n%s", c, out2)
	}
	st, err = state.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Assets["rime_ice"].Tag != "2099.01.01" {
		t.Errorf("update did not re-pin the stable tag: got %q\n%s", st.Assets["rime_ice"].Tag, out2)
	}
}

// TestChannelSwitchNightlyToStableLedgerCheck locks the hint/sha pairing: the
// recorded sha256 belongs to the PREVIOUS tag, so it must not be compared
// under the newly resolved tag. The old code hard-failed an immutable-tag
// mismatch on a plain channel switch (and on the CN flow where the first
// install fell back to releases/latest while the API was blocked).
func TestChannelSwitchNightlyToStableLedgerCheck(t *testing.T) {
	home := t.TempDir()
	setupFakeHost(t, home)
	local := t.TempDir()
	writeLocalRimeIce(t, local)
	seedCache(t, home)

	o1, out1 := newTestOpts()
	o1.LocalDir = local
	o1.MirrorSource = catalog.MirrorChina
	d1 := catalog.DefaultDesired()
	d1.Channel = "nightly"
	d1.Model = false // the fixture gram is not 420MB
	if c := Install(d1, false, o1); c != ExitOK {
		t.Fatalf("nightly install: %d\n%s", c, out1)
	}

	assets.ResolveStableTag = func(context.Context) string { return "2026.07.15" }
	o2, out2 := newTestOpts()
	o2.LocalDir = local
	o2.MirrorSource = catalog.MirrorChina
	d2 := catalog.DefaultDesired()
	d2.Model = false
	if c := Install(d2, false, o2); c != ExitOK {
		t.Fatalf("nightly→stable must not trip the immutable-tag check: %d\n%s", c, out2)
	}
	st, err := state.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Assets["rime_ice"].Tag != "2026.07.15" {
		t.Errorf("tag=%q, want 2026.07.15", st.Assets["rime_ice"].Tag)
	}
}

// TestUninstallBacksUpBeforeDeleting: the destructive path must snapshot what
// it deletes. state.json records the Desired the user chose (here an opted-in
// double pinyin); an uninstall that drops it silently is exactly the loss a
// reinstall cannot recover.
func TestUninstallBacksUpBeforeDeleting(t *testing.T) {
	home := t.TempDir()
	setupFakeHost(t, home)
	seedCache(t, home)

	d := catalog.DefaultDesired()
	d.Extra = []string{"zrm"}
	opts, out := newTestOpts()
	if c := Install(d, false, opts); c != ExitOK {
		t.Fatalf("install exit=%d\n%s", c, out)
	}
	if c := Uninstall(opts); c != ExitOK {
		t.Fatalf("uninstall exit=%d\n%s", c, out)
	}

	matches, _ := filepath.Glob(filepath.Join(state.Dir(), "backup-*"))
	if len(matches) == 0 {
		t.Fatal("uninstall created no backup")
	}
	var foundState, foundManaged bool
	for _, dir := range matches {
		if _, err := os.Stat(filepath.Join(dir, ".local", "state", "ompinyin", "state.json")); err == nil {
			foundState = true
		}
		if _, err := os.Stat(filepath.Join(dir, ".local", "share", "fcitx5", "rime", "double_pinyin.custom.yaml")); err == nil {
			foundManaged = true
		}
	}
	if !foundState {
		t.Error("state.json (the recorded Desired) was not backed up before removal")
	}
	if !foundManaged {
		t.Error("managed files were not backed up before removal")
	}
}

// TestApplyX11HiDPIResolvesSessionEnvFromManager pins the login-time root cause
// (thinkbook-omarchy, 2026-11-03): the publisher unit is pulled in by
// graphical-session.target, which on uwsm/Omarchy is reached seconds before the
// compositor imports DISPLAY into the systemd user manager. systemd freezes the
// unit's environment at start, so the unit runs with no DISPLAY, `xrdb -query`
// fails with "Can't open display ”", and the publish used to be skipped for the
// whole session (the path unit only watches monitors.lua). The value must come
// from the manager, not from inheritance.
func TestApplyX11HiDPIResolvesSessionEnvFromManager(t *testing.T) {
	home := t.TempDir()
	h := setupFakeHost(t, home)
	seedCache(t, home)

	opts, _ := newTestOpts()
	d := catalog.DefaultDesired()
	d.X11HiDPI = true
	if code := Install(d, false, opts); code != ExitOK {
		t.Fatalf("opt-in install exit=%d", code)
	}

	// Now make the process look exactly like the login unit: no session env, and
	// an X server that is only reachable once DISPLAY has been resolved.
	t.Setenv("DISPLAY", "")
	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "")
	h.xrdb = nil
	hidpi.ShowEnvironment = func() ([]byte, error) {
		return []byte("DISPLAY=:7\nHYPRLAND_INSTANCE_SIGNATURE=sig7\n"), nil
	}
	hidpi.Run = func(name string, args ...string) ([]byte, error) {
		switch {
		case name == "systemctl":
			return nil, nil
		case name == "hyprctl" && len(args) >= 1 && args[0] == "getoption":
			if os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") == "" {
				return nil, errors.New("HYPRLAND_INSTANCE_SIGNATURE not set!")
			}
			return []byte("bool: true\nset: true\n"), nil
		case name == "hyprctl":
			return []byte(`[{"focused":true,"scale":2}]`), nil
		case name == "xrdb" && len(args) >= 1 && args[0] == "-query":
			if os.Getenv("DISPLAY") == "" {
				return nil, errors.New(`xrdb: Can't open display ''`)
			}
			return h.xrdb, nil
		case name == "xrdb" && len(args) == 2 && args[0] == "-merge":
			if b, err := os.ReadFile(args[1]); err == nil {
				h.xrdb = b
			}
			return nil, nil
		}
		return nil, nil
	}

	opts2, out2 := newTestOpts()
	if code := ApplyX11HiDPI(opts2); code != ExitOK {
		t.Fatalf("ApplyX11HiDPI exit=%d\n%s", code, out2.String())
	}
	if got := os.Getenv("DISPLAY"); got != ":7" {
		t.Errorf("DISPLAY=%q, want :7 imported from the manager", got)
	}
	if got := os.Getenv("HYPRLAND_INSTANCE_SIGNATURE"); got != "sig7" {
		t.Errorf("HYPRLAND_INSTANCE_SIGNATURE=%q, want sig7 (hyprctl needs it)", got)
	}
	if !bytes.Contains(h.xrdb, []byte("Xft.dpi: 192")) {
		t.Errorf("must publish Xft.dpi=192 once DISPLAY is resolved, got %q", h.xrdb)
	}
	if !strings.Contains(out2.String(), "补全会话环境") {
		t.Errorf("the import must be reported, not silent:\n%s", out2.String())
	}
}

// TestInstallConvergesLegacyGroupAndAltTrigger: a host from the old default
// (two IMs in the opposite order, fcitx5's shipped AltTriggerKeys=Shift_L) must
// converge to the declared group shape + an EMPTY AltTriggerKeys section.
// Otherwise left Shift fires the same toggle() as Ctrl+Space instead of
// switching 中英 inside rime (真机 2026-11-03 的症状).
func TestInstallConvergesLegacyGroupAndAltTrigger(t *testing.T) {
	home := t.TempDir()
	setupFakeHost(t, home)
	useLegacyOmarchy(t) // the AltTrigger/trigger-key write path is pre-4.5 only
	seedCache(t, home)

	// legacy profile: keyboard-us first, rime second (what EnsureRime produced).
	if err := os.MkdirAll(filepath.Dir(observe.ProfilePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	legacyProfile := "[Groups/0]\nName=Default\nDefault Layout=us\nDefaultIM=rime\n\n" +
		"[Groups/0/Items/0]\nName=keyboard-us\nLayout=\n\n" +
		"[Groups/0/Items/1]\nName=rime\nLayout=\n\n[GroupOrder]\n0=Groups/0\n"
	if err := os.WriteFile(observe.ProfilePath(), []byte(legacyProfile), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(observe.ConfigPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	legacyConfig := "[Hotkey]\nModifierOnlyKeyTimeout=-1\n\n[Hotkey/TriggerKeys]\n0=Control+space\n\n" +
		"[Hotkey/AltTriggerKeys]\n0=Shift_L\n"
	if err := os.WriteFile(observe.ConfigPath(), []byte(legacyConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	opts, out := newTestOpts()
	if code := Install(catalog.DefaultDesired(), false, opts); code != ExitOK {
		t.Fatalf("install exit=%d\noutput:\n%s", code, out.String())
	}
	pb, _ := os.ReadFile(observe.ProfilePath())
	if got := profile.GroupIMs(string(pb)); !catalog.SameIMs(got, catalog.DefaultIMs()) {
		t.Errorf("group shape = %v; want %v\n%s", got, catalog.DefaultIMs(), pb)
	}
	cb, _ := os.ReadFile(observe.ConfigPath())
	if keys := hotkey.AltTriggerKeys(string(cb)); len(keys) != 0 {
		t.Errorf("AltTriggerKeys = %v; want empty\n%s", keys, cb)
	}
	if !hotkey.HasEmptyAltTrigger(string(cb)) {
		t.Errorf("empty AltTriggerKeys section missing:\n%s", cb)
	}
	if !strings.Contains(string(cb), "0=Control+space") {
		t.Errorf("trigger keys lost:\n%s", cb)
	}

	// A second run is a no-op: no drift, no new backup (§16.13).
	matches, _ := filepath.Glob(filepath.Join(home, ".local", "state", "ompinyin", "backup-*"))
	before := len(matches)
	opts2, out2 := newTestOpts()
	if code := Install(catalog.DefaultDesired(), false, opts2); code != ExitOK {
		t.Fatalf("re-run exit=%d\noutput:\n%s", code, out2.String())
	}
	matches, _ = filepath.Glob(filepath.Join(home, ".local", "state", "ompinyin", "backup-*"))
	if len(matches) != before {
		t.Errorf("converged re-run created a backup directory (before=%d after=%d)", before, len(matches))
	}
}

// TestInstallUpgradesFortyToFortyFive is the 4.5 migration fixture (§8.5): a
// host that ran ompinyin on Omarchy 4.0 (4.5 is a direct successor) and then
// old path created is on disk — notificationitem drop-in, `Fcitx` pin, theme-set
// hook + background.svg, Control+space trigger, the group order old versions
// wrote — and Omarchy's own config has no [Hotkey/AltTriggerKeys] section. One
// convergence must reach the 4.5 terminal state and the second must be a no-op.
func TestInstallUpgradesFortyToFortyFive(t *testing.T) {
	home := t.TempDir()
	h := setupFakeHost(t, home)
	seedCache(t, home)
	d := catalog.DefaultDesired()

	// --- the pre-4.5 footprint -------------------------------------------
	if _, err := tray.WriteDropIn(home, "omarchy-fcitx5.service", tray.DropInContent); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(tray.ShellJSONPath(home)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := tray.SetPinned(tray.ShellJSONPath(home), []string{"Fcitx"}); err != nil {
		t.Fatal(err)
	}
	hook := theme.HookPath(home)
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/usr/bin/env bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	themeDir := filepath.Join(home, ".local", "share", "fcitx5", "themes", "omarchy")
	if err := os.MkdirAll(themeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	svg := filepath.Join(themeDir, "background.svg")
	if err := os.WriteFile(svg, []byte("<svg/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Omarchy 4.5's own config: no trigger keys of ours, and NO AltTriggerKeys
	// section (its migration fills that in; ompinyin must not create it).
	if err := os.MkdirAll(filepath.Dir(observe.ConfigPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(observe.ConfigPath(), []byte("[Hotkey/TriggerKeys]\n0=Control+space\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// the profile shape old versions wrote: keyboard-us first
	legacyProfile := "[Groups/0]\nName=Default\nDefault Layout=us\nDefaultIM=rime\n\n" +
		"[Groups/0/Items/0]\nName=keyboard-us\nLayout=\n\n" +
		"[Groups/0/Items/1]\nName=rime\nLayout=\n\n[GroupOrder]\n0=Groups/0\n"
	if err := os.MkdirAll(filepath.Dir(observe.ProfilePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(observe.ProfilePath(), []byte(legacyProfile), 0o644); err != nil {
		t.Fatal(err)
	}

	// the dry-run must announce the migration and the hotkey hand-over
	var out, errb bytes.Buffer
	dry := Options{Stdout: &out, Stderr: &errb, Yes: true, DryRun: true}
	if c := Install(d, false, dry); c != ExitOK {
		t.Fatalf("dry-run exit=%d\n%s", c, errb.String())
	}
	if !strings.Contains(out.String(), "清理旧 drop-in/pin") || !strings.Contains(out.String(), "Control+space") {
		t.Errorf("dry-run must announce the cleanup:\n%s", out.String())
	}
	if len(backupDirs(t, home)) != 0 {
		t.Fatal("--dry-run must not create a backup")
	}

	// --- one real convergence --------------------------------------------
	opts, log := newTestOpts()
	if c := Install(d, false, opts); c != ExitOK {
		t.Fatalf("upgrade install exit=%d\n%s", c, log.String())
	}

	// old artifacts are gone
	if _, err := os.Stat(tray.DropInPath(home, "omarchy-fcitx5.service")); !os.IsNotExist(err) {
		t.Error("notificationitem drop-in survived the upgrade")
	}
	sj, _ := os.ReadFile(tray.ShellJSONPath(home))
	if pinned, _ := tray.ReadPinned(sj); tray.HasPin(pinned) {
		t.Errorf("Fcitx pin survived: %v", pinned)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "systemd", "user", "omarchy-fcitx5.service.d")); !os.IsNotExist(err) {
		t.Error("the emptied .d directory left behind")
	}
	if _, err := os.Stat(hook); !os.IsNotExist(err) {
		t.Error("theme-set hook survived the upgrade")
	}
	if _, err := os.Stat(svg); !os.IsNotExist(err) {
		t.Error("our generated background.svg survived the upgrade")
	}
	if !bar.IndicatorPresent(sj) {
		t.Errorf("bar indicator not put on an upgrading host:\n%s", sj)
	}

	// Control+space is gone; the empty AltTriggerKeys section must NOT exist
	cb, _ := os.ReadFile(observe.ConfigPath())
	if strings.Contains(strings.ToLower(string(cb)), "control+space") {
		t.Errorf("Control+space survived:\n%s", cb)
	}
	if hotkey.HasEmptyAltTrigger(string(cb)) || strings.Contains(string(cb), "AltTriggerKeys") {
		t.Errorf("ompiyin must not create [Hotkey/AltTriggerKeys] (Omarchy owns it):\n%s", cb)
	}

	// the group is the declared one, in declaration order
	pb, _ := os.ReadFile(observe.ProfilePath())
	if got := profile.GroupIMs(string(pb)); !catalog.SameIMs(got, catalog.DefaultIMs()) {
		t.Errorf("group = %v, want %v\n%s", got, catalog.DefaultIMs(), pb)
	}

	// exactly ONE backup for the whole run (one snapshot of the user's originals)
	if dirs := backupDirs(t, home); len(dirs) != 1 {
		t.Errorf("backups = %v, want exactly one", dirs)
	} else if b, err := os.ReadFile(filepath.Join(dirs[0], ".config", "fcitx5", "config")); err != nil ||
		!strings.Contains(string(b), "Control+space") {
		t.Errorf("the pre-change config must be in the backup (err=%v):\n%s", err, b)
	}

	// --- idempotence ------------------------------------------------------
	opts2, log2 := newTestOpts()
	if c := Install(d, false, opts2); c != ExitOK {
		t.Fatalf("second run exit=%d\n%s", c, log2.String())
	}
	if dirs := backupDirs(t, home); len(dirs) != 1 {
		t.Errorf("converged re-run created a backup: %v", dirs)
	}
	if h.barPuts != 1 || h.themeApply != 1 {
		t.Errorf("converged re-run re-did work: barPuts=%d themeApply=%d", h.barPuts, h.themeApply)
	}
	st, err := state.Load()
	if err != nil {
		t.Fatal(err)
	}
	if p := plan.Diff(d, observe.Collect(d, st), false); p.NeedsApply() {
		t.Errorf("converged host still needs apply:\n%s", p.Describe())
	}

	// status --json must agree with the plan it just printed
	var buf bytes.Buffer
	if c := Status(Options{Stdout: &buf, Stderr: &buf, Yes: true, JSON: true}); c != ExitOK {
		t.Fatalf("status --json exit=%d\n%s", c, buf.String())
	}
	var rep StatusReport
	if err := json.Unmarshal(buf.Bytes(), &rep); err != nil {
		t.Fatalf("status --json is not valid JSON: %v\n%s", err, buf.String())
	}
	if rep.Plan.NeedsApply {
		t.Errorf("status must report needsApply=false: %+v", rep.Plan)
	}

	// doctor: the IM rows must all be green. The env red line probes
	// /usr/share/omarchy (no seam), so the overall exit code is only asserted on
	// a host that actually has Omarchy installed.
	checks := verify.Doctor(d, observe.Collect(d, st))
	for _, name := range []string{"顶栏输入法指示器", "触发键", "输入法组", "候选框主题", "AltTriggerKeys"} {
		for _, chk := range checks {
			if chk.Name == name && !chk.OK {
				t.Errorf("doctor %s must pass: %s", name, chk.Detail)
			}
		}
	}
	if _, err := os.Stat("/usr/share/omarchy/default/environment.d/10-omarchy-fcitx.conf"); err == nil {
		var dbuf bytes.Buffer
		if c := Doctor(Options{Stdout: &dbuf, Stderr: &dbuf, Yes: true, JSON: true}); c != ExitOK {
			t.Errorf("doctor exit=%d\n%s", c, dbuf.String())
		}
	}
}

// TestInstallRemovesLegacyDropInInsideStopWindow locks the sequencing that makes
// the duplicate top-bar icon actually disappear: the pre-4.5 drop-in re-enabled
// fcitx5's SNI item, and the RUNNING instance was started with it, so deleting
// the file alone leaves the icon until the next login. The removal therefore has
// to (a) count as stop-window work (plan.NeedHost) and (b) happen while the unit
// is stopped, before daemon-reload + the deferred start.
func TestInstallRemovesLegacyDropInInsideStopWindow(t *testing.T) {
	home := t.TempDir()
	h := setupFakeHost(t, home)
	seedCache(t, home)
	d := catalog.DefaultDesired()

	// converge once so everything else (profile, group, bar, theme) is at the
	// terminal state and only the leftover drop-in is off target.
	o1, out1 := newTestOpts()
	if c := Install(d, false, o1); c != ExitOK {
		t.Fatalf("warmup install exit=%d\n%s", c, out1)
	}
	if h.unitActive != true {
		t.Fatal("precondition: the unit must be running")
	}
	if _, err := tray.WriteDropIn(home, "omarchy-fcitx5.service", tray.DropInContent); err != nil {
		t.Fatal(err)
	}

	// the plan must treat it as HOST (stop-window) work, not just tray work
	cur := observe.Collect(d, mustLoadState(t))
	if !cur.StaleTray || !cur.DropInExists {
		t.Fatalf("precondition: the drop-in must read as residue: %+v", cur)
	}
	p := plan.Diff(d, cur, false)
	if !p.NeedHost {
		t.Fatalf("a leftover drop-in must open the stop window (deleting it out of band leaves the icon alive):\n%s", p.Describe())
	}
	if !p.NeedTray {
		t.Errorf("the drop-in residue is also tray work:\n%s", p.Describe())
	}
	if out := p.Describe(); !strings.Contains(out, "旧 notificationitem drop-in") {
		t.Errorf("plan must name the drop-in work:\n%s", out)
	}

	// one run: the window opens (stop → daemon-reload → start) and the file is
	// gone by the time fcitx5 is restarted
	o2, out2 := newTestOpts()
	if c := Install(d, false, o2); c != ExitOK {
		t.Fatalf("install exit=%d\n%s", c, out2.String())
	}
	log := out2.String()
	stopAt := strings.Index(log, "[完成] L4 stop omarchy-fcitx5.service")
	reloadAt := strings.Index(log, "[完成] L4 start omarchy-fcitx5.service")
	removeAt := strings.Index(log, "已移除旧的 notificationitem drop-in")
	if stopAt < 0 || reloadAt < 0 {
		t.Fatalf("the stop window must open for the drop-in removal:\n%s", log)
	}
	if removeAt < 0 {
		t.Fatalf("the removal must be reported:\n%s", log)
	}
	if stopAt >= removeAt || removeAt >= reloadAt {
		t.Errorf("drop-in must be removed between stop and start (stop=%d remove=%d start=%d):\n%s", stopAt, removeAt, reloadAt, log)
	}
	if _, err := os.Stat(tray.DropInPath(home, "omarchy-fcitx5.service")); !os.IsNotExist(err) {
		t.Error("drop-in survived the run")
	}
	if !h.unitActive {
		t.Error("fcitx5 left stopped — user has no input method")
	}

	// and the next run is a true no-op: no window, no removal line
	o3, out3 := newTestOpts()
	if c := Install(d, false, o3); c != ExitOK {
		t.Fatalf("re-run exit=%d\n%s", c, out3.String())
	}
	if !strings.Contains(out3.String(), "[跳过] L4 stop 窗口未开启") {
		t.Errorf("converged re-run must keep the stop window closed:\n%s", out3.String())
	}
	if strings.Contains(out3.String(), "已移除旧的 notificationitem drop-in") {
		t.Errorf("re-run must not re-report a removal:\n%s", out3.String())
	}
}

// mustLoadState loads the ledger or fails the test.
func mustLoadState(t *testing.T) *state.State {
	t.Helper()
	st, err := state.Load()
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// TestInstallRemovesLegacyV1Override is the field bug from a real 4.5 host:
// ompinyin v1.0 (commit 7c289dc) wrote a GENERIC override.conf with no managed
// header, so it cannot be recognised by name:
//
//	[Service]
//	ExecStart=
//	ExecStart=/usr/bin/fcitx5
//
// It replaces Omarchy's `--replace --disable notificationitem`, fcitx5 keeps
// publishing its own SNI item, and the bar shows TWO input-method icons. Removing
// it only takes effect on restart, so it belongs inside the stop window.
func TestInstallRemovesLegacyV1Override(t *testing.T) {
	home := t.TempDir()
	h := setupFakeHost(t, home)
	seedCache(t, home)
	d := catalog.DefaultDesired()
	const unit = "omarchy-fcitx5.service"

	// a converged 4.5 host ...
	o1, out1 := newTestOpts()
	if c := Install(d, false, o1); c != ExitOK {
		t.Fatalf("warmup install exit=%d\n%s", c, out1)
	}
	// ... that still carries the v1.0 override from an ancient run
	over := tray.LegacyOverridePath(home, unit)
	if err := os.MkdirAll(filepath.Dir(over), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(over, []byte(tray.LegacyOverrideBody), 0o644); err != nil {
		t.Fatal(err)
	}

	cur := observe.Collect(d, mustLoadState(t))
	if !cur.StaleOverride || !cur.StaleTray {
		t.Fatalf("precondition: the v1.0 override must read as residue: %+v", cur)
	}
	if cur.ExecStartOK {
		t.Fatal("precondition: the effective ExecStart must have lost the disable flag")
	}
	p := plan.Diff(d, cur, false)
	if !p.NeedHost {
		t.Fatalf("a leftover override.conf must open the stop window:\n%s", p.Describe())
	}
	if !strings.Contains(p.Describe(), "override.conf") {
		t.Errorf("plan must name the override:\n%s", p.Describe())
	}

	// one run: removed inside the stop window, before the restart
	o2, out2 := newTestOpts()
	if c := Install(d, false, o2); c != ExitOK {
		t.Fatalf("install exit=%d\n%s", c, out2.String())
	}
	log := out2.String()
	removeAt := strings.Index(log, "已移除 v1.0 的 override.conf")
	startAt := strings.Index(log, "[完成] L4 start omarchy-fcitx5.service")
	if removeAt < 0 {
		t.Fatalf("the removal must be reported honestly:\n%s", log)
	}
	if startAt < 0 || removeAt > startAt {
		t.Errorf("the override must be removed before the unit restarts (remove=%d start=%d):\n%s", removeAt, startAt, log)
	}
	if _, err := os.Stat(over); !os.IsNotExist(err) {
		t.Fatal("v1.0 override.conf survived the convergence")
	}
	// §5.1: the deleted file was snapshotted first
	dirs := backupDirs(t, home)
	found := false
	for _, dir := range dirs {
		if _, err := os.Stat(filepath.Join(dir, ".config", "systemd", "user", unit+".d", "override.conf")); err == nil {
			found = true
		}
	}
	if !found {
		t.Errorf("the override was deleted without a backup: %v", dirs)
	}
	// ... and the effective ExecStart is Omarchy's own again
	if cur := observe.Collect(d, mustLoadState(t)); !cur.ExecStartOK || cur.StaleOverride {
		t.Errorf("after the run the effective ExecStart must keep --disable notificationitem: %+v", cur)
	}
	if !h.unitActive {
		t.Error("fcitx5 left stopped — user has no input method")
	}

	// second run: nothing left to do
	st := mustLoadState(t)
	if q := plan.Diff(d, observe.Collect(d, st), false); q.NeedsApply() {
		t.Errorf("converged host still needs apply:\n%s", q.Describe())
	}
	o3, out3 := newTestOpts()
	if c := Install(d, false, o3); c != ExitOK {
		t.Fatalf("re-run exit=%d\n%s", c, out3.String())
	}
	if strings.Contains(out3.String(), "已移除 v1.0 的 override.conf") {
		t.Errorf("re-run must not re-report a removal:\n%s", out3.String())
	}
}

// TestInstallKeepsUserOverride: the same file name may hold a USER's own config.
// ompinyin must not delete it — but it must not stay silent either: the effective
// ExecStart is still overridden, so L5/doctor fail and point at the directory.
func TestInstallKeepsUserOverride(t *testing.T) {
	home := t.TempDir()
	setupFakeHost(t, home)
	seedCache(t, home)
	d := catalog.DefaultDesired()
	const unit = "omarchy-fcitx5.service"

	o1, out1 := newTestOpts()
	if c := Install(d, false, o1); c != ExitOK {
		t.Fatalf("warmup install exit=%d\n%s", c, out1)
	}
	over := tray.LegacyOverridePath(home, unit)
	userBody := "[Service]\nExecStart=\nExecStart=/usr/local/bin/fcitx5 --replace --log-level=debug\n"
	if err := os.MkdirAll(filepath.Dir(over), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(over, []byte(userBody), 0o644); err != nil {
		t.Fatal(err)
	}

	cur := observe.Collect(d, mustLoadState(t))
	if cur.StaleOverride {
		t.Fatal("a user-authored override must not be classified as our residue")
	}
	if cur.ExecStartOK {
		t.Fatal("precondition: the effective ExecStart is overridden")
	}
	// not our file → not our work → the plan must not offer to delete it
	if p := plan.Diff(d, cur, false); p.NeedHost {
		t.Errorf("a user override is not stop-window work:\n%s", p.Describe())
	}

	o2, out2 := newTestOpts()
	if c := Install(d, false, o2); c != ExitExecFail {
		t.Fatalf("install must fail L5 while the ExecStart is overridden, got %d\n%s", c, out2.String())
	}
	b, err := os.ReadFile(over)
	if err != nil || string(b) != userBody {
		t.Fatalf("user override was touched (err=%v):\n%s", err, b)
	}
	if !strings.Contains(out2.String(), "已移除 v1.0 的 override.conf") &&
		!strings.Contains(out2.String(), "[失败] L5 fcitx5 启动参数") {
		t.Errorf("the failure must be explained:\n%s", out2.String())
	}

	// doctor: red, naming the drop-in dir and the file
	var dbuf bytes.Buffer
	code := Doctor(Options{Stdout: &dbuf, Stderr: &dbuf, Yes: true, JSON: true})
	if code == ExitOK {
		t.Fatalf("doctor must fail while the ExecStart is overridden:\n%s", dbuf.String())
	}
	for _, want := range []string{"fcitx5 启动参数", "override.conf", unit + ".d"} {
		if !strings.Contains(dbuf.String(), want) {
			t.Errorf("doctor output must mention %q:\n%s", want, dbuf.String())
		}
	}
}
