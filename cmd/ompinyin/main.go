// Command ompinyin is the end-state convergence CLI for the Chinese input
// method stack on Omarchy (§7): 雾凇全拼 + 万象 LMDG + 顶栏输入法图标,
// double pinyin optional via --dsp.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ProjectAILeap/ompinyin/internal/catalog"
	"github.com/ProjectAILeap/ompinyin/internal/converge"
	"github.com/ProjectAILeap/ompinyin/internal/source"
	"github.com/ProjectAILeap/ompinyin/internal/state"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// watchSignals cancels the run context on SIGINT/SIGTERM. That matters for two
// things: a 420MB download aborts promptly, and an interrupt inside the fcitx5
// stop window still runs the deferred start (a raw SIGKILL-style exit would
// leave the user with no input method). A second signal hard-exits 130 for the
// user who really wants out.
func watchSignals() (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-ch:
			cancel()
		case <-ctx.Done():
			return
		}
		select {
		case <-ch:
			os.Exit(130)
		case <-ctx.Done():
		}
	}()
	return ctx, func() { signal.Stop(ch); cancel() }
}

func run(args []string) int {
	ctx, stopSignals := watchSignals()
	defer stopSignals()
	currentCtx = ctx

	if len(args) == 0 {
		usage()
		return converge.ExitUsage
	}
	cmd := args[0]
	switch cmd {
	case "help", "-h", "--help":
		usage()
		return converge.ExitOK
	case "version", "--version", "-v":
		fmt.Printf("ompinyin %s\n", catalog.Version)
		return converge.ExitOK
	}
	// Every command resolves paths under $HOME; check it once at the boundary
	// instead of letting a getter terminate the process (评审 新增#3).
	if err := state.CheckHome(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return converge.ExitPrecheck
	}
	switch cmd {
	case "install":
		return cmdInstall(args[1:])
	case "update":
		return cmdUpdate(args[1:])
	case "switch":
		return cmdSwitch(args[1:])
	case "status":
		return cmdStatus(args[1:])
	case "doctor":
		return cmdDoctor(args[1:])
	case "clean":
		return cmdClean(args[1:])
	case "uninstall":
		return cmdUninstall(args[1:])
	case "source":
		return cmdSource(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		usage()
		return converge.ExitUsage
	}
}

func newOpts() converge.Options {
	// MirrorSource must default to the documented policy (`cn`): switch/update
	// build their Options here too, and an empty value silently meant "auto"
	// = GitHub-first, which stalls on the 420MB model for mainland users
	// (评审 P0-10).
	return converge.Options{
		Stdout:       os.Stdout,
		Stderr:       os.Stderr,
		Stdin:        os.Stdin,
		MirrorSource: catalog.DefaultMirrorSource(),
		Command:      "install",
		Context:      currentCtx,
	}
}

// currentCtx is the signal-cancellable context installed by run().
var currentCtx = context.Background()

// mirrorOpts interprets --mirror: a named preset selects a download policy, a
// local directory (or file:// URL) enables offline installs, any other URL
// overrides the asset mirror, and an empty value picks the default.
func mirrorOpts(v string) (source catalog.MirrorSource, override, localDir string) {
	if v == "" {
		return catalog.DefaultMirrorSource(), "", ""
	}
	if s, ok := catalog.ParseMirrorSource(v); ok {
		return s, "", ""
	}
	if dir, ok := localAssetDir(v); ok {
		return catalog.DefaultMirrorSource(), "", dir
	}
	return catalog.DefaultMirrorSource(), v, ""
}

// localAssetDir resolves a --mirror value that points at a directory on disk:
// either a plain path or a file:// URL. This is the offline entry point that
// replaces the "python3 -m http.server" workaround in the test notes.
func localAssetDir(v string) (string, bool) {
	p := v
	if u, err := url.Parse(v); err == nil && u.Scheme == "file" {
		p = u.Path
	}
	if strings.HasPrefix(p, "~") {
		p = state.Home() + p[1:]
	}
	if p == "" || strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
		return "", false
	}
	info, err := os.Stat(p)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return p, true
}

