package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	stubNoStray(t)
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

// TestStartRetriesAfterResetFailed: a unit that exited immediately (another
// fcitx5 owning org.fcitx.Fcitx5) trips systemd's start rate limit under
// Restart=always, and then even a manual start fails with "start request
// repeated too quickly" — so ompinyin must clear the limit and retry once
// instead of telling the user to run a command that cannot work.
func TestStartRetriesAfterResetFailed(t *testing.T) {
	stubNoStray(t)
	orig := Run
	defer func() { Run = orig }()

	t.Run("burst limit cleared, retry succeeds", func(t *testing.T) {
		var calls []string
		attempts := 0
		Run = func(name string, args ...string) error {
			calls = append(calls, strings.Join(args, " "))
			switch args[1] {
			case "reset-failed":
				return nil
			case "start":
				attempts++
				if attempts == 1 {
					return errors.New("start request repeated too quickly")
				}
			}
			return nil
		}
		if err := Start("omarchy-fcitx5.service"); err != nil {
			t.Fatalf("Start must succeed after reset-failed + retry, got %v", err)
		}
		want := []string{"--user is-active --quiet omarchy-fcitx5.service", "--user start omarchy-fcitx5.service", "--user reset-failed omarchy-fcitx5.service", "--user start omarchy-fcitx5.service"}
		if strings.Join(calls, " | ") != strings.Join(want, " | ") {
			t.Errorf("call sequence = %v, want %v", calls, want)
		}
	})

	t.Run("retry still failing names the bus-name holder", func(t *testing.T) {
		Run = func(name string, args ...string) error {
			if args[1] == "reset-failed" {
				return nil
			}
			return errors.New("exit 1")
		}
		err := Start("omarchy-fcitx5.service")
		if err == nil {
			t.Fatal("a failing retry must be reported")
		}
		for _, want := range []string{"reset-failed", "pgrep -x fcitx5", "journalctl"} {
			if !contains(err.Error(), want) {
				t.Errorf("error must mention %q, got %q", want, err.Error())
			}
		}
	})

	t.Run("reset-failed failing is reported too", func(t *testing.T) {
		Run = func(name string, args ...string) error { return errors.New("exit 1") }
		err := Start("omarchy-fcitx5.service")
		if err == nil {
			t.Fatal("a failing start must be reported")
		}
		for _, want := range []string{"reset-failed", "journalctl"} {
			if !contains(err.Error(), want) {
				t.Errorf("error must mention %q, got %q", want, err.Error())
			}
		}
	})
}

// stubNoStray keeps Start hermetic: without it a dev machine with a live fcitx5
// would make startUnit shell out to the real pkill.
func stubNoStray(t *testing.T) {
	t.Helper()
	origRunning, origCount, origKill := FcitxRunning, FcitxCount, KillStray
	t.Cleanup(func() { FcitxRunning, FcitxCount, KillStray = origRunning, origCount, origKill })
	FcitxRunning = func() bool { return false }
	FcitxCount = func() int { return 1 }
	KillStray = func() error { return nil }
}

// TestStartClearsStrayBeforeStarting locks the root-cause fix: a stray fcitx5
// owns org.fcitx.Fcitx5 while the unit is down, so a bare `systemctl start`
// spawns an instance that exits 0 immediately — Restart=always loops it into
// start-limit-hit while `start` still reports success. The stray must be ended
// before the unit starts.
func TestStartClearsStrayBeforeStarting(t *testing.T) {
	origRun, origRunning, origKill := Run, FcitxRunning, KillStray
	t.Cleanup(func() { Run, FcitxRunning, KillStray = origRun, origRunning, origKill })

	var seq []string
	running := true
	FcitxRunning = func() bool { return running }
	KillStray = func() error {
		seq = append(seq, "kill")
		running = false // the stray releases the bus name
		return nil
	}
	Run = func(name string, args ...string) error {
		switch args[1] {
		case "is-active":
			return errors.New("inactive") // unit down → the live fcitx5 is unmanaged
		case "start":
			seq = append(seq, "start")
		}
		return nil
	}

	if err := Start("omarchy-fcitx5.service"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(seq, ","); got != "kill,start" {
		t.Errorf("stray must be cleared before the unit starts, sequence = %q", got)
	}
}

// TestStartDoesNotKillAHealthyUnit: Start is also the idempotent
// close-the-window call, so an already-active unit must keep its instance.
func TestStartDoesNotKillAHealthyUnit(t *testing.T) {
	origRun, origRunning, origKill := Run, FcitxRunning, KillStray
	t.Cleanup(func() { Run, FcitxRunning, KillStray = origRun, origRunning, origKill })

	killed := false
	FcitxRunning = func() bool { return true } // the unit's own instance
	KillStray = func() error { killed = true; return nil }
	Run = func(name string, args ...string) error { return nil } // is-active → active

	if err := Start("omarchy-fcitx5.service"); err != nil {
		t.Fatal(err)
	}
	if killed {
		t.Error("an already-active unit must not have its instance killed")
	}
}

// TestStartReportsAStrayItCannotClear: failing to clear the stray is a real
// failure, not a silent fall-through into a start that cannot work.
func TestStartReportsAStrayItCannotClear(t *testing.T) {
	origRun, origRunning, origKill := Run, FcitxRunning, KillStray
	t.Cleanup(func() { Run, FcitxRunning, KillStray = origRun, origRunning, origKill })

	FcitxRunning = func() bool { return true }
	KillStray = func() error { return errors.New("permission denied") }
	Run = func(name string, args ...string) error { return errors.New("inactive") }

	err := Start("omarchy-fcitx5.service")
	if err == nil {
		t.Fatal("a stray that could not be cleared must fail the start")
	}
	for _, want := range []string{"清理游离 fcitx5", "pgrep -x fcitx5"} {
		if !contains(err.Error(), want) {
			t.Errorf("error must mention %q, got %q", want, err)
		}
	}
}
