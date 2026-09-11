// Package verify implements L5: strictly read-only terminal-state checks
// (§3 L5) plus the doctor checklist (§7): service health, IM tri-state,
// environment-variable red line, trigger keys, tray icon, legacy dirs.
package verify

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ProjectAILeap/ompinyin/internal/catalog"
	"github.com/ProjectAILeap/ompinyin/internal/hotkey"
	"github.com/ProjectAILeap/ompinyin/internal/observe"
	"github.com/ProjectAILeap/ompinyin/internal/service"
	"github.com/ProjectAILeap/ompinyin/internal/state"
	"github.com/ProjectAILeap/ompinyin/internal/theme"
	"github.com/ProjectAILeap/ompinyin/internal/tray"
)

// Check is one verification result.
type Check struct {
	Name   string
	OK     bool
	Detail string
}

// TerminalState runs the L5 read-only convergence audit.
func TerminalState(d catalog.Desired, c *observe.Current) []Check {
	var out []Check

	// 1. build artifacts per enabled schema. Strict: with the map-form
	// schema_list, --build compiles EVERY enabled schema, so a missing artifact
	// is a real failure (short-form schema_list or deployer failure) — the old
	// lenient "懒编译" pass-through masked that (P2-6).
	missing := c.BuildMissing
	if len(missing) == 0 {
		out = append(out, Check{Name: "build 产物", OK: true,
			Detail: fmt.Sprintf("schema_list 中 %d 个方案均已编译", len(d.SchemaList()))})
	} else {
		out = append(out, Check{Name: "build 产物", OK: false,
			Detail: "产物缺失（--build 应全量编译；检查 default.custom.yaml 是否为 - schema: <id> map 格式）: " + strings.Join(missing, ", ")})
	}

	// 2. grammar compiled into build artifacts (official penalty values)
	if d.Model {
		out = append(out, checkGrammarCompiled(d, c))
	}

	// 3. IM tri-state (read-only probe of current state)
	switch {
	case c.Unit == "" || !c.ServiceActive:
		out = append(out, Check{Name: "IM 三态", OK: false, Detail: serviceDownDetail(c)})
	case c.FcitxCount > 1:
		// Restart=always flap: an unmanaged fcitx5 owns org.fcitx.Fcitx5, so the
		// unit's own instance exits immediately and `is-active` reads "active"
		// only for the few hundred ms it lives. A second process exposes it: the
		// transient reading is not a terminal state, so L5 must not pass on it.
		out = append(out, Check{Name: "IM 三态", OK: false, Detail: strayCoexistDetail(c)})
	default:
		n, err := service.RemoteState()
		if err != nil {
			out = append(out, Check{Name: "IM 三态", OK: false, Detail: "fcitx5-remote 不可用: " + err.Error()})
		} else {
			what := map[int]string{0: "未激活", 1: "英文", 2: "中文"}[n]
			out = append(out, Check{Name: "IM 三态", OK: true, Detail: fmt.Sprintf("fcitx5-remote=%d（%s）；往返切换用触发键", n, what)})
		}
	}

	// 4. tray visible: drop-in + pinned
	out = append(out, Check{Name: "托盘 drop-in", OK: c.DropInOK,
		Detail: dropInDetail(c)})
	out = append(out, Check{Name: "托盘 pin", OK: c.PinnedHasFc,
		Detail: map[bool]string{true: "omarchy.tray.pinned 含 Fcitx", false: "omarchy.tray.pinned 不含 Fcitx"}[c.PinnedHasFc]})
	if !d.X11HiDPI {
		out = append(out, Check{Name: "X11 HiDPI（可选）", OK: true, Detail: x11OptionalDetail(c)})
	} else if !c.X11ForceZeroScaling {
		out = append(out, Check{Name: "X11 HiDPI", OK: true,
			Detail: "Hyprland force_zero_scaling=false：合成器已在缩放 X11 窗口，无需 Xft.dpi（发布反而会二次放大）"})
	} else if c.X11Available {
		out = append(out, Check{Name: "X11 HiDPI", OK: c.X11ConfigOK && c.X11UnitsOK && !c.X11UnitExecMissing && c.X11DPIActual == c.X11DPIDesired,
			Detail: fmt.Sprintf("Xft.dpi 期望=%d 实际=%d（scale=%.2f）%s%s", c.X11DPIDesired, c.X11DPIActual, c.X11Scale, c.X11ForeignNote(), c.X11UnitExecNote())})
	} else {
		out = append(out, Check{Name: "X11 HiDPI", OK: c.X11ConfigOK && c.X11UnitsOK && !c.X11UnitExecMissing,
			Detail: "未检测到 XWayland；已收敛 Xresources 与缩放监听，待 X11 会话发布" + c.X11ForeignNote() + c.X11UnitExecNote()})
	}

	// 5. candidate-window theming (§6.6)
	out = append(out, themeCheck(c))

	return out
}

