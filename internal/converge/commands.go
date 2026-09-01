package converge

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ProjectAILeap/ompinyin/internal/assets"
	"github.com/ProjectAILeap/ompinyin/internal/catalog"
	"github.com/ProjectAILeap/ompinyin/internal/observe"
	"github.com/ProjectAILeap/ompinyin/internal/patches"
	"github.com/ProjectAILeap/ompinyin/internal/plan"
	"github.com/ProjectAILeap/ompinyin/internal/profile"
	"github.com/ProjectAILeap/ompinyin/internal/selfup"
	"github.com/ProjectAILeap/ompinyin/internal/service"
	"github.com/ProjectAILeap/ompinyin/internal/state"
	"github.com/ProjectAILeap/ompinyin/internal/tray"
	"github.com/ProjectAILeap/ompinyin/internal/verify"
)

// SwitchArgs carries the parsed switch flags (§7).
type SwitchArgs struct {
	DSP        string // layout id, or "none"
	DSPDefault bool
	NoQuanpin  bool
	Full       bool
	OSOverride string
	Yes        bool
	DryRun     bool
	JSON       bool
	Stdin      interface{ Read([]byte) (int, error) }
}

// Switch mutates the persisted desired state (add/remove double pinyin,
// re-pin primary) and re-converges (§7). Returns the exit code.
func Switch(args SwitchArgs, opts Options) int {
	st, err := state.Load()
	if err != nil {
		opts.errf("[失败] 读取状态清单：%v", err)
		return ExitExecFail
	}
	d := st.Desired
	if d.IsZero() {
		d = catalog.DefaultDesired()
	}

	switch {
	case args.Full:
		// 全拼改回 schema_list[0]；已装双拼留在 Extra
		d.Primary = "quanpin"
	case args.DSP == "none":
		// 去掉双拼，回到仅全拼
		d.Extra = nil
	case args.DSP != "":
		l, ok := catalog.Lookup(args.DSP)
		if !ok || !l.DoublePinyin {
			opts.errf("[失败] --dsp 取值须为双拼 ID：%s（或 none）", strings.Join(catalog.DoublePinyinIDs(), "|"))
			return ExitUsage
		}
		// Mirror install (cmd/ompinyin main.go): keep the Primary/Extra pairing
		// consistent with the same flags on install, so `switch --dsp X --dsp-default`
		// keeps quanpin in Extra (rather than dropping it, which used to happen
		// because d.Extra was wholesale replaced with [X]).
		switch {
		case args.NoQuanpin:
			// --dsp X --no-quanpin: only the dsp layout, quanpin dropped.
			d.Primary = args.DSP
			d.Extra = nil
		case args.DSPDefault:
			// --dsp X --dsp-default: dsp becomes schema_list[0], quanpin stays in Extra.
			d.Primary = args.DSP
			d.Extra = []string{"quanpin"}
		default:
			// --dsp X: quanpin stays primary, dsp added as an F4-switchable Extra.
			d.Primary = "quanpin"
			d.Extra = []string{args.DSP}
		}
	case args.DSPDefault || args.NoQuanpin:
		opts.errf("[失败] --dsp-default / --no-quanpin 必须伴随 --dsp")
		return ExitUsage
	default:
		opts.errf("[失败] switch 需要 --dsp ID [--dsp-default] / --dsp none / --full")
		return ExitUsage
	}

	if err := d.Validate(); err != nil {
		opts.errf("[失败] 终态非法：%v", err)
		return ExitUsage
	}
	opts.OSOverride = args.OSOverride
	opts.Yes = args.Yes
	opts.DryRun = args.DryRun
	opts.JSON = args.JSON
	return Install(d, false, opts)
}

// Update refreshes L2 assets to latest and re-converges (§7).
func Update(opts Options) int {
	st, err := state.Load()
	if err != nil {
		opts.errf("[失败] 读取状态清单：%v", err)
		return ExitExecFail
	}
	d := st.Desired
	if d.IsZero() {
		d = catalog.DefaultDesired()
	}
	code := Install(d, true, opts)
	if code != ExitOK || !opts.Self || opts.JSON {
		return code
	}
	// Self-upgrade the ompinyin binary (data refresh already completed above).
	if opts.DryRun {
		r, err := selfup.Check(catalog.Version)
		if err != nil {
			opts.errf("[失败] 检查新版本：%v", err)
			return ExitExecFail
		}
		if r.Newer {
			opts.outf("(dry-run) 自升级：%s → %s（未下载/未替换）", r.Current, r.Latest)
		} else {
			opts.outf("(dry-run) 自升级：已是 %s", r.Latest)
		}
		return code
	}
	r, err := selfup.Apply(catalog.Version)
	if err != nil {
		opts.errf("[失败] 自升级：%v", err)
		return ExitExecFail
	}
	opts.outf("[完成] 自升级：%s", r.Message)
	return code
}

