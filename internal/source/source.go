// Package source manages the system pacman repo mirror list for the "core+extra
// 走中国源" convenience. It is deliberately separate from the IM convergence:
// writing /etc/pacman.d/mirrorlist is a system-level, root-owned config that
// ompinyin does NOT touch during install/update/switch — this is an opt-in
// helper the user runs once (e.g. from mainland China) so pacman -Sy / -S hit
// domestic stock-Arch mirrors instead of the Cloudflare-fronted omarchy.org.
//
// Safety: the existing mirrorlist is backed up before any write, a dry-run
// prints the target without touching anything, and the preset lines keep
// stable-mirror.omarchy.org as a fallback so a China-host can still reach the
// official mirror if the domestic one flakes. We warn that a full `pacman -Syu`
// would drift omarchy's version-pinned snapshot.
package source

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// MirrorlistPath is the system pacman mirror list this helper manages.
const MirrorlistPath = "/etc/pacman.d/mirrorlist"

// Preset names a pacman mirror policy.
type Preset string

const (
	// PresetCN keeps core/extra/multilib on China stock-Arch mirrors first
	// (fast/reliable for mainland), with the official Omarchy mirror as fallback.
	PresetCN Preset = "cn"
	// PresetUpstream restores the single official Omarchy mirror (default).
	PresetUpstream Preset = "upstream"
)

// ParsePreset maps a CLI value onto a Preset.
func ParsePreset(s string) (Preset, bool) {
	switch Preset(s) {
	case PresetCN, PresetUpstream:
		return Preset(s), true
	}
	return "", false
}

// Exec seams (hermetic T0 tests stub these).
var (
	// ReadFile reads the current mirrorlist (world-readable, no sudo needed).
	ReadFile = func(p string) ([]byte, error) { return os.ReadFile(p) }
	// RunSudo executes a command as root with the caller's tty wired (sudo
	// password prompt visible) — used for backup/cp/write into /etc/pacman.d.
	// Without a controlling terminal (agent/CI) it uses sudo -n so it fails fast.
	RunSudo = func(args ...string) error {
		if !sudoTTY() {
			args = append([]string{"-n"}, args...)
		}
		c := exec.Command("sudo", args...)
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	}
	// Now is injectable for deterministic backup timestamps in tests.
	Now = time.Now
)

// sudoTTY mirrors the pkgs heuristic: only prompt for a sudo password when a
// controlling terminal exists; otherwise -n fails fast for headless agents.
func sudoTTY() bool {
	f, err := os.Open("/dev/tty")
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// lines returns the Server lines for a preset, in priority order.
func lines(p Preset) []string {
	switch p {
	case PresetUpstream:
		return []string{"Server = https://stable-mirror.omarchy.org/$repo/os/$arch"}
	case PresetCN:
		return []string{
			"Server = https://mirrors.aliyun.com/archlinux/$repo/os/$arch",
			"Server = https://mirrors.tuna.tsinghua.edu.cn/archlinux/$repo/os/$arch",
			"Server = https://stable-mirror.omarchy.org/$repo/os/$arch",
		}
	}
	return nil
}

// Content returns the full mirrorlist text for a preset.
func Content(p Preset) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# managed by ompinyin (pacman mirror; preset=%s) — hand edits will be overwritten\n", p)
	for _, l := range lines(p) {
		b.WriteString(l + "\n")
	}
	return b.String()
}

// servers extracts the existing `Server = ...` URLs from a mirrorlist in order.
func servers(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "Server") {
			continue
		}
		_, v, ok := strings.Cut(strings.TrimSpace(strings.TrimPrefix(t, "Server")), "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if strings.HasPrefix(v, "http") {
			out = append(out, v)
		}
	}
	return out
}

// isChina reports whether the first listed server is a domestic stock-Arch mirror.
func isChina(s []string) bool {
	if len(s) == 0 {
		return false
	}
	first := s[0]
	return strings.Contains(first, "aliyun") || strings.Contains(first, "tuna") ||
		strings.Contains(first, "ustc") || strings.Contains(first, "sjtug") || strings.Contains(first, "nju")
}

// isUpstreamOnly reports whether the list is exactly the single official mirror.
func isUpstreamOnly(s []string) bool {
	return len(s) == 1 && strings.Contains(s[0], "stable-mirror.omarchy.org")
}

// matched reports whether the current list already conforms to a preset.
func matched(text string, p Preset) bool {
	s := servers(text)
	switch p {
	case PresetCN:
		return isChina(s)
	case PresetUpstream:
		return isUpstreamOnly(s)
	}
	return false
}