// serviceDownDetail explains a stopped fcitx5 with an actionable remedy. Two
// traps worth naming: the unit cannot start while a non-unit fcitx5 owns
// org.fcitx.Fcitx5 (a stray is easy to create — any fcitx5-remote/bus call in a
// shell D-Bus-activates one), and systemd's start rate limit then makes even a
// correct start fail as "repeated too quickly", so the remedy clears it first.
func serviceDownDetail(c *observe.Current) string {
	unit := c.Unit
	if unit == "" {
		unit = "omarchy-fcitx5.service"
	}
	start := fmt.Sprintf("`systemctl --user reset-failed %s && systemctl --user start %s`", unit, unit)
	if c.StrayFcitx {
		return fmt.Sprintf("fcitx5 服务未运行，但检测到非单元的 fcitx5 进程占着 org.fcitx.Fcitx5 —— 先 `pkill -x fcitx5`，再 %s", start)
	}
	return "fcitx5 服务未运行：" + start
}

// strayCoexistDetail explains a unit that reads "active" while a second fcitx5
// coexists: the unmanaged instance holds org.fcitx.Fcitx5, so the unit's own
// process exits immediately and Restart=always loops it. The transient
// `is-active` reading is not a terminal state.
func strayCoexistDetail(c *observe.Current) string {
	unit := c.Unit
	if unit == "" {
		unit = "omarchy-fcitx5.service"
	}
	return fmt.Sprintf("检测到 %d 个 fcitx5 进程：非单元实例占着 org.fcitx.Fcitx5，%s 在 Restart=always 下反复重启（瞬时 active 不是终态）——先 `pkill -x fcitx5`，再 `systemctl --user reset-failed %s && systemctl --user start %s`", c.FcitxCount, unit, unit, unit)
}

// x11OptionalDetail is the read-only diagnosis shown when the optional X11
// HiDPI mode is off. It reports the observed facts (not a static sentence) so
// `doctor` is really the "default = diagnose only" surface the docs promise,
// and it flags leftover artifacts that the next `install` will withdraw.
func x11OptionalDetail(c *observe.Current) string {
	if c.X11DPIDesired == 0 {
		return "未启用（未采集 X11 事实）：Xft.dpi 是全局 XWayland 资源，仅在确认旧 X11 应用候选框过小时用 install --x11-hidpi 启用。混合 DPI 多屏无法同时精确。"
	}
	residual := ""
	if c.X11ManagedPresent || c.X11UnitsPresent {
		residual = "；检测到历史产物，下次 install 将撤销"
	}
	if c.X11Available {
		return fmt.Sprintf("未启用（scale=%.2f 期望 Xft.dpi=%d 实际=%d）%s；Xft.dpi 是全局 XWayland 资源，候选框过小时用 install --x11-hidpi 启用。%s",
			c.X11Scale, c.X11DPIDesired, c.X11DPIActual, residual, c.X11UnitExecNote())
	}
	return fmt.Sprintf("未启用（scale=%.2f 期望 Xft.dpi=%d；无可用 X 会话）%s；Xft.dpi 是全局 XWayland 资源，候选框过小时用 install --x11-hidpi 启用。%s",
		c.X11Scale, c.X11DPIDesired, residual, c.X11UnitExecNote())
}

