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
	"github.com/ProjectAILeap/ompinyin/internal/hidpi"
	"github.com/ProjectAILeap/ompinyin/internal/observe"
	"github.com/ProjectAILeap/ompinyin/internal/patches"
	"github.com/ProjectAILeap/ompinyin/internal/plan"
	"github.com/ProjectAILeap/ompinyin/internal/profile"
	"github.com/ProjectAILeap/ompinyin/internal/selfup"
	"github.com/ProjectAILeap/ompinyin/internal/service"
	"github.com/ProjectAILeap/ompinyin/internal/state"
	"github.com/ProjectAILeap/ompinyin/internal/theme"
	"github.com/ProjectAILeap/ompinyin/internal/tray"
	"github.com/ProjectAILeap/ompinyin/internal/verify"
)

// SwitchArgs carries the switch-only flags (§7). Run-wide knobs (Yes, DryRun,
// JSON, OSOverride, mirror…) live in Options.
type SwitchArgs struct {
	DSP        string // layout id, or "none"
	DSPDefault bool
	NoQuanpin  bool
	Full       bool
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
		// Same pairing rules as `install --dsp`: pairPrimary is the single source
		// (keeps `switch --dsp X --dsp-default` putting quanpin in Extra).
		d.Primary, d.Extra = catalog.PairPrimary(args.DSP, args.DSPDefault, args.NoQuanpin)
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
	RimeDir           string   `json:"rimeDir"`
	Unit              string   `json:"unit"`
	ServiceActive     bool     `json:"serviceActive"`
	PackagesMissing   []string `json:"packagesMissing,omitempty"`
	RimeDataExists    bool     `json:"rimeDataExists"`
	GramFileExists    bool     `json:"gramFileExists"`
	ProfileHasRime    bool     `json:"profileHasRime"`
	HotkeyOK          bool     `json:"hotkeyOK"`
	DropInOK          bool     `json:"dropInOK"`
	PinnedHasFcitx    bool     `json:"pinnedHasFcitx"`
	ShellRunning      bool     `json:"shellRunning"`
	ThemeOK           bool     `json:"themeOK"`
	BuildMissing      []string `json:"buildMissing,omitempty"`
	OrphanManaged     []string `json:"orphanManaged,omitempty"`
	LegacyDirExists   bool     `json:"legacyDirExists"`
	X11Scale          float64  `json:"x11Scale"`
	X11DPIDesired     int      `json:"x11DpiDesired"`
	X11DPIActual      int      `json:"x11DpiActual"`
	X11Available      bool     `json:"x11Available"`
	X11ConfigOK       bool     `json:"x11ConfigOK"`
	X11UnitsOK        bool     `json:"x11UnitsOK"`
	X11UnitsPresent   bool     `json:"x11UnitsPresent"`
	X11ManagedPresent bool     `json:"x11ManagedPresent"`
	X11ForeignDPI     bool     `json:"x11ForeignDpi"`
	X11PackageMissing bool     `json:"x11PackageMissing"`
	X11ForceZeroScale bool     `json:"x11ForceZeroScaling"`
	X11ScalingKnown   bool     `json:"x11ScalingKnown"`
}

