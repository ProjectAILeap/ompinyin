// Package service wraps systemctl --user operations with omarchy unit
// discovery: omarchy-fcitx5.service preferred, generic fcitx5.service
// fallback (§6.3).
package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Run is the exec seam for tests (fake systemctl in T0).
var Run = func(name string, args ...string) error {
	c := exec.Command(name, args...)
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

// Start starts the unit.
func Start(unit string) error {
	if err := Run("systemctl", "--user", "start", unit); err != nil {
		return fmt.Errorf("start %s: %w (check `journalctl --user -u %s`)", unit, err, unit)
	}
	return nil
}

// IsActive reports whether the unit is currently active.
func IsActive(unit string) bool {
	err := Run("systemctl", "--user", "is-active", "--quiet", unit)
	return err == nil
}

// DaemonReload runs systemctl --user daemon-reload (after drop-in writes).
func DaemonReload() error {
	return Run("systemctl", "--user", "daemon-reload")
}

// RunOutput is the output seam for tests.
var RunOutput = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
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