// Status prints current vs desired diff plus asset versions (read-only).
func Status(opts Options) int {
	st, err := state.Load()
	if err != nil {
		opts.errf("[失败] 读取状态清单：%v", err)
		return ExitExecFail
	}
	d := st.Desired
	if d.IsZero() {
		d = catalog.DefaultDesired()
		if !opts.JSON {
			opts.outf("（尚无状态清单，按默认终态评估）")
		}
	}
	cur := observe.Collect(d, st)
	p := plan.Diff(d, cur, false)
	if opts.JSON {
		return writeJSON(opts.Stdout, StatusReport{
			Tool:     "ompinyin",
			Version:  catalog.Version,
			Command:  "status",
			Desired:  d,
			Assets:   st.Assets,
			Warnings: statusWarnings(d, st),
			Plan:     planReportOf(p),
			Host:     hostReportOf(cur),
		})
	}
	opts.outf("── ompinyin status：现状 vs 终态 ──")
	opts.outf("desired: primary=%s extra=%v model=%v channel=%s schema_list=%v",
		d.Primary, d.Extra, d.Model, d.Channel, d.SchemaList())
	for _, name := range []string{"rime_ice", "wanxiang"} {
		if rec, ok := st.Assets[name]; ok {
			opts.outf("asset %s: tag=%s sha256=%s…", name, displayTag(rec.Tag), trunc(rec.SHA256, 12))
		} else {
			opts.outf("asset %s: 未记录", name)
		}
	}
	for _, w := range statusWarnings(d, st) {
		opts.outf("提示: %s", w)
	}
	opts.outf("%s", strings.TrimRight(p.Describe(), "\n"))
	return ExitOK
}