// themeCheck reports whether the candidate window follows the Omarchy theme:
// classicui.conf points at the omarchy theme, the theme-set hook is installed
// (so theme changes stay applied), and the generated theme dir is in place.
func themeCheck(c *observe.Current) Check {
	ok := c.ThemeConfOK && c.ThemeHookOK && c.ThemeDirOK
	var detail string
	switch {
	case !c.ThemeConfOK:
		detail = "classicui.conf 未指向 " + theme.ThemeName + " 主题（或缺少 managed 头）——候选框不会跟随 Omarchy 配色"
	case !c.ThemeHookOK:
		detail = "theme-set 钩子缺失——换 Omarchy 主题后候选框不会自动刷新"
	case !c.ThemeDirOK:
		detail = "候选框主题目录未生成（重跑 ompinyin install，或用 omarchy theme set 触发钩子）"
	default:
		detail = "classicui.conf→omarchy；钩子已装；主题目录已生成"
	}
	return Check{Name: "候选框主题", OK: ok, Detail: detail}
}

// checkGrammarCompiled greps the compiled schemas for the model language and
// the official collocation penalty (真机 checklist §9). EVERY enabled schema
// must carry it (§16 invariant 5): probing only schema_list[0] would pass a
// host whose second (double-pinyin) schema lost the model.
func checkGrammarCompiled(d catalog.Desired, c *observe.Current) Check {
	schemas := d.SchemaList()
	if len(schemas) == 0 {
		return Check{Name: "grammar 编入", OK: false, Detail: "schema_list 为空"}
	}
	for _, s := range schemas {
		b, err := os.ReadFile(filepath.Join(c.RimeDir, "build", s+".schema.yaml"))
		if err != nil {
			return Check{Name: "grammar 编入", OK: false, Detail: s + " 无法确认 grammar 编入（build 产物缺失，见上）"}
		}
		txt := string(b)
		// rime 编译产物会把值加引号（如 collocation_penalty: "-14"），检查须容忍引号。
		hasLang := strings.Contains(txt, catalog.GrammarLanguage)
		hasPenalty := strings.Contains(txt, fmt.Sprintf("collocation_penalty: %d", catalog.GrammarCollocationPenalty)) ||
			strings.Contains(txt, fmt.Sprintf("collocation_penalty: \"%d\"", catalog.GrammarCollocationPenalty))
		switch {
		case hasLang && hasPenalty:
			// ok
		case hasLang:
			return Check{Name: "grammar 编入", OK: false, Detail: s + " 含模型但惩罚项非官方值（可能被其它工具改写）"}
		default:
			return Check{Name: "grammar 编入", OK: false, Detail: s + " 未编入万象 grammar（重跑一次收敛）"}
		}
	}
	detail := schemas[0] + " 含 " + catalog.GrammarLanguage + " + 官方惩罚项"
	if len(schemas) > 1 {
		detail = fmt.Sprintf("schema_list 中 %d 个方案均含 %s + 官方惩罚项", len(schemas), catalog.GrammarLanguage)
	}
	return Check{Name: "grammar 编入", OK: true, Detail: detail}
}

