package profile

import (
	"strings"
	"testing"
)

const fullProfile = `[Groups/0]
Name=Default
Default Layout=us
DefaultIM=rime

[Groups/0/Items/0]
Name=keyboard-us
Layout=

[Groups/0/Items/1]
Name=rime
Layout=

[GroupOrder]
0=Groups/0
`

// Scenario 4: already ok → no change (minimal edit).
func TestEnsureRimeAlreadyOK(t *testing.T) {
	out, changed, err := EnsureRime(fullProfile)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Errorf("no-op profile reported changed:\n%s", out)
	}
}

// Scenario 1: missing file (empty content) → full default profile.
func TestEnsureRimeMissing(t *testing.T) {
	out, changed, err := EnsureRime("")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("empty profile must change")
	}
	// strict format: no spaces around '='
	if strings.Contains(out, " = ") {
		t.Errorf("strict write violated:\n%s", out)
	}
	for _, want := range []string{"DefaultIM=rime", "Name=rime", "Layout=", "[GroupOrder]"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if names := ItemNames(out); len(names) != 2 || names[0] != "keyboard-us" || names[1] != "rime" {
		t.Errorf("items = %v", names)
	}
	// GroupOrder rendered last
	if strings.LastIndex(out, "[GroupOrder]") < strings.LastIndex(out, "[Groups/0/Items/1]") {
		t.Errorf("GroupOrder must be last:\n%s", out)
	}
}

// Scenario 2: no rime registered → append item + set DefaultIM.
func TestEnsureRimeNoRime(t *testing.T) {
	in := `[Groups/0]
Name=Default
Default Layout=us
DefaultIM=keyboard-us

[Groups/0/Items/0]
Name=keyboard-us
Layout=

[GroupOrder]
0=Groups/0
`
	out, changed, err := EnsureRime(in)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("must change")
	}
	if names := ItemNames(out); len(names) != 2 || names[1] != "rime" {
		t.Fatalf("items = %v", names)
	}
	if !strings.Contains(out, "DefaultIM=rime") {
		t.Errorf("DefaultIM not fixed:\n%s", out)
	}
	// tolerant read: original spacing preserved in output content (strict rewrite)
	if strings.Contains(out, "Name=keyboard-us\nLayout") && !strings.Contains(out, "[Groups/0/Items/1]") {
		t.Errorf("rime item missing:\n%s", out)
	}
}

// Scenario 3: rime present but DefaultIM wrong → only fix DefaultIM.
func TestEnsureRimeWrongDefaultIM(t *testing.T) {
	in := `[Groups/0]
Name=Default
Default Layout=us
DefaultIM=keyboard-us

[Groups/0/Items/0]
Name=keyboard-us
Layout=

[Groups/0/Items/1]
Name=rime
Layout=

[GroupOrder]
0=Groups/0
`
	out, changed, err := EnsureRime(in)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("must change DefaultIM")
	}
	if names := ItemNames(out); len(names) != 2 {
		t.Errorf("must not duplicate the rime item: %v", names)
	}
	if !strings.Contains(out, "DefaultIM=rime") {
		t.Errorf("DefaultIM not fixed:\n%s", out)
	}
	// no extra item appended
	if strings.Count(out, "[Groups/0/Items/") != 2 {
		t.Errorf("item count wrong:\n%s", out)
	}
}

func TestRemoveRime(t *testing.T) {
	out, changed := RemoveRime(fullProfile)
	if !changed {
		t.Fatal("must change")
	}
	if strings.Contains(out, "Name=rime") {
		t.Errorf("rime item still present:\n%s", out)
	}
	if strings.Contains(out, "DefaultIM=rime") {
		t.Errorf("DefaultIM still rime:\n%s", out)
	}
	if names := ItemNames(out); len(names) != 1 || names[0] != "keyboard-us" {
		t.Errorf("items = %v", names)
	}
	// idempotent
	if _, changed2 := RemoveRime(out); changed2 {
		t.Error("RemoveRime not idempotent")
	}
}

// TestRenderGroupOrderLast locks §6.1: GroupOrder must be rendered LAST even
// when the input file carries it between item sections. (Regression: Render
// computed the reordered slice and then rendered the original one.)
func TestRenderGroupOrderLast(t *testing.T) {
	in := `[Groups/0]
Name=Default
Default Layout=us
DefaultIM=keyboard-us

[Groups/0/Items/0]
Name=keyboard-us
Layout=

[GroupOrder]
0=Groups/0

[Groups/0/Items/1]
Name=rime
Layout=
`
	out, changed, err := EnsureRime(in)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("input is not the converged shape, must change")
	}
	gi, lastItem := strings.LastIndex(out, "[GroupOrder]"), strings.LastIndex(out, "[Groups/0/Items/")
	if gi < lastItem {
		t.Errorf("GroupOrder must be rendered last:\n%s", out)
	}
	if names := ItemNames(out); len(names) != 2 || names[1] != "rime" {
		t.Errorf("items = %v", names)
	}
}

// TestRemoveRimeRenumbersItems locks review P0-7: deleting the item at index 0
// must not leave a hole (only Groups/0/Items/1), which is a shape no fcitx5
// tool produces.
func TestRemoveRimeRenumbersItems(t *testing.T) {
	in := `[Groups/0]
Name=Default
DefaultIM=rime

[Groups/0/Items/0]
Name=rime
Layout=

[Groups/0/Items/1]
Name=keyboard-us
Layout=

[GroupOrder]
0=Groups/0
`
	out, changed := RemoveRime(in)
	if !changed {
		t.Fatal("must change")
	}
	if strings.Contains(out, "[Groups/0/Items/1]") {
		t.Errorf("index hole left behind:\n%s", out)
	}
	if !strings.Contains(out, "[Groups/0/Items/0]\nName=keyboard-us") {
		t.Errorf("surviving item not renumbered to 0:\n%s", out)
	}
	if strings.Contains(out, "DefaultIM=rime") {
		t.Errorf("DefaultIM must fall back to the first remaining item:\n%s", out)
	}
	if strings.LastIndex(out, "[GroupOrder]") < strings.LastIndex(out, "[Groups/0/Items/0]") {
		t.Errorf("GroupOrder must stay last:\n%s", out)
	}
}

// TestEnsureRimePacksHoles: a pre-existing hole in the input is normalized on
// any write, so ompinyin never emits a non-canonical profile.
func TestEnsureRimePacksHoles(t *testing.T) {
	in := `[Groups/0]
Name=Default
DefaultIM=keyboard-us

[Groups/0/Items/1]
Name=keyboard-us
Layout=

[GroupOrder]
0=Groups/0
`
	out, _, err := EnsureRime(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[Groups/0/Items/0]") || !strings.Contains(out, "[Groups/0/Items/1]") {
		t.Errorf("items must be contiguous 0 and 1:\n%s", out)
	}
	if names := ItemNames(out); len(names) != 2 || names[0] != "keyboard-us" || names[1] != "rime" {
		t.Errorf("items = %v (numeric order expected)", names)
	}
}

func TestRoundtripTolerantReadStrictWrite(t *testing.T) {
	// tolerant: accepts spaces around '=', comments, blank lines
	in := `# a comment
[Groups/0]
Name = Default
DefaultIM = rime

[Groups/0/Items/0]
Name = keyboard-us
Layout=

[Groups/0/Items/1]
Name = rime
Layout=

[GroupOrder]
0 = Groups/0
`
	out, changed, err := EnsureRime(in)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Errorf("tolerant read should recognize the ok state:\n%s", out)
	}
}
