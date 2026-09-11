// Package converge orchestrates the five-layer terminal-state convergence
// run (§3) with the single stop window of §6.0:
//
//	lock → facts → desired → current → plan →(dry-run)→ backup →
//	L1 pkgs → L2 assets → L3 patches →
//	stop fcitx5 → deploy.Build (full --build compiles every enabled schema,
//	given the map-form schema_list) → L4 profile/hotkey/drop-in →
//	daemon-reload → start fcitx5 → confirm build artifacts → L4 tray set →
//	L5 verify → state → unlock
package converge

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ProjectAILeap/ompinyin/internal/assets"
	"github.com/ProjectAILeap/ompinyin/internal/catalog"
	"github.com/ProjectAILeap/ompinyin/internal/deploy"
	"github.com/ProjectAILeap/ompinyin/internal/facts"
	"github.com/ProjectAILeap/ompinyin/internal/hidpi"
	"github.com/ProjectAILeap/ompinyin/internal/hotkey"
	"github.com/ProjectAILeap/ompinyin/internal/observe"
	"github.com/ProjectAILeap/ompinyin/internal/patches"
	"github.com/ProjectAILeap/ompinyin/internal/pkgs"
	"github.com/ProjectAILeap/ompinyin/internal/plan"
	"github.com/ProjectAILeap/ompinyin/internal/profile"
	"github.com/ProjectAILeap/ompinyin/internal/service"
	"github.com/ProjectAILeap/ompinyin/internal/state"
	"github.com/ProjectAILeap/ompinyin/internal/theme"
	"github.com/ProjectAILeap/ompinyin/internal/tray"
	"github.com/ProjectAILeap/ompinyin/internal/verify"
)

// Options carries the run-wide knobs shared by all commands.
type Options struct {
	Stdout       io.Writer
	Stderr       io.Writer
	Stdin        io.Reader
	Yes          bool
	Mirror       string
	LocalDir     string // --mirror <dir>: offline asset directory (no HTTP)
	MirrorSource catalog.MirrorSource
	FullBackup   bool
	DryRun       bool
	OSOverride   string
	JSON         bool   // machine-readable status/doctor output
	Command      string // command name for --json reports (install/update/switch)
	Self         bool   // also upgrade the ompinyin binary (update --self)

	// Context carries SIGINT/SIGTERM cancellation so a long 420MB download
	// aborts cleanly and the stop window's defer still restarts fcitx5. nil is
	// treated as context.Background().
	Context context.Context

	// stdin is read through ONE buffered reader; a fresh bufio.Reader per
	// prompt swallows the rest of a piped answer stream (评审 P1-14).
	reader *bufio.Reader
}

func (o *Options) outf(format string, args ...any) {
	w := o.Stdout
	if o.JSON {
		// machine-readable mode: keep human diagnostics off stdout, which is
		// reserved for the JSON document (status/doctor/install --dry-run --json).
		w = o.Stderr
	}
	fmt.Fprintf(w, format+"\n", args...)
}

func (o *Options) errf(format string, args ...any) {
	fmt.Fprintf(o.Stderr, format+"\n", args...)
}

// Exit codes (§7).
const (
	ExitOK       = 0
	ExitExecFail = 1
	ExitUsage    = 2
	ExitPrecheck = 3
)

func (o *Options) confirm(prompt string) bool {
	if o.Yes {
		return true
	}
	if o.Stdin == nil {
		o.errf("[失败] %s 且未提供 --yes；中止该步骤", prompt)
		return false
	}
	if o.reader == nil {
		o.reader = bufio.NewReader(o.Stdin)
	}
	fmt.Fprintf(o.Stdout, "%s [y/N] ", prompt)
	line, err := o.reader.ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes"
}

// ctx returns the run context (Background when none was wired in).
func (o *Options) ctx() context.Context {
	if o.Context == nil {
		return context.Background()
	}
	return o.Context
}

// DryRunReport is the machine-readable payload for `install --dry-run --json`.
type DryRunReport struct {
	Tool    string          `json:"tool"`
	Version string          `json:"version"`
	Command string          `json:"command"`
	Desired catalog.Desired `json:"desired"`
	Plan    PlanReport      `json:"plan"`
}

// dryRunReport assembles the agent-facing dry-run plan. command is the
// invoking command name (e.g. "install"), rendered as "<command> --dry-run".
func dryRunReport(d catalog.Desired, p *plan.Plan, command string) DryRunReport {
	return DryRunReport{
		Tool: "ompinyin", Version: catalog.Version, Command: command,
		Desired: d, Plan: planReportOf(p),
	}
}

