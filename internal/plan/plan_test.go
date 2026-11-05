package plan

import (
	"strings"
	"testing"

	"github.com/ProjectAILeap/ompinyin/internal/catalog"
	"github.com/ProjectAILeap/ompinyin/internal/observe"
	"github.com/ProjectAILeap/ompinyin/internal/patches"
)

// freshCurrent models a fresh Omarchy 4.5+ host: the IM integration is
// Omarchy's own (bar indicator, no SNI drop-in/pin, no trigger keys, themed by
// omarchy-theme-set-fcitx5). legacyCurrent() models the pre-4.5 host (Omarchy
// went straight from 4.0 to 4.5) instead.
func freshCurrent() *observe.Current {
	return &observe.Current{
		Managed:           map[string]patches.Status{},
		PackagesMissing:   []string{"fcitx5"},
		OmarchyManagesIME: true,
		// Omarchy ships xwayland force_zero_scaling=true: X11 apps self-scale,
		// so the Xft.dpi bridge is applicable. Tests that model the compositor
		// scaling X11 override this to false.
		X11ForceZeroScaling: true,
	}
}

// TestDiffFreshHost: on a fresh host every layer must be [计划] needed.
func TestDiffFreshHost(t *testing.T) {
	d := catalog.DefaultDesired()
	p := Diff(d, freshCurrent(), false)
	if !p.NeedsApply() {
		t.Fatal("fresh host must have work")
	}
	out := p.Describe()
	for _, want := range []string{"[计划] L1", "pacman -S --needed fcitx5", "L2", "L3", "L4", "L5"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan output missing %q:\n%s", want, out)
		}
	}
	// 4.5+：顶栏是 Omarchy 自己的 widget，步骤只能说「放上 widget + 清残留」
	if !strings.Contains(out, "omarchy.keyboard-layout") {
		t.Errorf("bar-indicator step missing:\n%s", out)
	}
}

// convergedCurrent is a current-state snapshot that satisfies every layer, so
// a diff isolates exactly the one field a test perturbs.
func convergedCurrent() *observe.Current {
	c := freshCurrent()
	c.PackagesMissing = nil
	c.GramFileExists = true
	c.RimeDataExists = true
	c.Managed = map[string]patches.Status{}
	c.ContentEqual = map[string]bool{}
	for _, rel := range []string{
		"default.custom.yaml", "radical_pinyin.custom.yaml",
		"melt_eng.custom.yaml", "rime_ice.custom.yaml",
	} {
		c.Managed[rel] = patches.StatusManaged
		c.ContentEqual[rel] = true
	}
	c.ProfileHasRime = true
	c.GroupIMs = catalog.DefaultIMs()
	c.HotkeyOK = true
	c.AltTriggerOK = true
	c.DropInExists = false // 4.5+: the drop-in is residue, not terminal state
	c.DropInOK = false
	c.PinnedHasFc = false
	c.BarIndicator = true
	c.ThemeEqual = true
	c.ThemeConfOK = true
	c.ThemeHookOK = false // the hook is retired
	c.BuildMissing = nil
	return c
}

// legacyCurrent is the same snapshot on a pre-4.5 Omarchy: there the tray
// (notificationitem + pin) and the trigger keys are still ompinyin's.
func legacyCurrent() *observe.Current {
	c := convergedCurrent()
	c.OmarchyManagesIME = false
	c.HotkeyOK = true
	c.AltTriggerOK = true
	c.DropInExists = true
	c.DropInOK = true // present AND enabling notificationitem for the live unit
	c.PinnedHasFc = true
	c.BarIndicator = false
	return c
}

// TestDiffConvergedHost: when observations match the terminal state, all
// L1–L4 steps are [跳过].
func TestDiffConvergedHost(t *testing.T) {
	d := catalog.DefaultDesired()
	for name, c := range map[string]*observe.Current{
		"omarchy 4.5+": convergedCurrent(),
		"omarchy 4.0":  legacyCurrent(),
	} {
		p := Diff(d, c, false)
		for _, s := range p.Steps {
			if s.Layer != "L5" && s.Needed {
				t.Errorf("%s: converged host step %s/%s should be skipped", name, s.Layer, s.Title)
			}
		}
		if p.NeedsApply() {
			t.Errorf("%s: a converged host must not need apply", name)
		}
	}
}

