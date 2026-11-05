// Package plan implements the convergence planner: diff(desired, current) →
// an ordered, layer-tagged step list (§3). The same diff powers install /
// update dry-run preview and status reporting.
package plan

import (
	"fmt"
	"strings"

	"github.com/ProjectAILeap/ompinyin/internal/bar"
	"github.com/ProjectAILeap/ompinyin/internal/catalog"
	"github.com/ProjectAILeap/ompinyin/internal/observe"
	"github.com/ProjectAILeap/ompinyin/internal/patches"
)

// Step is one convergence step with its layer and a human description.
// Needed=false steps render as [跳过].
type Step struct {
	Layer  string // L1..L5
	Title  string
	Needed bool
}

// Plan is the ordered list of steps derived from diffing current state against
// the desired terminal state, plus the typed per-layer decisions.
//
// The Need* fields are computed by the very predicates that render the steps,
// so Install consumes the SAME decision it just printed — that is what makes
// --dry-run truthful and a converged re-run silent (评审 P0-9 tail).
type Plan struct {
	Steps []Step

	NeedL1      bool // pacman work outstanding
	NeedL2      bool // assets must be fetched/extracted
	NeedL3      bool // managed files differ from the generated content
	NeedDeploy  bool // rime_deployer --build must run
	NeedHost    bool // profile / hotkey / drop-in work
	NeedService bool // the discovered fcitx5 unit is not running
	NeedTray    bool // notificationitem drop-in or the Fcitx pin
	NeedTheme   bool // candidate-window theming work (§6.6)
	NeedHidpi   bool // X11 Xft.dpi block / publisher units / published value
	// NeedHidpiUndo withdraws a previously opted-in compat mode: the managed
	// Xresources block or the two publisher units exist, but Desired no longer
	// opts in. Keeping it separate from NeedHidpi keeps the fcitx5 stop window
	// (which only applies the mode) free of teardown work.
	NeedHidpiUndo bool
}

// NeedsApply reports whether any mutating layer has work. L5 is read-only and
// never counts, so a converged host re-run returns false and skips the backup.
func (p *Plan) NeedsApply() bool {
	return p.NeedL1 || p.NeedL2 || p.NeedL3 || p.NeedDeploy || p.NeedHost || p.NeedService || p.NeedTray || p.NeedTheme || p.NeedHidpi || p.NeedHidpiUndo
}

// New returns an empty plan.
func New() *Plan { return &Plan{} }

// Add appends a step and returns the plan for chaining.
func (p *Plan) Add(layer, title string, needed bool) *Plan {
	p.Steps = append(p.Steps, Step{Layer: layer, Title: title, Needed: needed})
	return p
}

// Describe renders the human-friendly step listing with [计划]/[跳过] markers.
func (p *Plan) Describe() string {
	var out string
	for _, s := range p.Steps {
		mark := "[跳过]"
		if s.Needed {
			mark = "[计划]"
		}
		out += fmt.Sprintf("%s %-4s %s\n", mark, s.Layer, s.Title)
	}
	return out
}