// Install converges to the desired terminal state. forceRefetch re-downloads
// L2 assets (update). Returns the process exit code.
func Install(d catalog.Desired, forceRefetch bool, opts Options) int {
	// ---- lock (§8) ----
	lock, err := state.Acquire()
	if err != nil {
		opts.errf("[失败] %v", err)
		return ExitExecFail
	}
	defer lock.Release()

	// ---- facts precheck (exit 3) ----
	pc, err := facts.Collect(opts.OSOverride, state.Home())
	if err != nil {
		opts.errf("[失败] 预检异常：%v", err)
		return ExitPrecheck
	}
	if len(pc.Failures) > 0 {
		for _, f := range pc.Failures {
			opts.errf("[失败] 预检：%s", f)
		}
		return ExitPrecheck
	}
	if pc.OSOverridden {
		opts.errf("[警告] 预检：--os-override 已绕过 ID=omarchy 检查（实际 ID=%s）；仅限容器/VM 测试用",
			pc.OS.ID)
	}
	for _, w := range pc.ToolWarnings {
		opts.errf("[警告] 预检：%s", w)
	}
	herdr := "未检测到"
	if pc.HerdrPrefix {
		herdr = "已检测到（触发键已避让 Ctrl+Space 前缀）"
	}
	opts.outf("[完成] 预检：ID=%s BUILD_ID=%s 磁盘空闲 %d MiB octagram ok herdr %s",
		pc.OS.ID, pc.OS.BuildID, pc.DiskFree>>20, herdr)

	if err := d.Validate(); err != nil {
		opts.errf("[失败] 终态非法：%v", err)
		return ExitUsage
	}

	// ---- observe + plan ----
	st, err := state.Load()
	if err != nil {
		opts.errf("[失败] 读取状态清单：%v", err)
		return ExitExecFail
	}
	cur := observe.Collect(d, st)
	// A channel change is a change of terminal state, so it must refresh L2 just
	// like `update` does — otherwise the old build's bytes would be kept while
	// the ledger records the new channel.
	forceL2 := forceRefetch || (st.Desired.Channel != d.Channel)
	p := plan.Diff(d, cur, forceL2)

	// Machine-readable plan for agents: `install/switch/update --dry-run --json`
	// emits a structured plan (desired + layer predicates + steps) up front, so
	// an agent can assert "safe to apply" without parsing human text.
	if opts.DryRun && opts.JSON {
		cmd := opts.Command
		if cmd == "" {
			cmd = "install"
		}
		return writeJSON(opts.Stdout, dryRunReport(d, p, cmd+" --dry-run"))
	}

	opts.outf("── 收敛计划（desired: primary=%s extra=%v model=%v channel=%s）──",
		d.Primary, d.Extra, d.Model, d.Channel)
	opts.outf("%s", strings.TrimRight(p.Describe(), "\n"))
	if opts.DryRun {
		opts.outf("(dry-run: no changes applied)")
		return ExitOK
	}

	// X11 HiDPI is a compatibility opt-in whose artifacts (marker block + two
	// user units) live outside the rime ownership ledger. Record the accepted
	// user intent in EITHER direction as soon as it is accepted: a run that
	// applies the change and then fails a read-only check (L5) must not be
	// reinterpreted by the next bare `install` as the opposite intent (a failed
	// opt-in would be torn down; a failed opt-out would be silently re-enabled).
	// SaveLedger() cannot do this (it deliberately keeps the on-disk Desired),
	// so persist the flag directly; every other Desired field stays at its last
	// achieved value until the run completes. Placed AFTER the dry-run guard:
	// --dry-run must never write state.
	if d.X11HiDPI != st.Desired.X11HiDPI {
		st.Desired.X11HiDPI = d.X11HiDPI
		if err := st.Save(); err != nil {
			opts.errf("[警告] 记录 X11 HiDPI 模式意图失败：%v", err)
		}
	}

	// ---- backup (§8) ----
	// Only when a mutating layer actually has work: a converged re-run must not
	// leave a backup dir behind (评审 P0-9 tail).
	backupDir, err := obackup(opts, d, cur, st, p)
	if err != nil {
		opts.errf("[失败] 备份失败：%v", err)
		return ExitExecFail
	}
	if backupDir != "" {
		opts.outf("[完成] 备份：%s", backupDir)
	}

	// ---- L1 pkgs ----
	if p.NeedL1 {
		if missing, _ := pkgs.Missing(requiredPackages(d, cur)...); len(missing) > 0 {
			opts.outf("[计划] L1 安装系统包：%s", strings.Join(missing, " "))
			if err := pkgs.Install(missing, opts.Yes); err != nil {
				opts.errf("[失败] L1 %v", err)
				return ExitExecFail
			}
			opts.outf("[完成] L1 系统包安装完毕")
		} else {
			opts.outf("[跳过] L1 系统包已齐")
		}
	} else {
		opts.outf("[跳过] L1 系统包已齐")
	}

	// ---- L2 assets (gated by the same decision the plan printed) ----
	mgr := &assets.Manager{MirrorOverride: opts.Mirror, MirrorSource: opts.MirrorSource, LocalDir: opts.LocalDir, Logf: func(f string, a ...any) {
		opts.outf(f, a...)
	}}
	ctx := opts.ctx()
	if p.NeedL2 {
		if code := fetchAssets(opts, ctx, mgr, d, cur, st, forceRefetch); code != ExitOK {
			return code
		}
		// persist the asset records immediately: they describe bytes on disk
		saveLedger(opts, st)
	} else {
		opts.outf("[跳过] L2 数据资产已就位（未重下；ompinyin update 可强制刷新）")
	}

	// ---- L3 patches (ownership protocol, §5.1) ----
	l3Changed := false
	for _, f := range patches.ManagedFiles(d) {
		abs := filepath.Join(cur.RimeDir, f.RelPath)
		ledger := st.ManagedFiles[filepath.Base(f.RelPath)]
		status := patches.Classify(abs, ledger)
		if (status == patches.StatusAbsent || status == patches.StatusManaged) && cur.ContentEqual[f.RelPath] {
			// disk bytes already equal the desired content — rewriting would
			// churn mtimes and force a pointless rebuild (P1-1a: plan says
			// [跳过], execution must skip too)
			opts.outf("[跳过] L3 %s 已是目标内容", f.RelPath)
			continue
		}
		switch status {
		case patches.StatusAbsent, patches.StatusManaged:
			// ok to write
		case patches.StatusUserModified, patches.StatusForeign:
			prompt := fmt.Sprintf("%s 是%s，覆盖前将备份。继续？", f.RelPath, status)
			if !opts.confirm(prompt) {
				opts.errf("[失败] L3 用户拒绝覆盖 %s；中止", f.RelPath)
				return ExitExecFail
			}
			if err := copyToBackup(backupDir, abs); err != nil {
				opts.errf("[失败] L3 备份 %s 失败，拒绝覆盖：%v", f.RelPath, err)
				return ExitExecFail
			}
			opts.outf("[完成] L3 %s 原内容已备份", f.RelPath)
		}
		if err := patches.WriteFile(abs, f.Content, st); err != nil {
			opts.errf("[失败] L3 写 %s：%v", f.RelPath, err)
			return ExitExecFail
		}
		l3Changed = true
	}
	// Orphans: managed files the desired state no longer wants. This is NOT
	// limited to Model=false — switching layouts used to leave the previous
	// schema's grammar file on disk forever (评审 P1-5).
	for _, rel := range patches.OrphanFiles(st, d) {
		abs := filepath.Join(cur.RimeDir, rel)
		ledger := st.ManagedFiles[rel]
		if patches.Classify(abs, ledger) != patches.StatusManaged {
			if !opts.confirm(rel + " 已被手改，确认删除？") {
				continue
			}
		}
		if err := copyToBackup(backupDir, abs); err != nil {
			opts.errf("[失败] L3 备份孤儿文件 %s 失败，拒绝删除：%v", rel, err)
			return ExitExecFail
		}
		if err := patches.RemoveFile(abs, st); err != nil {
			opts.errf("[失败] L3 删除 %s：%v", rel, err)
			return ExitExecFail
		}
		l3Changed = true // the compiled set changed → rebuild
		opts.outf("[完成] L3 删除孤儿受管文件 %s（终态已不含它）", rel)
	}
	if l3Changed {
		opts.outf("[完成] L3 受管配置已生成（schema_list=%v）", d.SchemaList())
		// the ownership ledger now describes bytes on disk — persist it before
		// anything can fail (评审 P0-5)
		saveLedger(opts, st)
	} else {
		opts.outf("[跳过] L3 受管配置与终态一致（schema_list=%v，未重写）", d.SchemaList())
	}

	// ---- the single stop window (§6.0) ----
	// Opened only when there is deploy/config work inside it (P1-1a): an
	// already-converged host re-runs with zero stop/build/start churn.
	//
	// Every decision here comes from the plan the run just printed (invariant
	// 13): Install must consume Plan.Need* instead of recomputing the same
	// question from `cur`. A recomputation can drift from the predicate the user
	// read (the drop-in check used to look at file EXISTENCE while the plan and
	// L5 looked at CONTENT, so a stale drop-in was reported as work, then
	// skipped forever).
	deployNeeded := p.NeedDeploy
	hostNeeded := p.NeedHost
	serviceNeeded := p.NeedService
	hidpiNeeded := p.NeedHidpi
	if deployNeeded || hostNeeded || hidpiNeeded || serviceNeeded {
		if code := runStopWindow(opts, backupDir, cur, d, deployNeeded, hostNeeded, hidpiNeeded); code != ExitOK {
			return code
		}
		saveLedger(opts, st)
	} else {
		opts.outf("[跳过] L4 stop 窗口未开启（部署产物 / profile / hotkey / drop-in 均已达成，服务在运行）")
	}

	// ---- X11 HiDPI opt-out: withdraw the compat mode ----
	// Outside the stop window: teardown never touches fcitx5, so it must not
	// churn the input method for a feature the user just disabled.
	if p.NeedHidpiUndo {
		if code := undoHidpi(opts, backupDir); code != ExitOK {
			return code
		}
	}

	// ---- L4 tray pin: read → merge → set (ADR 13) ----
	if code := traySet(opts, backupDir); code != ExitOK {
		return code
	}

	// ---- L4 candidate-window theming (§6.6): classicui.conf + theme-set hook
	// + generate now + hot reload. Outside the stop window: fcitx5 reads these
	// without a restart (ReloadAddonConfig), unlike profile/hotkey/drop-in. ----
	if code := themeApply(opts, backupDir, st); code != ExitOK {
		return code
	}

	// ---- confirm the build artifacts exist (produced by deploy.Build) ----
	// Only meaningful when this run actually deployed; on a converged re-run
	// the artifacts from the previous run remain valid (P1-1a).
	if deployNeeded {
		// CompileSchemas is a test seam standing in for fcitx5-rime's lazy
		// deploy; in production --build already compiled everything.
		if err := deploy.CompileSchemas(cur.RimeDir, d.SchemaList()); err != nil {
			opts.errf("[失败] rime 编译：%v", err)
			return ExitExecFail
		}
		if missing := deploy.WaitForBuild(cur.RimeDir, d.SchemaList(), 20*time.Second); len(missing) > 0 {
			opts.errf("[失败] rime 产物缺失，无法出词：%s（检查 default.custom.yaml 是否为 - schema: <id> map 格式）",
				strings.Join(missing, ", "))
		}
	}

	// ---- L5 verify (read-only) ----
	cur2 := observe.Collect(d, st)
	checks := verify.TerminalState(d, cur2)
	failed := 0
	for _, chk := range checks {
		mark := "[完成]"
		if !chk.OK {
			mark = "[失败]"
			failed++
		}
		opts.outf("%s L5 %s：%s", mark, chk.Name, chk.Detail)
	}

	// ---- state ----
	// §8: a failed convergence must not record a terminal state it did not
	// reach. The ledger facts (managed-file hashes, asset records) are still
	// persisted, because they describe bytes that really are on disk.
	if failed > 0 {
		saveLedger(opts, st)
		opts.errf("[失败] %d 项 L5 复核未达终态；不自动回滚，下次收敛继续修（可用 ompinyin doctor 查看）", failed)
		return ExitExecFail
	}
	st.Desired = d
	st.SchemaList = d.SchemaList()
	if err := st.Save(); err != nil {
		opts.errf("[失败] 写状态清单：%v", err)
		return ExitExecFail
	}
	// rotate backups only after a successful convergence (never delete the
	// snapshot a failed run might still need)
	if removed, err := state.PruneBackups(state.LedgerKeepBackups, state.LedgerBackupBudget); err != nil {
		opts.errf("[警告] 清理旧备份失败：%v", err)
	} else if len(removed) > 0 {
		opts.outf("[完成] 已清理 %d 个旧备份（保留最近 %d 个）", len(removed), state.LedgerKeepBackups)
	}
	opts.outf("[完成] 终态收敛完成；F4 切方案，触发键切中英（Alt+Space）")
	return ExitOK
}

