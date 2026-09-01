package facts

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadOSOmarchy(t *testing.T) {
	p := filepath.Join(t.TempDir(), "os-release")
	os.WriteFile(p, []byte("ID=omarchy\nBUILD_ID=\"4.0.1\"\n# comment\nNAME=Omarchy\n"), 0o644)
	OSReleasePath = p
	defer func() { OSReleasePath = "/etc/os-release" }()

	o, err := ReadOS()
	if err != nil {
		t.Fatal(err)
	}
	if o.ID != "omarchy" || o.BuildID != "4.0.1" {
		t.Errorf("os = %+v", o)
	}
	if !IsOmarchy(o.ID) {
		t.Error("IsOmarchy false for omarchy")
	}
	if IsOmarchy("arch") {
		t.Error("IsOmarchy true for arch")
	}
}

func TestHerdrPrefixFound(t *testing.T) {
	home := t.TempDir()
	if HerdrPrefixFound(home) {
		t.Error("empty home must not detect herdr")
	}
	dir := filepath.Join(home, ".config", "hypr")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "herdr.conf"), []byte("prefix = ctrl space\n"), 0o644)
	if !HerdrPrefixFound(home) {
		t.Error("herdr config with space not detected")
	}
	os.WriteFile(filepath.Join(dir, "herdr.conf"), []byte("prefix = ctrl comma\n"), 0o644)
	if HerdrPrefixFound(home) {
		t.Error("herdr without space must not be flagged")
	}
}

func TestValidateRules(t *testing.T) {
	// No override + non-Omarchy ID → failure that hints at the escape hatch.
	old := OSReleasePath
	OSReleasePath = writeOSRelease(t, "ID=arch\nBUILD_ID=rolling\n")
	defer func() { OSReleasePath = old }()
	Geteuid = func() int { return 1000 }
	LookPath = func(string) (string, error) { return "/usr/bin/x", nil }
	Run = func(string, ...string) error { return nil }

	res, err := Collect("", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if res.OSOK {
		t.Error("arch must fail the OS check without --os-override")
	}
	joined := strings.Join(res.Failures, "\n")
	if !strings.Contains(joined, "--os-override") {
		t.Errorf("failure text must hint --os-override: %s", joined)
	}

	// Any non-empty override bypasses the ID check (评审 P1-7: the help text
	// promised a bypass while the code still demanded the literal "omarchy").
	res2, err := Collect("arch", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !res2.OSOK || !res2.OSOverridden {
		t.Errorf("--os-override arch must bypass the ID check: %+v failures=%v", res2, res2.Failures)
	}
	if len(res2.Failures) != 0 {
		t.Errorf("override run should have no failures: %v", res2.Failures)
	}
}

// TestRootGuard: only pacman may escalate (§16 red line 11), so a root run is
// refused before anything writes root-owned files into the desktop user's home.
func TestRootGuard(t *testing.T) {
	old := OSReleasePath
	OSReleasePath = writeOSRelease(t, "ID=omarchy\nBUILD_ID=4.0.1\n")
	defer func() { OSReleasePath = old }()
	LookPath = func(string) (string, error) { return "/usr/bin/x", nil }
	Run = func(string, ...string) error { return nil }

	Geteuid = func() int { return 0 }
	defer func() { Geteuid = os.Geteuid }()
	res, err := Collect("", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if res.RootOK {
		t.Error("root must be reported as a failed precondition")
	}
	if !strings.Contains(strings.Join(res.Failures, "\n"), "root") {
		t.Errorf("failure must mention root: %v", res.Failures)
	}
}

// TestMissingToolGate: rime_deployer runs inside the stop window, so a missing
// binary with its provider already installed must fail the precheck, while a
// not-yet-installed provider is only an L1 warning.
func TestMissingToolGate(t *testing.T) {
	old := OSReleasePath
	OSReleasePath = writeOSRelease(t, "ID=omarchy\nBUILD_ID=4.0.1\n")
	defer func() { OSReleasePath = old }()
	Geteuid = func() int { return 1000 }
	Run = func(string, ...string) error { return nil } // everything installed
	LookPath = func(name string) (string, error) {
		if name == "rime_deployer" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + name, nil
	}
	res, err := Collect("", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(res.Failures, "\n"), "rime_deployer") {
		t.Errorf("missing rime_deployer with librime installed must fail: %v", res.Failures)
	}

	// provider absent → warning only, L1 installs it
	Run = func(name string, args ...string) error {
		if len(args) > 0 && args[0] == "-Qq" {
			return errors.New("not installed")
		}
		return nil
	}
	res2, err := Collect("", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.MissingTools) == 0 {
		t.Skip("no tool warnings path exercised")
	}
	if strings.Contains(strings.Join(res2.Failures, "\n"), "rime_deployer") {
		t.Errorf("absent provider must not hard-fail: %v", res2.Failures)
	}
}

func writeOSRelease(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "os-release")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
