// Package selfup upgrades the ompinyin binary itself (`update --self`).
//
// It resolves the newest release tag (releases/latest redirect), downloads the
// matching `ompinyin_linux_<arch>` asset, verifies it against `checksums.txt`,
// backs up the running binary and atomically replaces it. Seams keep it
// hermetic (T0 stubs; CI never touches the network).
package selfup

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/ProjectAILeap/ompinyin/internal/execcmd"
	"github.com/ProjectAILeap/ompinyin/internal/state"
)

const (
	// latestURL redirects to the newest non-prerelease release tag.
	latestURL = "https://github.com/ProjectAILeap/ompinyin/releases/latest"
	// downloadBase is the release asset root for a given tag.
	downloadBase = "https://github.com/ProjectAILeap/ompinyin/releases/download"
)

// Exec seams (T0 tests stub these; CI must never touch the network).
var (
	// Version returns the newest release tag (e.g. "v1.1.0"), via the
	// releases/latest HTTP redirect (no JSON, no api.github.com).
	Version = func() (string, error) { return latestVersion() }
	// Fetch writes url to dest, following redirects; non-nil unless HTTP 200.
	Fetch = func(url, dest string) error { return fetch(url, dest) }
	// Executable is the running binary path. A var so tests can redirect.
	Executable = os.Executable
	// Sudo runs a privileged command for a non-root caller. A var so T0 tests
	// never shell out to the real sudo; the two call sites are the pre-upgrade
	// backup and the install into an unwritable directory.
	Sudo = func(args ...string) error { return runSudo(args...) }
)

// Result reports the outcome of a self-upgrade attempt.
type Result struct {
	Current string // current catalog.Version
	Latest  string // newest release tag
	Asset   string // the asset name that would be / was fetched
	Newer   bool   // Latest is newer than Current
	Applied bool   // the binary was actually replaced
	Message string // human-readable summary
}

// Check resolves the latest release and compares to current. Never downloads.
func Check(current string) (Result, error) {
	latest, err := Version()
	if err != nil {
		return Result{}, err
	}
	asset, err := assetName()
	if err != nil {
		return Result{}, err
	}
	return Result{Current: current, Latest: latest, Asset: asset, Newer: newer(latest, current)}, nil
}

// Apply checks the version and, when newer, downloads + verifies + replaces
// the running binary. Returns a Result; Applied=false when already current.
func Apply(current string) (Result, error) {
	r, err := Check(current)
	if err != nil {
		return r, err
	}
	if !r.Newer {
		r.Message = fmt.Sprintf("已是 %s", stripV(current))
		return r, nil
	}
	if err := replace(r.Latest, r.Asset); err != nil {
		return r, err
	}
	r.Applied = true
	r.Message = fmt.Sprintf("已升级 %s → %s（重新运行 ompinyin 以加载新版本）",
		stripV(current), stripV(r.Latest))
	return r, nil
}

// latestVersion follows the releases/latest redirect and reads its Location.
func latestVersion() (string, error) {
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(latestURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("releases/latest 未返回跳转位置（status %d）", resp.StatusCode)
	}
	tag := path.Base(loc)
	if tag == "" || tag == "latest" {
		return "", fmt.Errorf("releases/latest 解析到 %q", loc)
	}
	return tag, nil
}

// assetName returns the release asset for the running architecture.
func assetName() (string, error) {
	switch runtime.GOARCH {
	case "amd64", "arm64":
		return "ompinyin_linux_" + runtime.GOARCH, nil
	default:
		return "", fmt.Errorf("不支持的架构 %q：自升级仅提供 linux amd64/arm64", runtime.GOARCH)
	}
}

