package plan

import (
	"strings"
	"testing"

	"github.com/ProjectAILeap/ompinyin/internal/catalog"
	"github.com/ProjectAILeap/ompinyin/internal/observe"
	"github.com/ProjectAILeap/ompinyin/internal/patches"
	"github.com/ProjectAILeap/ompinyin/internal/tray"
)

func freshCurrent() *observe.Current {
	return &observe.Current{
		Managed:         map[string]patches.Status{},
		PackagesMissing: []string{"fcitx5"},
	}
}

// TestDiffFreshHost: on a fresh host every layer must be [计划] needed.
func TestDiffFreshHost(t *testing.T) {
	d := catalog.DefaultDesired()
	p := Diff(d, freshCurrent(), false)
	if !p.HasWork() {
		t.Fatal("fresh host must have work")
	}
	out := p.Describe()
	for _, want := range []string{"[计划] L1", "pacman -S --needed fcitx5", "L2", "L3", "L4", "L5"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan output missing %q:\n%s", want, out)
		}
	}
	// 顶栏图标是必做终态（ADR 12）：pin 缺失 → L4 tray 步骤必须出现
	if !strings.Contains(out, "pin Fcitx") {
		t.Errorf("tray pin step missing:\n%s", out)
	}
}

// TestDiffConvergedHost: when observations match the terminal state, all
// L1–L4 steps are [跳过].
func TestDiffConvergedHost(t *testing.T) {
	d := catalog.DefaultDesired()
	c := freshCurrent()
	c.PackagesMissing = nil
	c.GramFileExists = true
	c.RimeDataExists = true
	c.Managed["default.custom.yaml"] = patches.StatusManaged
	c.Managed["radical_pinyin.custom.yaml"] = patches.StatusManaged
	c.Managed["melt_eng.custom.yaml"] = patches.StatusManaged
	c.Managed["rime_ice.custom.yaml"] = patches.StatusManaged
	c.ProfileHasRime = true
	c.HotkeyOK = true
	c.DropInExists = true
	c.DropInOK = true // present AND enabling notificationitem for the live unit
	c.PinnedHasFc = true
	c.Pinned = []string{tray.FcitxId}
	c.ThemeEqual = true
	c.ThemeDirOK = true
	c.ThemeConfOK = true
	c.ThemeHookOK = true
	c.BuildMissing = nil
	c.ContentEqual = map[string]bool{}
	for rel := range c.Managed {
		c.ContentEqual[rel] = true
	}

	p := Diff(d, c, false)
	for _, s := range p.Steps {
		if s.Layer != "L5" && s.Needed {
			t.Errorf("converged host: step %s/%s should be skipped", s.Layer, s.Title)
		}
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

// TestX11HiDPIOptOutWithdrawsArtifacts: once opted in, a host carries the
// managed Xresources block and the two units; turning the mode off must plan
// their removal (and count as work — otherwise --no-x11-hidpi is a lie).
func TestX11HiDPIOptOutWithdrawsArtifacts(t *testing.T) {
	c := freshCurrent()
	c.X11ManagedPresent = true
	c.X11UnitsOK = true
	p := Diff(catalog.DefaultDesired(), c, false)
	if !p.NeedHidpiUndo {
		t.Fatal("opt-out with existing artifacts must plan an undo")
	}
	if !p.NeedsApply() {
		t.Fatal("undo work must count towards NeedsApply")
	}
	out := p.Describe()
	if !strings.Contains(out, "撤销") {
		t.Errorf("undo step missing from plan:\n%s", out)
	}
	// a clean non-opted host has nothing to withdraw
	c2 := freshCurrent()
	if q := Diff(catalog.DefaultDesired(), c2, false); q.NeedHidpiUndo {
		t.Error("clean non-opted host must not plan an undo")
	}
}
