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
// font baked in and the accent-portal path disabled (Omarchy's generator
// carries the colors, so follow-system-accent would fight it).
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
		t.Error("UseDarkTheme must not follow the system scheme (Omarchy generates the colors)")
	}
}

// TestObserveTransitions: an empty home reads as not converged; after writing
// the real file it reads as converged; a hand-edited classicui.conf flips
// ConfEqual while the header check still sees the right theme name choice.
func TestObserveTransitions(t *testing.T) {
	home := t.TempDir()

	st := state.New()
	resetFont := CurrentFont
	CurrentFont = func() string { return "TestFont" }
	defer func() { CurrentFont = resetFont }()

	if o := Observe(home, st); o.ConfEqual || o.HookOK {
		t.Error("empty home must not be theme-converged")
	}

	// converge classicui.conf by hand (what themeApply writes)
	content := ConfContent("", "TestFont")
	abs := ConfPath(home)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	st.ManagedFiles[ConfRelPath] = state.HashBytes([]byte(content))

	o := Observe(home, st)
	if !o.ConfOK || !o.ConfEqual || o.HookOK {
		t.Errorf("converged theming not detected: %+v", o)
	}

	// hand edit → ConfEqual flips, header check stays true; the §5.1 classify
	// reports user-modified so converge will ask before overwriting
	os.WriteFile(abs, []byte("Theme=default\n"), 0o644)
	o = Observe(home, st)
	if o.ConfOK || o.ConfEqual {
		t.Errorf("edited classicui.conf must read as drifted: %+v", o)
	}
	if got := patches.Classify(abs, st.ManagedFiles[ConfRelPath]); got != patches.StatusUserModified {
		t.Errorf("edited classicui.conf classified as %v, want user-modified", got)
	}
}

// TestRetireArtifacts: convergence deletes exactly the files ompinyin's old
// generator wrote — the hook and its two SVGs — and leaves Omarchy's theme.conf
// and symlinks alone (a `omarchy theme set` writes them now).
func TestRetireArtifacts(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, filepath.FromSlash(themeDirRel))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	hook := HookPath(home)
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{hook, filepath.Join(dir, "background.svg"), filepath.Join(dir, "highlight.svg")} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// not ours: Omarchy's generated theme.conf plus a theme symlink
	keep := []string{filepath.Join(dir, "theme.conf"), filepath.Join(dir, "arrow.svg"), filepath.Join(dir, "current")}
	for _, p := range keep {
		if err := os.WriteFile(p, []byte("y"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if !ArtifactsPresent(home) {
		t.Fatal("precondition: retired artifacts must read as present")
	}

	removed, err := RetireArtifacts(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 3 {
		t.Errorf("removed = %v, want the hook and the two SVGs", removed)
	}
	if ArtifactsPresent(home) {
		t.Error("artifacts still present after retirement")
	}
	for _, p := range keep {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("not-ours file %s was deleted: %v", p, err)
		}
	}
	// idempotent, and a fresh home is a no-op
	if again, err := RetireArtifacts(home); err != nil || len(again) != 0 {
		t.Errorf("second call removed=%v err=%v", again, err)
	}
	if got, err := RetireArtifacts(t.TempDir()); err != nil || len(got) != 0 {
		t.Errorf("empty home removed=%v err=%v", got, err)
	}
}

// TestApplyUpstream: the theme is handed to Omarchy's own generator; a host
// without it (pre-4.5) yields the ignorable ErrNoUpstream instead of an exec.
func TestApplyUpstream(t *testing.T) {
	resetLook, resetRun := LookPath, Run
	defer func() { LookPath, Run = resetLook, resetRun }()

	var calls [][]string
	Run = func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		return nil
	}

	LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	if err := ApplyUpstream(); err != ErrNoUpstream {
		t.Errorf("missing generator must yield ErrNoUpstream, got %v", err)
	}
	if len(calls) != 0 {
		t.Errorf("nothing may be exec'd without the script: %v", calls)
	}

	LookPath = func(string) (string, error) { return "/usr/bin/omarchy-theme-set-fcitx5", nil }
	if err := ApplyUpstream(); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || len(calls[0]) != 2 || calls[0][1] != "--activate" {
		t.Errorf("unexpected exec: %v", calls)
	}
	// a failing generator is reported (converge downgrades it to a note)
	Run = func(string, ...string) error { return os.ErrPermission }
	if err := ApplyUpstream(); err == nil || err == ErrNoUpstream {
		t.Errorf("generator failure must surface, got %v", err)
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
