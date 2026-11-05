package hotkey

import (
	"strings"
	"testing"
)

func TestValidateKeyWhitelist(t *testing.T) {
	for _, ok := range []string{"Alt+space", "Control+Shift_L", "Super+space", "Control+Alt+t"} {
		if err := ValidateKey(ok); err != nil {
			t.Errorf("valid key %s rejected: %v", ok, err)
		}
	}
	// 裸 Shift 被 fcitx5 静默丢弃 — must be rejected (§6.2)
	for _, bad := range []string{"Shift", "Control", "Alt", "Super", "", "Control+Shift+", "Control Shift", "Shift+Alt+bogus*"} {
		if err := ValidateKey(bad); err == nil {
			t.Errorf("invalid key %q accepted", bad)
		}
	}
}

func TestEnsureTriggerMergePreservesOtherSections(t *testing.T) {
	in := strings.Join([]string{
		"[Hotkey/EnumerateForwardKeys]",
		"0=Control+Shift_R",
		"",
		"[Hotkey/TriggerKeys]",
		"0=Alt+space",
		"",
		"[Hotkey/EnumerateBackwardKeys]",
		"0=Control+Shift_L",
		"",
	}, "\n")
	out, changed, err := EnsureTrigger(in, DefaultKeys)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("must change")
	}
	// other sections untouched
	if !strings.Contains(out, "[Hotkey/EnumerateForwardKeys]\n0=Control+Shift_R") {
		t.Errorf("forward section clobbered:\n%s", out)
	}
	if !strings.Contains(out, "[Hotkey/EnumerateBackwardKeys]\n0=Control+Shift_L") {
		t.Errorf("backward section clobbered:\n%s", out)
	}
	// trigger section rewritten: the managed key takes the slot, the old
	// Alt+space (window-menu chord) is pruned.
	if !strings.Contains(out, "[Hotkey/TriggerKeys]\n0=Control+space\n") {
		t.Errorf("trigger section wrong:\n%s", out)
	}
	for _, pruned := range PruneKeys {
		if strings.Contains(strings.ToLower(out), strings.ToLower(pruned)) {
			t.Errorf("%s must be pruned:\n%s", pruned, out)
		}
	}
}

func TestEnsureTriggerSkipWhenEqual(t *testing.T) {
	in := "[Hotkey/TriggerKeys]\n0=Control+space\n1=Control+Shift_L\n"
	out, changed, err := EnsureTrigger(in, DefaultKeys)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Errorf("equal section must skip:\n%s", out)
	}
}

func TestEnsureTriggerAppendsWhenMissing(t *testing.T) {
	in := "[Some/Section]\nkey=value\n"
	out, changed, err := EnsureTrigger(in, DefaultKeys)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("must change")
	}
	if !HasTrigger(out, DefaultKeys) {
		t.Errorf("trigger not appended:\n%s", out)
	}
	if !strings.Contains(out, "[Some/Section]\nkey=value") {
		t.Errorf("existing section lost:\n%s", out)
	}
}

func TestEnsureTriggerRejectsBareShift(t *testing.T) {
	if _, _, err := EnsureTrigger("", []string{"Shift"}); err == nil {
		t.Fatal("bare Shift must be rejected")
	}
}

// TestEnsureTriggerPreservesUserKeys: the user's extra trigger keys must
// survive convergence, EXCEPT a pruned one — convergence adds its managed key
// and drops the legacy chord, so a hand-tuned host reaches the same terminal
// state as a fresh one.
func TestEnsureTriggerPreservesUserKeys(t *testing.T) {
	in := "[Hotkey/TriggerKeys]\n0=Alt+space\n1=Super+space\n"
	out, changed, err := EnsureTrigger(in, DefaultKeys)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("managed keys missing, must change")
	}
	// the managed key takes the priority slot, the pruned legacy key is gone,
	// so the user's Super+space follows it.
	for _, want := range []string{"0=Control+space", "1=Super+space"} {
		if !strings.Contains(out, want) {
			t.Errorf("merged section missing %q:\n%s", want, out)
		}
	}
	for _, pruned := range PruneKeys {
		if strings.Contains(strings.ToLower(out), strings.ToLower(pruned)) {
			t.Errorf("%s must be pruned even if the user had it:\n%s", pruned, out)
		}
	}
	// second run: converged (contains managed keys) → no churn
	out2, changed2, err := EnsureTrigger(out, DefaultKeys)
	if err != nil {
		t.Fatal(err)
	}
	if changed2 {
		t.Errorf("second run must skip:\n%s", out2)
	}
	// HasTrigger accepts sections with extra user keys
	if !HasTrigger(out, DefaultKeys) {
		t.Error("HasTrigger must pass with extra user keys present")
	}
	if HasTrigger("[Hotkey/TriggerKeys]\n0=Super+space\n", DefaultKeys) {
		t.Error("HasTrigger must fail when managed keys are missing")
	}
	// a pruned chord that is still bound means "not converged"
	if HasTrigger("[Hotkey/TriggerKeys]\n0=Control+space\n1=Alt+space\n", DefaultKeys) {
		t.Error("HasTrigger must fail while a pruned key is still bound")
	}
	// case/spelling insensitive (Ctrl alias + lower-case key names)
	if HasTrigger("[Hotkey/TriggerKeys]\n0=ALT+SPACE\n1=Ctrl+space\n", DefaultKeys) {
		t.Error("HasTrigger must normalise Ctrl/Control and case, and reject a bound pruned key")
	}
}

