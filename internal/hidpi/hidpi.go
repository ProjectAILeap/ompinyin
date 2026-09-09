// Package hidpi converges the X11 ClassicUI DPI bridge.  XWayland reports
// 96dpi through RandR, so fcitx5 must instead receive Xft.dpi via xrdb.
package hidpi

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	BeginMarker = "# >>> ompinyin-managed >>>"
	EndMarker   = "# <<< ompinyin-managed <<<"
	ServiceName = "ompinyin-x11-hidpi.service"
	PathName    = "ompinyin-x11-hidpi.path"
)

// DPI uses the X11/CSS logical 96dpi baseline, not physical panel DPI.
func DPI(scale float64) int { return int(math.Floor(96*scale + .5)) }

// MergeXresources owns only its marked Xft.dpi block and leaves all other
// Xresources lines byte-for-byte intact.
func MergeXresources(in string, dpi int) (string, bool) {
	block := fmt.Sprintf("%s\nXft.dpi: %d\n%s", BeginMarker, dpi, EndMarker)
	re := regexp.MustCompile(`(?ms)^` + regexp.QuoteMeta(BeginMarker) + `\n.*?^` + regexp.QuoteMeta(EndMarker) + `(?:\n|$)`)
	if loc := re.FindStringIndex(in); loc != nil {
		out := in[:loc[0]] + block + "\n" + in[loc[1]:]
		out = strings.TrimRight(out, "\n") + "\n"
		return out, out != in
	}
	if in == "" {
		return block + "\n", true
	}
	out := strings.TrimRight(in, "\n") + "\n\n" + block + "\n"
	return out, out != in
}

// ManagedBlockPresent reports whether the ompinyin-scoped Xft.dpi block exists
// in the given Xresources content. It is the opt-out predicate: the mode's
// artifacts are exactly the block and the two units, so presence of either
// means a previously opted-in host must be withdrawn.
func ManagedBlockPresent(in string) bool {
	re := regexp.MustCompile(`(?ms)^` + regexp.QuoteMeta(BeginMarker) + `\n.*?^` + regexp.QuoteMeta(EndMarker) + `(?:\n|$)`)
	return re.MatchString(in)
}

// RemoveXresources removes only the marked block. empty reports whether the
// remaining file has no user content and may safely be removed.
func RemoveXresources(in string) (out string, changed, empty bool) {
	re := regexp.MustCompile(`(?ms)^` + regexp.QuoteMeta(BeginMarker) + `\n.*?^` + regexp.QuoteMeta(EndMarker) + `(?:\n|$)`)
	out = strings.TrimRight(re.ReplaceAllString(in, ""), "\n")
	if out != "" {
		out += "\n"
	}
	return out, out != in, strings.TrimSpace(out) == ""
}

// ScaleFromMonitorsJSON returns the focused monitor scale from hyprctl JSON.
func ScaleFromMonitorsJSON(b []byte) (float64, bool) {
	var monitors []struct {
		Focused bool    `json:"focused"`
		Scale   float64 `json:"scale"`
	}
	if json.Unmarshal(b, &monitors) != nil {
		return 0, false
	}
	for _, m := range monitors {
		if m.Focused && m.Scale > 0 {
			return m.Scale, true
		}
	}
	return 0, false
}

var luaScale = regexp.MustCompile(`(?m)omarchy_monitor_scale\s*=\s*([0-9]+(?:\.[0-9]+)?)`)

func ScaleFromMonitorsLua(b []byte) (float64, bool) {
	m := luaScale.FindSubmatch(b)
	if len(m) != 2 {
		return 0, false
	}
	v, err := strconv.ParseFloat(string(m[1]), 64)
	return v, err == nil && v > 0
}

// ParseXftDPI reads the value published by xrdb -query.
func ParseXftDPI(b []byte) (int, bool) {
	for _, line := range strings.Split(string(b), "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 || !strings.EqualFold(strings.TrimSpace(parts[0]), "Xft.dpi") {
			continue
		}
		v, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		return v, err == nil
	}
	return 0, false
}

func XresourcesPath(home string) string { return filepath.Join(home, ".Xresources") }
func UnitDir(home string) string        { return filepath.Join(home, ".config", "systemd", "user") }
func ServicePath(home string) string    { return filepath.Join(UnitDir(home), ServiceName) }
func PathPath(home string) string       { return filepath.Join(UnitDir(home), PathName) }

// UnitContent uses the installed command as a small, idempotent publisher.
// The path unit lets an Omarchy scale edit refresh X11 candidates without
// managing monitors.lua itself.
func UnitContent() (service, path string) {
	service = `[Unit]
Description=ompinyin X11 HiDPI resources

[Service]
Type=oneshot
Environment=DISPLAY=:0
ExecStart=/usr/bin/env ompinyin x11-hidpi-apply

[Install]
WantedBy=graphical-session.target
`
	path = `[Unit]
Description=Watch Hyprland monitor scale for ompinyin X11 HiDPI

[Path]
PathChanged=%h/.config/hypr/monitors.lua
Unit=ompinyin-x11-hidpi.service

[Install]
WantedBy=default.target
`
	return service, path
}

// ReadScale implements the documented read-only source fallback order.
func ReadScale(monitorsJSON, monitorsLua, xresources []byte) (float64, string) {
	if s, ok := ScaleFromMonitorsJSON(monitorsJSON); ok {
		return s, "hyprctl"
	}
	if s, ok := ScaleFromMonitorsLua(monitorsLua); ok {
		return s, "monitors.lua"
	}
	if dpi, ok := ParseXftDPI(xresources); ok && dpi > 0 {
		return float64(dpi) / 96, "Xresources"
	}
	return 1, "default"
}

// ReadFile exists so production code has a narrow seam in tests.
var ReadFile = os.ReadFile

// Run is the command seam for hyprctl and xrdb.  Keeping it here makes the
// L4 bridge fully fakeable in T0 without executing against the developer's X
// server.
var Run = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}