func cmdInstall(argv []string) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	var (
		dspFlags   = fs.String("dsp", "", "double pinyin layout id: "+strings.Join(catalog.DoublePinyinIDs(), "|")+" (repeatable usage is an error)")
		dspDefault = fs.Bool("dsp-default", false, "make the --dsp layout the default schema (requires --dsp)")
		noQuanpin  = fs.Bool("no-quanpin", false, "drop quanpin from schema_list (requires --dsp)")
		noModel    = fs.Bool("no-model", false, "skip the wanxiang LMDG model")
		noModelS   = fs.Bool("s", false, "shorthand for --no-model")
		model      = fs.Bool("model", false, "re-enable the wanxiang LMDG model recorded in state.json")
		channel    = fs.String("channel", "stable", "asset channel: stable|nightly")
		yes        = fs.Bool("yes", false, "assume yes, non-interactive")
		yesS       = fs.Bool("y", false, "shorthand for --yes")
		dryRun     = fs.Bool("dry-run", false, "print the plan without applying changes")
		mirror     = fs.String("mirror", "", "download source: auto|cn|ghproxy|upstream|<URL>")
		fullBackup = fs.Bool("full-backup", false, "back up the full rime + fcitx5 config dirs")
		fullBakS   = fs.Bool("b", false, "shorthand for --full-backup")
		osOverride = fs.String("os-override", "", "bypass the ID=omarchy precheck (testing, e.g. containers)")
		jsonOut    = fs.Bool("json", false, "machine-readable output (with --dry-run: JSON plan)")
	)
	if err := fs.Parse(argv); err != nil {
		return converge.ExitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected arguments: %v\n", fs.Args())
		return converge.ExitUsage
	}

	// §2.2 flag rules: --dsp repeatable is an error; --dsp-default / --no-quanpin
	// require --dsp.
	dspCount := 0
	for _, a := range argv {
		if a == "--dsp" || a == "-dsp" || strings.HasPrefix(a, "--dsp=") || strings.HasPrefix(a, "-dsp=") {
			dspCount++
		}
	}
	if dspCount > 1 {
		fmt.Fprintln(os.Stderr, "--dsp 最多一个双拼（可重复视为错误，避免 F4 拥挤）")
		return converge.ExitUsage
	}
	if (*dspDefault || *noQuanpin) && *dspFlags == "" {
		fmt.Fprintln(os.Stderr, "--dsp-default / --no-quanpin 必须伴随 --dsp")
		return converge.ExitUsage
	}
	if *model && (*noModel || *noModelS) {
		fmt.Fprintln(os.Stderr, "--model 与 --no-model/-s 互斥")
		return converge.ExitUsage
	}
	if *jsonOut && !*dryRun {
		fmt.Fprintln(os.Stderr, "--json 仅与 --dry-run 连用（输出机器可读计划）")
		return converge.ExitUsage
	}

	// Terminal state baseline = the last persisted Desired, NOT the zero-flag
	// default. §3 says flags OVERRIDE state.json; rebuilding from
	// DefaultDesired() made a bare `ompinyin install` silently drop a chosen
	// --dsp and reset --no-model/--channel (评审 P0-11). Only flags that were
	// actually given on this command line overwrite the baseline.
	st, stErr := state.Load()
	base := catalog.DefaultDesired()
	if stErr == nil && !st.Desired.IsZero() {
		base = st.Desired
	}
	touched := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { touched[f.Name] = true })
	d := applyInstallFlags(base, installFlags{
		DSP: *dspFlags, Channel: *channel,
		DSPDefault: *dspDefault, NoQuanpin: *noQuanpin,
		NoModel: *noModel || *noModelS, Model: *model,
	}, touched)

	opts := newOpts()
	opts.Yes = *yes || *yesS
	opts.DryRun = *dryRun
	opts.MirrorSource, opts.Mirror, opts.LocalDir = mirrorOpts(*mirror)
	opts.FullBackup = *fullBackup || *fullBakS
	opts.OSOverride = *osOverride
	opts.JSON = *jsonOut

	if stErr == nil && !st.Desired.IsZero() {
		// --json 时 stdout 必须保持纯 JSON（AGENTS.md §JSON 契约）；这条基线说明
		// 属于诊断信息，应走 stderr，否则 `install --dry-run --json` 的 stdout 会
		// 混入人类文本，破坏机器可读输出（真机测试抓到的 bug）。
		dst := os.Stdout
		if opts.JSON {
			dst = os.Stderr
		}
		fmt.Fprintf(dst, "[基线] 终态继承自 state.json：primary=%s extra=%v model=%v channel=%s（未指定的选项不重置）\n",
			st.Desired.Primary, st.Desired.Extra, st.Desired.Model, st.Desired.Channel)
	}

	return converge.Install(d, false, opts)
}

