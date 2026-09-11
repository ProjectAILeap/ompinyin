// Package plan implements the convergence planner: diff(desired, current) →
// an ordered, layer-tagged step list (§3). The same diff powers install /
// update dry-run preview and status reporting.
package plan

import (
	"fmt"
	"strings"

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

// HasWork reports whether any step needs execution.
func (p *Plan) HasWork() bool {
	for _, s := range p.Steps {
		if s.Needed {
			return true
		}
	}
	return false
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
	p.NeedDeploy = len(c.BuildMissing) > 0 || p.NeedL3
	p.NeedHost = !c.ProfileHasRime || !c.HotkeyOK || !c.DropInOK
	// "fcitx5 is running" is part of the L4 terminal state (verify checks it,
	// §6.4 needs the SNI item live). Without this predicate a host whose unit
	// was stopped by hand could never be repaired: no other layer had work, so
	// the stop window — the only place service.Start runs — never opened.
	p.NeedService = c.Unit != "" && !c.ServiceActive
	p.NeedTray = !c.PinnedHasFc || !c.DropInOK
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
		p.Add("L4", fmt.Sprintf("stop %s → rime_deployer --build → profile/hotkey/drop-in → start", unitName(c)), true)
	case p.NeedService:
		p.Add("L4", fmt.Sprintf("start %s（服务未运行；fcitx5 存活是终态的一部分）", unitName(c)), true)
	default:
		p.Add("L4", "部署产物与宿主注册均已达成", false)
	}

	// L4 tray (mandatory terminal state, ADR 12)
	if p.NeedTray {
		p.Add("L4", "顶栏图标：启用 notificationitem + pin Fcitx（读→合并→set）", true)
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
		p.Add("L4", fmt.Sprintf("X11 HiDPI：收敛 Xft.dpi=%d、发布 xrdb 并安装缩放监听%s", c.X11DPIDesired, x11ForeignNote(c)), true)
	} else {
		p.Add("L4", fmt.Sprintf("X11 HiDPI 已发布 Xft.dpi=%d%s", c.X11DPIDesired, x11ForeignNote(c)), false)
	}

	// L4 candidate-window theming (§6.6)
	p.NeedTheme = !c.ThemeEqual || !c.ThemeDirOK
	if p.NeedTheme {
		p.Add("L4", "候选框主题：classicui.conf→omarchy + theme-set 钩子 + 立即按当前 Omarchy 主题生成", true)
	} else {
		p.Add("L4", "候选框已跟随 Omarchy 主题", false)
	}

	// L5 verify (read-only)
	p.Add("L5", "复核：build 产物 / grammar 编入 / IM 三态 / 托盘可见 / 候选框主题", true)
	return p
}

// x11ForeignNote warns when ~/.Xresources assigns Xft.dpi outside ompinyin's
// managed block. Ownership is line-scoped, so a competing line is never
// removed; xrdb applies assignments in file order (last wins), which makes the
// outcome ordering-dependent. Surface it instead of pretending convergence.
func x11ForeignNote(c *observe.Current) string {
	if !c.X11ForeignDPI {
		return ""
	}
	return "；注意：~/.Xresources 存在块外 Xft.dpi 行（xrdb 按文件顺序、后者胜）"
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

func unitName(c *observe.Current) string {
	if c.Unit != "" {
		return c.Unit
	}
	return "omarchy-fcitx5.service"
}
