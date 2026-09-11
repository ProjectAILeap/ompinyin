package hidpi

import (
	"strings"
	"testing"
)

func TestDPI(t *testing.T) {
	for _, tt := range []struct {
		scale float64
		want  int
	}{
		{1, 96}, {1.25, 120}, {1.5, 144}, {1.6, 154}, {1.75, 168}, {2, 192},
	} {
		if got := DPI(tt.scale); got != tt.want {
			t.Errorf("DPI(%v)=%d, want %d", tt.scale, got, tt.want)
		}
	}
}

func TestMergeXresourcesPreservesUserContentAndIsIdempotent(t *testing.T) {
	in := "Xft.antialias: true\nXcursor.size: 24\n"
	got, changed := MergeXresources(in, 192)
	if !changed || got == in {
		t.Fatal("expected managed block")
	}
	if again, changed := MergeXresources(got, 192); changed || again != got {
		t.Fatal("merge is not idempotent")
	}
	out, changed, empty := RemoveXresources(got)
	if !changed || empty || out != in {
		t.Fatalf("remove=%q changed=%v empty=%v", out, changed, empty)
	}
}

func TestScaleSources(t *testing.T) {
	json := []byte(`[{"focused":false,"scale":1},{"focused":true,"scale":1.6}]`)
	if got, ok := ScaleFromMonitorsJSON(json); !ok || got != 1.6 {
		t.Fatalf("json=%v,%v", got, ok)
	}
	if got, ok := ScaleFromMonitorsLua([]byte("omarchy_monitor_scale = 1.25")); !ok || got != 1.25 {
		t.Fatalf("lua=%v,%v", got, ok)
	}
	if got, ok := ParseXftDPI([]byte("Xft.dpi:\t192\n")); !ok || got != 192 {
		t.Fatalf("dpi=%v,%v", got, ok)
	}
}

// TestMergeReplacesStaleBlockValue: a block that exists with the wrong DPI must
// be rewritten in place (not duplicated).
func TestMergeReplacesStaleBlockValue(t *testing.T) {
	stale, _ := MergeXresources("Xcursor.theme: Adwaita\n", 96)
	got, changed := MergeXresources(stale, 192)
	if !changed {
		t.Fatal("stale DPI value must be rewritten")
	}
	if n := strings.Count(got, BeginMarker); n != 1 {
		t.Fatalf("expected exactly one managed block, got %d:\n%s", n, got)
	}
	if dpi, ok := ParseXftDPI([]byte(got)); !ok || dpi != 192 {
		t.Fatalf("rewritten DPI=%d,%v", dpi, ok)
	}
	if !strings.Contains(got, "Xcursor.theme: Adwaita") {
		t.Fatal("user content was dropped")
	}
	if !ManagedBlockPresent(got) {
		t.Fatal("ManagedBlockPresent must see the rewritten block")
	}
}

// TestRemoveOnlyBlockDeletesFile: a file that held nothing but the managed
// block reports empty so the caller can remove it.
func TestRemoveOnlyBlockDeletesFile(t *testing.T) {
	only, _ := MergeXresources("", 192)
	out, changed, empty := RemoveXresources(only)
	if !changed || !empty || strings.TrimSpace(out) != "" {
		t.Fatalf("out=%q changed=%v empty=%v", out, changed, empty)
	}
}

// TestUnitContentUsesAbsoluteExecAndNoHardcodedDisplay pins the two fixes for
// the publisher unit: an absolute binary path (systemd --user PATH does not
// include ~/.local/bin) and no DISPLAY=:0 hardcode (Hyprland's XWayland number
// is not guaranteed to be 0).
func TestUnitContentUsesAbsoluteExecAndNoHardcodedDisplay(t *testing.T) {
	prev := ExecPath
	ExecPath = func() string { return "/home/u/.local/bin/ompinyin" }
	t.Cleanup(func() { ExecPath = prev })

	service, path := UnitContent()
	if !strings.Contains(service, "ExecStart=/home/u/.local/bin/ompinyin x11-hidpi-apply") {
		t.Fatalf("unit must bake the absolute binary path:\n%s", service)
	}
	if strings.Contains(service, "/usr/bin/env") {
		t.Fatalf("unit must not rely on the manager PATH:\n%s", service)
	}
	if strings.Contains(service, "DISPLAY=") {
		t.Fatalf("unit must not hardcode DISPLAY:\n%s", service)
	}
	if !strings.Contains(path, "PathChanged=%h/.config/hypr/monitors.lua") {
		t.Fatalf("path unit must watch monitors.lua:\n%s", path)
	}
}