// describe renders the current servers for the status line.
func describe(s []string) string {
	if len(s) == 0 {
		return "（无 Server 行 / 未配置）"
	}
	return strings.Join(s, "  ·  ")
}

// Result reports what Ensure did.
type Result struct {
	Preset   Preset
	Skipped  bool   // already matched, nothing written
	Changed  bool   // mirrorlist rewritten
	BackedUp string // path of the timestamped backup, "" if none was needed
}

// EnsureArgs carries the command inputs.
type EnsureArgs struct {
	Preset Preset
	DryRun bool
	Yes    bool
	Stdout io.Writer
	Stdin  io.Reader
}

// Ensure reads the current mirrorlist, then (unless already matched or under a
// dry-run) backs it up and writes the target preset via sudo.
func Ensure(args EnsureArgs) (Result, error) {
	res := Result{Preset: args.Preset}
	cur, err := ReadFile(MirrorlistPath)
	if err != nil {
		// A mirrorlist is expected to exist; treat a genuine read problem as fatal
		// rather than silently clobbering it.
		return res, fmt.Errorf("读取 %s 失败：%w", MirrorlistPath, err)
	}
	curSrv := servers(string(cur))
	fmt.Fprintf(args.Stdout, "当前 %s：%s\n", MirrorlistPath, describe(curSrv))
	fmt.Fprintf(args.Stdout, "目标 preset：%s\n", args.Preset)

	target := Content(args.Preset)
	if args.DryRun {
		fmt.Fprintf(args.Stdout, "（--dry-run，未写入）即将写为：\n%s\n", target)
		return res, nil
	}

	if matched(string(cur), args.Preset) {
		res.Skipped = true
		fmt.Fprintf(args.Stdout, "[跳过] 已是 %s 预设，无需改动。\n", args.Preset)
		return res, nil
	}

	if !args.Yes {
		fmt.Fprintf(args.Stdout, "将写入 %s 为 %s 预设（先备份）。继续？[y/N] ", MirrorlistPath, args.Preset)
		line, _ := bufio.NewReader(args.Stdin).ReadString('\n')
		ans := strings.ToLower(strings.TrimSpace(line))
		if ans != "y" && ans != "yes" {
			return res, fmt.Errorf("已取消")
		}
	}

	// backup the existing list (skip when it's empty/absent) into /etc/pacman.d/
	if strings.TrimSpace(string(cur)) != "" {
		bak := fmt.Sprintf("%s.bak-%d", MirrorlistPath, Now().Unix())
		if err := RunSudo("cp", "--", MirrorlistPath, bak); err != nil {
			return res, fmt.Errorf("备份 %s 失败：%w", MirrorlistPath, err)
		}
		res.BackedUp = bak
		fmt.Fprintf(args.Stdout, "已备份到 %s\n", bak)
	}

	// write via a temp file + sudo cp (avoids the tee/stdin password clash)
	tmp, err := os.CreateTemp("", "ompinyin-mirrorlist-*")
	if err != nil {
		return res, fmt.Errorf("创建临时文件失败：%w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(target); err != nil {
		tmp.Close()
		return res, fmt.Errorf("写入临时文件失败：%w", err)
	}
	if err := tmp.Close(); err != nil {
		return res, fmt.Errorf("关闭临时文件失败：%w", err)
	}
	if err := RunSudo("cp", "--", tmpPath, MirrorlistPath); err != nil {
		return res, fmt.Errorf("写入 %s 失败：%w", MirrorlistPath, err)
	}

	res.Changed = true
	pruneOldBackups(MirrorlistPath)
	fmt.Fprintf(args.Stdout, "[完成] 已写入 %s（%s 预设）。\n", MirrorlistPath, args.Preset)
	fmt.Fprintf(args.Stdout, "提示：仅用 `pacman -Sy` / `pacman -S --needed`；勿跑全量 `pacman -Syu`（会漂移 Omarchy stable 锁定版本）。\n")
	return res, nil
}

// pruneOldBackups keeps at most keep newest mirrorlist.bak-* files in the dir,
// removing the rest as root. Best-effort: failures are ignored (a stale backup
// is harmless). GNU xargs (Arch) supports -r.
func pruneOldBackups(path string) {
	dir := path[:strings.LastIndex(path, "/")]
	cmd := fmt.Sprintf("ls -1t %q 2>/dev/null | tail -n +6 | xargs -r rm --",
		dir+"/mirrorlist.bak-*")
	_ = RunSudo("bash", "-c", cmd)
}
