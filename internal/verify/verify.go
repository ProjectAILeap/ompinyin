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

	return out
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
