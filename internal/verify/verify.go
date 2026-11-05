// Package verify implements L5: strictly read-only terminal-state checks
// (§3 L5) plus the doctor checklist (§7): service health, IM tri-state,
// environment-variable red line, trigger keys, tray icon, legacy dirs.
package verify

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ProjectAILeap/ompinyin/internal/bar"
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
			detail := fmt.Sprintf("fcitx5-remote=%d（%s）", n, what)
			if c.OmarchyManagesIME {
				detail += "；切换用 Super+I（循环）或左 Shift（进出输入法）"
			} else {
				detail += "；往返切换用触发键"
			}
			out = append(out, Check{Name: "IM 三态", OK: true, Detail: detail})
		}
	}

	// 3b. the group SHAPE (fcitx5 gates every IM hotkey on len(items) > 1)
	out = append(out, checkGroupIMs(d, c))

	// 4. tray/indicator. Omarchy 4.5+ renders the IM indicator itself, so the only
	// thing ompinyin can check (and clean) is that the widget is on the bar; the
	// legacy path created a notificationitem drop-in plus an SNI pin.
	if c.OmarchyManagesIME {
		out = append(out, checkBarIndicator(c))
		out = append(out, checkExecStart(c))
	} else {
		out = append(out, Check{Name: "托盘 drop-in", OK: c.DropInOK,
			Detail: dropInDetail(c)})
		out = append(out, Check{Name: "托盘 pin", OK: c.PinnedHasFc,
			Detail: map[bool]string{true: "omarchy.tray.pinned 含 Fcitx", false: "omarchy.tray.pinned 不含 Fcitx"}[c.PinnedHasFc]})
	}
	if !d.X11HiDPI {
		out = append(out, Check{Name: "X11 HiDPI（可选）", OK: true, Detail: x11OptionalDetail(c)})
	} else if !c.X11ForceZeroScaling {
		out = append(out, Check{Name: "X11 HiDPI", OK: true,
			Detail: "Hyprland force_zero_scaling=false：合成器已在缩放 X11 窗口，无需 Xft.dpi（发布反而会二次放大）"})
	} else if c.X11Available {
		out = append(out, Check{Name: "X11 HiDPI", OK: c.X11ConfigOK && c.X11UnitsOK && !c.X11UnitExecMissing && c.X11DPIActual == c.X11DPIDesired,
			Detail: fmt.Sprintf("Xft.dpi 期望=%d 实际=%d（scale=%.2f）%s%s%s", c.X11DPIDesired, c.X11DPIActual, c.X11Scale, c.X11ForeignNote(), c.X11UnitExecNote(), c.X11ToolkitNote())})
	} else {
		out = append(out, Check{Name: "X11 HiDPI", OK: c.X11ConfigOK && c.X11UnitsOK && !c.X11UnitExecMissing,
			Detail: "未检测到 XWayland；已收敛 Xresources 与缩放监听，待 X11 会话发布" + c.X11ForeignNote() + c.X11UnitExecNote()})
	}

	// 5. candidate-window theming (§6.6)
	out = append(out, themeCheck(c))

	return out
}

