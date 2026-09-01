// Package profile implements fcitx5 profile INI handling: tolerant reading,
// strict writing (Name=rime, Layout= without spaces, GroupOrder last) and the
// minimal-change rime registration (§6.1).
package profile

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Section is one INI section with ordered raw key=value lines.
type Section struct {
	Name string
	Keys [][2]string // ordered key/value pairs
}

// Get returns the value for key (first occurrence).
func (s *Section) Get(key string) (string, bool) {
	for _, kv := range s.Keys {
		if kv[0] == key {
			return kv[1], true
		}
	}
	return "", false
}

// Set replaces or appends key=value.
func (s *Section) Set(key, val string) {
	for i, kv := range s.Keys {
		if kv[0] == key {
			s.Keys[i][1] = val
			return
		}
	}
	s.Keys = append(s.Keys, [2]string{key, val})
}

// Delete removes all lines for key.
func (s *Section) Delete(key string) {
	out := s.Keys[:0]
	for _, kv := range s.Keys {
		if kv[0] != key {
			out = append(out, kv)
		}
	}
	s.Keys = out
}

// Parse reads INI content tolerantly: spaces around '=', blank lines, comments.
func Parse(content string) []*Section {
	var sections []*Section
	var cur *Section
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			cur = &Section{Name: strings.TrimSpace(line[1 : len(line)-1])}
			sections = append(sections, cur)
			continue
		}
		if cur == nil {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		cur.Keys = append(cur.Keys, [2]string{strings.TrimSpace(k), strings.TrimSpace(v)})
	}
	return sections
}

// Render writes INI strictly: Key=Value, no spaces, sections in order with
// blank-line separation. GroupOrder is always rendered last (§6.1).
func Render(sections []*Section) string {
	ordered := make([]*Section, 0, len(sections))
	for _, s := range sections {
		if s.Name != "GroupOrder" {
			ordered = append(ordered, s)
		}
	}
	for _, s := range sections {
		if s.Name == "GroupOrder" {
			ordered = append(ordered, s)
		}
	}
	var b strings.Builder
	for i, s := range ordered {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("[" + s.Name + "]\n")
		for _, kv := range s.Keys {
			b.WriteString(kv[0] + "=" + kv[1] + "\n")
		}
	}
	return b.String()
}

// findSection returns the section or nil.
func findSection(sections []*Section, name string) *Section {
	for _, s := range sections {
		if s.Name == name {
			return s
		}
	}
	return nil
}

// DefaultProfile renders a fresh profile with keyboard-us + rime.
func DefaultProfile() string {
	return Render([]*Section{
		{Name: "Groups/0", Keys: [][2]string{
			{"Name", "Default"},
			{"Default Layout", "us"},
			{"DefaultIM", "rime"},
		}},
		{Name: "Groups/0/Items/0", Keys: [][2]string{
			{"Name", "keyboard-us"},
			{"Layout", ""},
		}},
		{Name: "Groups/0/Items/1", Keys: [][2]string{
			{"Name", "rime"},
			{"Layout", ""},
		}},
		{Name: "GroupOrder", Keys: [][2]string{{"0", "Groups/0"}}},
	})
}

