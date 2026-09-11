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
	if c.Unit != "" && c.ServiceActive {
		n, err := service.RemoteState()
		if err != nil {
			out = append(out, Check{Name: "IM 三态", OK: false, Detail: "fcitx5-remote 不可用: " + err.Error()})
		} else {
			what := map[int]string{0: "未激活", 1: "英文", 2: "中文"}[n]
			out = append(out, Check{Name: "IM 三态", OK: true, Detail: fmt.Sprintf("fcitx5-remote=%d（%s）；往返切换用触发键", n, what)})
		}
	} else {
		out = append(out, Check{Name: "IM 三态", OK: false, Detail: "fcitx5 服务未运行"})
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
		out = append(out, Check{Name: "X11 HiDPI", OK: c.X11ConfigOK && c.X11UnitsOK && c.X11DPIActual == c.X11DPIDesired,
			Detail: fmt.Sprintf("Xft.dpi 期望=%d 实际=%d（scale=%.2f）%s", c.X11DPIDesired, c.X11DPIActual, c.X11Scale, x11ForeignNote(c))})
	} else {
		out = append(out, Check{Name: "X11 HiDPI", OK: c.X11ConfigOK && c.X11UnitsOK,
			Detail: "未检测到 XWayland；已收敛 Xresources 与缩放监听，待 X11 会话发布" + x11ForeignNote(c)})
	}

	// 5. candidate-window theming (§6.6)
	out = append(out, themeCheck(c))

	return out
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
		return fmt.Sprintf("未启用（scale=%.2f 期望 Xft.dpi=%d 实际=%d）%s；Xft.dpi 是全局 XWayland 资源，候选框过小时用 install --x11-hidpi 启用。",
			c.X11Scale, c.X11DPIDesired, c.X11DPIActual, residual)
	}
	return fmt.Sprintf("未启用（scale=%.2f 期望 Xft.dpi=%d；无可用 X 会话）%s；Xft.dpi 是全局 XWayland 资源，候选框过小时用 install --x11-hidpi 启用。",
		c.X11Scale, c.X11DPIDesired, residual)
}

// x11ForeignNote warns when ~/.Xresources assigns Xft.dpi outside ompinyin's
// managed block. ompinyin never removes a competing line, and xrdb applies
// assignments in file order (last wins), so a competing line after the block
// defeats convergence — the live-value diff already fails L5 in that case;
// this note explains why before the user has to read the raw values.
func x11ForeignNote(c *observe.Current) string {
	if !c.X11ForeignDPI {
		return ""
	}
	return "；注意：~/.Xresources 存在块外 Xft.dpi 行（xrdb 按文件顺序、后者胜，冲突时以实际值为准）"
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

// checkGrammarCompiled greps the compiled schema for the model language and
// the official collocation penalty (真机 checklist §9).
func checkGrammarCompiled(d catalog.Desired, c *observe.Current) Check {
	probe := d.SchemaList()
	if len(probe) == 0 {
		return Check{Name: "grammar 编入", OK: false, Detail: "schema_list 为空"}
	}
	p := filepath.Join(c.RimeDir, "build", probe[0]+".schema.yaml")
	b, err := os.ReadFile(p)
	if err != nil {
		return Check{Name: "grammar 编入", OK: false, Detail: "无法确认 grammar 编入（build 产物缺失，见上）"}
	}
	s := string(b)
	// rime 编译产物会把值加引号（如 collocation_penalty: "-14"），检查须容忍引号。
	hasLang := strings.Contains(s, catalog.GrammarLanguage)
	hasPenalty := strings.Contains(s, fmt.Sprintf("collocation_penalty: %d", catalog.GrammarCollocationPenalty)) ||
		strings.Contains(s, fmt.Sprintf("collocation_penalty: \"%d\"", catalog.GrammarCollocationPenalty))
	switch {
	case hasLang && hasPenalty:
		return Check{Name: "grammar 编入", OK: true, Detail: probe[0] + " 含 " + catalog.GrammarLanguage + " + 官方惩罚项"}
	case hasLang:
		return Check{Name: "grammar 编入", OK: false, Detail: probe[0] + " 含模型但惩罚项非官方值（可能被其它工具改写）"}
	default:
		return Check{Name: "grammar 编入", OK: false, Detail: probe[0] + " 未编入万象 grammar（重跑一次收敛）"}
	}
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
// check the known path (and a fallback walk) for 10-omarchy-fcitx.conf
func checkEnvRedLine() (bool, string) {
	omarchyConf := "/usr/share/omarchy/default/environment.d/10-omarchy-fcitx.conf"
	found := false
	if _, err := os.Stat(omarchyConf); err == nil {
		found = true
	} else {
		_ = filepath.WalkDir("/usr/share/omarchy", func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && d.Name() == "10-omarchy-fcitx.conf" {
				found = true
			}
			return nil
		})
	}
	if !found {
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
