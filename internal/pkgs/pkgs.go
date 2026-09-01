// Package pkgs implements L1: system package convergence via pacman.
package pkgs

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Needed lists the required packages (§3 L1) — the MINIMAL set whose
// dependency closure supplies everything fcitx5 needs on Omarchy:
//
//	fcitx5-rime       → fcitx5 + librime (librime bundles the octagram plugin
//	                     and transitively pulls opencc/marisa/yaml-cpp/…)
//	fcitx5-configtool → fcitx5 + fcitx5-qt (the Qt IM module Omarchy sets via
//	                     QT_IM_MODULE=fcitx)
//	fcitx5-gtk        → GTK IM module; Required By: None, so nothing else would
//	                     restore it — it must be named explicitly. (The Omarchy
//	                     ISO ships it, but a host that loses it never gets it
//	                     back from rime/configtool.)
//
// `fcitx5` and `opencc` are deliberately NOT declared: both arrive via the
// closure above (fcitx5 is a dependency of fcitx5-rime and fcitx5-configtool,
// opencc is a dependency of librime), so naming them is redundant. `octagram`
// is not a pacman package at all — it is the librime plugin librime-octagram.so
// shipped inside /usr/lib/rime-plugins/.
var Needed = []string{"fcitx5-rime", "fcitx5-configtool", "fcitx5-gtk"}

// Run is the exec seam for tests (fake pacman in T0).
var Run = func(name string, args ...string) error {
	c := exec.Command(name, args...)
	return c.Run()
}

// runInteractive executes with the caller's terminal wired in (sudo password
// prompt, pacman progress)。
var runInteractive = func(name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// hasTTY reports whether the process has a controlling terminal that sudo can
// prompt on. Without one (CI, agent, pipe), sudo must use -n so it fails fast
// with "a password is required" instead of hanging on a prompt nobody answers.
var hasTTY = func() bool {
	f, err := os.Open("/dev/tty")
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// sudoArgv builds the sudo argv for a non-root command, inserting -n when there
// is no controlling terminal (agent/CI). runInteractive keeps stdout/stderr
// wired so pacman progress stays visible.
func sudoArgv(cmds ...string) []string {
	if hasTTY() {
		return append([]string{"sudo"}, cmds...)
	}
	return append([]string{"sudo", "-n"}, cmds...)
}

// sudoInvoke runs a non-root command via sudo, choosing -n when there is no tty.
func sudoInvoke(cmds ...string) error {
	argv := sudoArgv(cmds...)
	return runInteractive(argv[0], argv[1:]...)
}

// Installed returns the subset of names that are already installed.
// Semantics: `pacman -Qq <names...>` exits 0 IFF every queried package is
// installed — the exit status IS the answer, no output parsing needed
// (评审 P3: the old combinedOutput hack echoed the args back as pseudo-output).
func Installed(names ...string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(names) == 0 {
		return out, nil
	}
	if Run("pacman", append([]string{"-Qq"}, names...)...) == nil {
		for _, n := range names {
			out[n] = true
		}
		return out, nil
	}
	// some or all missing; fall back to per-package queries
	for _, n := range names {
		if Run("pacman", "-Qq", n) == nil {
			out[n] = true
		}
	}
	return out, nil
}

// syncDBPaths are the pacman sync databases the L1 closure actually needs.
// Verified (pacman -Si): fcitx5-rime/configtool/gtk + fcitx5-qt/librime/
// libxcb/wayland are in extra; glibc/gcc-libs/openssl/zlib… are in core.
// multilib and the [omarchy] custom repo are NOT part of the L1 closure, so
// a failed refresh of those (e.g. the Cloudflare-fronted omarchy.org wobbling
// from China) must NOT block L1 — only core.db + extra.db matter.
var syncDBPaths = []string{
	"/var/lib/pacman/sync/core.db",
	"/var/lib/pacman/sync/extra.db",
}

// missingSyncDBs returns the sync databases the L1 closure needs that are
// absent or empty (pacman writes each repo DB atomically, so a repo that failed
// to sync stays absent/old instead of half-written).
func missingSyncDBs() []string {
	var miss []string
	for _, p := range syncDBPaths {
		if st, err := os.Stat(p); err != nil || st.Size() == 0 {
			miss = append(miss, p)
		}
	}
	return miss
}

// ensureSyncDB freshens the databases the L1 closure needs (core+extra) on a
// fresh install (e.g. the Omarchy ISO ships without them, which makes -S fail
// with "target not found"). A `pacman -Sy` that exits non-zero because an
// *unrelated* repo (multilib/[omarchy]) failed must not be fatal: we re-check
// only the needed DBs and fail when core.db or extra.db is still missing.
func ensureSyncDB() error {
	if len(missingSyncDBs()) == 0 {
		return nil
	}
	// best-effort refresh; the exit code alone is not authoritative, re-check below.
	_ = sudoInvoke("pacman", "-Sy", "--noconfirm")
	if miss := missingSyncDBs(); len(miss) > 0 {
		return fmt.Errorf("pacman sync DBs still missing (%s): fix your pacman mirrors then `sudo pacman -Sy`", strings.Join(miss, ", "))
	}
	return nil
}

// Install runs `sudo pacman -S --needed [--noconfirm] <pkgs>`.
func Install(names []string, yes bool) error {
	if len(names) == 0 {
		return nil
	}
	if err := ensureSyncDB(); err != nil {
		return err
	}
	args := []string{"-S", "--needed"}
	if yes {
		args = append(args, "--noconfirm")
	}
	args = append(args, names...)
	cmdArgs := append([]string{"pacman"}, args...)
	// 透传终端：sudo 需要提示输密码，pacman 输出需可见（§7 步骤化输出/出错可见性）。
	// 无控制终端（agent/CI）时 sudo -n 快速失败，避免死在无人应答的密码提示上。
	if err := sudoInvoke(cmdArgs...); err != nil {
		suffix := ""
		if !hasTTY() {
			suffix = "（无交互终端：请为 pacman 配置 NOPASSWD sudoers，或在有 tty 的终端重跑）"
		}
		return fmt.Errorf("pacman install failed (%v): run `sudo pacman -S --needed %s` manually%s", err, strings.Join(names, " "), suffix)
	}
	return nil
}

// Missing returns the names not yet installed.
func Missing(names ...string) ([]string, error) {
	inst, err := Installed(names...)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, n := range names {
		if !inst[n] {
			missing = append(missing, n)
		}
	}
	return missing, nil
}