// checkGroupIMs asserts the fcitx5 group's item list matches Desired.IMs. The
// shape is not cosmetic: fcitx5 gates every IM hotkey on len(items) > 1, so one
// stale item (the "keyboard-us" old versions created, or the installer's own
// `pinyin`) changes what the IM hotkeys do and which IM they land on.
func checkGroupIMs(d catalog.Desired, c *observe.Current) Check {
	want := d.IMsOrDefault()
	if catalog.SameIMs(c.GroupIMs, want) {
		detail := "[" + strings.Join(want, " ") + "]"
		switch {
		case c.OmarchyManagesIME:
			detail += "；组形状与声明一致（Omarchy 的 Super+I / 左 Shift 在此组内动作）"
		case len(want) == 1:
			detail += "；单 IM：fcitx5 的 IM 热键全部失效（canTrigger 要求 >1），中英靠 rime 的 Shift"
		default:
			detail += "；Ctrl+Space 在此两项间切换，中英在 rime 内由 Shift 完成"
		}
		return Check{Name: "输入法组", OK: true, Detail: detail}
	}
	have := "无"
	if len(c.GroupIMs) > 0 {
		have = strings.Join(c.GroupIMs, " ")
	}
	why := "组内 IM 的数量与顺序决定了 fcitx5 能不能动作：>1 个时触发键/AltTrigger 才生效，并会与 rime 抢左 Shift"
	if c.OmarchyManagesIME {
		why = "组内 IM 的数量与顺序决定了 Omarchy 的热键能不能动作（>1 个才轮到快捷键）"
	}
	hint := ""
	if removed := catalog.RemovedIMs(c.GroupIMs, want); len(removed) > 0 {
		// D2: the engine and its package stay — only the group loses it.
		hint = "；未声明的引擎会被移出组（引擎与包保留，`omarchy setup input " + removed[0] + "` 可加回）"
	}
	return Check{Name: "输入法组", OK: false,
		Detail: fmt.Sprintf("现为 [%s]，终态 [%s]（%s）%s —— 重跑 ompinyin install", have, strings.Join(want, " "), why, hint)}
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

// checkBarIndicator asserts Omarchy 4.5+'s own IM indicator is on the bar. It is
// a separate row from the terminal-state audit's other checks because the fix is
// a shell command, not a file ompinyin owns.
//
// A shell that is NOT running cannot be converged (bar put needs a live shell) and
// its default config already carries the widget, so "absent + no shell" is not a
// failure — reporting one would be a red nothing can clear until login.
func checkBarIndicator(c *observe.Current) Check {
	stale := ""
	if c.StaleTray {
		stale = "；检测到旧产物（notificationitem drop-in / Fcitx pin / theme-set 钩子 / v1.0 override.conf），下次收敛会清理"
	}
	if c.BarIndicator {
		return Check{Name: "顶栏输入法指示器", OK: true,
			Detail: "omarchy.keyboard-layout 在 bar 上（由 Omarchy 自己渲染）" + stale}
	}
	if !c.ShellRunning {
		return Check{Name: "顶栏输入法指示器", OK: true,
			Detail: "外壳未运行，无法确认顶栏指示器（Omarchy 默认配置已含该 widget，可用 `omarchy bar put " +
				bar.WidgetID + "` 确认）" + stale}
	}
	return Check{Name: "顶栏输入法指示器", OK: false,
		Detail: "bar 上没有 " + bar.WidgetID + " —— 运行 `omarchy bar put " + bar.WidgetID +
			"`（或 ompinyin install）" + stale}
}

// checkExecStart asserts the EFFECTIVE unit ExecStart keeps Omarchy's
// `--disable notificationitem`. Reading the unit file would not be enough: any
// drop-in can rewrite ExecStart — the v1.0 generic override.conf did exactly that
// with no managed header — and then fcitx5 publishes its own SNI item, so the bar
// shows two input-method icons. 4.5+ only: the pre-4.5 path removes that flag on
// purpose.
func checkExecStart(c *observe.Current) Check {
	if c.ExecStartOK {
		return Check{Name: "fcitx5 启动参数", OK: true,
			Detail: "生效的 ExecStart 保留 Omarchy 的 --disable notificationitem（fcitx5 不自建托盘项，顶栏只由 Omarchy 渲染）"}
	}
	dir := "$HOME/.config/systemd/user/"
	if c.Unit != "" {
		dir += c.Unit + ".d/"
	}
	detail := "某个 drop-in 覆盖了 Omarchy 的 ExecStart（缺 --disable notificationitem）：fcitx5 会自己发托盘图标，" +
		"与顶栏指示器重复（两个图标）——检查 " + dir + "（v1.0 的 override.conf 就是这种内容，ompinyin install 会删掉" +
		"自己写过的；自定义的需你手动处理）"
	if c.EffectiveExecStart != "" {
		detail += "；当前生效值：" + truncateDetail(c.EffectiveExecStart, 160)
	}
	return Check{Name: "fcitx5 启动参数", OK: false, Detail: detail}
}

// truncateDetail shortens a raw `systemctl show` line for the doctor output.
func truncateDetail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// themeCheck reports whether the candidate window follows the Omarchy theme.
// classicui.conf is the only file ompinyin still owns: the colors come from
// Omarchy's own generator (fcitx5-theme.conf.tpl → omarchy-theme-set-fcitx5), so
// the old theme-set hook counts as residue, not as part of the terminal state.
func themeCheck(c *observe.Current) Check {
	ok := c.ThemeConfOK && !c.ThemeHookOK
	var detail string
	switch {
	case !c.ThemeConfOK:
		detail = "classicui.conf 未指向 " + theme.ThemeName + " 主题（或缺少 managed 头）——候选框不会跟随 Omarchy 配色"
	case c.ThemeHookOK:
		detail = "检测到旧 theme-set 钩子残留——重跑 ompinyin install 清理（配色已归 omarchy-theme-set-fcitx5，两者会并发写同一目录）"
	case c.OmarchyManagesIME:
		detail = "classicui.conf→omarchy；配色由 Omarchy 的 omarchy-theme-set-fcitx5 生成（换主题自动刷新）"
	default:
		detail = "classicui.conf→omarchy；钩子已退役（配色依赖 Omarchy 的 fcitx5 主题生成器）"
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

// triggerDetail states what the trigger key situation is on this host.
func triggerDetail(d catalog.Desired, c *observe.Current) string {
	if c.OmarchyManagesIME {
		return "触发键归 Omarchy：`Super+I` 循环输入法、左 Shift 进出输入法；Ctrl+Space 留给终端"
	}
	keys := strings.Join(hotkey.DefaultKeys, " / ")
	ims := d.IMsOrDefault()
	if len(ims) > 1 {
		return keys + "（在 " + strings.Join(ims, " / ") + " 间切换；中英切换在 rime 内由 Shift 完成）"
	}
	return keys + "（单 IM：fcitx5 的 canTrigger() 要求组内 >1 个 IM，此键不生效；中英切换在 rime 内由 Shift 完成）"
}

// checkAltTrigger asserts [Hotkey/AltTriggerKeys] is an EMPTY section. fcitx5
// ships Shift_L there; with >1 IM it fires the same toggle() as the trigger key
// and steals left Shift from rime's commit_code. "Empty" must be encoded as a
// present-but-childless section: an absent section means the shipped default.
func checkAltTrigger(c *observe.Current) Check {
	if c.AltTriggerOK {
		return Check{Name: "AltTriggerKeys", OK: true,
			Detail: "[Hotkey/AltTriggerKeys] 空段 —— 左 Shift 归 rime（fcitx5 不再把它当「临时切换输入法」）"}
	}
	bound := "缺段（fcitx5 默认 Shift_L）"
	if len(c.AltTriggerKeys) > 0 {
		bound = strings.Join(c.AltTriggerKeys, " / ")
	}
	return Check{Name: "AltTriggerKeys", OK: false,
		Detail: "现为 " + bound + "：组内 >1 个 IM 时它会把左 Shift 变成「切输入法」，与 Ctrl+Space 同效 —— 重跑 ompinyin install（收敛会清空该段）"}
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

	// trigger keys: 4.5+ they belong to Omarchy, and the only drift ompinyin still
	// owns is a leftover Control+space from an older run.
	if c.OmarchyManagesIME {
		out = append(out, Check{Name: "触发键", OK: !c.ControlSpace, Detail: triggerDetail(d, c)})
	} else if c.HotkeyOK {
		out = append(out, Check{Name: "触发键", OK: true,
			Detail: triggerDetail(d, c) + c.HerdrNote()})
	} else {
		out = append(out, Check{Name: "触发键", OK: false,
			Detail: "[Hotkey/TriggerKeys] 未达目标值（期望 " + strings.Join(hotkey.DefaultKeys, " / ") + "）" + c.HerdrNote()})
	}
	// AltTriggerKeys: only the pre-4.5 path configures it. Omarchy 4.5+ writes
	// Shift_L there itself, so asserting it would be a check that passes by luck.
	if !c.OmarchyManagesIME {
		out = append(out, checkAltTrigger(c))
	}

	// managed feel: the full_shape reset is written by INDEX
	// (switches/@4/reset), so an upstream switch reorder would silently reset a
	// different switch. Read the compiled schema and assert the index still
	// points at full_shape instead of trusting the constant.
	out = append(out, checkFullShapeIndex(d, c))

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

// checkFullShapeIndex verifies that switches[catalog.FullShapeSwitchIndex] is
// still full_shape in every enabled schema's COMPILED config. rime patches pick
// list items by index, so this is the one managed 手感项 that can rot silently
// when upstream inserts or reorders a switch.
func checkFullShapeIndex(d catalog.Desired, c *observe.Current) Check {
	var checked, bad []string
	for _, schema := range d.SchemaList() {
		path := filepath.Join(c.RimeDir, "build", schema+".schema.yaml")
		b, err := os.ReadFile(path)
		if err != nil {
			continue // not compiled yet: L5's build check owns that failure
		}
		names := switchNames(b)
		checked = append(checked, schema)
		got := "缺失"
		if len(names) > catalog.FullShapeSwitchIndex {
			got = names[catalog.FullShapeSwitchIndex]
		}
		if got != "full_shape" {
			bad = append(bad, fmt.Sprintf("%s: switches[%d]=%s", schema, catalog.FullShapeSwitchIndex, got))
		}
	}
	if len(checked) == 0 {
		return Check{Name: "手感项（全半角）", OK: true, Detail: "未找到已编译的方案配置；收敛后会复核"}
	}
	if len(bad) > 0 {
		return Check{Name: "手感项（全半角）", OK: false,
			Detail: "全半角补丁按下标选择开关，但 " + strings.Join(bad, "；") + " ——上游重排了 switches，需更新 catalog.FullShapeSwitchIndex"}
	}
	return Check{Name: "手感项（全半角）", OK: true,
		Detail: fmt.Sprintf("switches[%d]=full_shape（%s）；reset=0 且不跨会话记忆", catalog.FullShapeSwitchIndex, strings.Join(checked, ", "))}
}

// switchNames returns the `- name:` entries of the top-level switches list, in
// order. A line-based scan is enough: the compiled list is machine-rendered.
func switchNames(b []byte) []string {
	var names []string
	inSwitches := false
	for _, line := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimSpace(line)
		if !inSwitches {
			if trimmed == "switches:" {
				inSwitches = true
			}
			continue
		}
		if line != "" && line[0] != ' ' && line[0] != '\t' {
			break // next top-level key
		}
		if rest, ok := strings.CutPrefix(trimmed, "- name:"); ok {
			names = append(names, strings.TrimSpace(rest))
		}
	}
	return names
}