// TestDiffLegacyTrayTerminalState: on 4.0 the tray really is ours, so a
// missing pin/drop-in is still work and the step keeps the old wording.
func TestDiffLegacyTrayTerminalState(t *testing.T) {
	c := legacyCurrent()
	c.DropInOK = false
	p := Diff(catalog.DefaultDesired(), c, false)
	if !p.NeedTray || !strings.Contains(p.Describe(), "pin Fcitx") {
		t.Fatalf("legacy tray drift must be planned:\n%s", p.Describe())
	}
	// the retired hook is NOT terminal state there either (convergence retires it)
	c2 := legacyCurrent()
	c2.ThemeHookOK = true
	if q := Diff(catalog.DefaultDesired(), c2, false); !q.NeedTheme {
		t.Error("a surviving theme hook must count as theming work")
	}
}

// TestDiffUserModifiedWarning: hand-edited managed files must surface a
// warning in the plan (never silently overwritten, §5.1).
func TestDiffUserModifiedWarning(t *testing.T) {
	d := catalog.DefaultDesired()
	c := freshCurrent()
	c.PackagesMissing = nil
	c.GramFileExists = true
	c.RimeDataExists = true
	c.Managed["default.custom.yaml"] = patches.StatusUserModified

	p := Diff(d, c, false)
	out := p.Describe()
	if !strings.Contains(out, "用户改动文件：default.custom.yaml") {
		t.Errorf("user-modified warning missing:\n%s", out)
	}
}

func TestDiffModelFalse(t *testing.T) {
	d := catalog.Desired{Primary: "quanpin", Model: false, Channel: "stable"}
	c := freshCurrent()
	c.PackagesMissing = nil
	out := Diff(d, c, false).Describe()
	if !strings.Contains(out, "Model=false，无方案级 grammar") {
		t.Errorf("model=false annotation missing:\n%s", out)
	}
}

func TestX11HiDPIRequiresExplicitOptIn(t *testing.T) {
	c := freshCurrent()
	c.X11DPIDesired = 192
	c.X11PackageMissing = true
	defaultPlan := Diff(catalog.DefaultDesired(), c, false)
	if strings.Contains(defaultPlan.Describe(), "xorg-xrdb") || defaultPlan.NeedHidpi {
		t.Fatalf("default plan must not enable global X11 HiDPI:\n%s", defaultPlan.Describe())
	}
	d := catalog.DefaultDesired()
	d.X11HiDPI = true
	optInPlan := Diff(d, c, false)
	if !optInPlan.NeedHidpi || !strings.Contains(optInPlan.Describe(), "xorg-xrdb") {
		t.Fatalf("opt-in plan must install and converge X11 HiDPI:\n%s", optInPlan.Describe())
	}
}

// TestX11HiDPIInertWhenCompositorScales: with xwayland:force_zero_scaling=false
// Hyprland scales X11 windows itself, so publishing a global Xft.dpi would
// double-scale every X11 client (candidate included). The opt-in must become a
// no-op with an explanatory step, not a 4x footgun.
func TestX11HiDPIInertWhenCompositorScales(t *testing.T) {
	c := freshCurrent()
	c.X11ForceZeroScaling = false
	c.X11ScalingKnown = true
	c.X11DPIDesired = 192
	c.X11PackageMissing = true
	d := catalog.DefaultDesired()
	d.X11HiDPI = true
	p := Diff(d, c, false)
	if p.NeedHidpi {
		t.Fatalf("must not publish Xft.dpi when the compositor scales X11:\n%s", p.Describe())
	}
	if !strings.Contains(p.Describe(), "force_zero_scaling=false") {
		t.Errorf("plan must explain the inert mode:\n%s", p.Describe())
	}
	if strings.Contains(p.Describe(), "xorg-xrdb") {
		t.Errorf("must not plan the L1 package when the mode is inert:\n%s", p.Describe())
	}
}

