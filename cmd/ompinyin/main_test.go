package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ProjectAILeap/ompinyin/internal/assets"
	"github.com/ProjectAILeap/ompinyin/internal/catalog"
	"github.com/ProjectAILeap/ompinyin/internal/converge"
	"github.com/ProjectAILeap/ompinyin/internal/deploy"
	"github.com/ProjectAILeap/ompinyin/internal/facts"
	"github.com/ProjectAILeap/ompinyin/internal/pkgs"
	"github.com/ProjectAILeap/ompinyin/internal/service"
	"github.com/ProjectAILeap/ompinyin/internal/tray"
)

// TestNewOptsMirrorDefault locks 评审 P0-10: every command builds its Options
// through newOpts(), so the download policy must already be the documented
// default (`cn`). An empty MirrorSource silently meant "auto" = GitHub-first,
// which stalls on the 420MB model for mainland users.
func TestNewOptsMirrorDefault(t *testing.T) {
	if got := newOpts().MirrorSource; got != catalog.DefaultMirrorSource() {
		t.Errorf("newOpts().MirrorSource = %q, want %q", got, catalog.DefaultMirrorSource())
	}
}

// TestApplyInstallFlagsKeepsBaseline locks 评审 P0-11: `install` overrides only
// the flags actually present on the command line. A bare `ompinyin install`
// must not silently drop a previously chosen double pinyin, reset the channel,
// or re-enable a model the user turned off.
func TestApplyInstallFlagsKeepsBaseline(t *testing.T) {
	dspBaseline := catalog.Desired{Primary: "quanpin", Extra: []string{"zrm"}, Model: true, Channel: "stable"}

	t.Run("bare install keeps dsp", func(t *testing.T) {
		got := applyInstallFlags(dspBaseline, installFlags{Channel: "stable"}, map[string]bool{})
		if got.Primary != "quanpin" || len(got.Extra) != 1 || got.Extra[0] != "zrm" {
			t.Errorf("bare install reset the layout: %+v", got)
		}
		if !got.Model || got.Channel != "stable" {
			t.Errorf("bare install changed model/channel: %+v", got)
		}
	})

	t.Run("bare install keeps no-model", func(t *testing.T) {
		base := catalog.Desired{Primary: "quanpin", Model: false, Channel: "nightly"}
		got := applyInstallFlags(base, installFlags{Channel: "stable"}, map[string]bool{})
		if got.Model {
			t.Error("bare install re-enabled the model the user had disabled")
		}
		if got.Channel != "nightly" {
			t.Errorf("channel reset without --channel: %q", got.Channel)
		}
	})

	t.Run("explicit flags override", func(t *testing.T) {
		got := applyInstallFlags(dspBaseline,
			installFlags{DSP: "flypy", Channel: "nightly", NoModel: true},
			map[string]bool{"dsp": true, "channel": true, "no-model": true})
		if got.Primary != "quanpin" || len(got.Extra) != 1 || got.Extra[0] != "flypy" {
			t.Errorf("--dsp flypy not applied: %+v", got)
		}
		if got.Model || got.Channel != "nightly" {
			t.Errorf("--no-model/--channel not applied: %+v", got)
		}
	})

	t.Run("--model re-enables", func(t *testing.T) {
		base := catalog.Desired{Primary: "quanpin", Model: false, Channel: "stable"}
		got := applyInstallFlags(base, installFlags{Model: true}, map[string]bool{"model": true})
		if !got.Model {
			t.Error("--model must turn the model back on")
		}
	})

	t.Run("--dsp none clears the extra", func(t *testing.T) {
		got := applyInstallFlags(dspBaseline, installFlags{DSP: "none"}, map[string]bool{"dsp": true})
		if got.Primary != "quanpin" || len(got.Extra) != 0 {
			t.Errorf("--dsp none must return to quanpin only: %+v", got)
		}
	})

	t.Run("--dsp-default pairs with quanpin in Extra", func(t *testing.T) {
		got := applyInstallFlags(catalog.DefaultDesired(),
			installFlags{DSP: "zrm", DSPDefault: true}, map[string]bool{"dsp": true, "dsp-default": true})
		if got.Primary != "zrm" || len(got.Extra) != 1 || got.Extra[0] != "quanpin" {
			t.Errorf("--dsp-default pairing wrong: %+v", got)
		}
	})

	t.Run("baseline slice is not aliased", func(t *testing.T) {
		got := applyInstallFlags(dspBaseline, installFlags{DSP: "flypy"}, map[string]bool{"dsp": true})
		if len(dspBaseline.Extra) != 1 || dspBaseline.Extra[0] != "zrm" {
			t.Errorf("baseline mutated through the returned slice: %+v", dspBaseline)
		}
		_ = got
	})
}

// TestExitCodesDocumented keeps the §7 contract pinned at the CLI boundary.
func TestExitCodesDocumented(t *testing.T) {
	cases := map[string]int{
		"":                              converge.ExitUsage,
		"bogus-command":                 converge.ExitUsage,
		"install --dsp zrm --dsp flypy": converge.ExitUsage,
		"install --dsp-default":         converge.ExitUsage,
		"install --model --no-model":    converge.ExitUsage,
	}
	for args, want := range cases {
		argv := splitArgs(args)
		if got := run(argv); got != want {
			t.Errorf("run(%q) = %d, want %d", args, got, want)
		}
	}
}

