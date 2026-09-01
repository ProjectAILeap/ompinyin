// Package facts implements the pre-check layer: host identity, octagram
// plugin, disk capacity, tool availability and herdr prefix detection.
package facts

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// MinFreeBytes is the disk pre-check threshold (§8): model 420MB × data +
// cache copy, rounded up.
const MinFreeBytes = 2 << 30 // 2 GiB

// OS holds derived identity facts about the target host.
type OS struct {
	ID      string // e.g. "omarchy", "arch", "fedora"
	BuildID string // e.g. "4.0.1"
}

// Run is the exec seam (fake pacman in T0 tests).
var Run = defaultRun

// LookPath is the exec.LookPath seam.
var LookPath = defaultLookPath

// Geteuid is the privilege seam for the root guard (§16 red line 11).
var Geteuid = os.Geteuid

// RequiredTools are the binaries the convergence shells out to (§1.1).
// Pkg is the package that provides it: when the package is absent the tool
// may legitimately be missing pre-L1 (warning), when it is installed and the
// binary still is not, the host is broken (failure).
var RequiredTools = []struct {
	Tool string
	Pkg  string
}{
	{"fcitx5-remote", "fcitx5"},
	{"rime_deployer", "librime"},
	{"omarchy", ""}, // not a pacman package; must always be present on Omarchy
}

// OSReleasePath is injectable for hermetic tests.
var OSReleasePath = "/etc/os-release"

// ReadOS reads key=value identifiers from the os-release file.
func ReadOS() (OS, error) { return readOSFrom(OSReleasePath) }

func readOSFrom(path string) (OS, error) {
	var o OS
	f, err := os.Open(path)
	if err != nil {
		return o, err
	}
	defer f.Close()

	vals := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		vals[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"`)
	}
	o.ID = vals["ID"]
	o.BuildID = vals["BUILD_ID"]
	return o, nil
}

// IsOmarchy reports whether ID is the Omarchy distribution.
func IsOmarchy(id string) bool { return id == "omarchy" }