// TestX11HiDPIOptOutWithdrawsArtifacts: once opted in, a host carries the
// managed Xresources block and the two units; turning the mode off must plan
// their removal (and count as work — otherwise --no-x11-hidpi is a lie).
func TestX11HiDPIOptOutWithdrawsArtifacts(t *testing.T) {
	c := freshCurrent()
	c.X11ManagedPresent = true
	p := Diff(catalog.DefaultDesired(), c, false)
	if !p.NeedHidpiUndo {
		t.Fatal("opt-out with an existing managed block must plan an undo")
	}
	if !p.NeedsApply() {
		t.Fatal("undo work must count towards NeedsApply")
	}
	out := p.Describe()
	if !strings.Contains(out, "撤销") {
		t.Errorf("undo step missing from plan:\n%s", out)
	}
	// A stale or half-removed unit is still an owned artifact: the undo
	// predicate must be presence-based, not content-equality-based, or those
	// files stay behind forever.
	c2 := freshCurrent()
	c2.X11UnitsPresent = true
	c2.X11UnitsOK = false
	if q := Diff(catalog.DefaultDesired(), c2, false); !q.NeedHidpiUndo {
		t.Error("stale/partial publisher units must still be withdrawn")
	}
	// a clean non-opted host has nothing to withdraw
	c3 := freshCurrent()
	if q := Diff(catalog.DefaultDesired(), c3, false); q.NeedHidpiUndo {
		t.Error("clean non-opted host must not plan an undo")
	}
}

// TestX11OptionalPlanReportsFacts: "default = diagnose only" is only useful if
// the diagnosis is visible. The [跳过] step must carry the observed scale and
// expected/published DPI instead of a static sentence.
func TestX11OptionalPlanReportsFacts(t *testing.T) {
	c := freshCurrent()
	c.X11Scale = 2
	c.X11DPIDesired = 192
	c.X11DPIActual = 96
	c.X11Available = true
	out := Diff(catalog.DefaultDesired(), c, false).Describe()
	for _, want := range []string{"scale=2.00", "Xft.dpi=192", "实际=96"} {
		if !strings.Contains(out, want) {
			t.Errorf("default X11 diagnosis missing %q:\n%s", want, out)
		}
	}
}

// TestDiffStoppedServiceIsWork: "fcitx5 is running" is part of the terminal
// state (verify checks it), so a stopped unit must be planned and counted as
// work — otherwise install never opens the stop window that starts it.
func TestDiffStoppedServiceIsWork(t *testing.T) {
	c := convergedCurrent()
	c.Unit = "omarchy-fcitx5.service"
	c.ServiceActive = false
	p := Diff(catalog.DefaultDesired(), c, false)
	if !p.NeedService {
		t.Fatal("a stopped unit must be planned as service work")
	}
	if !p.NeedsApply() {
		t.Fatal("service work must count towards NeedsApply")
	}
	if !strings.Contains(p.Describe(), "start omarchy-fcitx5.service") {
		t.Errorf("plan must render the start step:\n%s", p.Describe())
	}

	c.ServiceActive = true
	if q := Diff(catalog.DefaultDesired(), c, false); q.NeedService {
		t.Error("a running unit must not be planned as work")
	}
}

// TestDiffUserModifiedIsL3Work locks invariant 13 for managed files: a
// user-modified file is rewritten (after confirmation) even when its bytes
// already match, so it must count as L3 work. The old predicate only looked at
// ContentEqual, so the plan said [跳过] while execution still prompted.
func TestDiffUserModifiedIsL3Work(t *testing.T) {
	c := freshCurrent()
	c.ContentEqual = map[string]bool{"default.custom.yaml": true}
	c.Managed["default.custom.yaml"] = patches.StatusUserModified
	p := Diff(catalog.DefaultDesired(), c, false)
	if !p.NeedL3 {
		t.Fatal("a user-modified managed file counts as L3 work (invariant 13)")
	}
	if !p.NeedDeploy {
		t.Error("L3 work must force a rebuild")
	}
}

// TestPlanServiceNeedsStrayHint: when a non-unit fcitx5 holds the bus name, the
// "start the service" step must say so — the start cannot succeed until it is
// gone, and the user reads this line before the run touches anything.
func TestPlanServiceNeedsStrayHint(t *testing.T) {
	c := convergedCurrent()
	c.Unit = "omarchy-fcitx5.service"
	c.ServiceActive = false
	c.StrayFcitx = true
	p := Diff(catalog.DefaultDesired(), c, false)
	var step string
	for _, s := range p.Steps {
		if s.Layer == "L4" && strings.Contains(s.Title, "start ") {
			step = s.Title
		}
	}
	if !strings.Contains(step, "非单元 fcitx5") {
		t.Errorf("service step must warn about the stray instance, got %q", step)
	}
	// plan == execution: install clears the stray itself (startUnit→KillStray),
	// so the plan must not tell the user to run pkill by hand.
	if strings.Contains(step, "pkill") {
		t.Errorf("the plan must not delegate the cleanup to the user, got %q", step)
	}
	if !strings.Contains(step, "先清理再启动") {
		t.Errorf("service step must say install clears the stray, got %q", step)
	}
}