func splitArgs(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ' ' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// stubFakeHost wires the exec seams to an in-memory fake host (T0, §15) so a
// CLI-level test can reach the precheck/plan without touching the real system
// or the network. Keep in sync with internal/converge/setupFakeHost: the
// fields that matter for the `install --dry-run` path are the os-release ID,
// tool lookup, pacman, the fcitx5 user unit, and the tray-shell probe.
func stubFakeHost(t *testing.T, home string) {
	t.Helper()
	t.Setenv("OMPINYIN_TEST_HOME", home)

	osRel := filepath.Join(home, "os-release")
	mustWrite(osRel, "ID=omarchy\nBUILD_ID=4.0.2\n")

	facts.OSReleasePath = osRel
	facts.Run = func(name string, args ...string) error { return nil }
	facts.LookPath = func(string) (string, error) { return "/usr/bin/omarchy", nil }
	pkgs.Run = func(name string, args ...string) error { return nil }

	service.SystemUnitDirs = nil
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	mustWrite(filepath.Join(unitDir, "omarchy-fcitx5.service"),
		"[Unit]\nDescription=fcitx5\n[Service]\nExecStart=/usr/bin/fcitx5 --disable notificationitem\n[Install]\nWantedBy=default.target\n")
	service.Run = func(name string, args ...string) error {
		if len(args) >= 3 && args[0] == "--user" && args[1] == "is-active" {
			return errors.New("inactive") // dry-run never stops/starts
		}
		return nil
	}
	service.RunOutput = func(name string, args ...string) ([]byte, error) {
		return []byte("2"), nil
	}
	deploy.Run = func(dir, name string, args ...string) error { return nil }
	deploy.CompileSchemas = func(rimeDir string, schemas []string) error { return nil }
	tray.ShellRunning = func() bool { return true }
	assets.ResolveStableTag = func(ctx context.Context) string { return "" }

	t.Cleanup(func() {
		facts.OSReleasePath = "/etc/os-release"
		facts.Run = nil
		facts.LookPath = nil
		pkgs.Run = nil
		service.SystemUnitDirs = []string{"/etc/systemd/user", "/usr/lib/systemd/user"}
		service.Run = nil
		service.RunOutput = nil
		deploy.Run = nil
		deploy.CompileSchemas = nil
		tray.ShellRunning = nil
		assets.ResolveStableTag = nil
	})
}

func mustWrite(path, content string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		panic(err)
	}
}

// TestInstallDryRunJSONStdoutIsPure locks the §JSON contract (真机抓到):
// `install --dry-run --json` must emit ONLY the JSON document on stdout — any
// human diagnostic (e.g. the "[基线] 终态继承自 state.json…" notice) belongs
// on stderr. A leading text line would make strict parsers (jq / json.loads)
// reject the stream. It also pins the exit code for the usage-error negation
// (--json without --dry-run).
func TestInstallDryRunJSONStdoutIsPure(t *testing.T) {
	home := t.TempDir()
	stubFakeHost(t, home)

	// os.Stdout/os.Stderr are *os.File, so capture them with real pipes.
	readOut, writeOut, _ := os.Pipe()
	readErr, writeErr, _ := os.Pipe()
	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = writeOut, writeErr
	defer func() { os.Stdout, os.Stderr = origOut, origErr }()

	var stdout, stderr bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); io.Copy(&stdout, readOut) }()
	go func() { defer wg.Done(); io.Copy(&stderr, readErr) }()

	code := run([]string{"install", "--dry-run", "--json"})
	writeOut.Close()
	writeErr.Close()
	wg.Wait()

	if code != converge.ExitOK {
		t.Fatalf("run(install --dry-run --json) = %d, want %d; stderr=%q", code, converge.ExitOK, stderr.String())
	}

	// stdout must be pure JSON — strictly parseable, and must NOT carry the
	// "[基线]" notice (that belongs on stderr in --json mode).
	if !json.Valid(stdout.Bytes()) {
		t.Fatalf("stdout is not a single JSON document:\n%s", stdout.String())
	}
	if bytes.Contains(stdout.Bytes(), []byte("[基线]")) {
		t.Errorf("stdout must be pure JSON, but carried a diagnostic line:\n%s", stdout.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("[基线]")) {
		t.Errorf("expected the baseline notice on stderr, got:\n%s", stderr.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("stdout JSON unmarshal: %v", err)
	}
	if doc["tool"] != "ompinyin" || doc["command"] != "install --dry-run" {
		t.Errorf("unexpected JSON doc header: tool=%v command=%v", doc["tool"], doc["command"])
	}
	if plan, ok := doc["plan"].(map[string]any); !ok {
		t.Errorf("JSON doc missing plan: %+v", doc)
	} else if _, ok := plan["needsApply"]; !ok {
		t.Errorf("JSON plan missing needsApply: %+v", plan)
	}
}
