// Package theme implements the L4 candidate-window theming (§6.6): the fcitx5
// classicui.conf plus the omarchy theme-set hook that make the candidate
// window follow the current Omarchy theme.
//
// Ownership split (the hook is the single source of the color mapping):
//
//   - ompinyin owns (ledger-tracked, whole-file): classicui.conf
//     (Theme=omarchy …) and the hook script below; both get the managed
//     header and the §5.1 ownership protocol.
//   - the HOOK owns the generated theme dir
//     ~/.local/share/fcitx5/themes/omarchy (SVGs + theme.conf read from the
//     current colors.toml). It runs on every `omarchy theme set`, so the
//     colors follow the theme without ompinyin re-running; ompinyin also runs
//     it once at converge time so the first install applies immediately.
//     The dir is NOT ledger-tracked (its bytes legitimately change whenever
//     the user switches theme); uninstall deletes it explicitly.
//
// Reload: fcitx5 must be told to re-read the addon config. `fcitx5-remote -r`
// only reloads the GLOBAL config — it does NOT re-read conf/classicui.conf or
// the theme dir (verified on host: after -r the panel still showed the old
// theme; after ReloadAddonConfig("classicui") it switched). The DBus method
// org.fcitx.Fcitx.Controller1.ReloadAddonConfig is the same one
// fcitx5-configtool uses, and it hot-swaps the theme without restarting fcitx5.
package theme

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ProjectAILeap/ompinyin/internal/catalog"
	"github.com/ProjectAILeap/ompinyin/internal/patches"
	"github.com/ProjectAILeap/ompinyin/internal/state"
)

// Home-relative paths (§6.6). Ledger keys for theme files are these FULL
// relative paths (contain "/"), which keeps them out of the rime-dir
// basename key space and out of patches.OrphanFiles (basename != rel).
const (
	ConfRelPath = ".config/fcitx5/conf/classicui.conf"
	HookRelPath = ".config/omarchy/hooks/theme-set.d/fcitx5-theme"
	ThemeDirRel = ".local/share/fcitx5/themes/omarchy"
	// ThemeName is the fcitx5 theme name the generated dir provides.
	ThemeName = "omarchy"
)

// ConfPath returns the absolute classicui.conf path.
func ConfPath(home string) string {
	return filepath.Join(home, filepath.FromSlash(ConfRelPath))
}

// HookPath returns the absolute theme-set hook path.
func HookPath(home string) string {
	return filepath.Join(home, filepath.FromSlash(HookRelPath))
}

// ThemeDir returns the absolute generated theme dir.
func ThemeDir(home string) string {
	return filepath.Join(home, filepath.FromSlash(ThemeDirRel))
}

// DefaultFont is the UI-font fallback when omarchy-font-current is missing.
const DefaultFont = "Sans"