// TestPlanServiceNeedsFlapRepair: a unit that reads "active" while a second
// fcitx5 coexists is flapping under Restart=always, so the plan must schedule
// the start (which clears the stray) — the transient reading must not declare
// the service satisfied forever.
func TestPlanServiceNeedsFlapRepair(t *testing.T) {
	c := convergedCurrent()
	c.Unit = "omarchy-fcitx5.service"
	c.ServiceActive = true
	c.FcitxCount = 2
	p := Diff(catalog.DefaultDesired(), c, false)
	if !p.NeedService {
		t.Error("a service flapping beside a stray must be scheduled for repair")
	}
	var step string
	for _, s := range p.Steps {
		if s.Layer == "L4" && strings.Contains(s.Title, "start ") {
			step = s.Title
		}
	}
	for _, want := range []string{"抖动", "先清理再启动"} {
		if !strings.Contains(step, want) {
			t.Errorf("service step must explain the flap (%q), got %q", want, step)
		}
	}
}

// TestDiffForceL2Deploys: `update` re-extracts the rime data files, so even when
// the bytes are identical the sources end up newer than build/. The deploy must
// follow, or the host is left with a source/build timestamp inversion that reads
// like a stale build (dev-notes P4) — and `update`'s contract is "refresh and
// recompile".
func TestDiffForceL2Deploys(t *testing.T) {
	// Reuse the fully-converged fixture: every layer satisfied, so any deploy
	// that shows up can only come from the forced refresh.
	c := convergedCurrent()
	d := catalog.DefaultDesired()

	if plain := Diff(d, c, false); plain.NeedDeploy {
		t.Fatal("a converged host must not deploy (idempotence)")
	}
	forced := Diff(d, c, true)
	if !forced.NeedL2 {
		t.Error("forceL2 must refresh L2")
	}
	if !forced.NeedDeploy {
		t.Error("forceL2 must also deploy: re-extracted sources would never be compiled")
	}
}

// TestDiffGroupShapeDriftIsHostWork: the group SHAPE is terminal state. fcitx5
// gates every IM hotkey on len(items) > 1, so a stale second item (or the wrong
// enumeration order) must open the L4 stop window instead of passing as
// "rime is registered".
func TestDiffGroupShapeDriftIsHostWork(t *testing.T) {
	d := catalog.DefaultDesired()
	c := convergedCurrent()
	c.GroupIMs = []string{catalog.IMEnglish, catalog.IMRime} // same items, wrong order
	p := Diff(d, c, false)
	if !p.NeedHost {
		t.Fatalf("group order drift must be host work:\n%s", p.Describe())
	}
	if out := p.Describe(); !strings.Contains(out, "输入法组 [keyboard-us rime] → [rime keyboard-us]") {
		t.Errorf("plan must name the group drift:\n%s", out)
	}
}

// TestDiffAltTriggerDriftIsHostWork: fcitx5 ships AltTriggerKeys=Shift_L. With
// >1 IM it fires the same toggle() as the trigger key, so left Shift stops
// switching 中英 inside rime. Convergence empties the section; drift = work.
func TestDiffAltTriggerDriftIsHostWork(t *testing.T) {
	d := catalog.DefaultDesired()
	c := legacyCurrent()
	c.AltTriggerOK = false
	c.AltTriggerKeys = []string{"Shift_L"}
	p := Diff(d, c, false)
	if !p.NeedHost {
		t.Fatalf("AltTriggerKeys drift must be host work:\n%s", p.Describe())
	}
	out := p.Describe()
	if !strings.Contains(out, "清空 [Hotkey/AltTriggerKeys]") || !strings.Contains(out, "Shift_L") {
		t.Errorf("plan must name the alt-trigger drift:\n%s", out)
	}
}

