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

// managedBlockRe matches one complete ompinyin-scoped block, including its
// trailing newline (or EOF). Compiled once: every read of ~/.Xresources runs
// it (observe, plan, converge).
var managedBlockRe = regexp.MustCompile(`(?ms)^` + regexp.QuoteMeta(BeginMarker) + `\n.*?^` + regexp.QuoteMeta(EndMarker) + `(?:\n|$)`)

// DPI uses the X11/CSS logical 96dpi baseline, not physical panel DPI.
func DPI(scale float64) int { return int(math.Floor(96*scale + .5)) }

// MergeXresources owns only its marked Xft.dpi block and leaves all other
// Xresources lines byte-for-byte intact.
func MergeXresources(in string, dpi int) (string, bool) {
	block := fmt.Sprintf("%s\nXft.dpi: %d\n%s", BeginMarker, dpi, EndMarker)
	if loc := managedBlockRe.FindStringIndex(in); loc != nil {
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
	return managedBlockRe.MatchString(in)
}

// RemoveXresources removes only the marked block. empty reports whether the
// remaining file has no user content and may safely be removed.
func RemoveXresources(in string) (out string, changed, empty bool) {
	out = strings.TrimRight(managedBlockRe.ReplaceAllString(in, ""), "\n")
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

// xpropXftDPIRe reads Xft.dpi out of `xprop -root RESOURCE_MANAGER`, where the
// property is C-escaped (e.g. `Xft.dpi:\t192`). xorg-xrdb is not always
// installed (the machine this was written on publishes via xprop), but xprop
// usually is — this keeps the default diagnosis honest instead of reporting
// "no X session" whenever xrdb happens to be absent.
var xpropXftDPIRe = regexp.MustCompile(`(?i)Xft\.dpi:\s*(?:\\[A-Za-z])?\s*([0-9]+)`)

// ParseXftDPIAny accepts either `xrdb -query` output or `xprop -root
// RESOURCE_MANAGER` output.
func ParseXftDPIAny(b []byte) (int, bool) {
	if v, ok := ParseXftDPI(b); ok {
		return v, true
	}
	if m := xpropXftDPIRe.FindSubmatch(b); len(m) == 2 {
		v, err := strconv.Atoi(string(m[1]))
		return v, err == nil
	}
	return 0, false
}

func XresourcesPath(home string) string { return filepath.Join(home, ".Xresources") }
func UnitDir(home string) string        { return filepath.Join(home, ".config", "systemd", "user") }
func ServicePath(home string) string    { return filepath.Join(UnitDir(home), ServiceName) }
func PathPath(home string) string       { return filepath.Join(UnitDir(home), PathName) }

// ExecPath is the absolute path of the running ompinyin binary, baked into the
// publisher unit. `/usr/bin/env ompinyin` would depend on the systemd user
// manager's PATH, which does not reliably include ~/.local/bin — the install
// location the README recommends — so the unit would fail silently (127). A
// var so T0 can pin a deterministic path.
var ExecPath = func() string {
	if p, err := os.Executable(); err == nil && p != "" {
		return p
	}
	return "ompinyin"
}

// systemdEscape quotes an argv token the way systemd's ExecStart parser
// expects. Paths without shell metacharacters stay readable.
func systemdEscape(s string) string {
	if strings.ContainsAny(s, " \t\"'\\") {
		return strconv.Quote(s)
	}
	return s
}

// UnitContent uses the installed binary as a small, idempotent publisher.
// The path unit lets an Omarchy scale edit refresh X11 candidates without
// managing monitors.lua itself. DISPLAY is deliberately inherited from the
// systemd user manager (imported by the graphical session): hardcoding :0 is
// wrong when Hyprland's XWayland picked another display number.
func UnitContent() (service, path string) {
	service = fmt.Sprintf(`[Unit]
Description=ompinyin X11 HiDPI resources

[Service]
Type=oneshot
# DISPLAY is inherited from the graphical session; never hardcode :0.
ExecStart=%s x11-hidpi-apply

[Install]
WantedBy=graphical-session.target
`, systemdEscape(ExecPath()))
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

// ParseForceZeroScaling reads `hyprctl getoption xwayland:force_zero_scaling`.
//
// true (Omarchy's default, /usr/share/omarchy/default/hypr/envs.lua) means
// XWayland windows are NOT scaled by the compositor: every toolkit must scale
// itself, so the X11 ClassicUI candidate needs Xft.dpi. false means Hyprland
// scales X11 windows itself (pixelated but sized) — publishing Xft.dpi then
// double-scales every X11 app, candidate included. unknown is reported so the
// caller can keep the Omarchy default instead of guessing.
func ParseForceZeroScaling(b []byte) (value, known bool) {
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "bool:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "bool:")) == "true", true
		}
	}
	return false, false
}

// HasForeignXftDPI reports whether the Xresources content assigns Xft.dpi
// outside the ompinyin-scoped block. ompinyin owns only its marked block and
// never removes a competing line; xrdb applies assignments in file order, so a
// competing line AFTER the block wins. Surface that as a conflict instead of
// silently fighting over it (or pretending the config is converged).
func HasForeignXftDPI(in string) bool {
	stripped := managedBlockRe.ReplaceAllString(in, "")
	for _, line := range strings.Split(stripped, "\n") {
		t := strings.TrimSpace(line)
		// '!' is an X resource comment; '#' lines are cpp directives/our
		// markers and are ignored by xrdb (see xrdb.c GetEntries).
		if t == "" || strings.HasPrefix(t, "!") || strings.HasPrefix(t, "#") {
			continue
		}
		key, _, ok := strings.Cut(t, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "Xft.dpi") {
			return true
		}
	}
	return false
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

// Run is the command seam for hyprctl and xrdb.  Keeping it here makes the
// L4 bridge fully fakeable in T0 without executing against the developer's X
// server.
var Run = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}