// replace fetches the asset, verifies its sha256 against checksums.txt, backs
// up the running binary and replaces it atomically.
//
// The downloads are staged in $TMPDIR, which on Omarchy is a DIFFERENT
// filesystem than the binary (/tmp is tmpfs, $HOME is btrfs). rename(2) cannot
// cross mounts, so the staged file is copied into the binary's own directory
// first and renamed there (replaceInDir). The $TMPDIR staging is kept because
// the sudo fallback installs from it when the directory is not writable.
func replace(tag, asset string) error {
	exe, err := Executable()
	if err != nil {
		return fmt.Errorf("定位当前程序失败：%w", err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		// /proc/self/exe is already resolved; a symlinked $PATH is the rare case.
		exe, _ = os.Executable()
	}
	dir := filepath.Dir(exe)
	base := downloadBase + "/" + tag + "/"

	// Stage the downloads in a private temp dir, not next to the binary: the
	// exe often lives in a root-owned dir (/usr/local/bin), where os.Create
	// would fail BEFORE the sudo fallback could ever run — making the
	// documented "sudo when not writable" path unreachable. The sudo branch
	// below installs from this dir instead.
	tmpDir, err := os.MkdirTemp("", "ompinyin-self-*")
	if err != nil {
		return fmt.Errorf("创建临时目录：%w", err)
	}
	defer os.RemoveAll(tmpDir)
	csPath := filepath.Join(tmpDir, "checksums.txt")
	binPath := filepath.Join(tmpDir, "ompinyin.new")

	if err := Fetch(base+"checksums.txt", csPath); err != nil {
		return fmt.Errorf("下载 checksums.txt：%w", err)
	}
	want, err := checksumFor(csPath, asset)
	if err != nil {
		return err
	}
	if err := Fetch(base+asset, binPath); err != nil {
		return fmt.Errorf("下载 %s：%w", asset, err)
	}
	if got := state.HashFile(binPath); !strings.EqualFold(got, want) {
		return fmt.Errorf("sha256 不匹配：期望 %s，实得 %s；拒绝覆盖", want, got)
	}

	bak := filepath.Join(dir, "."+filepath.Base(exe)+".ompinyin.bak")
	if err := state.CopyFile(exe, bak); err != nil {
		// The same permission wall as the binary itself: keep the promise of a
		// pre-upgrade backup by escalating just this copy.
		if serr := Sudo("cp", "-p", exe, bak); serr != nil {
			return fmt.Errorf("备份旧程序 %s：%w", bak, err)
		}
	}
	if err := os.Chmod(binPath, 0o755); err != nil {
		return err
	}
	if err := replaceInDir(binPath, exe); err != nil {
		// not writable (e.g. /usr/local/bin) — escalate just this write via sudo.
		if serr := Sudo("install", "-m", "755", binPath, exe); serr != nil {
			return fmt.Errorf("替换 %s 失败（%v），sudo 也失败（%v）；请手动下载新版本并用 `install -Dm755` 放到该路径", exe, err, serr)
		}
	}
	return nil
}

// replaceInDir installs src as dst by copying it into dst's directory and
// renaming there. The copy is what makes the replacement work regardless of
// where src lives: rename(2) returns EXDEV for a src and dst on different
// mounts (/tmp tmpfs vs the root filesystem), which is exactly the layout on an
// Omarchy host — the bug that made every `update --self` fail with a
// misleading "无权限". The copy is removed again when the rename fails, so a
// failed attempt leaves no litter next to the binary.
func replaceInDir(src, dst string) error {
	staged := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".new")
	if err := state.CopyFile(src, staged); err != nil {
		return err // unwritable directory: the signal for the sudo fallback
	}
	// CopyFile opens with 0644; without this the replaced binary loses its
	// executable bit.
	if err := os.Chmod(staged, 0o755); err != nil {
		_ = os.Remove(staged)
		return err
	}
	if err := os.Rename(staged, dst); err != nil {
		_ = os.Remove(staged)
		return err
	}
	return nil
}

// checksumFor parses a sha256sum-style file for the named asset.
func checksumFor(path, name string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.Fields(sc.Text())
		if len(line) >= 2 && line[1] == name {
			return strings.ToLower(line[0]), nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("checksums.txt 里没有 %s 的条目", name)
}

// runSudo executes a privileged command for a non-root caller, using sudo -n
// when there is no controlling terminal (agent/CI) so it fails fast.
func runSudo(args ...string) error {
	return execcmd.RunInteractive("sudo", execcmd.SudoArgs(execcmd.HasTTY(), args...)...)
}

// newer reports whether a is a semantically higher version than b. A leading v
// is ignored; -suffix (e.g. -dirty) is dropped. When b is unparseable (a dev
// build hash) it is treated as older, so an upgrade is offered.
func newer(a, b string) bool {
	av, okA := parseVer(a)
	bv, okB := parseVer(b)
	if !okA {
		return false
	}
	if !okB {
		return true // b is not a release (dev build) — an upgrade is available
	}
	for i := 0; i < len(av) || i < len(bv); i++ {
		x, y := 0, 0
		if i < len(av) {
			x = av[i]
		}
		if i < len(bv) {
			y = bv[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// parseVer splits "v1.2.3" into numeric components, ignoring a -suffix.
func parseVer(v string) ([]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v = v[:i]
	}
	if v == "" {
		return nil, false
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

// stripV returns the version with a leading v removed, for display.
func stripV(v string) string { return strings.TrimPrefix(v, "v") }

// fetch writes url to dest (following redirects); non-nil unless HTTP 200.
func fetch(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
