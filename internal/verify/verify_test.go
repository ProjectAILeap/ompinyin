package verify

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ProjectAILeap/ompinyin/internal/catalog"
	"github.com/ProjectAILeap/ompinyin/internal/observe"
	"github.com/ProjectAILeap/ompinyin/internal/service"
)

func convergedHost() *observe.Current {
	return &observe.Current{
		RimeDir:        "/tmp/rime",
		Unit:           "omarchy-fcitx5.service",
		ServiceActive:  true,
		DropInExists:   true,
		DropInOK:       true,
		PinnedHasFc:    true,
		HotkeyOK:       true,
		ProfileHasRime: true,
	}
}

func find(t *testing.T, checks []Check, name string) Check {
	t.Helper()
	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("check %q missing from %v", name, checks)
	return Check{}
}

// TestBuildArtifactsAreAHardFailure locks 评审 P0-3: a missing build artifact
// is exactly the condition that makes the input method type nothing at all, so
// L5 must fail it. The old lenient "懒编译" pass-through made the whole audit
// unable to detect the tool's own worst failure mode.
func TestBuildArtifactsAreAHardFailure(t *testing.T) {
	d := catalog.DefaultDesired()

	c := convergedHost()
	if got := find(t, TerminalState(d, c), "build 产物"); !got.OK {
		t.Errorf("complete artifacts must pass: %s", got.Detail)
	}

	c = convergedHost()
	c.BuildMissing = []string{"rime_ice"}
	got := find(t, TerminalState(d, c), "build 产物")
	if got.OK {
		t.Error("missing build artifact reported as OK — L5 can no longer detect a useless install")
	}
	if got.Detail == "" {
		t.Error("failure must explain itself")
	}
}

// TestGrammarCompiledCheck reads the real compiled schema.
func TestGrammarCompiledCheck(t *testing.T) {
	d := catalog.DefaultDesired()
	dir := t.TempDir()
	build := filepath.Join(dir, "build")
	if err := os.MkdirAll(build, 0o755); err != nil {
		t.Fatal(err)
	}
	c := convergedHost()
	c.RimeDir = dir

	// absent artifact → hard failure (paired with the build check above)
	if got := find(t, TerminalState(d, c), "grammar 编入"); got.OK {
		t.Error("missing compiled schema must not pass the grammar check")
	}

	write := func(body string) {
		if err := os.WriteFile(filepath.Join(build, "rime_ice.schema.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// rime quotes negative values in compiled output — the probe must tolerate it
	write("grammar:\n  language: " + catalog.GrammarLanguage + "\n  collocation_penalty: \"-14\"\n")
	if got := find(t, TerminalState(d, c), "grammar 编入"); !got.OK {
		t.Errorf("quoted penalty must be accepted: %s", got.Detail)
	}
	// model present but non-official penalties → fail (community installer shape)
	write("grammar:\n  language: " + catalog.GrammarLanguage + "\n  collocation_penalty: -7\n")
	if got := find(t, TerminalState(d, c), "grammar 编入"); got.OK {
		t.Error("non-official grammar constants must be flagged")
	}
	// no model at all → fail
	write("grammar:\n  language: other\n")
	if got := find(t, TerminalState(d, c), "grammar 编入"); got.OK {
		t.Error("missing wanxiang grammar must be flagged")
	}
	// Model=false → the check is not part of the terminal state
	noModel := catalog.Desired{Primary: "quanpin", Model: false, Channel: "stable"}
	for _, chk := range TerminalState(noModel, c) {
		if chk.Name == "grammar 编入" {
			t.Error("grammar check must not run when Model=false")
		}
	}
}

// TestIMTriStateRequiresService covers the fcitx5-remote probe and its
// dependency on a running unit.
func TestIMTriStateRequiresService(t *testing.T) {
	d := catalog.DefaultDesired()
	orig := service.RunOutput
	defer func() { service.RunOutput = orig }()

	c := convergedHost()
	c.ServiceActive = false
	c.Unit = ""
	if got := find(t, TerminalState(d, c), "IM 三态"); got.OK {
		t.Error("no running service cannot satisfy the IM check")
	}

	c = convergedHost()
	service.RunOutput = func(string, ...string) ([]byte, error) { return []byte("1"), nil }
	if got := find(t, TerminalState(d, c), "IM 三态"); !got.OK {
		t.Errorf("english state is a valid tri-state reading: %s", got.Detail)
	}
	service.RunOutput = func(string, ...string) ([]byte, error) { return nil, os.ErrClosed }
	if got := find(t, TerminalState(d, c), "IM 三态"); got.OK {
		t.Error("unusable fcitx5-remote must fail the check")
	}
}

// TestDropInCheckRequiresEnabledContent locks 评审 P1-4: presence is not
// enough — the file must actually enable notificationitem.
func TestDropInCheckRequiresEnabledContent(t *testing.T) {
	d := catalog.DefaultDesired()

	c := convergedHost()
	c.DropInOK = false
	c.DropInExists = true
	c.DropInPath = "/home/x/.config/systemd/user/omarchy-fcitx5.service.d/ompinyin-notificationitem.conf"
	got := find(t, TerminalState(d, c), "托盘 drop-in")
	if got.OK {
		t.Error("a drop-in that still disables notificationitem must fail")
	}
	if got.Detail == "" {
		t.Error("detail must explain the content problem")
	}

	c.DropInExists = false
	c.DropInPath = ""
	if got := find(t, TerminalState(d, c), "托盘 drop-in"); got.OK {
		t.Error("missing drop-in must fail")
	}
}

// TestDoctorAddsHostChecks: doctor is the superset (service, red line, trigger
// keys, legacy dir) on top of the terminal-state audit.
func TestDoctorAddsHostChecks(t *testing.T) {
	d := catalog.DefaultDesired()
	c := convergedHost()
	c.LegacyDirExists = true
	c.HotkeyOK = false

	checks := Doctor(d, c)
	names := map[string]bool{}
	for _, chk := range checks {
		names[chk.Name] = true
	}
	for _, want := range []string{"服务", "环境变量红线", "触发键", "遗留目录", "build 产物"} {
		if !names[want] {
			t.Errorf("doctor is missing the %q check", want)
		}
	}
	if got := find(t, checks, "遗留目录"); got.OK {
		t.Error("~/.config/fcitx/rime present must be reported (§6.5 duplicate data)")
	}
	if got := find(t, checks, "触发键"); got.OK {
		t.Error("wrong trigger keys must be reported")
	}
}
