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
