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
	s := ConfContent("", "JetBrainsMono Nerd Font")
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
		ConfRelPath: ConfContent("", "TestFont"),
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

// TestConfContentPreservesUnmanagedKeys: classicui.conf ownership is KEY-scoped.
// The old whole-file write silently dropped TrayFont / PerScreenDPI /
// EnableFractionalScale / VerticalCandidateList (all present in the
// pre-ompinyin backup), so a user who tuned them lost them on every converge.
func TestConfContentPreservesUnmanagedKeys(t *testing.T) {
	existing := strings.Join([]string{
		"# managed by ompinyin v1.0.0 — hand edits will be overwritten",
		"Theme=installer-dark",
		"Font=Old Font 9",
		"TrayFont=Sans Bold 10",
		"PerScreenDPI=True",
		"UseDarkTheme=False",
		"",
	}, "\n")
	out := ConfContent(existing, "TestFont")

	for _, want := range []string{"TrayFont=Sans Bold 10", "PerScreenDPI=True"} {
		if !strings.Contains(out, want) {
			t.Errorf("unmanaged key %q was dropped:\n%s", want, out)
		}
	}
	// managed keys always come from ompinyin, wherever the user put them
	for _, want := range []string{"Theme=omarchy", "Font=TestFont 12", "MenuFont=TestFont 11"} {
		if !strings.Contains(out, want) {
			t.Errorf("managed key %q missing:\n%s", want, out)
		}
	}
	if strings.Contains(out, "installer-dark") || strings.Contains(out, "Old Font") {
		t.Errorf("managed keys must be overwritten:\n%s", out)
	}
	// preserved keys are not duplicated by the managed block
	if strings.Count(out, "UseDarkTheme=") != 1 {
		t.Errorf("UseDarkTheme duplicated:\n%s", out)
	}
	// idempotent: merging the output again is a no-op
	if again := ConfContent(out, "TestFont"); again != out {
		t.Errorf("not idempotent:\nfirst:\n%s\nsecond:\n%s", out, again)
	}
}
