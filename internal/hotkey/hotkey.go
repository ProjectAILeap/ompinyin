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

// DefaultKeys are the convergence target trigger keys: Alt+Space avoids the
// herdr Ctrl+Space prefix. It is the sole trigger — Control+Shift_L was
// dropped because it collided with too many app-level chords (Alt+Tab-family
// focus / terminal shift selection) and is redundant with Alt+Space.
var DefaultKeys = []string{"Alt+space"}

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
// any existing user keys not among them (dedup, order preserved).
func mergeKeys(existing, managed []string) []string {
	out := append([]string{}, managed...)
	for _, v := range existing {
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
			have[v] = true
		}
		for _, k := range keys {
			if !have[k] {
				return false
			}
		}
		return true
	}
	return false
}