// EnsureAltTriggerEmpty encodes "no alt-trigger keys" as a section that EXISTS
// with no children: an absent section means fcitx5's shipped Shift_L.
func TestEnsureAltTriggerEmpty(t *testing.T) {
	in := strings.Join([]string{
		"[Hotkey]",
		"ModifierOnlyKeyTimeout=-1",
		"",
		"[Hotkey/TriggerKeys]",
		"0=Control+space",
		"",
		"[Hotkey/AltTriggerKeys]",
		"0=Shift_L",
		"",
		"[Behavior]",
		"ActiveByDefault=True",
		"",
	}, "\n")
	out, changed, err := EnsureAltTriggerEmpty(in)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("stripping Shift_L must report changed")
	}
	if keys := AltTriggerKeys(out); len(keys) != 0 {
		t.Errorf("AltTriggerKeys = %v; want empty", keys)
	}
	if !HasEmptyAltTrigger(out) {
		t.Errorf("section must exist and be empty:\n%s", out)
	}
	for _, want := range []string{"[Hotkey/TriggerKeys]\n0=Control+space", "ModifierOnlyKeyTimeout=-1", "ActiveByDefault=True"} {
		if !strings.Contains(out, want) {
			t.Errorf("neighbouring section damaged, missing %q:\n%s", want, out)
		}
	}
	// Idempotent: a second pass is a no-op with identical bytes.
	out2, changed2, err := EnsureAltTriggerEmpty(out)
	if err != nil || changed2 || out2 != out {
		t.Errorf("not idempotent (changed=%v err=%v):\n%s", changed2, err, out2)
	}
}

// A missing section is created empty (the only encoding of "no keys").
func TestEnsureAltTriggerEmptyCreatesSection(t *testing.T) {
	in := "[Hotkey/TriggerKeys]\n0=Control+space\n"
	out, changed, err := EnsureAltTriggerEmpty(in)
	if err != nil || !changed {
		t.Fatalf("err=%v changed=%v", err, changed)
	}
	if !HasEmptyAltTrigger(out) {
		t.Errorf("empty section not created:\n%s", out)
	}
	if !strings.Contains(out, "0=Control+space") {
		t.Errorf("trigger keys lost:\n%s", out)
	}
}

// HasEmptyAltTrigger must distinguish "no section" (fcitx5 falls back to
// Shift_L) from "empty section" (nothing bound).
func TestHasEmptyAltTriggerDistinguishesAbsent(t *testing.T) {
	if HasEmptyAltTrigger("[Hotkey/TriggerKeys]\n0=Control+space\n") {
		t.Error("absent section must NOT count as empty (fcitx5 would use Shift_L)")
	}
	if got := AltTriggerKeys("[Hotkey/TriggerKeys]\n0=Control+space\n"); got != nil {
		t.Errorf("absent section must read nil, got %v", got)
	}
	if !HasEmptyAltTrigger("\n[Hotkey/AltTriggerKeys]\n\n[Behavior]\n") {
		t.Error("present-but-childless section must count as empty")
	}
}

// RemoveTriggerKey is how 4.5+ convergence gives the chord back: the key goes,
// the section stays (fcitx5 reads an absent section as "shipped default").
func TestRemoveTriggerKey(t *testing.T) {
	in := strings.Join([]string{
		"[Hotkey]",
		"ModifierOnlyKeyTimeout=-1",
		"",
		"[Hotkey/TriggerKeys]",
		"0=Control+space",
		"",
		"[Behavior]",
		"ActiveByDefault=False",
		"",
	}, "\n")
	out, changed, err := RemoveTriggerKey(in, "Control+space")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("removing a bound chord must report changed")
	}
	if strings.Contains(strings.ToLower(out), "control+space") {
		t.Errorf("chord still bound:\n%s", out)
	}
	if !strings.Contains(out, "[Hotkey/TriggerKeys]\n") {
		t.Errorf("the (now empty) section must be kept:\n%s", out)
	}
	if !strings.Contains(out, "[Behavior]\nActiveByDefault=False") {
		t.Errorf("neighbouring section damaged:\n%s", out)
	}
	// idempotent: nothing left to remove
	out2, changed2, err := RemoveTriggerKey(out, "Control+space")
	if err != nil || changed2 || out2 != out {
		t.Errorf("not idempotent (changed=%v err=%v):\n%s", changed2, err, out2)
	}
	// absent key / absent section: no churn
	if _, changed, _ := RemoveTriggerKey("[Hotkey/TriggerKeys]\n0=Super+space\n", "Control+space"); changed {
		t.Error("an unbound chord must not change the file")
	}
	if _, changed, _ := RemoveTriggerKey("[Hotkey]\n", "Control+space"); changed {
		t.Error("a missing section must not be created")
	}
	// survivors are renumbered contiguously
	renum, changed, err := RemoveTriggerKey("[Hotkey/TriggerKeys]\n0=Control+space\n1=Super+space\n", "Control+space")
	if err != nil || !changed {
		t.Fatalf("err=%v changed=%v", err, changed)
	}
	if !strings.Contains(renum, "0=Super+space") || strings.Contains(renum, "1=") {
		t.Errorf("survivors not renumbered:\n%s", renum)
	}
	// spelling/case normalisation (Ctrl alias) and validation
	if _, changed, _ := RemoveTriggerKey("[Hotkey/TriggerKeys]\n0=ctrl+SPACE\n", "Control+space"); !changed {
		t.Error("Ctrl/Control and case must compare equal")
	}
	if _, _, err := RemoveTriggerKey("", "Shift"); err == nil {
		t.Error("an invalid keysym must be rejected")
	}
}