// TestDiffControlSpaceResidueIsHostWork: on 4.5+ the only hotkey drift ompinyin
// still owns is the chord IT wrote in older versions. Convergence removes it and
// must say so (the user has to know Ctrl+Space moved back to the terminal).
func TestDiffControlSpaceResidueIsHostWork(t *testing.T) {
	d := catalog.DefaultDesired()
	c := convergedCurrent()
	c.ControlSpace = true
	p := Diff(d, c, false)
	if !p.NeedHost {
		t.Fatalf("a leftover Control+space must be host work:\n%s", p.Describe())
	}
	out := p.Describe()
	for _, want := range []string{"Control+space", "Super+I"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan must explain the hotkey hand-over (%q):\n%s", want, out)
		}
	}
	// The legacy AltTrigger clause must NOT appear on 4.5: Omarchy writes that
	// section itself, so claiming we would empty it would be a lie.
	if strings.Contains(out, "AltTriggerKeys") {
		t.Errorf("4.5+ plan must not promise AltTriggerKeys work:\n%s", out)
	}
}

// TestDiffGroupDriftNamesRemovedEngine: D2 — the group shape stays ours, and the
// plan must name the engine it removes and how to put it back (the installer's
// own `pinyin` is the common case), not just "the group drifted".
func TestDiffGroupDriftNamesRemovedEngine(t *testing.T) {
	d := catalog.DefaultDesired()
	c := convergedCurrent()
	c.GroupIMs = []string{catalog.IMEnglish, "pinyin", catalog.IMRime}

	p := Diff(d, c, false)
	if !p.NeedHost {
		t.Fatal("engine surplus must be host work")
	}
	out := p.Describe()
	for _, want := range []string{"pinyin", "移出组", "omarchy setup input pinyin"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan must name the removed engine (%q):\n%s", want, out)
		}
	}
}

// TestDiffBarIndicatorIsTrayWork: on 4.5+ the terminal state is Omarchy's own
// widget being on the bar — a missing widget or a leftover drop-in/pin is the
// whole L4 tray step.
func TestDiffBarIndicatorIsTrayWork(t *testing.T) {
	d := catalog.DefaultDesired()

	c := convergedCurrent()
	c.BarIndicator = false
	p := Diff(d, c, false)
	if !p.NeedTray {
		t.Fatal("a missing bar indicator must be tray work")
	}
	if out := p.Describe(); !strings.Contains(out, "omarchy.keyboard-layout") || !strings.Contains(out, "清理旧 drop-in/pin") {
		t.Errorf("plan must describe the 4.5 tray step:\n%s", out)
	}

	c2 := convergedCurrent()
	c2.StaleTray = true // artifacts only; the widget is already there
	if q := Diff(d, c2, false); !q.NeedTray {
		t.Error("stale pre-4.5 artifacts must be cleaned up")
	}
}

// TestDiffLegacyOverrideResidueIsHostWork: the v1.0 generic override.conf
// rewrites the unit's ExecStart, so fcitx5 keeps publishing its own SNI tray item
// beside Omarchy's indicator. Like the drop-in it only takes effect on restart →
// it must open the stop window (tray work alone would never restart the unit).
func TestDiffLegacyOverrideResidueIsHostWork(t *testing.T) {
	d := catalog.DefaultDesired()
	c := convergedCurrent()
	c.StaleOverride = true
	c.StaleTray = true

	p := Diff(d, c, false)
	if !p.NeedHost {
		t.Fatalf("a leftover override.conf must be host work:\n%s", p.Describe())
	}
	if !p.NeedTray {
		t.Errorf("it is also tray residue:\n%s", p.Describe())
	}
	out := p.Describe()
	for _, want := range []string{"override.conf", "ExecStart", "stop 窗口"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan must explain the override (%q):\n%s", want, out)
		}
	}
	// a residual PIN or hook needs no restart: they must not open the window
	c2 := convergedCurrent()
	c2.PinnedHasFc, c2.StaleTray = true, true
	if q := Diff(d, c2, false); q.NeedHost {
		t.Error("an SNI pin alone must not open the stop window")
	}
	c3 := convergedCurrent()
	c3.ThemeHookOK, c3.StaleTray = true, true
	if q := Diff(d, c3, false); q.NeedHost {
		t.Error("a theme hook alone must not open the stop window")
	}
}
