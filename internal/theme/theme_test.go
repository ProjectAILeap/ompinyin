package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProjectAILeap/ompinyin/internal/catalog"
	"github.com/ProjectAILeap/ompinyin/internal/patches"
	"github.com/ProjectAILeap/ompinyin/internal/state"
)

// TestConfContentShape: the managed classicui.conf must be a whole file with
// the managed header, pointing the panel at the omarchy theme, with the UI
// font baked in and the accent-portal path disabled (the generated theme
// carries the colors, so follow-system-accent would fight the hook).
func TestConfContentShape(t *testing.T) {
	s := ConfContent("JetBrainsMono Nerd Font")
	for _, want := range []string{
		catalog.ManagedHeader(),
		"Theme=omarchy",
		"DarkTheme=omarchy",
		"UseDarkTheme=False",
		"UseAccentColor=False",
		"Font=JetBrainsMono Nerd Font 12",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("classicui.conf missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "UseDarkTheme=True") {
		t.Error("UseDarkTheme must not follow the system scheme (hook writes the colors)")
	}
}

// TestHookContentShape: the managed hook must be a valid bash script carrying
// the managed header (a bash `#` comment), the color-mapping markers, and the
// DBus reload (fcitx5-remote -r would NOT re-read the theme — verified).
func TestHookContentShape(t *testing.T) {
	s := HookContent()
	if !strings.HasPrefix(s, catalog.ManagedHeader()+"\n") {
		t.Errorf("hook must start with the managed header:\n%s", s[:80])
	}
	for _, want := range []string{
		"set -euo pipefail", "omarchy-theme-color",
		"HighlightCandidateColor", "ReloadAddonConfig", "classicui",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("hook script missing %q", want)
		}
	}
}

// TestObserveTransitions: an empty home reads as not converged; after writing
// the real files it reads as converged; a hand-edited classicui.conf flips
// ConfEqual while the header check still sees the right theme name choice.
func TestObserveTransitions(t *testing.T) {
	home := t.TempDir()

	st := state.New()
	resetFont := CurrentFont
	CurrentFont = func() string { return "TestFont" }
	defer func() { CurrentFont = resetFont }()

	if o := Observe(home, st); o.ConfEqual && o.HookEqual && o.DirOK {
		t.Error("empty home must not be theme-converged")
	}

	// converge the files by hand (what themeApply writes)
	for rel, content := range map[string]string{
		ConfRelPath: ConfContent("TestFont"),
		HookRelPath: HookContent(),
	} {
		abs := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		st.ManagedFiles[rel] = state.HashBytes([]byte(content))
	}
	if err := os.MkdirAll(ThemeDir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ThemeDir(home), "theme.conf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	o := Observe(home, st)
	if !o.ConfOK || !o.ConfEqual || !o.HookOK || !o.HookEqual || !o.DirOK {
		t.Errorf("converged theming not detected: %+v", o)
	}

	// hand edit → ConfEqual flips, header check stays true; the §5.1 classify
	// reports user-modified so converge will ask before overwriting
	confAbs := ConfPath(home)
	os.WriteFile(confAbs, []byte("Theme=default\n"), 0o644)
	o = Observe(home, st)
	if o.ConfOK || o.ConfEqual {
		t.Errorf("edited classicui.conf must read as drifted: %+v", o)
	}
	if got := patches.Classify(confAbs, st.ManagedFiles[ConfRelPath]); got != patches.StatusUserModified {
		t.Errorf("edited classicui.conf classified as %v, want user-modified", got)
	}
	if got := patches.Classify(HookPath(home), st.ManagedFiles[HookRelPath]); got != patches.StatusManaged {
		t.Errorf("untouched hook classified as %v, want managed", got)
	}
}