func planReportOf(p *plan.Plan) PlanReport {
	return PlanReport{
		Need: map[string]bool{
			"l1": p.NeedL1, "l2": p.NeedL2, "l3": p.NeedL3,
			"deploy": p.NeedDeploy, "host": p.NeedHost, "service": p.NeedService,
			"tray":  p.NeedTray,
			"theme": p.NeedTheme,
			"hidpi": p.NeedHidpi || p.NeedHidpiUndo,
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
		ShellRunning:  c.ShellRunning,
		ThemeOK:       c.ThemeConfOK && c.ThemeHookOK && c.ThemeDirOK,
		BuildMissing:  c.BuildMissing,
		OrphanManaged: c.Orphans, LegacyDirExists: c.LegacyDirExists,
		X11Scale: c.X11Scale, X11DPIDesired: c.X11DPIDesired, X11DPIActual: c.X11DPIActual,
		X11Available: c.X11Available, X11ConfigOK: c.X11ConfigOK, X11UnitsOK: c.X11UnitsOK,
		X11UnitsPresent: c.X11UnitsPresent, X11ManagedPresent: c.X11ManagedPresent, X11PackageMissing: c.X11PackageMissing,
		X11ForeignDPI:     c.X11ForeignDPI,
		X11ForceZeroScale: c.X11ForceZeroScaling, X11ScalingKnown: c.X11ScalingKnown,
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

// trunc shortens s for display without panicking on an empty/short value (a
// failed HashFile returns "").
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

// RemoveThemeFile removes one theming file following the §5.1 ownership
// protocol: tool-written files go silently, hand-edited ones ask first.
func removeThemeFile(opts Options, st *state.State, rel, label string) error {
	abs := filepath.Join(state.Home(), filepath.FromSlash(rel))
	status := patches.Classify(abs, st.ManagedFiles[rel])
	if _, err := os.Stat(abs); os.IsNotExist(err) {
		delete(st.ManagedFiles, rel)
		return nil
	}
	if status == patches.StatusUserModified || status == patches.StatusForeign {
		if !opts.confirm(label + " 被手改过，确认删除？") {
			opts.outf("[跳过] 保留 " + label)
			return nil
		}
	}
	if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
		return err
	}
	delete(st.ManagedFiles, rel)
	opts.outf("[完成] 删除 " + label)
	return nil
}

// backupForUninstall snapshots everything Uninstall is about to delete, so the
// §5.1 "先备份后写" promise also holds on the destructive path. Without it a
// plain uninstall silently discarded the managed files AND state.json — the
// only record of the user's chosen Desired (e.g. an opted-in --dsp) — which is
// precisely the loss a reinstall cannot recover. Returns "" when nothing the
// tool owns exists (nothing to lose). A backup failure is fatal: never delete
// without a snapshot.
func backupForUninstall(st *state.State) (string, error) {
	home := state.Home()
	rimeDir := observe.DataDir()
	var targets []string
	for rel := range st.ManagedFiles {
		if filepath.Base(rel) != rel {
			continue // non-rime-dir ledger keys are handled explicitly below
		}
		targets = append(targets, filepath.Join(rimeDir, rel))
	}
	targets = append(targets,
		theme.ConfPath(home), theme.HookPath(home),
		tray.DropInPath(home, service.FindUnit(home)),
		filepath.Join(home, filepath.FromSlash(tray.DropInRelPathLegacy)),
		tray.ShellJSONPath(home),
		hidpi.XresourcesPath(home),
		state.Path(),
	)
	for _, u := range hidpiUnits(home) {
		targets = append(targets, u.path)
	}

	var existing []string
	for _, p := range targets {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			existing = append(existing, p)
		}
	}
	if len(existing) == 0 {
		return "", nil
	}
	dir, err := uniqueBackupDir()
	if err != nil {
		return "", err
	}
	for _, src := range existing {
		if err := copyInto(dir, src); err != nil {
			return "", err
		}
	}
	return dir, nil
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

	// Snapshot before deleting: state.json (the Desired the user chose) and every
	// owned artifact. §5.1 is a hard promise, and uninstall was the one path
	// that deleted first and asked nothing.
	backupDir, err := backupForUninstall(st)
	if err != nil {
		opts.errf("[失败] 卸载前备份失败，拒绝删除：%v", err)
		return ExitExecFail
	}
	if backupDir != "" {
		opts.outf("[完成] 备份：%s", backupDir)
	}

	rimeDir := observe.DataDir()

	// candidate-window theming (§6.6): classicui.conf + theme-set hook + the
	// hook-generated theme dir (not ledger-tracked — delete it explicitly)
	if err := removeThemeFile(opts, st, theme.ConfRelPath, "classicui.conf"); err != nil {
		opts.errf("[失败] 删除 classicui.conf：%v", err)
		return ExitExecFail
	}
	if err := removeThemeFile(opts, st, theme.HookRelPath, "theme-set 钩子 (fcitx5-theme)"); err != nil {
		opts.errf("[失败] 删除 theme-set 钩子：%v", err)
		return ExitExecFail
	}
	if err := os.RemoveAll(theme.ThemeDir(state.Home())); err != nil {
		opts.errf("[警告] 删除候选框主题目录失败：%v", err)
	}

	// managed files: only delete tool-written ones without asking; hand-edited
	// ones follow the ownership protocol (§5.1). Keys with a path separator are
	// NOT rime-dir files (candidate-window theming etc., handled above), and
	// must never be resolved under rimeDir.
	for rel, ledger := range st.ManagedFiles {
		if filepath.Base(rel) != rel {
			continue
		}
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
			opts.errf("[失败] 重启 %s：%v（请手动 `systemctl --user reset-failed %s && systemctl --user start %s`）", unit, err, unit, unit)
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

	// hot-reload so a running fcitx5 drops the themed candidate window and
	// falls back to the default (uninstall restarted it above when active)
	_ = theme.Reload()

	// X11 HiDPI owns only its marked Xresources block and its two dedicated
	// user units. Never remove a user's other Xresources settings. Same helper
	// as --no-x11-hidpi: watcher disabled BEFORE the file it watches is edited.
	// Uninstall warns instead of aborting — the rest of the teardown still has
	// to run.
	if removed, err := removeHidpiArtifacts("", opts.errf); err != nil {
		opts.errf("[警告] %v", err)
	} else if removed {
		opts.outf("[完成] X11 HiDPI 受管 Xft.dpi 已移除（其余 Xresources 保留）")
	}

	if err := state.Remove(); err == nil {
		opts.outf("[完成] 状态清单已清除")
	}
	// rotate backups after a successful uninstall too (never delete the snapshot
	// this very run just wrote)
	if _, err := state.PruneBackups(state.LedgerKeepBackups, state.LedgerBackupBudget); err != nil {
		opts.errf("[警告] 清理旧备份失败：%v", err)
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
	// Clean deletes the download cache that a concurrent install may be
	// streaming the 420MB model into; take the same instance lock so the two
	// never overlap.
	lock, err := state.Acquire()
	if err != nil {
		opts.errf("[失败] %v", err)
		return ExitExecFail
	}
	defer lock.Release()

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