// Run is the exec seam for running the hook script (`bash <hook>`), which
// regenerates the theme files from the current Omarchy colors (T0 stubs it).
var Run = func(name string, args ...string) error {
	c := exec.Command(name, args...)
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
	out, err := exec.Command("omarchy-font-current").Output()
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
func ConfContent(font string) string {
	if font == "" {
		font = DefaultFont
	}
	return strings.Join([]string{
		catalog.ManagedHeader(),
		"# fcitx5 candidate window follows the current Omarchy theme",
		"Theme=" + ThemeName,
		"DarkTheme=" + ThemeName,
		"UseDarkTheme=False",
		"UseAccentColor=False",
		"Font=" + font + " 12",
		"MenuFont=" + font + " 11",
		"",
	}, "\n")
}

// HookContent renders the managed theme-set hook (whole file). It regenerates
// ~/.local/share/fcitx5/themes/omarchy from the current Omarchy colors.toml,
// then hot-reloads fcitx5. The managed header doubles as a bash `#` comment so
// the script stays a valid hook for omarchy-hook (which runs `bash <file>`).
func HookContent() string {
	return catalog.ManagedHeader() + "\n" + hookScript
}

// Observed is the read-only snapshot of the theming terminal state.
type Observed struct {
	// ConfOK: classicui.conf exists with the managed header and points at
	// ThemeName. ConfEqual adds "bytes already equal the desired content".
	ConfOK, ConfEqual bool
	// HookOK: the hook exists with the managed header. HookEqual adds
	// "bytes already equal".
	HookOK, HookEqual bool
	// DirOK: the generated theme dir is populated (theme.conf present).
	DirOK bool
}

// Equal reports whether the whole theming set needs no convergence work.
func (o Observed) Equal() bool { return o.ConfEqual && o.HookEqual && o.DirOK }

// Observe probes the theming terminal state (read-only).
func Observe(home string, st *state.State) Observed {
	var o Observed
	o.DirOK = ThemeDirPopulated(home)

	if b, err := os.ReadFile(ConfPath(home)); err == nil {
		s := string(b)
		o.ConfOK = strings.Contains(s, catalog.ManagedHeader()) &&
			strings.Contains(s, "Theme="+ThemeName) &&
			strings.Contains(s, "UseDarkTheme=False")
		o.ConfEqual = s == ConfContent(CurrentFont())
	}
	if b, err := os.ReadFile(HookPath(home)); err == nil {
		s := string(b)
		o.HookOK = strings.Contains(s, catalog.ManagedHeader())
		o.HookEqual = s == HookContent()
	}
	return o
}

// ThemeDirPopulated reports whether the generated theme is in place.
func ThemeDirPopulated(home string) bool {
	if _, err := os.Stat(filepath.Join(ThemeDir(home), "theme.conf")); err != nil {
		return false
	}
	ents, err := os.ReadDir(ThemeDir(home))
	return err == nil && len(ents) > 0
}

// Generate runs the installed hook once so the CURRENT Omarchy theme is
// applied immediately (install-time application; afterwards the hook also runs
// on every `omarchy theme set`). It fails when the hook cannot produce the
// theme dir — the current Omarchy theme colors (colors.toml) are missing, so
// fcitx5 would silently fall back to the default white panel.
func Generate(home string) error {
	if err := Run("bash", HookPath(home)); err != nil {
		return err
	}
	if !ThemeDirPopulated(home) {
		return fmt.Errorf("主题目录未生成（当前 Omarchy 主题颜色不可用？需要 ~/.local/state/omarchy/current/theme/colors.toml 与 omarchy-theme-color）")
	}
	return nil
}

// ObservedFromClassify re-exports patches.Classify for one theme file so
// converge can apply the §5.1 ownership protocol without importing patches
// itself in two places.
func Classify(abs, ledger string) patches.Status { return patches.Classify(abs, ledger) }

// hookScript is the runtime theme generator shipped by ompinyin. It is the
// single source of the color-mapping logic (theme.conf keys + SVG art + the
// luminance-based contrast text); ompinyin itself only installs it and runs it
// via Generate. Keep it in sync with the fcitx5 theme engine's field mapping
// (InputPanel/Background → panel; InputPanel/Highlight → selected candidate;
// HighlightCandidateColor → text on the highlight …) — see DESIGN §6.6.
const hookScript = `# fcitx5 candidate-window theme generator: follows the current Omarchy theme
# Triggered by: omarchy theme set <name> (theme-set hook, run by omarchy-hook)
# Manual: bash ${HOME}/.config/omarchy/hooks/theme-set.d/fcitx5-theme [theme-slug]
set -euo pipefail

STATE_DIR="$HOME/.local/state/omarchy/current"
COLORS="$STATE_DIR/theme/colors.toml"
THEME_OUT="$HOME/.local/share/fcitx5/themes/omarchy"
THEME_SLUG="${1:-$(cat "$STATE_DIR/theme.name" 2>/dev/null || echo unknown)}"

# No current Omarchy theme colors: keep the default candidate window, exit quietly.
[[ -f "$COLORS" ]] || exit 0
command -v omarchy-theme-color >/dev/null 2>&1 || exit 0

color() {
  local value
  value="$(omarchy-theme-color --file "$COLORS" "$1" 2>/dev/null || true)"
  printf '%s' "${value:-$2}"
}

# Perceived brightness 0-255 (ITU-R BT.601).
lum() {
  local h="${1#\#}"
  if [[ ! $h =~ ^[0-9A-Fa-f]{6}$ ]]; then
    printf '0'
    return
  fi
  printf '%s' "$(( (299 * 16#${h:0:2} + 587 * 16#${h:2:2} + 114 * 16#${h:4:2}) / 1000 ))"
}

bg="$(color background '#1e1e1e')"
border="$(color lighter_background "$(color selection '#3c3836')")"
fg="$(color foreground '#d4be98')"
dim="$(color dark_foreground "$(color muted '#7c6f64')")"
accent="$(color accent "$(color blue '#7daea3')")"
inline_bg="$(color selection '#504945')"
inline_fg="$(color bright_foreground "$fg")"

# Contrast text on the accent highlight: light accent -> dark text, and vice
# versa. Theme-relative where possible, hard guarantees otherwise.
light_fg="$(color background '#f5f5f5')"
if (( $(lum "$light_fg") < 150 )); then light_fg='#f5f5f5'; fi
dark_fg="$(color darker_background '#161616')"
if (( $(lum "$dark_fg") > 110 )); then dark_fg='#161616'; fi
if (( $(lum "$accent") >= 150 )); then
  accent_fg="$dark_fg"
else
  accent_fg="$light_fg"
fi
accent_dim="${accent_fg}b3"

mkdir -p "$THEME_OUT"

# Rounded 9-slice panel background (32x32, margin 12 -> corner radius 12).
cat >"$THEME_OUT/background.svg" <<EOF
<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 32 32">
  <rect x="0.5" y="0.5" width="31" height="31" rx="12" ry="12" fill="$bg" stroke="$border" stroke-width="1"/>
</svg>
EOF

# Rounded selected-candidate highlight (24x24, margin 6 -> corner radius 6).
cat >"$THEME_OUT/highlight.svg" <<EOF
<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24">
  <rect x="0" y="0" width="24" height="24" rx="6" ry="6" fill="$accent"/>
</svg>
EOF

cat >"$THEME_OUT/prev.svg" <<EOF
<svg xmlns="http://www.w3.org/2000/svg" width="16" height="20" viewBox="0 0 16 20" fill="none">
  <path d="M10.5 5 L6 10 L10.5 15" stroke="$dim" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>
</svg>
EOF

cat >"$THEME_OUT/next.svg" <<EOF
<svg xmlns="http://www.w3.org/2000/svg" width="16" height="20" viewBox="0 0 16 20" fill="none">
  <path d="M5.5 5 L10 10 L5.5 15" stroke="$dim" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>
</svg>
EOF

cat >"$THEME_OUT/radio.svg" <<EOF
<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 12 12">
  <circle cx="6" cy="6" r="3" fill="$accent"/>
</svg>
EOF

cat >"$THEME_OUT/arrow.svg" <<EOF
<svg xmlns="http://www.w3.org/2000/svg" width="10" height="15" viewBox="0 0 10 15" fill="none">
  <path d="M4 4.5 L7 7.5 L4 10.5" stroke="$dim" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>
</svg>
EOF

cat >"$THEME_OUT/theme.conf" <<EOF
[Metadata]
Name=Omarchy
Version=1
Author=omarchy fcitx5-theme hook
Description=Omarchy theme colours ($THEME_SLUG)
ScaleWithDPI=True

[InputPanel]
NormalColor=$fg
CandidateLabelColor=$dim
CandidateCommentColor=$dim
HighlightColor=$inline_fg
HighlightBackgroundColor=$inline_bg
HighlightCandidateColor=$accent_fg
HighlightCandidateLabelColor=$accent_dim
HighlightCandidateCommentColor=$accent_dim
PageButtonAlignment=Last Candidate
FullWidthHighlight=True

[InputPanel/Background]
Image=background.svg
Color=$bg
BorderColor=$border
BorderWidth=1

[InputPanel/Background/Margin]
Left=12
Right=12
Top=12
Bottom=12

[InputPanel/ContentMargin]
Left=6
Right=6
Top=6
Bottom=6

[InputPanel/TextMargin]
Left=6
Right=6
Top=6
Bottom=6

[InputPanel/Highlight]
Image=highlight.svg
Color=$accent
BorderColor=$accent
BorderWidth=0

[InputPanel/Highlight/Margin]
Left=6
Right=6
Top=6
Bottom=6

[InputPanel/Highlight/HighlightClickMargin]
Left=2
Right=2
Top=2
Bottom=2

[InputPanel/PrevPage]
Image=prev.svg

[InputPanel/PrevPage/ClickMargin]
Left=4
Right=4
Top=4
Bottom=4

[InputPanel/NextPage]
Image=next.svg

[InputPanel/NextPage/ClickMargin]
Left=4
Right=4
Top=4
Bottom=4

[Menu]
NormalColor=$fg
HighlightCandidateColor=$accent_fg
Spacing=2

[Menu/Background]
Image=background.svg
Color=$bg
BorderColor=$border
BorderWidth=1

[Menu/Background/Margin]
Left=12
Right=12
Top=12
Bottom=12

[Menu/ContentMargin]
Left=6
Right=6
Top=6
Bottom=6

[Menu/TextMargin]
Left=6
Right=6
Top=6
Bottom=6

[Menu/Highlight]
Image=highlight.svg
Color=$accent
BorderColor=$accent
BorderWidth=0

[Menu/Highlight/Margin]
Left=6
Right=6
Top=6
Bottom=6

[Menu/Separator]
Color=$border

[Menu/CheckBox]
Image=radio.svg

[Menu/SubMenu]
Image=arrow.svg
EOF

# Hot-reload the classicui addon config over DBus. NOTE: 'fcitx5-remote -r'
# only reloads the global config and would NOT re-read the theme (verified on
# host); ReloadAddonConfig is what fcitx5-configtool uses.
reload_fcitx5() {
  command -v fcitx5-remote >/dev/null 2>&1 || return 1
  fcitx5-remote >/dev/null 2>&1 || return 1 # fcitx5 not running: nothing to reload
  if command -v gdbus >/dev/null 2>&1; then
    gdbus call --session --dest org.fcitx.Fcitx5 --object-path /controller \
      --method org.fcitx.Fcitx.Controller1.ReloadAddonConfig classicui \
      >/dev/null 2>&1 && return 0
  fi
  if command -v busctl >/dev/null 2>&1; then
    busctl --user call org.fcitx.Fcitx5 /controller \
      org.fcitx.Fcitx.Controller1 ReloadAddonConfig s classicui \
      >/dev/null 2>&1 && return 0
  fi
  if command -v dbus-send >/dev/null 2>&1; then
    dbus-send --session --dest=org.fcitx.Fcitx5 --print-reply \
      /controller org.fcitx.Fcitx.Controller1.ReloadAddonConfig \
      string:classicui >/dev/null 2>&1 && return 0
  fi
  return 1
}

reload_fcitx5 || true
exit 0
`