func cmdUpdate(argv []string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	var (
		yes        = fs.Bool("yes", false, "assume yes")
		yesS       = fs.Bool("y", false, "shorthand for --yes")
		dryRun     = fs.Bool("dry-run", false, "print the plan without applying changes")
		mirror     = fs.String("mirror", "", "download source: auto|cn|ghproxy|upstream|<URL>")
		osOverride = fs.String("os-override", "", "override os-release ID check (testing)")
		jsonOut    = fs.Bool("json", false, "machine-readable output (with --dry-run: JSON plan)")
		selfUp     = fs.Bool("self", false, "also upgrade the ompinyin binary itself")
	)
	if err := fs.Parse(argv); err != nil {
		return converge.ExitUsage
	}
	if *jsonOut && !*dryRun {
		fmt.Fprintln(os.Stderr, "--json 仅与 --dry-run 连用（输出机器可读计划）")
		return converge.ExitUsage
	}
	opts := newOpts()
	opts.Yes = *yes || *yesS
	opts.DryRun = *dryRun
	opts.JSON = *jsonOut
	opts.Command = "update"
	opts.Self = *selfUp
	opts.MirrorSource, opts.Mirror, opts.LocalDir = mirrorOpts(*mirror)
	opts.OSOverride = *osOverride
	return converge.Update(opts)
}

func cmdSwitch(argv []string) int {
	fs := flag.NewFlagSet("switch", flag.ContinueOnError)
	var (
		dsp        = fs.String("dsp", "", "double pinyin layout id or 'none'")
		dspDefault = fs.Bool("dsp-default", false, "make the --dsp layout the default schema")
		noQuanpin  = fs.Bool("no-quanpin", false, "drop quanpin from schema_list (requires --dsp)")
		full       = fs.Bool("full", false, "quanpin back to schema_list[0] (installed double pinyin stays in Extra)")
		yes        = fs.Bool("yes", false, "assume yes")
		yesS       = fs.Bool("y", false, "shorthand for --yes")
		dryRun     = fs.Bool("dry-run", false, "print the plan without applying changes")
		mirror     = fs.String("mirror", "", "download source: auto|cn|ghproxy|upstream|<URL>")
		fullBackup = fs.Bool("full-backup", false, "back up the full rime + fcitx5 config dirs")
		fullBakS   = fs.Bool("b", false, "shorthand for --full-backup")
		osOverride = fs.String("os-override", "", "bypass the ID=omarchy precheck (testing)")
		jsonOut    = fs.Bool("json", false, "machine-readable output (with --dry-run: JSON plan)")
	)
	if err := fs.Parse(argv); err != nil {
		return converge.ExitUsage
	}
	if (*dspDefault || *noQuanpin) && *dsp == "" && !*full {
		fmt.Fprintln(os.Stderr, "--dsp-default / --no-quanpin 必须伴随 --dsp（switch --full 除外）")
		return converge.ExitUsage
	}
	if *jsonOut && !*dryRun {
		fmt.Fprintln(os.Stderr, "--json 仅与 --dry-run 连用（输出机器可读计划）")
		return converge.ExitUsage
	}
	opts := newOpts()
	opts.Yes = *yes || *yesS
	opts.DryRun = *dryRun
	opts.JSON = *jsonOut
	opts.Command = "switch"
	opts.MirrorSource, opts.Mirror, opts.LocalDir = mirrorOpts(*mirror)
	opts.FullBackup = *fullBackup || *fullBakS
	return converge.Switch(converge.SwitchArgs{
		DSP: *dsp, DSPDefault: *dspDefault, NoQuanpin: *noQuanpin,
		Full: *full, OSOverride: *osOverride, Yes: *yes || *yesS, DryRun: *dryRun,
		JSON: *jsonOut,
	}, opts)
}

// installFlags are the terminal-state flags of `install`, extracted from the
// flag set so the override logic stays testable without running a command.
type installFlags struct {
	DSP        string
	Channel    string
	DSPDefault bool
	NoQuanpin  bool
	NoModel    bool
	Model      bool
}

// applyInstallFlags overlays the EXPLICITLY GIVEN flags on the persisted
// baseline terminal state (§3). `touched` is the set of flag names the user
// actually passed (flag.FlagSet.Visit), which is what makes a bare
// `ompinyin install` stop resetting --dsp / --no-model / --channel (评审 P0-11).
func applyInstallFlags(base catalog.Desired, f installFlags, touched map[string]bool) catalog.Desired {
	d := base
	d.Extra = append([]string{}, base.Extra...)
	switch {
	case touched["model"]:
		d.Model = true
	case touched["no-model"] || touched["s"]:
		d.Model = false
	}
	if touched["channel"] {
		d.Channel = f.Channel
	}
	if !touched["dsp"] {
		return d
	}
	switch {
	case f.DSP == "none":
		// symmetric with `switch --dsp none`: back to full pinyin only
		d.Primary = "quanpin"
		d.Extra = nil
	case f.NoQuanpin:
		d.Primary = f.DSP
		d.Extra = nil
	case f.DSPDefault:
		d.Primary = f.DSP
		d.Extra = []string{"quanpin"}
	default:
		d.Primary = "quanpin"
		d.Extra = []string{f.DSP}
	}
	return d
}

