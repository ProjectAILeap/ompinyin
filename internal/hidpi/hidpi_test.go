package hidpi

import "testing"

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