// Doctor runs the full health checklist (§7 doctor).
func Doctor(d catalog.Desired, c *observe.Current) []Check {
	out := TerminalState(d, c)

	// service
	out = append(out, Check{Name: "服务", OK: c.Unit != "" && c.ServiceActive,
		Detail: fmt.Sprintf("unit=%s active=%v", c.Unit, c.ServiceActive)})

	// environment red line (§6.5): 10-omarchy-fcitx.conf exists; user
	// environment.d must NOT set GTK_IM_MODULE back.
	ok, detail := checkEnvRedLine()
	out = append(out, Check{Name: "环境变量红线", OK: ok, Detail: detail})

	// trigger keys
	if c.HotkeyOK {
		out = append(out, Check{Name: "触发键", OK: true,
			Detail: strings.Join(hotkey.DefaultKeys, " / ") + "（herdr Ctrl+Space 已避让）"})
	} else {
		out = append(out, Check{Name: "触发键", OK: false, Detail: "[Hotkey/TriggerKeys] 未达目标值"})
	}

	// legacy dir
	out = append(out, Check{Name: "遗留目录", OK: !c.LegacyDirExists,
		Detail: map[bool]string{true: "~/.config/fcitx/rime 存在历史副本（可 clean --legacy 清理）", false: "无"}[c.LegacyDirExists]})

	return out
}

// dropInDetail explains the notificationitem drop-in state. Presence alone is
// not enough: the file must carry an ExecStart that no longer disables the
// addon, and it must live under the unit that actually runs (§6.4).
func dropInDetail(c *observe.Current) string {
	p := c.DropInPath
	if p == "" {
		p = tray.DropInPath("~", "")
	}
	switch {
	case c.DropInOK:
		return p + " 存在且已启用 notificationitem"
	case c.DropInExists:
		return p + " 存在但 ExecStart 仍禁用 notificationitem（或内容为空）——重跑 ompinyin install"
	default:
		return "缺少专用 drop-in（" + p + "）：notificationitem 仍被禁用，顶栏不会有输入法图标"
	}
}

// Omarchy injects the IM environment via its default environment.d file;
// check the known paths (and a BOUNDED fallback walk) for 10-omarchy-fcitx.conf.

// omarchyEnvFileCandidates are the known locations of Omarchy's IM environment
// injection.
var omarchyEnvFileCandidates = []string{
	"/usr/share/omarchy/default/environment.d/10-omarchy-fcitx.conf",
	"/usr/share/omarchy/environment.d/10-omarchy-fcitx.conf",
}

// omarchyEnvMaxDepth bounds the fallback search under /usr/share/omarchy. The
// walk used to be unbounded and ran on every `doctor`; the file only ever lives
// a few levels down (…/default/environment.d/…).
const omarchyEnvMaxDepth = 4

// findOmarchyEnvFile reports whether Omarchy's fcitx environment file exists.
func findOmarchyEnvFile() bool {
	return findFileBounded(omarchyEnvFileCandidates, "/usr/share/omarchy", "10-omarchy-fcitx.conf", omarchyEnvMaxDepth)
}

// findFileBounded reports whether name exists at one of the exact candidate
// paths, or within maxDepth directory levels below root. The depth bound keeps
// a fallback search from walking a whole source tree.
func findFileBounded(candidates []string, root, name string, maxDepth int) bool {
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	found := false
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if d.IsDir() {
			rel, rerr := filepath.Rel(root, path)
			if rerr == nil && rel != "." && strings.Count(rel, string(filepath.Separator)) >= maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == name {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func checkEnvRedLine() (bool, string) {
	if !findOmarchyEnvFile() {
		return false, "未找到 Omarchy 的 10-omarchy-fcitx.conf 环境注入（版本过旧？）"
	}
	// environment.d must not set GTK_IM_MODULE
	envd := filepath.Join(state.Home(), ".config", "environment.d")
	if entries, err := os.ReadDir(envd); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".conf") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(envd, e.Name()))
			if err != nil {
				continue
			}
			if strings.Contains(string(b), "GTK_IM_MODULE") {
				return false, e.Name() + " 设置了 GTK_IM_MODULE —— Wayland 反模式，请移除（§6.5）"
			}
		}
	}
	return true, "无 IM 环境变量泄漏；Omarchy 自带注入生效"
}