// EnsureRime merges rime registration into profile content with minimal
// changes: only fixes DefaultIM and appends the rime item when absent.
// Returns (newContent, changed, error). Handles the four roundtrip scenarios:
// missing file (empty content), no rime, wrong DefaultIM, already ok.
func EnsureRime(content string) (string, bool, error) {
	if strings.TrimSpace(content) == "" {
		return DefaultProfile(), true, nil
	}
	sections := Parse(content)
	g0 := findSection(sections, "Groups/0")
	if g0 == nil {
		return "", false, fmt.Errorf("profile has no [Groups/0] section; cannot merge rime safely")
	}

	changed := false

	// 1. Ensure a rime item exists (minimal: skip if already present).
	hasRime := false
	maxItem := -1
	for _, s := range sections {
		if !strings.HasPrefix(s.Name, itemPrefix) {
			continue
		}
		if idx := itemIndex(s); idx > maxItem {
			maxItem = idx
		}
		if name, _ := s.Get("Name"); name == "rime" {
			hasRime = true
		}
	}
	if !hasRime {
		// appended at maxItem+1, then packItems() normalizes the whole set
		sections = append(sections, &Section{
			Name: itemPrefix + strconv.Itoa(maxItem+1),
			Keys: [][2]string{{"Name", "rime"}, {"Layout", ""}},
		})
		changed = true
	}

	// 2. Ensure DefaultIM=rime.
	if im, _ := g0.Get("DefaultIM"); im != "rime" {
		g0.Set("DefaultIM", "rime")
		changed = true
	}

	// 3. Ensure GroupOrder exists.
	if findSection(sections, "GroupOrder") == nil {
		sections = append(sections, &Section{Name: "GroupOrder", Keys: [][2]string{{"0", "Groups/0"}}})
		changed = true
	}

	if !changed {
		return content, false, nil
	}
	// items are always re-packed on write so the file stays canonical
	packItems(sections)
	return Render(sections), true, nil
}

// itemPrefix is the section prefix of a group's IM items.
const itemPrefix = "Groups/0/Items/"

// RemoveRime removes the rime IM registration (uninstall). Returns
// (newContent, changed). If rime was the DefaultIM, the first remaining item
// takes over.
//
// The surviving items are RE-NUMBERED contiguously (Items/0, Items/1, …):
// leaving a hole (only Items/1 after deleting Items/0) is not a shape any
// fcitx5 tool ever produces, and fcitx5-configtool rewrites the file with
// packed indices — ompinyin must not emit a profile it would consider dirty
// (评审 P0-7).
func RemoveRime(content string) (string, bool) {
	sections := Parse(content)
	changed := false
	var firstRemaining string
	var kept []*Section
	for _, s := range sections {
		if strings.HasPrefix(s.Name, itemPrefix) {
			if name, _ := s.Get("Name"); name == "rime" {
				changed = true
				continue
			}
			if firstRemaining == "" {
				if name, _ := s.Get("Name"); name != "" {
					firstRemaining = name
				}
			}
		}
		kept = append(kept, s)
	}
	if g0 := findSection(kept, "Groups/0"); g0 != nil {
		if im, _ := g0.Get("DefaultIM"); im == "rime" {
			if firstRemaining != "" {
				g0.Set("DefaultIM", firstRemaining)
			} else {
				g0.Delete("DefaultIM")
			}
			changed = true
		}
	}
	if !changed {
		return content, false
	}
	// renumber the surviving items so the file never carries an index hole
	packItems(kept)
	return Render(kept), true
}

// packItems renumbers Groups/0/Items/* contiguously (0..n-1) in numeric order
// and reports whether anything changed. Item sections are written back into
// the slots they occupied so the surrounding layout stays stable.
func packItems(sections []*Section) bool {
	var slots []int
	var items []*Section
	for i, s := range sections {
		if strings.HasPrefix(s.Name, itemPrefix) {
			slots = append(slots, i)
			items = append(items, s)
		}
	}
	if len(items) == 0 {
		return false
	}
	changed := false
	sorted := append([]*Section{}, items...)
	sort.SliceStable(sorted, func(i, j int) bool { return itemIndex(sorted[i]) < itemIndex(sorted[j]) })
	for k, s := range sorted {
		if want := itemPrefix + strconv.Itoa(k); s.Name != want {
			s.Name = want
			changed = true
		}
	}
	for k, slot := range slots {
		if sections[slot] != sorted[k] {
			sections[slot] = sorted[k]
			changed = true
		}
	}
	return changed
}

// itemIndex returns the numeric suffix of an item section (-1 when unparsable).
func itemIndex(s *Section) int {
	n, err := strconv.Atoi(strings.TrimPrefix(s.Name, itemPrefix))
	if err != nil {
		return -1
	}
	return n
}

// ItemNames returns the IM names registered in Groups/0 in order.
func ItemNames(content string) []string {
	var names []string
	items := []*Section{}
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
