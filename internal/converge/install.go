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
	"github.com/ProjectAILeap/ompinyin/internal/hotkey"
	"github.com/ProjectAILeap/ompinyin/internal/observe"
	"github.com/ProjectAILeap/ompinyin/internal/patches"
	"github.com/ProjectAILeap/ompinyin/internal/pkgs"
	"github.com/ProjectAILeap/ompinyin/internal/plan"
	"github.com/ProjectAILeap/ompinyin/internal/profile"
	"github.com/ProjectAILeap/ompinyin/internal/service"
	"github.com/ProjectAILeap/ompinyin/internal/state"
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
		if missing, _ := pkgs.Missing(pkgs.Needed...); len(missing) > 0 {
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
	deployNeeded := len(cur.BuildMissing) > 0 || l3Changed
	hostNeeded := !cur.ProfileHasRime || !cur.HotkeyOK || !cur.DropInExists
	if deployNeeded || hostNeeded {
		if code := runStopWindow(opts, backupDir, cur, d, deployNeeded, hostNeeded); code != ExitOK {
			return code
		}
		saveLedger(opts, st)
	} else {
		opts.outf("[跳过] L4 stop 窗口未开启（部署产物 / profile / hotkey / drop-in 均已达成）")
	}

	// ---- L4 tray pin: read → merge → set (ADR 13) ----
	if code := traySet(opts, backupDir); code != ExitOK {
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
	if removed, err := state.PruneBackups(state.LedgerKeepBackups); err != nil {
		opts.errf("[警告] 清理旧备份失败：%v", err)
	} else if len(removed) > 0 {
		opts.outf("[完成] 已清理 %d 个旧备份（保留最近 %d 个）", len(removed), state.LedgerKeepBackups)
	}
	opts.outf("[完成] 终态收敛完成；F4 切方案，触发键切中英（Alt+Space）")
	return ExitOK
}

// runStopWindow opens the ONE fcitx5 stop window of §6.0: stop → deploy
// --build → profile/hotkey/drop-in → daemon-reload → start.
//
// A deferred guard always closes the window: every early return (including a
// daemon-reload or drop-in failure) restarts the unit and reports a failed
// restart loudly, because leaving fcitx5 stopped means the user has no input
// method at all (评审 P0-4).
func runStopWindow(opts Options, backupDir string, cur *observe.Current, d catalog.Desired, deployNeeded, hostNeeded bool) (code int) {
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

	if !hostNeeded {
		return ExitOK // deferred guard restarts the unit
	}

	// L4 profile / hotkey / drop-in
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

	if err := service.DaemonReload(); err != nil {
		opts.errf("[失败] daemon-reload：%v", err)
		return ExitExecFail
	}
	// the deferred guard performs the start and reports its failure
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
		// The API is queried only when there is no usable pin. Retrying it on
		// every run would make a blocked api.github.com (normal in CN) warn on
		// every invocation (评审 新增#4); `ompinyin update` is the documented way
		// to re-resolve.
		tag := prev.Tag
		if !isStableTag(tag) {
			opts.outf("[计划] L2 解析最新 stable release tag…")
			if resolved := assets.ResolveStableTag(ctx); resolved != "" {
				tag = resolved
			} else {
				opts.errf("[提示] L2 无法解析 stable tag（API 不可达？），本次沿用 releases/latest；该 repo 上它指向滚动 nightly，API 可达时跑 ompinyin update 可重新 pin")
				tag = ""
			}
		}
		if tag != "" {
			ri = catalog.RimeIceTagged(tag)
		}
	}
	hintTag := ri.Tag
	if hintTag == "" {
		hintTag = prev.Tag
	}
	zipPath, zipSha, zipTag, err := mgr.Fetch(ctx, ri, hintTag, prev.SHA256)
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
			opts.outf("[完成] L2 万象 LMDG 模型就位（sha256 %s…）", trunc12(gramSha))
		} else {
			opts.outf("[跳过] L2 万象 LMDG 模型已是同一 sha256（%s…）", trunc12(gramSha))
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

// trunc12 shortens a sha256 for display without panicking on an empty/short
// value (a failed HashFile returns "").
func trunc12(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
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
		return false, fmt.Errorf("model checksum mismatch after placement (%s… != %s…)", trunc12(got), trunc12(sha))
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
// backup (§8)
// ---------------------------------------------------------------------------

// obackup snapshots files that convergence may touch. Returns "" when nothing
// needed backing up: a run with no mutating work (p.NeedsApply()==false) must
// not create a backup directory at all (评审 P0-9 tail).
func obackup(opts Options, d catalog.Desired, cur *observe.Current, st *state.State, p *plan.Plan) (string, error) {
	var targets []string
	if opts.FullBackup {
		targets = append(targets, cur.RimeDir, observe.FcitxConfigDir(), tray.ShellJSONPath(state.Home()))
	} else if p.NeedsApply() {
		for rel, status := range cur.Managed {
			if status == patches.StatusUserModified || status == patches.StatusForeign {
				targets = append(targets, filepath.Join(cur.RimeDir, rel))
			}
		}
		for _, f := range []string{observe.ProfilePath(), observe.ConfigPath(), tray.ShellJSONPath(state.Home()), cur.DropInPath} {
			if _, err := os.Stat(f); err == nil {
				targets = append(targets, f)
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
			return copyFile(path, dd)
		})
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return copyFile(src, dest)
}

// copyFile streams src to dest (never read the whole file into memory: a -b
// full backup includes the 420MB model).
func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