func requiredPackages(d catalog.Desired, cur *observe.Current) []string {
	out := append([]string{}, pkgs.Needed...)
	if d.X11HiDPI && cur.X11ForceZeroScaling {
		out = append(out, pkgs.X11HiDPIPackage)
	}
	return out
}

// runStopWindow opens the ONE fcitx5 stop window of §6.0: stop → deploy
// --build → profile/hotkey/drop-in → daemon-reload → start.
//
// A deferred guard always closes the window: every early return (including a
// daemon-reload or drop-in failure) restarts the unit and reports a failed
// restart loudly, because leaving fcitx5 stopped means the user has no input
// method at all (评审 P0-4).
func runStopWindow(opts Options, backupDir string, cur *observe.Current, d catalog.Desired, deployNeeded, hostNeeded, hidpiNeeded bool) (code int) {
	unit := service.FindUnit(state.Home())
	if unit == "" {
		unit = "omarchy-fcitx5.service" // installed by L1; discovery next run
	}
	stopped := false
	// closeWindow runs once on every exit path. It starts the unit even when we
	// never stopped it: "fcitx5 running" is part of the L4 terminal state (§6.4
	// needs the SNI item live), and `systemctl --user start` is idempotent.
	// A failed start downgrades the named return to ExitExecFail — the caller
	// must not keep going as if the window had closed (评审 P0-4).
	defer func() {
		if err := service.Start(unit); err != nil {
			if stopped {
				opts.errf("[失败] 无法重启 %s（请立即手动 `systemctl --user start %s`，否则无输入法可用）：%v", unit, unit, err)
			} else {
				opts.errf("[失败] L4 start %s：%v", unit, err)
			}
			if code == ExitOK {
				code = ExitExecFail
			}
			return
		}
		if stopped {
			opts.outf("[完成] L4 start %s（stop 窗口关闭）", unit)
		} else {
			opts.outf("[完成] L4 start %s（未需停止，直接启动）", unit)
		}
	}()

	if service.IsActive(unit) {
		if err := service.Stop(unit); err != nil {
			opts.errf("[失败] %v", err)
			return ExitExecFail
		}
		stopped = true
		opts.outf("[完成] L4 stop %s（唯一 stop 窗口开启）", unit)
	}

	// deploy seam: must run while stopped
	if deployNeeded {
		if err := deploy.Build(cur.RimeDir, d.SchemaList()); err != nil {
			opts.errf("[失败] %v", err)
			opts.errf("[提示] fcitx5 已停止；修复后可重跑 ompinyin install 收敛")
			return ExitExecFail
		}
		opts.outf("[完成] rime_deployer --build 完成")
	} else if stopped {
		opts.outf("[跳过] rime_deployer --build（配置与产物均无变化）")
	}

	if !hostNeeded && !hidpiNeeded {
		return ExitOK // deferred guard restarts the unit
	}

	// L4 profile / hotkey / drop-in
	if hostNeeded {
		if err := writeProfile(opts, backupDir); err != nil {
			opts.errf("[失败] L4 profile：%v", err)
			return ExitExecFail
		}
		if err := writeHotkey(opts, backupDir); err != nil {
			opts.errf("[失败] L4 hotkey：%v", err)
			return ExitExecFail
		}
		created, err := tray.WriteDropIn(state.Home(), unit, deriveDropIn(unit))
		if err != nil {
			opts.errf("[失败] L4 drop-in：%v", err)
			return ExitExecFail
		}
		if created {
			// 注：已有 drop-in 的原始内容已在 obackup 阶段快照（先备份后写）
			opts.outf("[完成] L4 notificationitem 专用 drop-in 已写入")
		} else {
			opts.outf("[跳过] L4 notificationitem drop-in 已是目标内容")
		}
	}
	if hidpiNeeded {
		if err := writeHidpi(opts, backupDir, cur); err != nil {
			opts.errf("[失败] L4 X11 HiDPI：%v", err)
			return ExitExecFail
		}
	}

	if err := service.DaemonReload(); err != nil {
		opts.errf("[失败] daemon-reload：%v", err)
		return ExitExecFail
	}
	if hidpiNeeded {
		if err := service.Run("systemctl", "--user", "enable", hidpi.ServiceName); err != nil {
			opts.errf("[失败] 启用 X11 HiDPI 登录发布：%v", err)
			return ExitExecFail
		}
		if err := service.Run("systemctl", "--user", "enable", "--now", hidpi.PathName); err != nil {
			opts.errf("[失败] 启用 X11 HiDPI 缩放监听：%v", err)
			return ExitExecFail
		}
	}
	// the deferred guard performs the start and reports its failure
	return ExitOK
}