// statusWarnings keeps the honest-channel note in one place for both output
// modes: on this repo releases/latest IS the rolling nightly, so when tag
// resolution was impossible the "stable" ledger may hold nightly bytes.
func statusWarnings(d catalog.Desired, st *state.State) []string {
	var out []string
	if d.Channel != "nightly" {
		if rec := st.Assets["rime_ice"]; rec.Tag == "" || rec.Tag == "nightly" {
			out = append(out, fmt.Sprintf(
				"channel=stable 但未 pin 到具体 stable tag（实际构建=%s）；API 可达时跑 ompinyin update 重新 pin",
				displayTag(rec.Tag)))
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// --json payloads (status / doctor are the machine-readable face of the tool)
// ---------------------------------------------------------------------------

// StatusReport is the `ompinyin status --json` document.
type StatusReport struct {
	Tool     string                       `json:"tool"`
	Version  string                       `json:"version"`
	Command  string                       `json:"command"`
	Desired  catalog.Desired              `json:"desired"`
	Assets   map[string]state.AssetRecord `json:"assets"`
	Warnings []string                     `json:"warnings,omitempty"`
	Plan     PlanReport                   `json:"plan"`
	Host     HostReport                   `json:"host"`
}

// DoctorReport is the `ompinyin doctor --json` document.
type DoctorReport struct {
	Tool    string          `json:"tool"`
	Version string          `json:"version"`
	Command string          `json:"command"`
	Desired catalog.Desired `json:"desired"`
	Checks  []CheckReport   `json:"checks"`
	OK      bool            `json:"ok"`
	Failed  int             `json:"failed"`
}

// CheckReport is one verification result in machine-readable form.
type CheckReport struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// PlanReport carries both the human steps and the typed predicates Install
// consumes, so a script can see exactly what a real run would do.
type PlanReport struct {
	Need       map[string]bool `json:"need"`
	Steps      []StepReport    `json:"steps"`
	NeedsApply bool            `json:"needsApply"`
}

// StepReport is one plan step.
type StepReport struct {
	Layer  string `json:"layer"`
	Title  string `json:"title"`
	Needed bool   `json:"needed"`
}

// HostReport is the observed snapshot, narrowed to what a script may care
// about (the full observe.Current is an internal shape).
type HostReport struct {
	RimeDir         string   `json:"rimeDir"`
	Unit            string   `json:"unit"`
	ServiceActive   bool     `json:"serviceActive"`
	PackagesMissing []string `json:"packagesMissing,omitempty"`
	RimeDataExists  bool     `json:"rimeDataExists"`
	GramFileExists  bool     `json:"gramFileExists"`
	ProfileHasRime  bool     `json:"profileHasRime"`
	HotkeyOK        bool     `json:"hotkeyOK"`
	DropInOK        bool     `json:"dropInOK"`
	PinnedHasFcitx  bool     `json:"pinnedHasFcitx"`
	ShellRunning    bool     `json:"shellRunning"`
	BuildMissing    []string `json:"buildMissing,omitempty"`
	OrphanManaged   []string `json:"orphanManaged,omitempty"`
	LegacyDirExists bool     `json:"legacyDirExists"`
}

func planReportOf(p *plan.Plan) PlanReport {
	return PlanReport{
		Need: map[string]bool{
			"l1": p.NeedL1, "l2": p.NeedL2, "l3": p.NeedL3,
			"deploy": p.NeedDeploy, "host": p.NeedHost, "tray": p.NeedTray,
		},
		Steps:      stepsOf(p),
		NeedsApply: p.NeedsApply(),
	}
}

func stepsOf(p *plan.Plan) []StepReport {
	out := make([]StepReport, 0, len(p.Steps))
	for _, s := range p.Steps {
		out = append(out, StepReport{Layer: s.Layer, Title: s.Title, Needed: s.Needed})
	}
	return out
}

func hostReportOf(c *observe.Current) HostReport {
	return HostReport{
		RimeDir: c.RimeDir, Unit: c.Unit, ServiceActive: c.ServiceActive,
		PackagesMissing: c.PackagesMissing, RimeDataExists: c.RimeDataExists,
		GramFileExists: c.GramFileExists, ProfileHasRime: c.ProfileHasRime,
		HotkeyOK: c.HotkeyOK, DropInOK: c.DropInOK, PinnedHasFcitx: c.PinnedHasFc,
		ShellRunning: c.ShellRunning, BuildMissing: c.BuildMissing,
		OrphanManaged: c.Orphans, LegacyDirExists: c.LegacyDirExists,
	}
}

func writeJSON(w io.Writer, v any) int {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[失败] JSON 序列化：%v\n", err)
		return ExitExecFail
	}
	fmt.Fprintln(w, string(b))
	return ExitOK
}

// displayTag renders an unrecorded tag readably.
func displayTag(tag string) string {
	if tag == "" {
		return "未记录"
	}
	return tag
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Doctor runs the health checklist (read-only).
func Doctor(opts Options) int {
	st, err := state.Load()
	if err != nil {
		opts.errf("[失败] 读取状态清单：%v", err)
		return ExitExecFail
	}
	d := st.Desired
	if d.IsZero() {
		d = catalog.DefaultDesired()
	}
	cur := observe.Collect(d, st)
	checks := verify.Doctor(d, cur)
	bad := 0
	for _, c := range checks {
		if !c.OK {
			bad++
		}
	}
	if opts.JSON {
		rep := DoctorReport{Tool: "ompinyin", Version: catalog.Version, Command: "doctor",
			Desired: d, OK: bad == 0, Failed: bad}
		for _, c := range checks {
			rep.Checks = append(rep.Checks, CheckReport{Name: c.Name, OK: c.OK, Detail: c.Detail})
		}
		code := writeJSON(opts.Stdout, rep)
		if bad > 0 && code == ExitOK {
			return ExitExecFail // JSON is valid but the host is not converged
		}
		return code
	}
	for _, c := range checks {
		mark := "[完成]"
		if !c.OK {
			mark = "[失败]"
		}
		opts.outf("%s %s：%s", mark, c.Name, c.Detail)
	}
	if bad > 0 {
		opts.errf("doctor：%d 项未达标", bad)
		return ExitExecFail
	}
	opts.outf("doctor：全部达标")
	return ExitOK
}

// UninstallArgs carries uninstall flags.
type UninstallArgs struct {
	Legacy bool // not used; legacy belongs to clean
	Yes    bool
}

// Uninstall removes the managed footprint (§7): managed files deleted (with
// ownership protocol), profile de-registered, tray restored, dedicated
// drop-in removed. System packages and the data dir stay.
func Uninstall(opts Options) int {
	lock, err := state.Acquire()
	if err != nil {
		opts.errf("[失败] %v", err)
		return ExitExecFail
	}
	defer lock.Release()

	st, err := state.Load()
	if err != nil {
		opts.errf("[失败] 读取状态清单：%v", err)
		return ExitExecFail
	}

	rimeDir := observe.DataDir()

	// managed files: only delete tool-written ones without asking; hand-edited
	// ones follow the ownership protocol (§5.1)
	for rel, ledger := range st.ManagedFiles {
		abs := filepath.Join(rimeDir, rel)
		if patches.Classify(abs, ledger) == patches.StatusManaged {
			if err := patches.RemoveFile(abs, st); err != nil {
				opts.errf("[失败] 删除受管文件 %s：%v", rel, err)
				return ExitExecFail
			}
			opts.outf("[完成] 删除受管文件 %s", rel)
			continue
		}
		if _, err := os.Stat(abs); err != nil {
			continue
		}
		if opts.confirm(rel + " 被手改过，确认删除？") {
			if err := patches.RemoveFile(abs, st); err != nil {
				opts.errf("[失败] 删除 %s：%v", rel, err)
				return ExitExecFail
			}
			opts.outf("[完成] 删除手改受管文件 %s", rel)
		} else {
			opts.outf("[跳过] 保留 %s", rel)
		}
	}

	// the single stop window for profile removal
	unit := service.FindUnit(state.Home())
	stopped := false
	if unit != "" && service.IsActive(unit) {
		if err := service.Stop(unit); err != nil {
			opts.errf("[失败] %v", err)
			return ExitExecFail
		}
		stopped = true
	}
	if b, err := os.ReadFile(observe.ProfilePath()); err == nil {
		if newContent, changed := profile.RemoveRime(string(b)); changed {
			if werr := state.WriteAtomic(observe.ProfilePath(), []byte(newContent)); werr != nil {
				opts.errf("[失败] 写 profile：%v", werr)
				return ExitExecFail
			}
			opts.outf("[完成] profile 已移除 rime")
		}
	}
	if err := tray.RemoveDropIn(state.Home(), unit); err != nil {
		opts.errf("[警告] 删除专用 drop-in 失败：%v", err)
	} else {
		opts.outf("[完成] 专用 drop-in 已删除")
	}
	if stopped || unit != "" {
		if err := service.DaemonReload(); err != nil {
			opts.errf("[警告] daemon-reload：%v", err)
		}
		if err := service.Start(unit); err != nil {
			opts.errf("[失败] 重启 %s：%v（请手动 `systemctl --user start %s`）", unit, err, unit)
			return ExitExecFail
		}
	}

	// tray restore: read pinned → remove Fcitx → write back (even empty array)
	b, err := os.ReadFile(tray.ShellJSONPath(state.Home()))
	if err == nil || os.IsNotExist(err) {
		if pinned, perr := tray.ReadPinned(b); perr == nil {
			if tray.HasPin(pinned) {
				if werr := tray.SetPinned(tray.ShellJSONPath(state.Home()), tray.RemovePin(pinned)); werr != nil {
					opts.errf("[失败] 托盘还原：%v", werr)
					return ExitExecFail
				}
				opts.outf("[完成] 托盘已移除 %s", tray.FcitxId)
			}
		}
	}
	if !tray.ShellRunning() {
		opts.outf("[希望] 外壳未运行，托盘还原已写入磁盘；外壳启动时将自动应用")
	}

	if err := state.Remove(); err == nil {
		opts.outf("[完成] 状态清单已清除")
	}
	opts.outf("[完成] uninstall 完成；系统包未动，数据目录 %s 保留（手动删除）", rimeDir)
	return ExitOK
}

// CleanArgs carries clean flags.
type CleanArgs struct {
	Legacy bool
	Yes    bool
}

// Clean removes the download cache and, with --legacy, the historical
// ~/.config/fcitx/rime duplicate (§6.5, ~450MB).
func Clean(args CleanArgs, opts Options) int {
	cache := assets.CacheDir()
	if _, err := os.Stat(cache); err == nil {
		if opts.confirm("清空下载缓存 " + cache + "？") {
			if err := os.RemoveAll(cache); err != nil {
				opts.errf("[失败] 清缓存：%v", err)
				return ExitExecFail
			}
			opts.outf("[完成] 缓存已清空")
		}
	} else {
		opts.outf("[跳过] 无缓存目录")
	}
	if args.Legacy {
		legacy := filepath.Join(state.Home(), ".config", "fcitx", "rime")
		if _, err := os.Stat(legacy); err == nil {
			if opts.confirm("删除历史遗留目录 " + legacy + "（老路径重复数据，~450MB）？") {
				if err := os.RemoveAll(legacy); err != nil {
					opts.errf("[失败] 清遗留：%v", err)
					return ExitExecFail
				}
				opts.outf("[完成] 遗留目录已删除")
			}
		} else {
			opts.outf("[跳过] 无遗留目录")
		}
	}
	return ExitOK
}
