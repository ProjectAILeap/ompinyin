// Package observe collects the current host state cheaply so that
// plan.Diff can compare desired vs current (§3).
package observe

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ProjectAILeap/ompinyin/internal/catalog"
	"github.com/ProjectAILeap/ompinyin/internal/deploy"
	"github.com/ProjectAILeap/ompinyin/internal/hidpi"
	"github.com/ProjectAILeap/ompinyin/internal/hotkey"
	"github.com/ProjectAILeap/ompinyin/internal/patches"
	"github.com/ProjectAILeap/ompinyin/internal/pkgs"
	"github.com/ProjectAILeap/ompinyin/internal/service"
	"github.com/ProjectAILeap/ompinyin/internal/state"
	"github.com/ProjectAILeap/ompinyin/internal/theme"
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
	// HerdrPrefix is herdr's own prefix chord ("" when herdr is not configured
	// here); HerdrPrefixConflict names the chord both tools want. herdr grabs
	// its prefix before the application sees it, so a shared chord would only
	// reach fcitx5 outside herdr — a warning, never a convergence failure.
	HerdrPrefix         string
	HerdrPrefixConflict string
	// DropInExists is the file presence; DropInOK additionally requires the
	// content to really enable notificationitem for the DISCOVERED unit.
	DropInExists bool
	DropInOK     bool
	DropInPath   string

	PinnedHasFc  bool
	ShellRunning bool

	// StrayFcitx: an fcitx5 process is alive although the unit is not active. In
	// that state the unit can never start (its fcitx5 exits immediately with
	// "another fcitx already running"), so the remedy is to end the stray first.
	StrayFcitx bool

	// FcitxCount: how many fcitx5 processes are alive. The unit's own instance is
	// one; a second one while ServiceActive is a transient "active" reading from
	// a Restart=always flap (a stray holds the bus name), not a healthy service.
	FcitxCount int

	// L4 candidate-window theming (§6.6): files point at the omarchy theme
	// (ConfOK/HookOK), the generated theme dir is in place (DirOK), and the
	// managed bytes already equal the desired content (Equal).
	ThemeConfOK bool
	ThemeHookOK bool
	ThemeDirOK  bool
	ThemeEqual  bool

	BuildMissing []string

	LegacyDirExists bool // ~/.config/fcitx/rime (§6.5)

	// X11 HiDPI facts (DESIGN §6.7). Whether the compat mode is on lives in
	// Desired (explicit opt-in); what the display reports is observed here and
	// never configured by ompinyin.
	X11Scale           float64
	X11DPIDesired      int
	X11DPIActual       int
	X11Available       bool
	X11ConfigOK        bool
	X11UnitsOK         bool
	X11UnitsPresent    bool   // either publisher unit file exists (any content)
	X11UnitExec        string // binary baked into the publisher unit ("" when absent)
	X11UnitExecMissing bool   // ... and it no longer exists: the watcher cannot exec
	X11ManagedPresent  bool   // ompinyin-scoped Xft.dpi block exists in ~/.Xresources
	X11ForeignDPI      bool   // a competing Xft.dpi assignment exists outside that block
	X11PackageMissing  bool
	// X11ForceZeroScaling mirrors Hyprland xwayland:force_zero_scaling. true
	// (Omarchy default) = X11 windows are NOT compositor-scaled, so each toolkit
	// scales itself and the X11 candidate needs Xft.dpi. false = the compositor
	// scales X11, so publishing Xft.dpi would double-scale everything.
	X11ForceZeroScaling bool
	X11ScalingKnown     bool // force_zero_scaling was actually read (vs defaulted true)
	// X11ToolkitFactors are the explicit toolkit scale factors in the session
	// (GDK_SCALE, …). Publishing Xft.dpi next to one of them makes every client
	// that reads both multiply — §6.7's "一个框架只能有一个缩放权". Diagnostics
	// only: ompinyin never edits Hyprland's env.
	X11ToolkitFactors []string
}