// writeHidpi maintains a line-scoped Xresources block plus user units.  It
// never writes monitor configuration or classicui.conf; the latter is a user
// preference and cannot fix XWayland's fixed RandR DPI anyway.
func writeHidpi(opts Options, backupDir string, cur *observe.Current) error {
	// Hyprland scales X11 windows itself when force_zero_scaling=false. In that
	// regime the candidate window is already scaled by the compositor, so
	// publishing a global Xft.dpi would double-scale every X11 client
	// (candidate included). The mode is inert there, not a failure.
	if !cur.X11ForceZeroScaling {
		opts.outf("[跳过] L4 X11 HiDPI：Hyprland force_zero_scaling=false（合成器已在缩放 X11 窗口），不发布 Xft.dpi（会二次放大）")
		return nil
	}
	home := state.Home()
	xres := hidpi.XresourcesPath(home)
	b, err := os.ReadFile(xres)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	merged, changed := hidpi.MergeXresources(string(b), cur.X11DPIDesired)
	if changed {
		if err := copyToBackup(backupDir, xres); err != nil {
			return err
		}
		if err := state.WriteAtomic(xres, []byte(merged)); err != nil {
			return err
		}
		opts.outf("[完成] L4 X11 HiDPI：~/.Xresources Xft.dpi=%d", cur.X11DPIDesired)
	}
	serviceBody, pathBody := hidpi.UnitContent()
	for _, u := range []struct{ path, body string }{{hidpi.ServicePath(home), serviceBody}, {hidpi.PathPath(home), pathBody}} {
		old, rerr := os.ReadFile(u.path)
		if rerr == nil && string(old) == u.body {
			continue
		}
		if rerr != nil && !os.IsNotExist(rerr) {
			return rerr
		}
		if err := copyToBackup(backupDir, u.path); err != nil {
			return err
		}
		if err := state.WriteAtomic(u.path, []byte(u.body)); err != nil {
			return err
		}
	}
	// Probe the LIVE X server instead of trusting cur.X11Available: that field
	// was observed before L1, and on a first opt-in xorg-xrdb is installed by
	// L1 itself. Reading the stale snapshot skipped the publish and then failed
	// L5 ("期望=192 实际=0") — the first opt-in needed a second run.
	// An unreachable X session is a legitimate no-op (pure Wayland), so probe
	// first and only treat a merge failure as fatal when the server IS there.
	if _, qerr := hidpi.Run("xrdb", "-query"); qerr != nil {
		opts.outf("[跳过] L4 X11 HiDPI：未检测到可用的 X 会话（无 XWayland 或 DISPLAY 不可达）；已安装登录/缩放监听，下次登录发布")
		return nil
	}
	if _, err := hidpi.Run("xrdb", "-merge", xres); err != nil {
		return fmt.Errorf("xrdb -merge: %w", err)
	}
	opts.outf("[完成] L4 X11 HiDPI：已发布 Xft.dpi=%d（fcitx5 将在本 stop 窗口后重启）", cur.X11DPIDesired)
	return nil
}

// hidpiUnits lists ompinyin's two publisher units. Order matters: the path
// unit triggers the service, so it is disabled first.
func hidpiUnits(home string) []struct{ unit, path, kind string } {
	return []struct{ unit, path, kind string }{
		{hidpi.PathName, hidpi.PathPath(home), "缩放监听单元"},
		{hidpi.ServiceName, hidpi.ServicePath(home), "登录发布单元"},
	}
}

