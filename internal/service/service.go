// Package service wraps systemctl --user operations with omarchy unit
// discovery: omarchy-fcitx5.service preferred, generic fcitx5.service
// fallback (§6.3).
package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ProjectAILeap/ompinyin/internal/execcmd"
)

// Run is the exec seam for tests (fake systemctl in T0).
var Run = func(name string, args ...string) error {
	c := execcmd.Command(name, args...)
	c.Stdout = os.Stderr
	c.Stderr = os.Stderr
	return c.Run()
}

// SystemUnitDirs are the non-$HOME locations searched for the fcitx5 user
// unit. A var so T0 tests stay hermetic: on a real Omarchy host
// /usr/lib/systemd/user/omarchy-fcitx5.service always exists, which would
// otherwise make every "generic unit" scenario resolve to the omarchy one.
var SystemUnitDirs = []string{"/etc/systemd/user", "/usr/lib/systemd/user"}

// unitCandidates returns possible unit file locations for name.
func unitCandidates(home, name string) []string {
	out := []string{filepath.Join(home, ".config", "systemd", "user", name)}
	for _, d := range SystemUnitDirs {
		out = append(out, filepath.Join(d, name))
	}
	return out
}

// FindUnit discovers the fcitx5 user unit: omarchy-fcitx5.service first,
// then the generic fcitx5.service. Returns "" if neither is installed.
func FindUnit(home string) string {
	for _, name := range []string{"omarchy-fcitx5.service", "fcitx5.service"} {
		if UnitFilePath(home, name) != "" {
			return name
		}
	}
	return ""
}

// UnitFilePath returns the first existing unit file path for name ("" if
// none of the candidate locations has it).
func UnitFilePath(home, name string) string {
	for _, p := range unitCandidates(home, name) {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// ExecStartLine extracts the ExecStart= value from the unit file at path
// (only the [Service] section; "" when absent/unreadable).
func ExecStartLine(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	inService := false
	for _, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			inService = t == "[Service]"
			continue
		}
		if inService && strings.HasPrefix(t, "ExecStart=") {
			return strings.TrimSpace(strings.TrimPrefix(t, "ExecStart="))
		}
	}
	return ""
}

// Stop stops the unit (the single stop window of §6.0).
func Stop(unit string) error {
	if err := Run("systemctl", "--user", "stop", unit); err != nil {
		return fmt.Errorf("stop %s: %w", unit, err)
	}
	return nil
}

// Start starts the unit. Teardown must not be cancellable: after the first
// SIGINT the run context is already canceled, so a restart issued from a defer
// would return context.Canceled without spawning anything and leave the user
// with no input method (§16 invariant 14, 评审 P0-4).
//
// A failed start then clears systemd's rate limit (`reset-failed`) and retries
// exactly once: a unit whose process exits immediately — e.g. because another
// fcitx5 already owned org.fcitx.Fcitx5 — trips StartLimitBurst under
// Restart=always, after which every start fails with "start request repeated
// too quickly". The manual `systemctl --user start` the caller prints could not
// work in that state either.
//
// The rate limit is the SYMPTOM; the stray instance owning the bus name is the
// cause, so startUnit ends the stray first (KillStray). Without that reset the
// retry only spawns another fcitx5 that exits 0 immediately — and because
// `systemctl start` returns 0 as soon as a Type=simple process is spawned, the
// run would report success while Restart=always loops forever.
func Start(unit string) error {
	var err error
	execcmd.Cleanup(func() { err = startUnit(unit) })
	return err
}

func startUnit(unit string) error {
	// Only when the unit is down: only then can the only live fcitx5 be an
	// unmanaged one. Start is also the idempotent close-the-window call on a
	// healthy host, where killing would take the running instance with it.
	if !IsActive(unit) && FcitxRunning() {
		if err := KillStray(); err != nil {
			return fmt.Errorf("清理游离 fcitx5 失败：%w（`pgrep -x fcitx5` / `journalctl --user -u %s`）", err, unit)
		}
	}
	if first := Run("systemctl", "--user", "start", unit); first != nil {
		if rerr := Run("systemctl", "--user", "reset-failed", unit); rerr != nil {
			return fmt.Errorf("start %s: %w (reset-failed 亦失败：%v；check `journalctl --user -u %s`)", unit, first, rerr, unit)
		}
		if err := Run("systemctl", "--user", "start", unit); err != nil {
			return fmt.Errorf("start %s: %w (已 reset-failed 并重试一次；若仍失败，多半是有非单元实例占着 org.fcitx.Fcitx5：`pgrep -x fcitx5`；check `journalctl --user -u %s`)", unit, err, unit)
		}
	}
	return nil
}

// IsActive reports whether the unit is currently active.
func IsActive(unit string) bool {
	err := Run("systemctl", "--user", "is-active", "--quiet", unit)
	return err == nil
}

// DaemonReload runs systemctl --user daemon-reload (after drop-in writes), with
// the same cancellation immunity as Start: a half-applied unit reload in a
// teardown path would strand systemd on stale unit state.
func DaemonReload() error {
	var err error
	execcmd.Cleanup(func() { err = Run("systemctl", "--user", "daemon-reload") })
	return err
}

// FcitxRunning reports whether any fcitx5 process is alive. Deliberately a
// plain process check: probing through fcitx5-remote / the session bus would
// D-Bus-activate an UNMANAGED fcitx5 whenever the service is down, and that
// instance then owns org.fcitx.Fcitx5 so the unit can never start again
// ("another fcitx already running" → Restart=always → start-limit-hit).
var FcitxRunning = func() bool {
	return execcmd.Command("pgrep", "-x", "fcitx5").Run() == nil
}

// FcitxCount counts live fcitx5 processes. Exactly one is the unit's own
// instance; a second one while the unit claims to be active means an unmanaged
// instance coexists, so the unit is flapping (Restart=always) rather than
// serving. A plain process probe for the same reason as FcitxRunning.
var FcitxCount = func() int {
	out, err := execcmd.Command("pgrep", "-c", "-x", "fcitx5").Output()
	if err != nil {
		return 0 // pgrep -c exits 1 when nothing matches
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return n
}

// KillStray terminates an unmanaged fcitx5 (the remedy the plan prints) and
// waits for it to release org.fcitx.Fcitx5, so the unit's own instance can win
// the name. pkill only guarantees the signal was sent — starting immediately
// would race the dying process for the bus name and re-enter the flap loop.
// Seam for T0.
var KillStray = func() error {
	if err := execcmd.Command("pkill", "-x", "fcitx5").Run(); err != nil {
		return err
	}
	for i := 0; i < 20 && FcitxRunning(); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

// RunOutput is the output seam for tests.
var RunOutput = func(name string, args ...string) ([]byte, error) {
	return execcmd.Command(name, args...).Output()
}

// RemoteState runs `fcitx5-remote` and returns 0/1/2 (0=inactive, 1=EN, 2=中文).
func RemoteState() (int, error) {
	out, err := RunOutput("fcitx5-remote")
	if err != nil {
		return -1, err
	}
	s := strings.TrimSpace(string(out))
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return -1, fmt.Errorf("fcitx5-remote output %q", s)
	}
	return n, nil
}