// OctagramPresent reports whether the octagram librime plugin is available.
// It checks known plugin paths and falls back to assuming the bundled librime
// package provides it.
func OctagramPresent() bool {
	candidates := []string{
		"/usr/lib/rime-plugins/octagram/octagram.so",
		"/usr/lib/rime-plugins/octagram.so",
		"/usr/lib/librime-plugin-octagram.so",
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	matches, _ := filepath.Glob("/usr/lib/rime-plugins/*octagram*")
	if len(matches) > 0 {
		return true
	}
	// octagram ships bundled with the librime package on Arch/Omarchy.
	return PkgInstalled("librime")
}

// PkgInstalled reports whether a pacman package is installed.
func PkgInstalled(name string) bool {
	err := Run("pacman", "-Qq", name)
	return err == nil
}

// ToolInPath reports whether name resolves in PATH.
func ToolInPath(name string) bool {
	_, err := LookPath(name)
	return err == nil
}

// DiskFreeBytes reports free bytes available to non-root on dir.
func DiskFreeBytes(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

// HerdrPrefixFound scans the user config tree for herdr hotkey configuration.
// Informational only: trigger keys are written unconditionally (§6.2).
func HerdrPrefixFound(home string) bool {
	for _, dir := range []string{
		filepath.Join(home, ".config", "hypr"),
		filepath.Join(home, ".config", "omarchy"),
	} {
		found := scanForHerdr(dir, 0)
		if found {
			return true
		}
	}
	return false
}

func scanForHerdr(dir string, depth int) bool {
	if depth > 3 {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if scanForHerdr(p, depth+1) {
				return true
			}
			continue
		}
		if !strings.Contains(strings.ToLower(e.Name()), "herdr") {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if strings.Contains(strings.ToLower(string(data)), "space") {
			return true
		}
	}
	return false
}

// PrecheckResult summarizes the gate facts (exit code 3 on failure).
type PrecheckResult struct {
	OS           OS
	OSOK         bool
	OSOverridden bool // --os-override bypassed the ID check (testing)
	RootOK       bool // not running as root (§16 red line 11)
	DiskOK       bool
	DiskFree     uint64
	OctagramOK   bool
	HerdrPrefix  bool     // informational: herdr Ctrl+Space prefix detected (§6.2)
	MissingTools []string // installed-provider-but-binary-missing
	ToolWarnings []string // binary missing only because L1 has not run yet
	Failures     []string
}

// Collect runs the full precheck gate. osOverride bypasses the ID check
// (containers/VMs) whatever its value — the help text used to promise a
// bypass while the code still demanded the literal "omarchy" (评审 P1-7).
// home is the user home dir.
func Collect(osOverride, home string) (*PrecheckResult, error) {
	r := &PrecheckResult{}

	// Root guard first: running as root would write root-owned files into the
	// desktop user's $HOME and break the session, and only pacman is allowed to
	// escalate (via sudo) — nothing else (§16 red line 11).
	r.RootOK = Geteuid() != 0
	if !r.RootOK {
		r.Failures = append(r.Failures, "ompinyin 不能以 root 运行（只有 pacman 通过 sudo 提权）；请用桌面用户执行")
	}

	osFacts, err := ReadOS()
	if err != nil {
		return nil, fmt.Errorf("cannot read os-release: %w", err)
	}
	r.OS = osFacts
	if osOverride != "" {
		r.OSOverridden = true
		r.OSOK = true
	} else {
		r.OSOK = IsOmarchy(osFacts.ID)
		if !r.OSOK {
			r.Failures = append(r.Failures, fmt.Sprintf("unsupported distro: ID=%q (targets Omarchy; use --os-override to bypass in containers)", osFacts.ID))
		}
	}

	free, err := DiskFreeBytes(home)
	if err != nil {
		return nil, fmt.Errorf("cannot stat disk space: %w", err)
	}
	r.DiskFree = free
	r.DiskOK = free >= MinFreeBytes
	if !r.DiskOK {
		r.Failures = append(r.Failures, fmt.Sprintf("insufficient disk space: %d MiB free, need ≥ %d MiB", free>>20, MinFreeBytes>>20))
	}

	// Tool presence (§1.1). rime_deployer runs inside the stop window, so
	// discovering it is missing AFTER we stopped fcitx5 is the worst possible
	// time — gate it up front instead.
	for _, t := range RequiredTools {
		if ToolInPath(t.Tool) {
			continue
		}
		switch {
		case t.Pkg != "" && !PkgInstalled(t.Pkg):
			r.ToolWarnings = append(r.ToolWarnings, fmt.Sprintf("%s 不在 PATH（%s 未装，L1 会安装）", t.Tool, t.Pkg))
		default:
			r.MissingTools = append(r.MissingTools, t.Tool)
			r.Failures = append(r.Failures, fmt.Sprintf("%s not found in PATH%s", t.Tool, func() string {
				if t.Pkg != "" {
					return fmt.Sprintf(" although %s is installed (broken package? run `sudo pacman -S --needed %s`)", t.Pkg, t.Pkg)
				}
				return " (not an Omarchy host?)"
			}()))
		}
	}

	r.OctagramOK = OctagramPresent()
	if !r.OctagramOK {
		if PkgInstalled("librime") {
			r.Failures = append(r.Failures, "librime installed but octagram plugin missing (needed by the wanxiang LMDG grammar)")
		} else {
			// librime 不在本机 → L1 会安装它，octagram 随 librime 带入（§3 L1）。
			// 这里不拦，避免全新机器在 L1 之前被预检卡死。
			r.OctagramOK = true
		}
	}
	// Informational only: the trigger keys are written unconditionally (§6.2).
	r.HerdrPrefix = HerdrPrefixFound(home)
	return r, nil
}