// disableHidpiUnits stops and disables the publisher units. Failures are
// reported but never abort: the files are removed next and ApplyX11HiDPI is a
// no-op once Desired is off, so a stranded watcher cannot re-publish a global
// resource. Not swallowing the error is still required — the previous `_ =`
// made "先禁单元再删块，消除竞态" a claim the code could not back.
func disableHidpiUnits(warn func(string, ...any)) {
	for _, u := range hidpiUnits(state.Home()) {
		if err := service.Run("systemctl", "--user", "disable", "--now", u.unit); err != nil {
			warn("[警告] 停用 %s 失败：%v（仍会删除文件；未 opt-in 时 apply 是 no-op，不会重新发布）", u.kind, err)
		}
	}
}

// removeHidpiArtifacts withdraws the compat mode's ONLY artifacts: the two
// publisher units and the managed Xresources block. Shared by --no-x11-hidpi
// and uninstall so the order (watcher first) cannot drift between them; the
// caller decides whether a removal error is fatal (opt-out) or a warning
// (uninstall). removedBlock reports whether the managed Xft.dpi block was there.
func removeHidpiArtifacts(backupDir string, warn func(string, ...any)) (removedBlock bool, err error) {
	disableHidpiUnits(warn)
	if err := removeHidpiUnits(backupDir); err != nil {
		return false, err
	}
	removed, err := removeManagedXresources(backupDir)
	if err != nil {
		return false, err
	}
	// Best-effort: the unit files are gone and ApplyX11HiDPI is a no-op without
	// the recorded opt-in, so a stale unit cache cannot re-publish Xft.dpi.
	if derr := service.DaemonReload(); derr != nil {
		warn("[警告] daemon-reload：%v", derr)
	}
	return removed, nil
}

// removeHidpiUnits backs up then deletes the two publisher unit files.
func removeHidpiUnits(backupDir string) error {
	for _, u := range hidpiUnits(state.Home()) {
		if _, err := os.Stat(u.path); os.IsNotExist(err) {
			continue
		}
		if err := copyToBackup(backupDir, u.path); err != nil {
			return fmt.Errorf("备份 %s 失败，拒绝删除：%w", u.kind, err)
		}
		if err := os.Remove(u.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("删除 %s：%w", u.kind, err)
		}
	}
	return nil
}

// removeManagedXresources drops the managed Xft.dpi block from ~/.Xresources,
// deleting the file when nothing user-owned remains. removed reports whether a
// block was present. Shared by undoHidpi and Uninstall so the ownership rules
// cannot drift between the two teardown paths.
func removeManagedXresources(backupDir string) (removed bool, err error) {
	xres := hidpi.XresourcesPath(state.Home())
	xb, rerr := os.ReadFile(xres)
	if rerr != nil {
		return false, nil // absent or unreadable: leave it alone
	}
	remaining, changed, empty := hidpi.RemoveXresources(string(xb))
	if !changed {
		return false, nil
	}
	if err := copyToBackup(backupDir, xres); err != nil {
		return false, fmt.Errorf("备份 Xresources 失败，拒绝修改：%w", err)
	}
	if empty {
		if err := os.Remove(xres); err != nil && !os.IsNotExist(err) {
			return true, fmt.Errorf("删除 Xresources：%w", err)
		}
		return true, nil
	}
	if err := state.WriteAtomic(xres, []byte(remaining)); err != nil {
		return true, fmt.Errorf("写 Xresources：%w", err)
	}
	return true, nil
}

// undoHidpi withdraws an opted-in compat mode. Order matters: the path unit
// re-publishes Xft.dpi on scale edits, so it is disabled and the units removed
// BEFORE the file they watch is edited; only then is the managed Xresources
// block dropped. The already-published RESOURCE_MANAGER value survives until
// logout — re-publishing an unset needs destructive root-window surgery
// (xrdb -load would wipe the user's whole resource database), so a running
// session keeps the old value; the next login starts clean. The block and the
// two units are the ONLY artifacts, so files outside them are never touched.
func undoHidpi(opts Options, backupDir string) int {
	if backupDir == "" {
		// §5.1 先备份后写 is a hard promise: never remove owned artifacts
		// without a snapshot. obackup only opens a dir when it found targets;
		// a host whose profile/config are gone still gets one here.
		var err error
		backupDir, err = uniqueBackupDir()
		if err != nil {
			opts.errf("[失败] 无备份目录，拒绝撤销 X11 HiDPI：%v", err)
			return ExitExecFail
		}
		opts.outf("[完成] 备份：%s", backupDir)
	}
	// 1. stop the watcher before changing anything inside it, then remove only
	// our two units and the managed block (§5.1 line-scoped ownership).
	removedBlock, err := removeHidpiArtifacts(backupDir, opts.errf)
	if err != nil {
		opts.errf("[失败] %v", err)
		return ExitExecFail
	}
	if removedBlock {
		opts.outf("[完成] X11 HiDPI 受管 Xft.dpi 块已移除（其余 Xresources 保留）")
	}
	opts.outf("[完成] X11 HiDPI 兼容模式已撤销；当前会话已发布的值保持到注销，下次登录不再发布")
	return ExitOK
}

