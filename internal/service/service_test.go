package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFindUnitPrefersOmarchyThenGeneric(t *testing.T) {
	home := t.TempDir()
	SystemUnitDirs = nil
	t.Cleanup(func() { SystemUnitDirs = []string{"/etc/systemd/user", "/usr/lib/systemd/user"} })

	dir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindUnit(home); got != "" {
		t.Errorf("no unit installed should resolve to \"\", got %q", got)
	}

	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("fcitx5.service", "[Service]\nExecStart=/usr/bin/fcitx5\n")
	if got := FindUnit(home); got != "fcitx5.service" {
		t.Errorf("generic unit should be discovered, got %q", got)
	}
	write("omarchy-fcitx5.service", "[Service]\nExecStart=/usr/bin/fcitx5 --disable notificationitem\n")
	if got := FindUnit(home); got != "omarchy-fcitx5.service" {
		t.Errorf("omarchy unit must win (§6.3), got %q", got)
	}
}

func TestFindUnitSeesSystemDirs(t *testing.T) {
	home := t.TempDir()
	sys := t.TempDir()
	SystemUnitDirs = []string{sys}
	t.Cleanup(func() { SystemUnitDirs = []string{"/etc/systemd/user", "/usr/lib/systemd/user"} })

	if err := os.WriteFile(filepath.Join(sys, "omarchy-fcitx5.service"), []byte("[Service]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := FindUnit(home); got != "omarchy-fcitx5.service" {
		t.Errorf("system-level unit file must be discovered, got %q", got)
	}
}

func TestExecStartLine(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "u.service")
	body := `[Unit]
Description=fcitx5
ExecStart=/should/be/ignored

[Service]
ExecStart=/usr/bin/fcitx5 --disable notificationitem

[Install]
WantedBy=default.target
`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ExecStartLine(p); got != "/usr/bin/fcitx5 --disable notificationitem" {
		t.Errorf("must read [Service] only, got %q", got)
	}
	if got := ExecStartLine(filepath.Join(dir, "missing")); got != "" {
		t.Errorf("missing unit file must yield \"\", got %q", got)
	}
	// no ExecStart at all
	p2 := filepath.Join(dir, "no.service")
	os.WriteFile(p2, []byte("[Service]\nRestart=always\n"), 0o644)
	if got := ExecStartLine(p2); got != "" {
		t.Errorf("unit without ExecStart must yield \"\", got %q", got)
	}
}

// TestStopStartPropagateErrors keeps the systemctl error wrapping (the §7
// "失败给修复提示" contract) from regressing into a bare bool.
func TestStopStartPropagateErrors(t *testing.T) {
	orig := Run
	defer func() { Run = orig }()

	Run = func(name string, args ...string) error { return nil }
	if err := Stop("u"); err != nil {
		t.Fatal(err)
	}
	if err := Start("u"); err != nil {
		t.Fatal(err)
	}
	if err := DaemonReload(); err != nil {
		t.Fatal(err)
	}

	Run = func(name string, args ...string) error { return errors.New("exit 1") }
	if err := Start("omarchy-fcitx5.service"); err == nil {
		t.Fatal("start failure must be reported")
	} else if got := err.Error(); got == "" || !contains(got, "journalctl") {
		t.Errorf("start error must hint how to diagnose: %q", got)
	}
	if err := Stop("u"); err == nil {
		t.Error("stop failure must be reported")
	}
	if IsActive("u") {
		t.Error("IsActive must be false when systemctl fails")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestRemoteState(t *testing.T) {
	orig := RunOutput
	defer func() { RunOutput = orig }()

	RunOutput = func(name string, args ...string) ([]byte, error) { return []byte("2\n"), nil }
	n, err := RemoteState()
	if err != nil || n != 2 {
		t.Fatalf("RemoteState = %d, %v", n, err)
	}
	RunOutput = func(name string, args ...string) ([]byte, error) { return []byte("nope"), nil }
	if _, err := RemoteState(); err == nil {
		t.Error("unparsable fcitx5-remote output must error")
	}
	RunOutput = func(name string, args ...string) ([]byte, error) { return nil, errors.New("not running") }
	if _, err := RemoteState(); err == nil {
		t.Error("transport failure must error")
	}
}