// Diff builds the convergence plan for the desired state against the observed
// current snapshot (§3 五层收敛 + §6.0 时序). forceL2 marks a run that must
// refresh L2 regardless of how populated the host looks: `update`, or an
// `install` whose --channel differs from the recorded terminal state (the
// channel is part of Desired, so stale bytes must not be silently kept).
func Diff(d catalog.Desired, c *observe.Current, forceL2 bool) *Plan {
	p := New()

	// L1 pkgs
	missingPkgs := append([]string{}, c.PackagesMissing...)
	// xorg-xrdb is only needed when the mode actually publishes (compositor
	// scaling off). Inert mode must not drag an unrelated package in.
	if d.X11HiDPI && c.X11ForceZeroScaling && c.X11PackageMissing {
		missingPkgs = append(missingPkgs, "xorg-xrdb")
	}
	p.NeedL1 = len(missingPkgs) > 0
	if p.NeedL1 {
		p.Add("L1", fmt.Sprintf("pacman -S --needed %s", strings.Join(missingPkgs, " ")), true)
	} else {
		p.Add("L1", "系统包已齐（fcitx5-rime fcitx5-configtool fcitx5-gtk）", false)
	}

	// L2 assets: needed when data is missing (either the rime-ice data dir or
	// the wanxiang gram) or when the run explicitly forces a refresh (P1-2).
	p.NeedL2 = forceL2 || !c.RimeDataExists || (d.Model && !c.GramFileExists)
	if p.NeedL2 {
		title := "获取雾凇 full.zip + 万象 LMDG 模型并落位"
		if forceL2 {
			title = "强制刷新雾凇 full.zip + 万象 LMDG 模型并重编译（update/channel 变更）"
		}
		p.Add("L2", title, true)
	} else {
		p.Add("L2", "数据资产已就位", false)
	}

	// L3 patches. Iterate the DESIRED managed set, not the observed map: a file
	// observe did not report is by definition not converged (this replaces the
	// old `len(c.Managed) < 3` magic number, 评审 P1-12).
	var userTouched []string
	for _, f := range patches.ManagedFiles(d) {
		// Keep this predicate identical to the write loop in Install: a
		// user-modified/foreign file is rewritten (after confirmation) even when
		// its bytes already equal the desired content, so it counts as L3 work
		// (invariant 13). Without the status term the plan said [跳过] while the
		// execution still prompted to overwrite.
		switch c.Managed[f.RelPath] {
		case patches.StatusUserModified, patches.StatusForeign:
			p.NeedL3 = true
			userTouched = append(userTouched, f.RelPath)
		default:
			if !c.ContentEqual[f.RelPath] {
				p.NeedL3 = true
			}
		}
	}
	if len(c.Orphans) > 0 {
		p.NeedL3 = true
	}
	if p.NeedL3 {
		title := fmt.Sprintf("生成受管 custom.yaml（default + radical_pinyin + melt_eng%s）",
			modelSuffix(d))
		if len(c.Orphans) > 0 {
			title += fmt.Sprintf("；删除孤儿受管文件：%s", strings.Join(c.Orphans, ", "))
		}
		if len(userTouched) > 0 {
			title += fmt.Sprintf("；注意：将被备份后覆盖的用户改动文件：%s", strings.Join(userTouched, ", "))
		}
		p.Add("L3", title, true)
	} else {
		p.Add("L3", "受管配置与终态一致", false)
	}

	// stop window: deploy + profile + hotkey + drop-in.
	// The deploy (rime_deployer --build) must re-run not only when build
	// artifacts are missing but also when managed content changed (schema
	// list / grammar edits), otherwise the new config is never compiled.
	// A forced L2 refresh re-extracts the data files, so even when the bytes are
	// identical the rime sources end up NEWER than build/ — and the deploy is the
	// only thing that puts them back in sync. Skipping it left `update` with a
	// source/build timestamp inversion that reads exactly like the stale-build
	// trap (docs/dev-notes.md P4), so `update` always deploys.
	p.NeedDeploy = len(c.BuildMissing) > 0 || p.NeedL3 || forceL2
	// The group SHAPE (not just "is rime registered") is part of L4: fcitx5
	// gates every IM hotkey on len(GroupIMs) > 1, so a stale second IM changes
	// what the IM hotkeys do.
	groupDrift := !catalog.SameIMs(c.GroupIMs, d.IMsOrDefault())
	if c.OmarchyManagesIME {
		// Omarchy 4.5+ owns the IM hotkeys (its config ships AltTriggerKeys=Shift_L,
		// `configure.py` deletes any Control+space on every --defaults, `Super+I`
		// cycles) and the tray indicator. Convergence keeps ONLY the group shape and
		// removes the chord older ompinyin versions wrote — fighting upstream there
		// is a permanent patch war (adapt 4.5 D1).
		//
		// DropInExists / StaleOverride are host work for a SECOND reason: deleting a
		// drop-in (ours or v1.0's generic override.conf) only takes effect when the
		// unit restarts, so it has to happen inside the stop window. Without these
		// terms the upgrade run would delete the files after the restart and no
		// later run would ever reopen the window — the duplicate SNI icon would
		// survive until the next login.
		p.NeedHost = !c.ProfileHasRime || groupDrift || c.ControlSpace || c.DropInExists || c.StaleOverride
	} else {
		p.NeedHost = !c.ProfileHasRime || !c.HotkeyOK || !c.DropInOK || groupDrift || !c.AltTriggerOK
	}
	// "fcitx5 is running" is part of the L4 terminal state (verify checks it,
	// §6.4 needs the SNI item live). Without this predicate a host whose unit
	// was stopped by hand could never be repaired: no other layer had work, so
	// the stop window — the only place service.Start runs — never opened.
	// FcitxCount>1 is that same repair for a flap: the unit reads "active" for
	// the few hundred ms its doomed instance lives, so "active" alone would
	// declare the service satisfied and never clear the stray that causes it.
	p.NeedService = c.Unit != "" && (!c.ServiceActive || c.FcitxCount > 1)
	if c.OmarchyManagesIME {
		// The indicator is Omarchy's own bar widget; the only tray work left is
		// making sure it is on the bar and dropping the pre-4.5 drop-in/pin.
		p.NeedTray = !c.BarIndicator || c.StaleTray
	} else {
		p.NeedTray = !c.PinnedHasFc || !c.DropInOK
	}
	// A zero desired DPI is the legacy/test snapshot meaning that X11 facts
	// were not collected. Real observe.Collect always supplies at least 96.
	// force_zero_scaling=false means Hyprland already scales X11 windows
	// itself, so publishing Xft.dpi would double-scale every X11 client: the
	// mode is inert there (it is a no-op, not a failure).
	p.NeedHidpi = d.X11HiDPI && c.X11ForceZeroScaling && c.X11DPIDesired > 0 && (!c.X11ConfigOK || !c.X11UnitsOK || (c.X11Available && c.X11DPIActual != c.X11DPIDesired))
	// Opt-out is itself convergence work: the mode's artifacts (managed block
	// and/or publisher units) must be withdrawn, or --no-x11-hidpi would be a
	// lie and the units would keep re-publishing Xft.dpi on every scale edit.
	// Presence, not content equality: a stale or half-removed unit is still an
	// owned artifact that must be withdrawn (X11UnitsOK would miss it).
	p.NeedHidpiUndo = !d.X11HiDPI && (c.X11ManagedPresent || c.X11UnitsPresent)
	switch {
	case p.NeedDeploy || p.NeedHost:
		inner := "profile/hotkey/drop-in"
		if c.OmarchyManagesIME {
			inner = "profile/清理旧触发键"
		}
		p.Add("L4", fmt.Sprintf("stop %s → rime_deployer --build → %s → start%s", unitName(c), inner, hostDriftNote(d, c)), true)
	case p.NeedService:
		state := "服务未运行"
		if c.ServiceActive {
			state = "服务在 Restart=always 下抖动（游离实例占着 bus name）"
		}
		p.Add("L4", fmt.Sprintf("start %s（%s；fcitx5 存活是终态的一部分%s）", unitName(c), state, strayNote(c)), true)
	default:
		p.Add("L4", "部署产物与宿主注册均已达成", false)
	}

	// L4 tray: 4.5+ = confirm Omarchy's bar indicator + drop the old artifacts
	if p.NeedTray {
		if c.OmarchyManagesIME {
			p.Add("L4", "顶栏输入法指示器（omarchy.keyboard-layout）+ 清理旧 drop-in/pin", true)
		} else {
			p.Add("L4", "顶栏图标：启用 notificationitem + pin Fcitx（读→合并→set）", true)
		}
	} else if c.OmarchyManagesIME {
		p.Add("L4", "顶栏输入法指示器已在 bar 上（"+bar.WidgetID+"）", false)
	} else {
		p.Add("L4", "顶栏图标已 pin", false)
	}
	if !d.X11HiDPI {
		if p.NeedHidpiUndo {
			p.Add("L4", "X11 HiDPI：撤销 opt-in——移除受管 Xft.dpi 块并禁用/删除缩放监听单元", true)
		} else {
			p.Add("L4", x11OptionalNote(c), false)
		}
	} else if !c.X11ForceZeroScaling {
		p.Add("L4", "X11 HiDPI：Hyprland force_zero_scaling=false（合成器已在缩放 X11 窗口），不发布 Xft.dpi（会二次放大）；如需本模式请把 force_zero_scaling 设为 true", false)
	} else if c.X11DPIDesired == 0 {
		p.Add("L4", "X11 HiDPI：未采集 X11 事实", false)
	} else if p.NeedHidpi {
		p.Add("L4", fmt.Sprintf("X11 HiDPI：收敛 Xft.dpi=%d、发布 xrdb 并安装缩放监听%s%s", c.X11DPIDesired, c.X11ForeignNote(), c.X11UnitExecNote()), true)
	} else {
		p.Add("L4", fmt.Sprintf("X11 HiDPI 已发布 Xft.dpi=%d%s%s", c.X11DPIDesired, c.X11ForeignNote(), c.X11UnitExecNote()), false)
	}

	// L4 candidate-window theming (§6.6): classicui is ours, the colors and the
	// theme dir belong to Omarchy's generator. A surviving theme hook is residue
	// from <=1.3 and counts as work (it races omarchy-theme-set-fcitx5).
	p.NeedTheme = !c.ThemeEqual || c.ThemeHookOK
	if p.NeedTheme {
		p.Add("L4", "候选框主题：classicui.conf→omarchy + 交给 omarchy-theme-set-fcitx5（并清理旧钩子）", true)
	} else {
		p.Add("L4", "候选框已跟随 Omarchy 主题", false)
	}

	// L5 verify (read-only)
	p.Add("L5", "复核：build 产物 / grammar 编入 / IM 三态 / 托盘可见 / 候选框主题", true)
	return p
}

