package profile

import (
	"sort"
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
	if names := itemNames(out); len(names) != 1 || names[0] != "rime" {
		t.Errorf("fresh profile must contain rime only, got %v", names)
	}
	if strings.Contains(out, "keyboard-us") {
		t.Errorf("fresh profile must not register keyboard-us:\n%s", out)
	}
	// GroupOrder rendered last
	if strings.LastIndex(out, "[GroupOrder]") < strings.LastIndex(out, "[Groups/0/Items/0]") {
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
	if names := itemNames(out); len(names) != 2 || names[1] != "rime" {
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
	if names := itemNames(out); len(names) != 2 {
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
	if names := itemNames(out); len(names) != 1 || names[0] != "keyboard-us" {
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
	if names := itemNames(out); len(names) != 2 || names[1] != "rime" {
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
	if names := itemNames(out); len(names) != 2 || names[0] != "keyboard-us" || names[1] != "rime" {
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

// itemNames is the test-side reader for the registered IM names in Groups/0
// order. It lives here, not in the package: production never needs it.
func itemNames(content string) []string {
	var names []string
	var items []*Section
	for _, s := range Parse(content) {
		if strings.HasPrefix(s.Name, itemPrefix) {
			items = append(items, s)
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return itemIndex(items[i]) < itemIndex(items[j]) })
	for _, s := range items {
		if n, ok := s.Get("Name"); ok {
			names = append(names, n)
		}
	}
	return names
}

// GroupIMs reports the group's items in ITEM ORDER (what Ctrl+Space walks).
func TestGroupIMsOrder(t *testing.T) {
	if got := GroupIMs(fullProfile); len(got) != 2 || got[0] != "keyboard-us" || got[1] != "rime" {
		t.Fatalf("GroupIMs = %v; want [keyboard-us rime]", got)
	}
	if got := GroupIMs(""); len(got) != 0 {
		t.Errorf("empty profile must report no items, got %v", got)
	}
}

// ConvergeGroup reorders to the requested shape, keeps DefaultIM=rime, and is
// idempotent — the "legacy host carried keyboard-us forever" repair.
func TestConvergeGroupReorders(t *testing.T) {
	out, changed, err := ConvergeGroup(fullProfile, []string{"rime", "keyboard-us"})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("reorder must report changed")
	}
	if got := GroupIMs(out); len(got) != 2 || got[0] != "rime" || got[1] != "keyboard-us" {
		t.Fatalf("GroupIMs after converge = %v; want [rime keyboard-us]", got)
	}
	if !strings.Contains(out, "DefaultIM=rime") {
		t.Errorf("DefaultIM must stay rime:\n%s", out)
	}
	// GroupOrder stays last (§16.18) and item indices are packed from 0.
	if !strings.HasSuffix(strings.TrimRight(out, "\n"), "0=Groups/0") {
		t.Errorf("GroupOrder must be last:\n%s", out)
	}
	if strings.Contains(out, "[Groups/0/Items/2]") {
		t.Errorf("item indices must be packed:\n%s", out)
	}
	out2, changed2, err := ConvergeGroup(out, []string{"rime", "keyboard-us"})
	if err != nil || changed2 || out2 != out {
		t.Errorf("second converge must be a no-op (changed=%v err=%v):\n%s", changed2, err, out2)
	}
}

// ConvergeGroup removes a stale second IM (EnsureRime alone only appends, which
// is how old hosts kept keyboard-us forever).
func TestConvergeGroupRemovesStaleIM(t *testing.T) {
	out, changed, err := ConvergeGroup(fullProfile, []string{"rime"})
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("removal must report changed")
	}
	if got := GroupIMs(out); len(got) != 1 || got[0] != "rime" {
		t.Fatalf("GroupIMs after converge = %v; want [rime]", got)
	}
	if strings.Contains(out, "keyboard-us") {
		t.Errorf("stale item left behind:\n%s", out)
	}
}

// Unknown ids are appended with the canonical (Name=, Layout=) pair, and an
// empty profile starts from DefaultProfile.
func TestConvergeGroupAppendsAndBootstraps(t *testing.T) {
	out, changed, err := ConvergeGroup("", []string{"rime", "keyboard-us"})
	if err != nil || !changed {
		t.Fatalf("bootstrap err=%v changed=%v", err, changed)
	}
	if got := GroupIMs(out); len(got) != 2 || got[0] != "rime" {
		t.Fatalf("GroupIMs = %v; want [rime keyboard-us]", got)
	}
	other, _, err := ConvergeGroup(fullProfile, []string{"rime", "pinyin-custom"})
	if err != nil {
		t.Fatal(err)
	}
	if got := GroupIMs(other); len(got) != 2 || got[1] != "pinyin-custom" {
		t.Fatalf("new item not appended: %v", got)
	}
	if !strings.Contains(other, "[Groups/0/Items/1]\nName=pinyin-custom\nLayout=") {
		t.Errorf("new item must carry the canonical Layout= line:\n%s", other)
	}
}

func TestConvergeGroupRejectsBadShapes(t *testing.T) {
	for name, tc := range map[string]struct {
		content string
		ims     []string
	}{
		"empty ims":    {fullProfile, nil},
		"missing rime": {fullProfile, []string{"keyboard-us"}},
		"duplicate":    {fullProfile, []string{"rime", "rime"}},
		"empty id":     {fullProfile, []string{"rime", ""}},
		"no Groups/0":  {"[GroupOrder]\n0=Groups/0\n", []string{"rime"}},
	} {
		if _, _, err := ConvergeGroup(tc.content, tc.ims); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
