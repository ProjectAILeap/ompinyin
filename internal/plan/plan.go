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

	NeedL1     bool // pacman work outstanding
	NeedL2     bool // assets must be fetched/extracted
	NeedL3     bool // managed files differ from the generated content
	NeedDeploy bool // rime_deployer --build must run
	NeedHost   bool // profile / hotkey / drop-in work
	NeedTray   bool // notificationitem drop-in or the Fcitx pin
}

// NeedsApply reports whether any mutating layer has work. L5 is read-only and
// never counts, so a converged host re-run returns false and skips the backup.
func (p *Plan) NeedsApply() bool {
	return p.NeedL1 || p.NeedL2 || p.NeedL3 || p.NeedDeploy || p.NeedHost || p.NeedTray
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
	p.NeedL1 = len(c.PackagesMissing) > 0
	if p.NeedL1 {
		p.Add("L1", fmt.Sprintf("pacman -S --needed %s", strings.Join(c.PackagesMissing, " ")), true)
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
		if !c.ContentEqual[f.RelPath] {
			p.NeedL3 = true
		}
		switch c.Managed[f.RelPath] {
		case patches.StatusUserModified, patches.StatusForeign:
			userTouched = append(userTouched, f.RelPath)
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
	p.NeedTray = !c.PinnedHasFc || !c.DropInOK
	if p.NeedDeploy || p.NeedHost {
		p.Add("L4", fmt.Sprintf("stop %s → rime_deployer --build → profile/hotkey/drop-in → start", unitName(c)), true)
	} else {
		p.Add("L4", "部署产物与宿主注册均已达成", false)
	}

	// L4 tray (mandatory terminal state, ADR 12)
	if p.NeedTray {
		p.Add("L4", "顶栏图标：启用 notificationitem + pin Fcitx（读→合并→set）", true)
	} else {
		p.Add("L4", "顶栏图标已 pin", false)
	}

	// L5 verify (read-only)
	p.Add("L5", "复核：build 产物 / grammar 编入 / IM 三态 / 托盘可见", true)
	return p
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