// Collect probes the host. Best-effort: probe errors are surfaced as
// "absent" states; the convergence run re-checks with hard errors.
func Collect(d catalog.Desired, st *state.State) *Current {
	c := &Current{RimeDir: DataDir(), Managed: map[string]patches.Status{}, ContentEqual: map[string]bool{}}

	missing, err := pkgs.Missing(pkgs.Needed...)
	if err == nil {
		c.PackagesMissing = missing
	}
	if missing, err := pkgs.Missing(pkgs.X11HiDPIPackage); err == nil {
		c.X11PackageMissing = len(missing) > 0
	}
	c.Unit = service.FindUnit(state.Home())
	c.ServiceActive = c.Unit != "" && service.IsActive(c.Unit)
	// A live fcitx5 with an inactive unit is an unmanaged instance: it owns the
	// bus name, so the unit's own start can never succeed. Check it with a
	// process probe, never a fcitx5-remote/bus probe (that would create one).
	c.StrayFcitx = !c.ServiceActive && service.FcitxRunning()
	// Counted separately: `is-active` can read "active" during the few hundred
	// ms a doomed instance lives under Restart=always, which would otherwise
	// hide the coexisting stray from verify.
	c.FcitxCount = service.FcitxCount()

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
	// Cross-tool check (§6.2): another program may already own the chord.
	prefix, extras := HerdrPrefix(state.Home())
	c.HerdrPrefix = prefix
	c.HerdrPrefixConflict = HerdrConflict(prefix, extras, hotkey.DefaultKeys)
	c.DropInPath = tray.DropInPath(state.Home(), c.Unit)
	if b, err := os.ReadFile(c.DropInPath); err == nil {
		c.DropInExists = true
		c.DropInOK = tray.DropInEnabled(string(b))
	}
	if b, err := os.ReadFile(tray.ShellJSONPath(state.Home())); err == nil {
		if pinned, perr := tray.ReadPinned(b); perr == nil {
			c.PinnedHasFc = tray.HasPin(pinned)
		}
	}
	c.ShellRunning = tray.ShellRunning()

	// L4 candidate-window theming (§6.6)
	th := theme.Observe(state.Home(), st)
	c.ThemeConfOK, c.ThemeHookOK, c.ThemeDirOK = th.ConfOK, th.HookOK, th.DirOK
	c.ThemeEqual = th.ConfEqual && th.HookEqual

	// XWayland may not exist in a pure Wayland session. In that case this
	// feature is a harmless no-op rather than a failed precondition.
	monitors, _ := hidpi.Run("hyprctl", "monitors", "-j")
	lua, _ := os.ReadFile(filepath.Join(state.Home(), ".config", "hypr", "monitors.lua"))
	xr, _ := os.ReadFile(hidpi.XresourcesPath(state.Home()))
	c.X11Scale = hidpi.ReadScale(monitors, lua, xr)
	c.X11DPIDesired = hidpi.DPI(c.X11Scale)
	if actual, err := hidpi.Run("xrdb", "-query"); err == nil {
		c.X11Available = true
		c.X11DPIActual, _ = hidpi.ParseXftDPI(actual)
	} else if prop, perr := hidpi.Run("xprop", "-root", "RESOURCE_MANAGER"); perr == nil {
		// xorg-xrdb is not always installed (some hosts publish the root
		// property with xprop). Fall back so "default diagnose only" still
		// reports the real published value instead of "no X session".
		c.X11Available = true
		c.X11DPIActual, _ = hidpi.ParseXftDPIAny(prop)
	}
	if merged, _ := hidpi.MergeXresources(string(xr), c.X11DPIDesired); string(xr) == merged {
		c.X11ConfigOK = true
	}
	c.X11ToolkitFactors = hidpi.ToolkitScaleFactorsInSession()
	c.X11ManagedPresent = hidpi.ManagedBlockPresent(string(xr))
	c.X11ForeignDPI = hidpi.HasForeignXftDPI(string(xr))
	// Hyprland's force_zero_scaling decides whether the compositor already
	// scales X11 windows. Default to Omarchy's true (apps self-scale → Xft.dpi
	// needed) when hyprctl is unavailable, so behavior only changes on a host
	// that explicitly opts into compositor X11 scaling.
	c.X11ForceZeroScaling = true
	if b, err := hidpi.Run("hyprctl", "getoption", "xwayland:force_zero_scaling"); err == nil {
		if v, ok := hidpi.ParseForceZeroScaling(b); ok {
			c.X11ForceZeroScaling, c.X11ScalingKnown = v, true
		}
	}
	serviceBody, pathBody := hidpi.UnitContent()
	svcPath, pathPath := hidpi.ServicePath(state.Home()), hidpi.PathPath(state.Home())
	sb, serr := os.ReadFile(svcPath)
	pb, perr := os.ReadFile(pathPath)
	c.X11UnitsPresent = serr == nil || perr == nil
	if serr == nil && perr == nil {
		c.X11UnitsOK = string(sb) == serviceBody && string(pb) == pathBody
	}
	// The publisher execs this path on every scale change; if it vanished the
	// watcher fails silently, so surface it instead of pretending convergence.
	c.X11UnitExec = hidpi.UnitExecPath(string(sb))
	if c.X11UnitExec != "" {
		if _, err := os.Stat(c.X11UnitExec); err != nil {
			c.X11UnitExecMissing = true
		}
	}

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

// X11ForeignNote warns when ~/.Xresources assigns Xft.dpi outside ompinyin's
// managed block. Ownership is line-scoped, so a competing line is never
// removed; xrdb applies assignments in file order (last wins), which makes the
// outcome ordering-dependent — the live-value diff already fails L5 in that
// case, this note explains why before the user has to read the raw values.
func (c *Current) X11ForeignNote() string {
	if !c.X11ForeignDPI {
		return ""
	}
	return "；注意：~/.Xresources 存在块外 Xft.dpi 行（xrdb 按文件顺序、后者胜，冲突时以实际值为准）"
}

// X11ToolkitNote warns when the session carries an explicit toolkit scale
// factor, which Xft.dpi would multiply (§6.7). Omarchy's own monitors.lua ships
// GDK_SCALE=2, so this is the common case, not an exotic one.
func (c *Current) X11ToolkitNote() string {
	if len(c.X11ToolkitFactors) == 0 {
		return ""
	}
	return "；⚠ 会话里已有显式缩放因子 " + strings.Join(c.X11ToolkitFactors, " ") +
		"，读 Xft.dpi 的 X11 客户端（GTK3/Qt5）会与它叠加（约 4×）"
}

// X11UnitExecNote warns when the publisher unit points at a binary that no
// longer exists. ompinyin cannot fix it by itself (the baked path is whatever
// the binary that opted in saw), so it must be visible instead of silently
// leaving a watcher that fails to exec.
func (c *Current) X11UnitExecNote() string {
	if !c.X11UnitExecMissing {
		return ""
	}
	return "；注意：发布单元指向的 ompinyin 不存在（" + c.X11UnitExec + "）——用最终路径的二进制重跑 `ompinyin install --x11-hidpi` 会重写单元"
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

// HerdrPrefix is the prefix herdr binds (its own config, default "ctrl+b").
// Empty means "herdr is not configured here" — no conflict to report.
func HerdrPrefix(home string) (prefix string, extras []string) {
	path := os.Getenv("HERDR_CONFIG_PATH")
	if path == "" {
		p := filepath.Join(home, ".config", "herdr", "config.toml")
		if _, err := os.Stat(p); err != nil {
			return "", nil // herdr absent: it would use its built-in ctrl+b
		}
		path = p
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", nil
	}
	return ParseHerdrPrefix(string(b))
}

// ParseHerdrPrefix reads `prefix` and `extra_prefixes` out of herdr's config. A
// line scan is enough and avoids a TOML dependency (the file is machine-shaped:
// `[keys]` followed by `prefix = "ctrl+space"`). "" when the section or keys are
// absent, which means herdr's built-in default applies.
func ParseHerdrPrefix(config string) (prefix string, extras []string) {
	inKeys := false
	for _, line := range strings.Split(config, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if strings.HasPrefix(t, "[") {
			inKeys = t == "[keys]"
			continue
		}
		if !inKeys {
			continue
		}
		k, v, ok := strings.Cut(t, "=")
		if !ok {
			continue
		}
		val := strings.Trim(strings.TrimSpace(v), `"'`)
		switch strings.TrimSpace(k) {
		case "prefix":
			prefix = val
		case "extra_prefixes":
			for _, e := range strings.Split(strings.Trim(val, "[]"), ",") {
				if e = strings.Trim(strings.TrimSpace(e), `"'`); e != "" {
					extras = append(extras, e)
				}
			}
		}
	}
	return prefix, extras
}

// HerdrConflict reports why another tool already owns one of these chords:
// herdr's prefix (and extra_prefixes) are grabbed before any application sees
// them, so an fcitx5 trigger key that matches would only work in windows herdr
// does not handle. empty when there is no clash.
func HerdrConflict(prefix string, extras []string, keys []string) string {
	chords := append([]string{prefix}, extras...)
	for _, c := range chords {
		if c == "" {
			continue
		}
		for _, k := range keys {
			if hotkey.SameKey(c, k) {
				return c
			}
		}
	}
	return ""
}

// HerdrNote turns a herdr conflict into the doctor/plan one-liner. It is a
// warning, not a failure: the chord still works everywhere herdr is not in the
// loop, and which side to move is the user's call (ompinyin never edits another
// tool's config).
func (c *Current) HerdrNote() string {
	if c.HerdrPrefixConflict == "" {
		return ""
	}
	return "；⚠ herdr 的前缀也是 " + c.HerdrPrefixConflict +
		"：在 herdr 的窗口里会先被它截获（输入法收不到）。把 herdr 的 [keys] prefix 改成 ctrl+b 即可，或用 fcitx5 的另一个触发键"
}
