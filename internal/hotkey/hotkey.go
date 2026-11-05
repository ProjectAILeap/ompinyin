// Package hotkey writes the fcitx5 trigger keys with INI merge semantics and
// keysym whitelist validation (§6.2).
//
// Omarchy's herdr prefix is Ctrl+Space, so the trigger keys are written
// unconditionally — the merge never overwrites unrelated user hotkey sections.
package hotkey

import (
	"fmt"
	"regexp"
	"strings"
)

// TriggerSection is the fcitx5 config section holding trigger keys.
const TriggerSection = "Hotkey/TriggerKeys"

// DefaultKeys are the convergence target trigger keys — the fcitx5-level
// "switch input method" chord. With the default single-`rime` profile it is the
// IME on/off switch (中英 lives inside rime on Shift, schema switching on F4).
//
// Control+space is the conventional Chinese-IME chord (fcitx4/Windows/搜狗).
// Its one collision on Omarchy is herdr's shipped prefix (also ctrl+space); that
// is resolved in herdr's own config (ctrl+b is its built-in default), not here.
// Nothing else claims it: Super+space / Super+Ctrl+space / Super+Alt+space are
// the Omarchy menus, Alt+space is the "window menu" chord, Ctrl+` belongs to
// editors. See docs/dev-notes.md D8.
var DefaultKeys = []string{"Control+space"}

// PruneKeys are trigger keys convergence REMOVES from [Hotkey/TriggerKeys].
//
// Alt+space is what older ompinyin versions wrote, before it became clear that
// it is the universal "window menu" chord (toolkits eat it) and that herdr's
// prefix could simply be moved off Control+space instead. Merging alone ("add,
// never remove") would leave both bound on an existing host, so the managed
// terminal state drops it.
var PruneKeys = []string{"Alt+space"}

// SameKey compares two chords across the two spellings in play (fcitx5's
// "Control+space" vs herdr's "ctrl+space"), ignoring case and spaces. Exported
// because host probes compare fcitx5's trigger keys against other tools' chords.
func SameKey(a, b string) bool {
	return canonicalKey(a) == canonicalKey(b)
}

// sameKey is SameKey for the package-internal call sites.
func sameKey(a, b string) bool { return SameKey(a, b) }

func canonicalKey(k string) string {
	k = strings.ToLower(strings.ReplaceAll(k, " ", ""))
	return strings.ReplaceAll(k, "ctrl+", "control+")
}

func isPruned(k string) bool {
	for _, p := range PruneKeys {
		if sameKey(k, p) {
			return true
		}
	}
	return false
}

// bareModifiers are silently dropped by fcitx5 when used alone (regression
// case from the field notes): bare `Shift` never reaches the config.
var bareModifiers = map[string]bool{
	"Shift": true, "Control": true, "Alt": true, "Super": true, "Meta": true,
}

var keysymRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// ValidateKey enforces the whitelist (§16 invariant 8): each '+'-separated
// final keysym must be a concrete keysym (Shift_L, not Shift).
func ValidateKey(k string) error {
	if k == "" {
		return fmt.Errorf("empty keysym")
	}
	if strings.Contains(k, " ") {
		return fmt.Errorf("keysym %q contains whitespace", k)
	}
	parts := strings.Split(k, "+")
	last := parts[len(parts)-1]
	if last == "" {
		return fmt.Errorf("keysym %q ends with '+", k)
	}
	if bareModifiers[last] {
		return fmt.Errorf("bare modifier %q is silently dropped by fcitx5; use e.g. Shift_L", last)
	}
	if !keysymRe.MatchString(last) {
		return fmt.Errorf("invalid keysym %q", last)
	}
	for _, p := range parts[:len(parts)-1] {
		switch p {
		case "Control", "Alt", "Super", "Shift", "Meta":
		default:
			return fmt.Errorf("unknown modifier %q in %q", p, k)
		}
	}
	return nil
}

// section is the generic parsed INI section reused here.
type section struct {
	Name  string
	Lines []string
}

func parseINI(content string) []section {
	var secs []section
	var cur *section
	for _, line := range strings.Split(content, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			secs = append(secs, section{Name: strings.TrimSpace(t[1 : len(t)-1])})
			cur = &secs[len(secs)-1]
			continue
		}
		if cur != nil {
			cur.Lines = append(cur.Lines, t)
		}
	}
	return secs
}

func renderINI(secs []section) string {
	var b strings.Builder
	for i, s := range secs {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("[" + s.Name + "]\n")
		for _, l := range s.Lines {
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}

// targetLines renders the numbered keys for the trigger section.
func targetLines(keys []string) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = fmt.Sprintf("%d=%s", i, k)
	}
	return out
}

func compact(lines []string) []string {
	var out []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		out = append(out, t)
	}
	return out
}

