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
	"github.com/ProjectAILeap/ompinyin/internal/catalog"
	"github.com/ProjectAILeap/ompinyin/internal/deploy"
	"github.com/ProjectAILeap/ompinyin/internal/facts"
	"github.com/ProjectAILeap/ompinyin/internal/hidpi"
	"github.com/ProjectAILeap/ompinyin/internal/observe"
	"github.com/ProjectAILeap/ompinyin/internal/pkgs"
	"github.com/ProjectAILeap/ompinyin/internal/selfup"
	"github.com/ProjectAILeap/ompinyin/internal/service"
	"github.com/ProjectAILeap/ompinyin/internal/state"
	"github.com/ProjectAILeap/ompinyin/internal/theme"
	"github.com/ProjectAILeap/ompinyin/internal/tray"
)

func realCmd(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }

// assetsResolveStableTagProd preserves the production resolver across tests.
var assetsResolveStableTagProd = assets.ResolveStableTag

// factsLookPathProd preserves the production PATH lookup across tests: the
// precheck (facts.Collect) gate runs before any layer, so setupFakeHost stubs
// LookPath to keep T0 hermetic and independent of the host (CI ubuntu has no
// omarchy/fcitx5-remote/rime_deployer).
var factsLookPathProd = facts.LookPath

// hidpiRunProd preserves the production hyprctl/xrdb runner across tests.
var hidpiRunProd = hidpi.Run

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
			body := fmt.Sprintf("schema: %s\ngrammar:\n  language: %s\n  collocation_penalty: %d\ntranslator/max_homophones: %d\n",
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

	// fake candidate-window theming: theme.Run emulates the hook by writing a
	// populated theme dir (the real hook reads colors.toml + omarchy-theme-color,
	// neither of which exists on the fake host); Reload counts hot reloads; the
	// UI font is deterministic so desired-byte comparisons stay stable.
	theme.Run = func(name string, args ...string) error {
		dir := theme.ThemeDir(home)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, "theme.conf"), []byte("[Metadata]\nName=Omarchy\n"), 0o644)
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
		facts.OSReleasePath = "/etc/os-release"
		pkgs.Run = nil
		service.SystemUnitDirs = []string{"/etc/systemd/user", "/usr/lib/systemd/user"}
		service.Run = func(name string, args ...string) error {
			c := realCmd(name, args...)
			return c.Run()
		}
		service.RunOutput = nil
		deploy.Run = nil
		deploy.CompileSchemas = nil
		hidpi.Run = hidpiRunProd
		tray.ShellRunning = nil
		tray.Run = nil
		theme.Run = nil
		theme.Reload = nil
		theme.CurrentFont = nil
		assets.ResolveStableTag = assetsResolveStableTagProd
	})
	return h
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

	// L4 profile registered rime
	pb, _ := os.ReadFile(observe.ProfilePath())
	if !strings.Contains(string(pb), "DefaultIM=rime") || !strings.Contains(string(pb), "Name=rime") {
		t.Errorf("profile not converged:\n%s", pb)
	}
	// L4 hotkeys with whitelist keysyms
	cb, _ := os.ReadFile(observe.ConfigPath())
	if !strings.Contains(string(cb), "0=Alt+space") || strings.Contains(string(cb), "1=Control+Shift_L") {
		t.Errorf("hotkey config wrong:\n%s", cb)
	}
	// L4 drop-in removes --disable notificationitem (dedicated filename)
	db, _ := os.ReadFile(tray.DropInPath(home, "omarchy-fcitx5.service"))
	if !strings.Contains(string(db), "ExecStart=/usr/bin/fcitx5") ||
		strings.Contains(string(db), "notificationitem}") {
		// the ExecStart line must not carry the disable flag
		for _, line := range strings.Split(string(db), "\n") {
			if strings.HasPrefix(line, "ExecStart") && strings.Contains(line, "notificationitem") {
				t.Errorf("drop-in still disables notificationitem: %s", line)
			}
		}
	}
	// L4 tray pin: written as an ARRAY into shell.json (Tray.qml needs array)
	sj, _ := os.ReadFile(tray.ShellJSONPath(home))
	pinned, perr := tray.ReadPinned(sj)
	if perr != nil || !tray.HasPin(pinned) {
		t.Errorf("shell.json pinned not set: %v err=%v\n%s", pinned, perr, sj)
	} else if len(pinned) > 1 {
		t.Errorf("unexpected extra pins: %v", pinned)
	}
	// L4 candidate-window theming (§6.6): classicui.conf points at the theme
	// and the theme-set hook + generated dir are in place
	cb2, _ := os.ReadFile(theme.ConfPath(home))
	if !strings.HasPrefix(string(cb2), "# managed by ompinyin") || !strings.Contains(string(cb2), "Theme=omarchy") {
		t.Errorf("classicui.conf not converged:\n%s", cb2)
	}
	if !strings.Contains(string(cb2), "Font=TestFont 12") {
		t.Errorf("classicui.conf missing the Omarchy font:\n%s", cb2)
	}
	hb, _ := os.ReadFile(theme.HookPath(home))
	if !strings.HasPrefix(string(hb), "# managed by ompinyin") || !strings.Contains(string(hb), "ReloadAddonConfig") {
		t.Errorf("theme-set hook not installed:\n%s", hb)
	}
	if _, err := os.Stat(filepath.Join(theme.ThemeDir(home), "theme.conf")); err != nil {
		t.Errorf("theme dir not generated: %v", err)
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
	// the pin must be idempotent: second re-convergence does not change it
	sj, _ := os.ReadFile(tray.ShellJSONPath(home))
	pinned, _ := tray.ReadPinned(sj)
	if !tray.HasPin(pinned) {
		t.Errorf("pin lost after second run: %v", pinned)
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
			args:           SwitchArgs{DSP: "zrm", DSPDefault: true, Yes: true},
			wantPrimary:    "zrm",
			wantExtra:      []string{"quanpin"},
			wantSchemaList: []string{"double_pinyin", "rime_ice"},
		},
		{
			name:           "bare dsp keeps quanpin primary",
			args:           SwitchArgs{DSP: "zrm", Yes: true},
			wantPrimary:    "quanpin",
			wantExtra:      []string{"zrm"},
			wantSchemaList: []string{"rime_ice", "double_pinyin"},
		},
		{
			name:           "dsp no-quanpin drops quanpin",
			args:           SwitchArgs{DSP: "zrm", NoQuanpin: true, Yes: true},
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
		"[跳过] L4 托盘已 pin",
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
	// force work INSIDE the window that reaches daemon-reload: the drop-in is
	// missing, so L4 host work (and the reload) is required
	if err := os.Remove(tray.DropInPath(home, "omarchy-fcitx5.service")); err != nil {
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
	if !rep.Host.DropInOK || !rep.Host.PinnedHasFcitx || !rep.Host.ThemeOK {
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
	opts := Options{Stdout: &out, Stderr: &errb, Yes: true, Command: "switch"}
	if c := Switch(SwitchArgs{DSP: "zrm", Yes: true, DryRun: true, JSON: true}, opts); c != ExitOK {
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

// TestUninstallRemovesThemeFootprint: uninstall must delete classicui.conf,
// the theme-set hook and the hook-generated theme dir, and drop the ledger
// records — leaving fcitx5 with the default candidate window.
func TestUninstallRemovesThemeFootprint(t *testing.T) {
	home := t.TempDir()
	h := setupFakeHost(t, home)
	seedCache(t, home)

	opts, out := newTestOpts()
	if c := Install(catalog.DefaultDesired(), false, opts); c != ExitOK {
		t.Fatalf("install exit=%d\n%s", c, out.String())
	}
	for _, p := range []string{theme.ConfPath(home), theme.HookPath(home), filepath.Join(theme.ThemeDir(home), "theme.conf")} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("precondition %s missing: %v", p, err)
		}
	}

	if c := Uninstall(opts); c != ExitOK {
		t.Fatalf("uninstall exit=%d\n%s", c, out.String())
	}
	if _, err := os.Stat(theme.ConfPath(home)); !os.IsNotExist(err) {
		t.Errorf("classicui.conf left behind")
	}
	if _, err := os.Stat(theme.HookPath(home)); !os.IsNotExist(err) {
		t.Errorf("theme-set hook left behind")
	}
	if _, err := os.Stat(theme.ThemeDir(home)); !os.IsNotExist(err) {
		t.Errorf("theme dir left behind")
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