// ApplyX11HiDPI is the private user-service entrypoint. The path unit invokes
// it after a monitor-scale edit. XCBUI reads RESOURCE_MANAGER only on startup,
// so fcitx5 is restarted only if the integer DPI actually changed. It is a
// no-op unless the mode is recorded as opted-in: the units are only installed
// by the opt-in path, but a stale enable link (or a manual re-enable) must not
// start publishing a global resource behind the user's back.
func ApplyX11HiDPI(opts Options) (code int) {
	// The path unit can fire while `ompinyin install` is mid-run. Taking the
	// same lock prevents two concurrent fcitx5 stop windows (AGENTS 关键陷阱 #1:
	// one stop window, always). Contention is not an error for a background
	// hook — skip and let the next scale change / login retry.
	lock, err := state.Acquire()
	if err != nil {
		opts.errf("[跳过] X11 HiDPI：%v（另一 ompinyin 实例在运行；下次缩放变化或登录会重试）", err)
		return ExitOK
	}
	defer lock.Release()

	st, err := state.Load()
	if err != nil {
		opts.errf("[失败] 读取状态清单：%v", err)
		return ExitExecFail
	}
	d := st.Desired
	if d.IsZero() {
		d = catalog.DefaultDesired()
	}
	if !d.X11HiDPI {
		opts.outf("[跳过] X11 HiDPI 未处于 opt-in 状态（--no-x11-hidpi 已撤销；不发布全局 Xft.dpi）")
		return ExitOK
	}
	cur := observe.Collect(d, st)
	if !cur.X11ForceZeroScaling {
		opts.outf("[跳过] X11 HiDPI：Hyprland force_zero_scaling=false（合成器已在缩放 X11 窗口），不发布 Xft.dpi")
		return ExitOK
	}
	if err := writeHidpi(opts, "", cur); err != nil {
		opts.errf("[失败] X11 HiDPI：%v", err)
		return ExitExecFail
	}
	if err := service.DaemonReload(); err != nil {
		opts.errf("[失败] daemon-reload：%v", err)
		return ExitExecFail
	}
	if err := service.Run("systemctl", "--user", "enable", hidpi.ServiceName); err != nil {
		opts.errf("[失败] 启用登录发布：%v", err)
		return ExitExecFail
	}
	if err := service.Run("systemctl", "--user", "enable", "--now", hidpi.PathName); err != nil {
		opts.errf("[失败] 启用缩放监听：%v", err)
		return ExitExecFail
	}
	if !cur.X11Available || cur.X11DPIActual == cur.X11DPIDesired {
		return ExitOK
	}
	unit := service.FindUnit(state.Home())
	if unit == "" || !service.IsActive(unit) {
		return ExitOK
	}
	if err := service.Stop(unit); err != nil {
		opts.errf("[失败] %v", err)
		return ExitExecFail
	}
	// Mirror the runStopWindow guard: a failed restart means no input method,
	// so it must downgrade the exit code and say how to recover (评审 P0-4).
	defer func() {
		if err := service.Start(unit); err != nil {
			opts.errf("[失败] 重启 %s：%v（请立即手动 `systemctl --user start %s`，否则无输入法可用）", unit, err, unit)
			if code == ExitOK {
				code = ExitExecFail
			}
			return
		}
		opts.outf("[完成] X11 HiDPI：Xft.dpi 已变化，已重启 %s", unit)
	}()
	return ExitOK
}

// fetchAssets runs the L2 layer: resolve the stable tag, fetch + verify the
// rime-ice archive and the wanxiang model, and place them. Returns an exit
// code. forceRefetch (= update) overwrites already-placed data files.
func fetchAssets(opts Options, ctx context.Context, mgr *assets.Manager, d catalog.Desired, cur *observe.Current, st *state.State, forceRefetch bool) int {
	if forceRefetch {
		// update: drop cached assets so latest releases are re-resolved
		// (glob covers per-tag immutable cache keys + the pre-review legacy name)
		for _, pattern := range []string{"rime-ice-full-*"} {
			matches, _ := filepath.Glob(filepath.Join(assets.CacheDir(), pattern))
			for _, m := range matches {
				os.Remove(m)
			}
		}
		os.Remove(filepath.Join(assets.CacheDir(), catalog.Wanxiang().Name))
	}

	prev := st.Assets["rime_ice"]
	ri := catalog.RimeIce(d.Channel)
	if d.Channel != "nightly" {
		// stable: pin to the concrete latest stable tag (P2-7). releases/latest
		// on this repo IS the rolling nightly, while the NJU LatestRelease
		// mirror serves the last stable snapshot — pinning a resolved tag makes
		// every mirror candidate byte-identical.
		//
		// Resolution happens when there is no usable pin (plain install) OR when
		// this run is `update` (forceRefetch): update is the documented entry
		// point that re-pins to the newest release, so it must not keep serving
		// the previously recorded tag forever. A plain install with a pin keeps
		// it, so a blocked api.github.com (normal in CN) is never retried on
		// every convergence. When resolution fails on update we keep the
		// recorded tag (a refresh of the same tag is still useful) instead of
		// silently falling back to the nightly bytes.
		tag := prev.Tag
		if forceRefetch || !isStableTag(tag) {
			opts.outf("[计划] L2 解析最新 stable release tag…")
			if resolved := assets.ResolveStableTag(ctx); resolved != "" {
				tag = resolved
			} else if !isStableTag(tag) {
				opts.errf("[提示] L2 无法解析 stable tag（API 不可达？），本次沿用 releases/latest；该 repo 上它指向滚动 nightly，API 可达时跑 ompinyin update 可重新 pin")
				tag = ""
			} else {
				opts.errf("[提示] L2 无法解析新 stable tag（API 不可达？），本次沿用已 pin 的 %s", tag)
			}
		}
		if tag != "" {
			ri = catalog.RimeIceTagged(tag)
		}
	}
	// The ledger cross-check is a comparison against the PREVIOUS download, so
	// the hint tag and hint sha must describe the same tag. Passing the new
	// ri.Tag together with prev.SHA256 made a tag change (channel switch
	// nightly→stable, or a fresh pin after a blocked-API fallback) look like a
	// tampered immutable tag and hard-failed. A different tag has no recorded
	// checksum to compare against, so no hint is passed.
	hintTag, hintSHA := prev.Tag, prev.SHA256
	if ri.Tag != prev.Tag {
		hintSHA = ""
	}
	zipPath, zipSha, zipTag, err := mgr.Fetch(ctx, ri, hintTag, hintSHA)
	if err != nil {
		opts.errf("[失败] L2 %v", err)
		return ExitExecFail
	}
	if extracted, _, err := mgr.ExtractZip(zipPath, cur.RimeDir, forceRefetch); err != nil {
		opts.errf("[失败] L2 解压：%v", err)
		return ExitExecFail
	} else if len(extracted) > 0 {
		opts.outf("[完成] L2 雾凇数据落位（%d 个文件，tag=%s）", len(extracted), zipTag)
	}
	st.Assets["rime_ice"] = state.AssetRecord{Tag: zipTag, SHA256: zipSha}

	if d.Model {
		prevW := st.Assets["wanxiang"]
		gramPath, gramSha, gramTag, err := mgr.Fetch(ctx, catalog.Wanxiang(), catalog.Wanxiang().Tag, prevW.SHA256)
		if err != nil {
			opts.errf("[失败] L2 万象模型：%v", err)
			return ExitExecFail
		}
		placed, err := placeGram(gramPath, filepath.Join(cur.RimeDir, catalog.GrammarLanguage+".gram"), gramSha)
		if err != nil {
			opts.errf("[失败] L2 模型落位：%v", err)
			return ExitExecFail
		}
		st.Assets["wanxiang"] = state.AssetRecord{Tag: gramTag, SHA256: gramSha}
		if placed {
			opts.outf("[完成] L2 万象 LMDG 模型就位（sha256 %s…）", trunc(gramSha, 12))
		} else {
			opts.outf("[跳过] L2 万象 LMDG 模型已是同一 sha256（%s…）", trunc(gramSha, 12))
		}
	}
	return ExitOK
}