// hostDriftNote names the host halves that are off target — the L4 title is the
// only place a human sees WHY the stop window opened (a converged re-run shows
// none of them). Empty when profile/hotkey/drop-in are all on target.
func hostDriftNote(d catalog.Desired, c *observe.Current) string {
	var parts []string
	want := d.IMsOrDefault()
	if !catalog.SameIMs(c.GroupIMs, want) {
		have := "无"
		if len(c.GroupIMs) > 0 {
			have = strings.Join(c.GroupIMs, " ")
		}
		note := fmt.Sprintf("输入法组 [%s] → [%s]", have, strings.Join(want, " "))
		// D2: the installer's own `pinyin` engine leaves the group; the engine and
		// its package stay installed, so say how to put it back instead of letting
		// the user discover the removal afterwards.
		if removed := catalog.RemovedIMs(c.GroupIMs, want); len(removed) > 0 {
			note += fmt.Sprintf("；将把 %s 移出组", strings.Join(removed, " "))
			// D2: an engine's package and dictionary stay, so name the way back.
			// A keyboard-* item is not an engine and `omarchy setup input` has no
			// preset for it, so it gets no hint.
			for _, im := range removed {
				if !strings.HasPrefix(im, "keyboard-") {
					note += fmt.Sprintf("（引擎与包保留，`omarchy setup input %s` 可加回）", im)
					break
				}
			}
		}
		parts = append(parts, note)
	}
	if c.OmarchyManagesIME {
		if c.DropInExists {
			parts = append(parts, "移除旧 notificationitem drop-in（只在 stop 窗口内生效：重启后 fcitx5 不再发第二个托盘图标）")
		}
		if c.StaleOverride {
			parts = append(parts, "移除 v1.0 的 override.conf（它覆盖了 Omarchy 的 ExecStart，同样只在 stop 窗口内生效）")
		}
		if c.ControlSpace {
			parts = append(parts, "删除 [Hotkey/TriggerKeys] 中的 Control+space（触发键归 Omarchy：Super+I 循环、左 Shift 进出输入法；Ctrl+Space 留给终端）")
		}
	} else if !c.AltTriggerOK {
		bound := "缺段（fcitx5 会回落 Shift_L）"
		if len(c.AltTriggerKeys) > 0 {
			bound = strings.Join(c.AltTriggerKeys, " ")
		}
		parts = append(parts, fmt.Sprintf("清空 [Hotkey/AltTriggerKeys]（现为 %s；组内 >1 个 IM 时它会把左 Shift 变成「切输入法」，与 Ctrl+Space 同效）", bound))
	}
	if len(parts) == 0 {
		return ""
	}
	return "；" + strings.Join(parts, "；")
}

