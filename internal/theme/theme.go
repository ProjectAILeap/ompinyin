// Package theme implements the L4 candidate-window theming (§6.6) after the
// Omarchy 4.5 hand-over: ompinyin owns the classicui.conf KEYS, Omarchy owns
// the colors.
//
//   - ompinyin owns (ledger-tracked, whole-file): classicui.conf
//     (Theme=omarchy / UseDarkTheme=False / UseAccentColor=False + the UI font),
//     key-scoped like ~/.Xresources is line-scoped (§5.1).
//   - Omarchy owns the theme itself: `default/themed/fcitx5-theme.conf.tpl` →
//     `omarchy-theme-set-fcitx5` writes ~/.local/share/fcitx5/themes/omarchy and
//     points classicui at it on every `omarchy theme set`. ompinyin used to
//     generate that directory from a theme-set hook; the two writers raced on
//     the same files (docs/dev-notes.md P10), so convergence now RETIRES our
//     hook and the two SVGs only we ever wrote (RetireArtifacts).
//
// Reload: fcitx5 must be told to re-read the addon config. `fcitx5-remote -r`
// only reloads the GLOBAL config — it does NOT re-read conf/classicui.conf or
// the theme (verified on host: after -r the panel still showed the old theme;
// after ReloadAddonConfig("classicui") it switched). The DBus method
// org.fcitx.Fcitx.Controller1.ReloadAddonConfig is the same one
// fcitx5-configtool uses, and it hot-swaps the theme without restarting fcitx5.
package theme

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ProjectAILeap/ompinyin/internal/catalog"
	"github.com/ProjectAILeap/ompinyin/internal/execcmd"
	"github.com/ProjectAILeap/ompinyin/internal/state"
)

// Home-relative paths (§6.6). Ledger keys for theme files are these FULL
// relative paths (contain "/"), which keeps them out of the rime-dir
// basename key space and out of patches.OrphanFiles (basename != rel).
const (
	ConfRelPath = ".config/fcitx5/conf/classicui.conf"
	// HookRelPath is the theme-set hook ompinyin USED to install. It is kept as
	// a path (not as content) so converge can retire it and drop its ledger
	// entry — a host upgraded from <=1.3 still carries the file.
	HookRelPath = ".config/omarchy/hooks/theme-set.d/fcitx5-theme"
	themeDirRel = ".local/share/fcitx5/themes/omarchy"
	// ThemeName is the fcitx5 theme name Omarchy's generator provides.
	ThemeName = "omarchy"
)

// retiredFiles are the files ONLY ompinyin's old generator wrote inside
// themeDirRel. Omarchy's own theme.conf and symlinks are never touched.
var retiredFiles = []string{"background.svg", "highlight.svg"}

// ConfPath returns the absolute classicui.conf path.
func ConfPath(home string) string {
	return filepath.Join(home, filepath.FromSlash(ConfRelPath))
}

// HookPath returns the absolute theme-set hook path.
func HookPath(home string) string {
	return filepath.Join(home, filepath.FromSlash(HookRelPath))
}

// DefaultFont is the UI-font fallback when omarchy-font-current is missing.
const DefaultFont = "Sans"

// Run is the exec seam for running the hook script (`bash <hook>`), which
// regenerates the theme files from the current Omarchy colors (T0 stubs it).
var Run = func(name string, args ...string) error {
	c := execcmd.Command(name, args...)
	return c.Run()
}

// Reload hot-reloads the classicui addon config (theme included) via the
// fcitx5 DBus API — the same call fcitx5-configtool uses. Best-effort: when
// fcitx5 is not running or no DBus client exists, it returns an error the
// caller downgrades to a hint (the fcitx5 restart / next login picks the files
// up anyway).
var Reload = func() error {
	candidates := [][]string{
		{"gdbus", "call", "--session", "--dest", "org.fcitx.Fcitx5",
			"--object-path", "/controller",
			"--method", "org.fcitx.Fcitx.Controller1.ReloadAddonConfig",
			"classicui"},
		{"busctl", "--user", "call", "org.fcitx.Fcitx5", "/controller",
			"org.fcitx.Fcitx.Controller1", "ReloadAddonConfig", "s", "classicui"},
		{"dbus-send", "--session", "--print-reply", "--dest=org.fcitx.Fcitx5",
			"/controller", "org.fcitx.Fcitx.Controller1.ReloadAddonConfig",
			"string:classicui"},
	}
	for _, args := range candidates {
		path, err := exec.LookPath(args[0])
		if err != nil {
			continue
		}
		if err := Run(path, args[1:]...); err != nil {
			return fmt.Errorf("%s: %w", args[0], err)
		}
		return nil
	}
	return fmt.Errorf("no dbus client (gdbus/busctl/dbus-send) in PATH")
}

// CurrentFont resolves the Omarchy UI font family for the candidate window.
// Pango falls back to a CJK font via fontconfig either way; this just makes
// Latin/candidate glyphs match the rest of the desktop.
var CurrentFont = func() string {
	out, err := execcmd.Command("omarchy-font-current").Output()
	if err != nil {
		return DefaultFont
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return DefaultFont
	}
	return s
}

