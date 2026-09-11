package theme

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProjectAILeap/ompinyin/internal/catalog"
)

// envWith returns the process environment with HOME/PATH replaced (appending a
// duplicate HOME/PATH is not reliable across execve implementations).
func envWith(home, path string) []string {
	var out []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "PATH=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "HOME="+home, "PATH="+path)
}

// writeFakeOmarchyTooling creates the two fakes the hook shells out to:
// omarchy-theme-color (per-key colors) and fcitx5-remote (exit 1, so the
// best-effort hot reload returns before ever touching a real session bus).
func writeFakeOmarchyTooling(t *testing.T, home string) string {
	t.Helper()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	color := `#!/usr/bin/env bash
case "${@: -1}" in
  background) echo '#112233' ;;
  selection|lighter_background) echo '#334455' ;;
  accent|blue) echo '#aabbcc' ;;
  foreground|bright_foreground) echo '#eeeeee' ;;
  dark_foreground|muted|darker_background) echo '#111111' ;;
  *) echo '#000000' ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "omarchy-theme-color"), []byte(color), 0o755); err != nil {
		t.Fatal(err)
	}
	// Not running: the hook's reload_fcitx5 returns 1 before any DBus call.
	if err := os.WriteFile(filepath.Join(bin, "fcitx5-remote"), []byte("#!/usr/bin/env bash\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// TestHookScriptGeneratesTheme runs the REAL shipped bash hook (not a stub)
// against a fake Omarchy theme, and pins the generated theme.conf. The hook is
// the single source of the color mapping — the Go side never duplicates it —
// so this is the only place that mapping is exercised.
func TestHookScriptGeneratesTheme(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	home := t.TempDir()

	stateDir := filepath.Join(home, ".local", "state", "omarchy", "current")
	if err := os.MkdirAll(filepath.Join(stateDir, "theme"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "theme", "colors.toml"), []byte("background = \"#112233\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "theme.name"), []byte("test-theme\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := writeFakeOmarchyTooling(t, home)
	hook := HookPath(home)
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte(HookContent()), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", hook)
	cmd.Env = envWith(home, bin+":"+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook failed: %v\n%s", err, out)
	}

	conf, err := os.ReadFile(filepath.Join(ThemeDir(home), "theme.conf"))
	if err != nil {
		t.Fatalf("theme.conf not generated: %v", err)
	}
	s := string(conf)
	for _, want := range []string{
		"[Metadata]",
		"Name=Omarchy",
		"Description=Omarchy theme colours (test-theme)",
		"Color=#112233",             // background → InputPanel/Background
		"HighlightBackgroundColor=", // accent-derived
		"HighlightCandidateColor=",  // computed contrast text
		"[InputPanel/Highlight]",
		"[Menu/Separator]",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("theme.conf missing %q:\n%s", want, s)
		}
	}
	for _, f := range []string{
		"background.svg", "highlight.svg", "prev.svg", "next.svg",
		"radio.svg", "arrow.svg", "theme.conf",
	} {
		if _, err := os.Stat(filepath.Join(ThemeDir(home), f)); err != nil {
			t.Errorf("generated theme missing %s: %v", f, err)
		}
	}
}

// TestHookScriptNoColorsIsQuiet: without colors.toml the hook must exit 0 and
// generate nothing (fcitx5 keeps its default candidate window).
func TestHookScriptNoColorsIsQuiet(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	home := t.TempDir()
	bin := writeFakeOmarchyTooling(t, home)
	hook := HookPath(home)
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte(HookContent()), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", hook)
	cmd.Env = envWith(home, bin+":"+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook must exit quietly without colors: %v\n%s", err, out)
	}
	if _, err := os.Stat(ThemeDir(home)); err == nil {
		t.Error("no theme dir may be generated without colors.toml")
	}
}

// TestHookContentIsManagedAndValid pins that the shipped hook starts with the
// managed header (omarchy-hook runs it with `bash <file>`, so the header must
// be a `#` comment) and is syntactically valid bash.
func TestHookContentIsManagedAndValid(t *testing.T) {
	s := HookContent()
	if !strings.HasPrefix(s, catalog.ManagedHeader()) {
		t.Errorf("hook must start with the managed header, got:\n%s", strings.SplitN(s, "\n", 2)[0])
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	cmd := exec.Command("bash", "-n")
	cmd.Stdin = strings.NewReader(s)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook is not valid bash: %v\n%s", err, out)
	}
}