func cmdStatus(argv []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "machine-readable JSON output")
	if err := fs.Parse(argv); err != nil {
		return converge.ExitUsage
	}
	opts := newOpts()
	opts.JSON = *jsonOut
	return converge.Status(opts)
}

func cmdDoctor(argv []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "machine-readable JSON output")
	if err := fs.Parse(argv); err != nil {
		return converge.ExitUsage
	}
	opts := newOpts()
	opts.JSON = *jsonOut
	return converge.Doctor(opts)
}

func cmdClean(argv []string) int {
	fs := flag.NewFlagSet("clean", flag.ContinueOnError)
	var (
		legacy = fs.Bool("legacy", false, "also remove the legacy ~/.config/fcitx/rime duplicate")
		yes    = fs.Bool("yes", false, "assume yes")
		yesS   = fs.Bool("y", false, "shorthand for --yes")
	)
	if err := fs.Parse(argv); err != nil {
		return converge.ExitUsage
	}
	opts := newOpts()
	opts.Yes = *yes || *yesS
	return converge.Clean(converge.CleanArgs{Legacy: *legacy, Yes: *yes || *yesS}, opts)
}

func cmdUninstall(argv []string) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	var (
		yes  = fs.Bool("yes", false, "assume yes")
		yesS = fs.Bool("y", false, "shorthand for --yes")
	)
	if err := fs.Parse(argv); err != nil {
		return converge.ExitUsage
	}
	opts := newOpts()
	opts.Yes = *yes || *yesS
	return converge.Uninstall(opts)
}

func cmdSource(argv []string) int {
	fs := flag.NewFlagSet("source", flag.ContinueOnError)
	var (
		preset = fs.String("preset", "cn", "pacman repo mirror policy: cn (China-first + Omarchy fallback) | upstream (Omarchy official only)")
		dryRun = fs.Bool("dry-run", false, "print the target mirrorlist without writing")
		yes    = fs.Bool("yes", false, "assume yes, non-interactive")
		yesS   = fs.Bool("y", false, "shorthand for --yes")
	)
	if err := fs.Parse(argv); err != nil {
		return converge.ExitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected arguments: %v\n", fs.Args())
		return converge.ExitUsage
	}
	p, ok := source.ParsePreset(*preset)
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown --preset %q (use cn|upstream)\n", *preset)
		return converge.ExitUsage
	}
	if _, err := source.Ensure(source.EnsureArgs{
		Preset: p, DryRun: *dryRun, Yes: *yes || *yesS,
		Stdout: os.Stdout, Stdin: os.Stdin,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "[失败] %v\n", err)
		return converge.ExitExecFail
	}
	return converge.ExitOK
}

func usage() {
	fmt.Print(`ompinyin — Omarchy 中文输入法一键部署（声明式终态收敛）

Usage:
  ompinyin install [--dsp ID|none] [--dsp-default] [--no-quanpin]
                [--model | -s|--no-model] [--channel stable|nightly] [-y|--yes] [--dry-run]
                [--mirror auto|cn|ghproxy|upstream|URL] [-b|--full-backup]
                [--os-override omarchy] [--dry-run --json]
  ompinyin update                            # L2 资产刷新到最新并重编译（--dry-run --json 可预览；--self 一并自升级）
  ompinyin switch --dsp ID [--dsp-default]   # 改/加双拼（--mirror/-b 可用，--dry-run --json 可预览）
  ompinyin switch --dsp none                 # 去掉双拼，回到仅全拼
  ompinyin switch --full                     # 全拼改回 schema_list[0]
  ompinyin status                            # 现状 vs 终态 diff
  ompinyin doctor                            # 服务 / IM 三态 / 红线 / 触发键 / 顶栏图标
  ompinyin clean [--legacy]                  # 清缓存 / 老路径 ~/.config/fcitx/rime
  ompinyin uninstall                         # 受管文件删除 + profile 移除 + 托盘还原
  ompinyin source [--preset cn|upstream]     # 配置 pacman 仓库镜像（默认 cn：core+extra 走中国源）
                [--dry-run] [-y|--yes]

双拼 ID (` + "`--dsp`" + `): ` + strings.Join(catalog.DoublePinyinIDs(), "|") + `

退出码: 0 成功 / 1 执行失败 / 2 用法错误 / 3 预检失败

终态继承：install 以 state.json 里的上次终态为基线，**只有命令行上显式给出的选项**
会覆盖它——裸跑 ` + "`ompinyin install`" + ` 不会静默掉你之前选的 --dsp / --no-model /
--channel。去掉双拼用 ` + "`ompinyin switch --dsp none`" + `（或 install --dsp none）。

顶栏输入法图标是必做终态：无 --tray-pin / --no-tray 选项，装上就有。
切方案用 F4；触发键（Alt+Space）切中英。
`)
}