// saveLedger persists the ownership/asset facts without advancing the desired
// state, so a failure in a later layer cannot make ompinyin forget the bytes
// it already wrote (评审 P0-5).
func saveLedger(opts Options, st *state.State) {
	if err := st.SaveLedger(); err != nil {
		opts.errf("[警告] 写状态清单（账本）失败：%v", err)
	}
}

// placeGram copies the cached gram into the rime dir atomically, streaming (the
// model is ~420MB; reading it into memory doubled the process RSS). Returns
// placed=false when the destination already carries the same checksum.
func placeGram(cachePath, destPath, sha string) (bool, error) {
	if cur := state.HashFile(destPath); cur == sha && cur != "" {
		return false, nil
	}
	src, err := os.Open(cachePath)
	if err != nil {
		return false, err
	}
	defer src.Close()
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(destPath), ".ompinyin-gram-*")
	if err != nil {
		return false, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(tmpName, destPath); err != nil {
		return false, err
	}
	// integrity of what actually landed: a mismatch means the cache or the copy
	// is broken, and a corrupt .gram must never be recorded as in place
	if got := state.HashFile(destPath); sha != "" && got != sha {
		return false, fmt.Errorf("model checksum mismatch after placement (%s… != %s…)", trunc(got, 12), trunc(sha, 12))
	}
	return true, nil
}

// deriveDropIn builds the drop-in body from the unit's original ExecStart so
// upstream flag/path changes survive (评审 2.2); falls back to the static
// content when the unit file cannot be read.
func deriveDropIn(unit string) string {
	p := service.UnitFilePath(state.Home(), unit)
	if line := service.ExecStartLine(p); line != "" {
		return tray.DeriveDropInContent(line)
	}
	return tray.DropInContent
}

// isStableTag reports whether tag looks like a rime-ice stable release tag
// (date-based, e.g. "2026.06.30"); the rolling nightly is excluded.
func isStableTag(tag string) bool {
	return tag != "" && tag != "nightly"
}

func writeProfile(opts Options, backupDir string) error {
	p := observe.ProfilePath()
	b, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	newContent, changed, err := profile.EnsureRime(string(b))
	if err != nil {
		return err
	}
	if !changed {
		opts.outf("[跳过] L4 profile 已注册 rime")
		return nil
	}
	if err := copyToBackup(backupDir, p); err != nil {
		return fmt.Errorf("backup %s: %w", p, err)
	}
	return state.WriteAtomic(p, []byte(newContent))
}

func writeHotkey(opts Options, backupDir string) error {
	p := observe.ConfigPath()
	b, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	newContent, changed, err := hotkey.EnsureTrigger(string(b), hotkey.DefaultKeys)
	if err != nil {
		return err
	}
	if !changed {
		opts.outf("[跳过] L4 触发键已含受管键（用户附加键保留）")
		return nil
	}
	if err := copyToBackup(backupDir, p); err != nil {
		return fmt.Errorf("backup %s: %w", p, err)
	}
	return state.WriteAtomic(p, []byte(newContent))
}

// traySet performs step B: read pinned → merge Fcitx → one `bar set`.
func traySet(opts Options, backupDir string) int {
	shellJSON := tray.ShellJSONPath(state.Home())
	b, err := os.ReadFile(shellJSON)
	if err != nil {
		if !os.IsNotExist(err) {
			opts.errf("[失败] L4 读取 shell.json：%v", err)
			return ExitExecFail
		}
		b = nil
	}
	pinned, err := tray.ReadPinned(b)
	if err != nil {
		opts.errf("[失败] L4 %v", err)
		return ExitExecFail
	}
	if tray.HasPin(pinned) {
		opts.outf("[跳过] L4 托盘已 pin %s", tray.FcitxId)
		return ExitOK
	}
	merged := tray.MergePin(pinned)
	if err := copyToBackup(backupDir, shellJSON); err != nil {
		opts.errf("[失败] L4 备份 shell.json 失败，拒绝写入：%v", err)
		return ExitExecFail
	}
	// 直接写数组到 shell.json：4.0.1 的 bar set/IPC 会把数组强转成字符串，
	// Tray.qml 只认数组；外壳的 FileView 监控会自动 reload 并采用该 pin。
	if err := tray.SetPinned(shellJSON, merged); err != nil {
		opts.errf("[失败] L4 写入托盘 pin：%v", err)
		return ExitExecFail
	}
	opts.outf("[完成] L4 托盘 pin：%v → %v（已写入 shell.json，外壳将自动应用）", pinned, merged)
	// pin 数组热加载只让图标出现；fcitx5 notificationitem 的 SNI 显示
	// （keyboard-us ↔ rime）需要重启外壳才刷新（见 tray.RestartShell，
	// 对应参考笔记的 omarchy-restart-shell）。
	if err := tray.RestartShell(); err != nil {
		opts.errf("[提示] L4 刷新外壳失败（可手动 omarchy restart shell）：%v", err)
	}
	return ExitOK
}

// ---------------------------------------------------------------------------
// L4 candidate-window theming (§6.6)
// ---------------------------------------------------------------------------

// themeApply converges the candidate-window theming terminal state:
// classicui.conf + theme-set hook (both ledger-tracked, §5.1 ownership
// protocol), immediate generation from the current Omarchy theme, and a DBus
// hot reload of the classicui addon. It never touches the stop window: fcitx5
// reads these files on reload, no restart needed.
func themeApply(opts Options, backupDir string, st *state.State) int {
	home := state.Home()
	confContent := theme.ConfContent(theme.CurrentFont())
	hookContent := theme.HookContent()

	// converged already: skip (also keeps a reconverged re-run a true no-op)
	if b, err := os.ReadFile(theme.ConfPath(home)); err == nil && string(b) == confContent {
		if hb, err := os.ReadFile(theme.HookPath(home)); err == nil && string(hb) == hookContent {
			if theme.ThemeDirPopulated(home) {
				opts.outf("[跳过] L4 候选框已跟随 Omarchy 主题")
				return ExitOK
			}
		}
	}

	changed, code := writeThemeFile(opts, backupDir, st, theme.ConfPath(home), theme.ConfRelPath, confContent, "classicui.conf", 0)
	if code != ExitOK {
		return code
	}
	if changed {
		opts.outf("[完成] L4 classicui.conf → Theme=%s", theme.ThemeName)
	}
	if _, code := writeThemeFile(opts, backupDir, st, theme.HookPath(home), theme.HookRelPath, hookContent, "theme-set 钩子 (fcitx5-theme)", 0o755); code != ExitOK {
		return code
	}

	// 立即按当前 Omarchy 主题生成（首次安装/目录缺失/换主题后未触发钩子时；幂等）
	if err := theme.Generate(home); err != nil {
		opts.errf("[失败] L4 候选框主题生成：%v", err)
		return ExitExecFail
	}
	opts.outf("[完成] L4 候选框主题已按当前 Omarchy 主题生成")

	// the ownership/hashes describe bytes on disk now — persist before the reload
	saveLedger(opts, st)

	// hot-reload the classicui addon (`fcitx5-remote -r` reloads only the
	// global config and would not re-read the theme — verified on host)
	if err := theme.Reload(); err != nil {
		opts.errf("[提示] L4 热重载 fcitx5 候选框主题失败（服务重启时会自然生效）：%v", err)
	} else {
		opts.outf("[完成] L4 fcitx5 已热重载候选框主题")
	}
	return ExitOK
}