// TestUnitContentQuotesPathsWithSpaces keeps a space-bearing HOME from
// producing an unparseable ExecStart.
func TestUnitContentQuotesPathsWithSpaces(t *testing.T) {
	prev := ExecPath
	ExecPath = func() string { return "/home/a b/bin/ompinyin" }
	t.Cleanup(func() { ExecPath = prev })
	service, _ := UnitContent()
	if !strings.Contains(service, `ExecStart="/home/a b/bin/ompinyin" x11-hidpi-apply`) {
		t.Fatalf("space-bearing path must be quoted:\n%s", service)
	}
}

// TestParseForceZeroScaling pins the hyprctl getoption output shape and the
// "unknown" contract (callers keep Omarchy's default true rather than guess).
func TestParseForceZeroScaling(t *testing.T) {
	if v, ok := ParseForceZeroScaling([]byte("bool: true\nset: true\n")); !ok || !v {
		t.Fatalf("true: v=%v ok=%v", v, ok)
	}
	if v, ok := ParseForceZeroScaling([]byte("bool: false\nset: true\n")); !ok || v {
		t.Fatalf("false: v=%v ok=%v", v, ok)
	}
	if _, ok := ParseForceZeroScaling([]byte(`[{"focused":true,"scale":2}]`)); ok {
		t.Fatal("monitors JSON must not be mistaken for the option")
	}
	if _, ok := ParseForceZeroScaling(nil); ok {
		t.Fatal("empty output must be unknown")
	}
}

// TestParseXftDPIAny: xrdb -query and xprop -root RESOURCE_MANAGER must both be
// accepted (xrdb is not always installed).
func TestParseXftDPIAny(t *testing.T) {
	if v, ok := ParseXftDPIAny([]byte("Xft.dpi:\t192\n")); !ok || v != 192 {
		t.Fatalf("xrdb form: v=%d ok=%v", v, ok)
	}
	prop := []byte(`RESOURCE_MANAGER(STRING) = "Xft.dpi:\t154\nXft.antialias:\t1\nXft.hintstyle:\thintfull"`)
	if v, ok := ParseXftDPIAny(prop); !ok || v != 154 {
		t.Fatalf("xprop form: v=%d ok=%v", v, ok)
	}
	if _, ok := ParseXftDPIAny([]byte(`RESOURCE_MANAGER(STRING) = "Xcursor.size:\t24"`)); ok {
		t.Fatal("must not invent an Xft.dpi")
	}
}

// TestHasForeignXftDPI: ompinyin owns only its marker block, so a competing
// Xft.dpi assignment must be reported (xrdb is last-wins) rather than fought
// over. Comments ('!') and cpp/`#` lines are not resources.
func TestHasForeignXftDPI(t *testing.T) {
	block, _ := MergeXresources("Xft.antialias: 1\n", 192)
	if HasForeignXftDPI(block) {
		t.Fatal("our own block must not count as foreign")
	}
	before, _ := MergeXresources("Xft.dpi: 96\nXft.antialias: 1\n", 192)
	if !HasForeignXftDPI(before) {
		t.Fatal("a bare Xft.dpi before the block is a competing source")
	}
	after, _ := MergeXresources("Xft.antialias: 1\n", 192)
	after += "Xft.dpi: 96\n"
	if !HasForeignXftDPI(after) {
		t.Fatal("a bare Xft.dpi after the block wins and must be reported")
	}
	if HasForeignXftDPI("! comment: Xft.dpi: 96\n# cpp: Xft.dpi: 96\n") {
		t.Fatal("comments/directives must not be treated as assignments")
	}
}

// TestUnitExecPath: the publisher unit must be parsed back to the binary it
// execs, including the quoted form systemdEscape produces for paths with spaces
// (a watcher whose ExecStart vanished fails silently, so this is what the
// doctor check keys off).
func TestUnitExecPath(t *testing.T) {
	svc, _ := UnitContent()
	if got := UnitExecPath(svc); got != ExecPath() {
		t.Errorf("UnitExecPath(generated unit) = %q, want %q", got, ExecPath())
	}
	cases := []struct{ unit, want string }{
		{"[Service]\nExecStart=/usr/local/bin/ompinyin x11-hidpi-apply\n", "/usr/local/bin/ompinyin"},
		{"[Service]\nExecStart=\"/opt/my bin/ompinyin\" x11-hidpi-apply\n", "/opt/my bin/ompinyin"},
		{"[Service]\nExecStart=\n", ""},
		{"[Service]\nExecStart=/usr/bin/fcitx5\n", "/usr/bin/fcitx5"},
		{"[Unit]\nDescription=x\n", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := UnitExecPath(c.unit); got != c.want {
			t.Errorf("UnitExecPath(%q) = %q, want %q", c.unit, got, c.want)
		}
	}
}