// x11OptionalNote renders the read-only X11 HiDPI diagnosis shown when the
// optional compat mode is off: the focused scale plus expected and currently
// published DPI. It is the human-readable half of "default = diagnose only"
// (the machine-readable half lives in status/doctor --json host).
func x11OptionalNote(c *observe.Current) string {
	if c.X11DPIDesired == 0 {
		return "X11 HiDPI 兼容模式未启用（未采集 X11 事实；Xft.dpi 是全局 XWayland 资源，默认只诊断，候选框过小时显式 --x11-hidpi）"
	}
	actual := "无 X 会话"
	if c.X11Available {
		actual = fmt.Sprintf("%d", c.X11DPIActual)
	}
	return fmt.Sprintf("X11 HiDPI 兼容模式未启用（scale=%.2f 期望 Xft.dpi=%d 实际=%s；Xft.dpi 是全局 XWayland 资源，默认只诊断，候选框过小时显式 --x11-hidpi）",
		c.X11Scale, c.X11DPIDesired, actual)
}

func modelSuffix(d catalog.Desired) string {
	if d.Model {
		schemas := d.SchemaList()
		return " + grammar × " + fmt.Sprint(len(schemas)) + " 个方案"
	}
	return "（Model=false，无方案级 grammar）"
}

// strayNote is the short plan-time hint for an unmanaged fcitx5 holding the
// bus name: the unit cannot start until it is gone, and install now clears it
// itself (service.startUnit → KillStray), so the plan must not ask the user to
// run pkill by hand.
func strayNote(c *observe.Current) string {
	if !c.StrayFcitx && c.FcitxCount <= 1 {
		return ""
	}
	return "；检测到非单元 fcitx5 进程占着 org.fcitx.Fcitx5，安装时会先清理再启动"
}

func unitName(c *observe.Current) string {
	if c.Unit != "" {
		return c.Unit
	}
	return "omarchy-fcitx5.service"
}