// writeThemeFile applies the §5.1 ownership protocol to one theming file and
// records the new hash in the ledger (keyed by the home-relative path).
func writeThemeFile(opts Options, backupDir string, st *state.State, abs, rel, content, label string, mode os.FileMode) (bool, int) {
	ledger := st.ManagedFiles[rel]
	status := patches.Classify(abs, ledger)
	if b, err := os.ReadFile(abs); err == nil && string(b) == content {
		return false, ExitOK
	}
	switch status {
	case patches.StatusAbsent, patches.StatusManaged:
		// ok to rewrite
	case patches.StatusUserModified, patches.StatusForeign:
		if !opts.confirm(fmt.Sprintf("%s 是%s，覆盖前将备份。继续？", label, status)) {
			opts.errf("[失败] 用户拒绝覆盖 %s；中止", label)
			return false, ExitExecFail
		}
		if err := copyToBackup(backupDir, abs); err != nil {
			opts.errf("[失败] 备份 %s 失败，拒绝覆盖：%v", label, err)
			return false, ExitExecFail
		}
		opts.outf("[完成] %s 原内容已备份", label)
	}
	if err := state.WriteAtomic(abs, []byte(content)); err != nil {
		opts.errf("[失败] 写 %s：%v", label, err)
		return false, ExitExecFail
	}
	if mode != 0 {
		if err := os.Chmod(abs, mode); err != nil {
			opts.errf("[失败] chmod %s：%v", label, err)
			return false, ExitExecFail
		}
	}
	st.ManagedFiles[rel] = state.HashBytes([]byte(content))
	return true, ExitOK
}

// ---------------------------------------------------------------------------
// backup (§8)
// ---------------------------------------------------------------------------

// obackup snapshots files that convergence may touch. Returns "" when nothing
// needed backing up: a run with no mutating work (p.NeedsApply()==false) must
// not create a backup directory at all (评审 P0-9 tail).
func obackup(opts Options, d catalog.Desired, cur *observe.Current, st *state.State, p *plan.Plan) (string, error) {
	var targets []string
	if opts.FullBackup {
		targets = append(targets, cur.RimeDir, observe.FcitxConfigDir(), tray.ShellJSONPath(state.Home()), theme.ThemeDir(state.Home()))
	} else if p.NeedsApply() {
		for rel, status := range cur.Managed {
			if status == patches.StatusUserModified || status == patches.StatusForeign {
				targets = append(targets, filepath.Join(cur.RimeDir, rel))
			}
		}
		for _, f := range []string{observe.ProfilePath(), observe.ConfigPath(), tray.ShellJSONPath(state.Home()), cur.DropInPath, theme.ConfPath(state.Home()), theme.HookPath(state.Home())} {
			if _, err := os.Stat(f); err == nil {
				targets = append(targets, f)
			}
		}
		// X11 HiDPI artifacts live outside the rime ledger; snapshot them too
		// (§5.1) so the opt-in / opt-out paths always have a pre-change copy even
		// when no other target exists on the host.
		if d.X11HiDPI || p.NeedHidpiUndo {
			for _, f := range []string{hidpi.XresourcesPath(state.Home()), hidpi.ServicePath(state.Home()), hidpi.PathPath(state.Home())} {
				if _, err := os.Stat(f); err == nil {
					targets = append(targets, f)
				}
			}
		}
	}
	if len(targets) == 0 {
		return "", nil
	}
	backupDir, err := uniqueBackupDir()
	if err != nil {
		return "", err
	}
	for _, src := range targets {
		if err := copyInto(backupDir, src); err != nil {
			return "", err
		}
	}
	return backupDir, nil
}

// uniqueBackupDir names backups by second but de-conflicts collisions (two runs
// inside the same second used to share — and mix — one directory).
func uniqueBackupDir() (string, error) {
	base := time.Now().Format("20060102-150405")
	for n := 0; n < 100; n++ {
		name := "backup-" + base
		if n > 0 {
			name = fmt.Sprintf("backup-%s-%d", base, n)
		}
		p := filepath.Join(state.Dir(), name)
		if _, err := os.Stat(p); os.IsNotExist(err) {
			if err := os.MkdirAll(p, 0o755); err != nil {
				return "", err
			}
			return p, nil
		}
	}
	return "", fmt.Errorf("cannot allocate a backup directory under %s", state.Dir())
}

// copyToBackup copies one file into the backup dir if backupDir is set.
// copyToBackup snapshots src before it is overwritten. A failed backup is
// fatal for that write: §5.1/§8 promise the user's original bytes exist
// before anything is replaced, so the error is returned, not dropped.
func copyToBackup(backupDir, src string) error {
	if backupDir == "" {
		return nil
	}
	return copyInto(backupDir, src)
}

// copyInto copies src into the backup dir (see state.CopyFile).
func copyInto(backupDir, src string) error {
	rel, err := filepath.Rel(state.Home(), src)
	if err != nil {
		return err
	}
	dest := filepath.Join(backupDir, rel)
	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.IsDir() {
		return filepath.WalkDir(src, func(path string, d os.DirEntry, werr error) error {
			if werr != nil {
				return nil
			}
			sub, _ := filepath.Rel(state.Home(), path)
			dd := filepath.Join(backupDir, sub)
			if d.IsDir() {
				return os.MkdirAll(dd, 0o755)
			}
			return copyInto(backupDir, path)
		})
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return state.CopyFile(src, dest)
}