// EnsureTrigger merges the trigger keys into fcitx5 config content without
// touching any other section. Returns (newContent, changed, error).
//
// Merge semantics (P2-5): managed keys are PREPENDED (priority), and the
// user's extra keys inside [Hotkey/TriggerKeys] are PRESERVED — convergence
// adds, never removes. If the existing managed keys are already present (any
// position), the content is returned unchanged (跳过 semantics).
func EnsureTrigger(content string, keys []string) (string, bool, error) {
	for _, k := range keys {
		if err := ValidateKey(k); err != nil {
			return "", false, err
		}
	}
	secs := parseINI(content)
	for i := range secs {
		if secs[i].Name != TriggerSection {
			continue
		}
		existing := sectionKeyValues(secs[i].Lines)
		merged := mergeKeys(existing, keys)
		if equalStrings(existing, merged) {
			return content, false, nil
		}
		secs[i].Lines = targetLines(merged)
		return renderINI(secs), true, nil
	}
	secs = append(secs, section{Name: TriggerSection, Lines: targetLines(keys)})
	return renderINI(secs), true, nil
}

// sectionKeyValues extracts the values of `N=value` lines (order preserved).
func sectionKeyValues(lines []string) []string {
	var out []string
	for _, l := range compact(lines) {
		if _, v, ok := strings.Cut(l, "="); ok {
			if v = strings.TrimSpace(v); v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}

// mergeKeys returns managed keys first (they own the priority slots), then
// any existing user keys that are neither already listed nor pruned.
func mergeKeys(existing, managed []string) []string {
	out := append([]string{}, managed...)
	for _, v := range existing {
		if isPruned(v) {
			continue
		}
		dup := false
		for _, m := range out {
			if v == m {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, v)
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// AltTriggerSection holds fcitx5's "temporarily toggle input method" keys
// (globalconfig.cpp: altTriggerKeys, shipped as {Shift_L}).
const AltTriggerSection = "Hotkey/AltTriggerKeys"

// EnsureAltTriggerEmpty empties [Hotkey/AltTriggerKeys] so LEFT Shift reaches
// rime's ascii_composer instead of fcitx5's own toggle.
//
// Why this is part of the terminal state: fcitx5 gates the alt trigger on
// `canTrigger()` — group item count > 1 — which is exactly the shipped
// two-IM shape (rime + keyboard-us, needed so Ctrl+Space can do anything at
// all). With two items, the shipped `Shift_L` becomes live and does the SAME
// `toggle()` as the trigger key, so "左 Shift 在 rime 内切中英" silently turns
// into "左 Shift 和 Ctrl+Space 一样" (真机 2026-11-03 复现).
//
// fcitx5's INI cannot express an empty list value: the only encoding of "no
// keys" is a section that EXISTS with no `N=` children — an absent section means
// "use the default", i.e. Shift_L again. Verified on fcitx5 5.1.23: fcitx5
// neither rewrites nor drops such a section on restart, and a converging
// `install` keeps it (this function is idempotent).
func EnsureAltTriggerEmpty(content string) (string, bool, error) {
	secs := parseINI(content)
	for i := range secs {
		if secs[i].Name != AltTriggerSection {
			continue
		}
		if len(compact(secs[i].Lines)) == 0 {
			return content, false, nil
		}
		// Dropping the keys IS the convergence: the section stays, empty.
		secs[i].Lines = nil
		return renderINI(secs), true, nil
	}
	secs = append(secs, section{Name: AltTriggerSection})
	return renderINI(secs), true, nil
}

// AltTriggerKeys returns the keys bound in [Hotkey/AltTriggerKeys] (nil when
// the section is absent — which for fcitx5 means the shipped Shift_L).
func AltTriggerKeys(content string) []string {
	for _, s := range parseINI(content) {
		if s.Name == AltTriggerSection {
			return sectionKeyValues(s.Lines)
		}
	}
	return nil
}

// HasEmptyAltTrigger reports whether the section exists and carries no keys.
// An ABSENT section is not "empty": fcitx5 would fall back to Shift_L.
func HasEmptyAltTrigger(content string) bool {
	for _, s := range parseINI(content) {
		if s.Name == AltTriggerSection {
			return len(sectionKeyValues(s.Lines)) == 0
		}
	}
	return false
}

// HasTrigger reports whether the trigger section already contains every
// managed key. Extra user keys are allowed — the plan predicate must not
// flap when the user adds their own trigger key (P2-5).
func HasTrigger(content string, keys []string) bool {
	for _, s := range parseINI(content) {
		if s.Name != TriggerSection {
			continue
		}
		have := map[string]bool{}
		for _, v := range sectionKeyValues(s.Lines) {
			have[canonicalKey(v)] = true
			if isPruned(v) {
				return false // a pruned chord is still bound: not converged
			}
		}
		for _, k := range keys {
			if !have[canonicalKey(k)] {
				return false
			}
		}
		return true
	}
	return false
}
