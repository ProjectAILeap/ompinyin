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
		"0=Control+space",
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
	// trigger section rewritten (managed keys first, existing keys preserved)
	if !strings.Contains(out, "[Hotkey/TriggerKeys]\n0=Alt+space\n1=Control+space") {
		t.Errorf("trigger section wrong:\n%s", out)
	}
}

func TestEnsureTriggerSkipWhenEqual(t *testing.T) {
	in := "[Hotkey/TriggerKeys]\n0=Alt+space\n1=Control+Shift_L\n"
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

// TestEnsureTriggerPreservesUserKeys (评审 P2-5): the user's extra trigger
// keys must survive convergence — convergence adds, never removes.
func TestEnsureTriggerPreservesUserKeys(t *testing.T) {
	in := "[Hotkey/TriggerKeys]\n0=Control+space\n1=Super+space\n"
	out, changed, err := EnsureTrigger(in, DefaultKeys)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("managed keys missing, must change")
	}
	// managed keys take the priority slots, user keys follow
	for _, want := range []string{"0=Alt+space", "1=Control+space", "2=Super+space"} {
		if !strings.Contains(out, want) {
			t.Errorf("merged section missing %q:\n%s", want, out)
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
	if HasTrigger("[Hotkey/TriggerKeys]\n0=Control+space\n", DefaultKeys) {
		t.Error("HasTrigger must fail when managed keys are missing")
	}
}