// ConfContent renders the managed classicui.conf (whole file).
// ManagedConfKeys are the only classicui.conf keys ompinyin owns. Everything
// else in that file belongs to the user: classicui.conf is a flat INI, so a
// whole-file write used to silently drop TrayFont / PerScreenDPI /
// EnableFractionalScale / VerticalCandidateList (they existed in the pre-ompinyin
// backup). Ownership is key-scoped, like ~/.Xresources is line-scoped (§5.1).
var ManagedConfKeys = []string{"Theme", "DarkTheme", "UseDarkTheme", "UseAccentColor", "Font", "MenuFont"}

// ConfContent renders the managed classicui.conf: managed header + managed
// keys, then every unmanaged `Key=Value` line of existing, in file order.
// Idempotent: feeding the output back yields the same bytes.
func ConfContent(existing, font string) string {
	if font == "" {
		font = DefaultFont
	}
	lines := []string{
		catalog.ManagedHeader(),
		"# fcitx5 candidate window follows the current Omarchy theme",
		"Theme=" + ThemeName,
		"DarkTheme=" + ThemeName,
		"UseDarkTheme=False",
		"UseAccentColor=False",
		"Font=" + font + " 12",
		"MenuFont=" + font + " 11",
	}
	for _, line := range strings.Split(existing, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		key, _, ok := strings.Cut(t, "=")
		if !ok || isManagedConfKey(strings.TrimSpace(key)) {
			continue
		}
		lines = append(lines, t)
	}
	return strings.Join(append(lines, ""), "\n")
}

func isManagedConfKey(key string) bool {
	for _, k := range ManagedConfKeys {
		if strings.EqualFold(k, key) {
			return true
		}
	}
	return false
}

// ReadConfContent returns the current classicui.conf ("" when absent), for the
// merge. Failures are reported as empty so convergence still writes ours.
func ReadConfContent(home string) string {
	b, err := os.ReadFile(ConfPath(home))
	if err != nil {
		return ""
	}
	return string(b)
}

// Observed is the read-only snapshot of the theming terminal state.
type Observed struct {
	// ConfOK: classicui.conf exists with the managed header and points at
	// ThemeName. ConfEqual adds "bytes already equal the desired content".
	ConfOK, ConfEqual bool
	// HookOK: the RETIRED theme-set hook still exists (residue from <=1.3; the
	// terminal state no longer contains it).
	HookOK bool
}

// Observe probes the theming terminal state (read-only).
func Observe(home string, st *state.State) Observed {
	var o Observed
	if b, err := os.ReadFile(ConfPath(home)); err == nil {
		s := string(b)
		o.ConfOK = strings.Contains(s, catalog.ManagedHeader()) &&
			strings.Contains(s, "Theme="+ThemeName) &&
			strings.Contains(s, "UseDarkTheme=False")
		o.ConfEqual = s == ConfContent(s, CurrentFont())
	}
	if _, err := os.Stat(HookPath(home)); err == nil {
		o.HookOK = true
	}
	return o
}

// ArtifactsPresent reports whether any retired artifact is still on disk (the
// hook or one of the two SVGs only ompinyin's old generator wrote).
func ArtifactsPresent(home string) bool {
	if _, err := os.Stat(HookPath(home)); err == nil {
		return true
	}
	for _, name := range retiredFiles {
		if _, err := os.Stat(filepath.Join(home, filepath.FromSlash(themeDirRel), name)); err == nil {
			return true
		}
	}
	return false
}

// RetireArtifacts deletes what ompinyin's own generator produced and the
// terminal state no longer contains: the theme-set hook and the two SVGs.
// theme.conf and Omarchy's symlinks are NOT ours and stay. Idempotent; returns
// the paths it actually removed so converge can report one honest line.
func RetireArtifacts(home string) ([]string, error) {
	paths := []string{HookPath(home)}
	for _, name := range retiredFiles {
		paths = append(paths, filepath.Join(home, filepath.FromSlash(themeDirRel), name))
	}
	var removed []string
	for _, p := range paths {
		if err := os.Remove(p); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return removed, err
		}
		removed = append(removed, p)
	}
	return removed, nil
}

// ErrNoUpstream means the host has no omarchy-theme-set-fcitx5 (pre-4.5): there
// is nothing to hand over, and classicui points fcitx5 at a theme name that does
// not exist there, so the candidate window falls back to its default look.
// Callers treat it as a note, never a failure.
var ErrNoUpstream = errors.New("omarchy-theme-set-fcitx5 不存在（Omarchy < 4.5）")

// LookPath is the exec seam for probing the upstream generator.
var LookPath = exec.LookPath

// ApplyUpstream runs Omarchy's own fcitx5 theme generator: it reads
// default/themed/fcitx5-theme.conf.tpl, writes
// ~/.local/share/fcitx5/themes/omarchy and points classicui at it, so the
// candidate window follows `omarchy theme set` without ompinyin in the loop
// (adapt 4.5 §5). `--activate` applies the CURRENT theme immediately.
func ApplyUpstream() error {
	path, err := LookPath("omarchy-theme-set-fcitx5")
	if err != nil {
		return ErrNoUpstream
	}
	return Run(path, "--activate")
}
