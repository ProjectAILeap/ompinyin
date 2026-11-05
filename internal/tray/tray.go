// Package tray implements the L4 mandatory tray terminal state (§6.4):
// step A — the dedicated systemd user drop-in enabling notificationitem;
// step B — pinning `Fcitx` via `omarchy bar set` with read→merge→set
// semantics (never a bare overwrite, which would drop the user's pins).
package tray

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ProjectAILeap/ompinyin/internal/catalog"
	"github.com/ProjectAILeap/ompinyin/internal/execcmd"
	"github.com/ProjectAILeap/ompinyin/internal/service"
	"github.com/ProjectAILeap/ompinyin/internal/state"
)

// FcitxId is the SNI Id published by fcitx5; Tray.qml matches it
// case-sensitively via pinnedIds.indexOf(item.id).
const FcitxId = "Fcitx"

// DefaultUnit is the preferred fcitx5 user unit on Omarchy (§6.3).
const DefaultUnit = "omarchy-fcitx5.service"

// DropInFileName is the dedicated drop-in file name. NOT override.conf, so
// that uninstall can delete it without nuking the user's other drop-ins
// (ADR 13).
const DropInFileName = "ompinyin-notificationitem.conf"

// DropInRelPath returns the drop-in path relative to $HOME FOR THE GIVEN UNIT.
// It must follow the unit service.FindUnit actually discovered: writing an
// omarchy-fcitx5.service.d/ drop-in on a host that runs the generic
// fcitx5.service leaves notificationitem disabled while every existence check
// still passes (评审 P1-4).
func DropInRelPath(unit string) string {
	if unit == "" {
		unit = DefaultUnit
	}
	return ".config/systemd/user/" + unit + ".d/" + DropInFileName
}

// DropInRelPathLegacy is the historical hardcoded path, kept so uninstall can
// clean up after a run that wrote it before the path was unit-aware.
const DropInRelPathLegacy = ".config/systemd/user/omarchy-fcitx5.service.d/ompinyin-notificationitem.conf"

// OverrideFileName is generic by nature: any tool (or the user) may own it.
// That is exactly why ompinyin may only delete it when its CONTENT is one of the
// bodies ompinyin itself ever wrote (see LegacyOverrideMatches).
const OverrideFileName = "override.conf"

// LegacyOverrideBody is the literal body ompinyin v1.0 wrote to override.conf
// (commit 7c289dc): no managed header, so it cannot be recognised by name or by
// header — only by these exact bytes. On Omarchy 4.5+ it is harmful: it replaces
// the unit's `--replace --disable notificationitem` and fcitx5 publishes its own
// SNI item again, so the bar shows two input-method icons.
const LegacyOverrideBody = "[Service]\nExecStart=\nExecStart=/usr/bin/fcitx5\n"

// LegacyOverrideRelPath returns the generic override.conf path for the unit.
func LegacyOverrideRelPath(unit string) string {
	if unit == "" {
		unit = DefaultUnit
	}
	return ".config/systemd/user/" + unit + ".d/" + OverrideFileName
}

// LegacyOverridePath returns the absolute override.conf path for a home + unit.
func LegacyOverridePath(home, unit string) string {
	return filepath.Join(home, filepath.FromSlash(LegacyOverrideRelPath(unit)))
}

// LegacyOverrideMatches reports whether override.conf content is one ompinyin
// itself wrote: the v1.0 literal body, or the derived drop-in body an older run
// may have written under this generic name. Anything else belongs to someone
// else and must never be deleted (pure).
func LegacyOverrideMatches(content, derivedBody string) bool {
	c := strings.TrimRight(content, "\n")
	if c == strings.TrimRight(LegacyOverrideBody, "\n") {
		return true
	}
	return derivedBody != "" && c == strings.TrimRight(derivedBody, "\n")
}

// DerivedDropInBody derives the notificationitem drop-in body from the unit file
// that actually runs (the unit's own ExecStart, with only
// `--disable notificationitem` stripped), falling back to the static body when
// the unit is unreadable.
func DerivedDropInBody(home, unit string) string {
	if line := service.ExecStartLine(service.UnitFilePath(home, unit)); line != "" {
		return DeriveDropInContent(line)
	}
	return DropInContent
}

// RemoveLegacyOverride deletes override.conf ONLY when its content is one
// ompinyin wrote (LegacyOverrideMatches). Returns removed=false — and touches
// nothing — for a user-authored override, even though that file may well be
// breaking the host: saying so is verify's job, deleting it is not ours.
func RemoveLegacyOverride(home, unit, derivedBody string) (removed bool, err error) {
	p := LegacyOverridePath(home, unit)
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !LegacyOverrideMatches(string(b), derivedBody) {
		return false, nil
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if dir := filepath.Dir(p); isEmptyDir(dir) {
		os.Remove(dir)
	}
	return true, nil
}

// DropInContent is the fallback drop-in body when the original unit file
// cannot be read. Prefer DeriveDropInContent: it preserves whatever other
// ExecStart flags upstream ships, only stripping --disable notificationitem,
// so an upstream flag/path change is not silently clobbered (评审 2.2).
var DropInContent = DeriveDropInContent("/usr/bin/fcitx5 --disable notificationitem")

// DeriveDropInContent builds the managed drop-in body from the unit's original
// ExecStart line: the `--disable notificationitem` token pair is removed and
// everything else (binary path, other flags) is preserved verbatim.
func DeriveDropInContent(execStart string) string {
	if execStart == "" {
		execStart = "/usr/bin/fcitx5"
	}
	tokens := strings.Fields(execStart)
	var kept []string
	for i := 0; i < len(tokens); i++ {
		if tokens[i] == "--disable" && i+1 < len(tokens) && tokens[i+1] == "notificationitem" {
			i++ // skip the flag AND its argument
			continue
		}
		kept = append(kept, tokens[i])
	}
	line := strings.Join(kept, " ")
	if line == "" {
		line = "/usr/bin/fcitx5"
	}
	return strings.Join([]string{
		catalog.ManagedHeader(),
		"[Service]",
		"ExecStart=",
		"ExecStart=" + line,
		"",
	}, "\n")
}

// DropInPath returns the absolute drop-in path for a home dir and unit.
func DropInPath(home, unit string) string {
	return filepath.Join(home, filepath.FromSlash(DropInRelPath(unit)))
}

// ShellJSONPath returns ~/.config/omarchy/shell.json.
func ShellJSONPath(home string) string {
	return filepath.Join(home, ".config", "omarchy", "shell.json")
}

// DropInEnabled reports whether the drop-in content really enables the SNI
// item: it must carry an ExecStart that does NOT pass
// `--disable notificationitem`. Existence alone is not enough (评审 P1-4:
// verify used to be satisfied by any file at that path).
func DropInEnabled(content string) bool {
	inService := false
	hasExec := false
	for _, line := range strings.Split(content, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]"):
			inService = t == "[Service]"
		case inService && strings.HasPrefix(t, "ExecStart="):
			if strings.Contains(t, "--disable") && strings.Contains(t, "notificationitem") {
				return false
			}
			if strings.TrimSpace(strings.TrimPrefix(t, "ExecStart=")) != "" {
				hasExec = true
			}
		}
	}
	return hasExec
}

// WriteDropIn atomically writes the notificationitem drop-in for the given
// unit; created reports whether bytes changed.
func WriteDropIn(home, unit, content string) (created bool, err error) {
	p := DropInPath(home, unit)
	if b, statErr := os.ReadFile(p); statErr == nil && string(b) == content {
		return false, nil
	}
	return true, state.WriteAtomic(p, []byte(content))
}

// RemoveDropIn deletes the dedicated drop-in for the unit (uninstall only),
// plus the legacy hardcoded path and the empty .d directory it leaves behind.
// It never touches other drop-in files (ADR 13) — the generic override.conf is
// handled separately (RemoveLegacyOverride), because its name is not ours.
func RemoveDropIn(home, unit string) error {
	for _, rel := range []string{DropInRelPath(unit), DropInRelPathLegacy} {
		p := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		// drop the .d dir when we emptied it (never when the user has others)
		if dir := filepath.Dir(p); isEmptyDir(dir) {
			os.Remove(dir)
		}
	}
	return nil
}

func isEmptyDir(dir string) bool {
	ents, err := os.ReadDir(dir)
	return err == nil && len(ents) == 0
}

// ---------------------------------------------------------------------------
// shell.json pinned read + merge (pure functions, unit-tested)
// ---------------------------------------------------------------------------

// ReadPinned extracts omarchy.tray.pinned from shell.json content by walking
// bar.layout.{left,center,right} entries with id == "omarchy.tray".
// Tolerant: missing structure yields an empty (nil) slice.
func ReadPinned(shellJSON []byte) ([]string, error) {
	if len(shellJSON) == 0 {
		return nil, nil
	}
	var root map[string]any
	if err := json.Unmarshal(shellJSON, &root); err != nil {
		return nil, fmt.Errorf("shell.json is not valid JSON: %w", err)
	}
	bar, _ := root["bar"].(map[string]any)
	layout, _ := bar["layout"].(map[string]any)
	if layout == nil {
		return nil, nil
	}
	for _, key := range []string{"left", "center", "right"} {
		arr, _ := layout[key].([]any)
		for _, e := range arr {
			entry, _ := e.(map[string]any)
			if entry == nil {
				continue
			}
			if id, _ := entry["id"].(string); id == "omarchy.tray" {
				return pinnedFromEntry(entry), nil
			}
		}
	}
	return nil, nil
}

func pinnedFromEntry(entry map[string]any) []string {
	arr, _ := entry["pinned"].([]any)
	var out []string
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// MergePin returns pinned with Fcitx appended if absent (pure).
func MergePin(pinned []string) []string {
	for _, p := range pinned {
		if p == FcitxId {
			return pinned
		}
	}
	out := make([]string, 0, len(pinned)+1)
	out = append(out, pinned...)
	return append(out, FcitxId)
}

// RemovePin returns pinned without Fcitx (pure; uninstall).
func RemovePin(pinned []string) []string {
	out := pinned[:0:0]
	for _, p := range pinned {
		if p != FcitxId {
			out = append(out, p)
		}
	}
	return out
}

// HasPin reports whether pinned contains Fcitx.
func HasPin(pinned []string) bool {
	for _, p := range pinned {
		if p == FcitxId {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// omarchy bar set (live shell IPC + persist; never hand-edit shell.json)
// ---------------------------------------------------------------------------

// Run is the exec seam for tests.
var Run = func(name string, args ...string) error {
	return execcmd.Command(name, args...).Run()
}

// RestartShell restarts the Omarchy shell so it re-enumerates the SNI tray
// item. shell.json's pinned array hot-reloads via FileView watchChanges, but
// the fcitx5 notificationitem was just enabled by this convergence and a
// running shell does not refresh its StatusNotifierItem display (it stays on
// "Keyboard - English (US)" instead of rime until restarted — verified on
// Omarchy 4.0.1). The manual 中文输入法 note pins then runs
// `omarchy-restart-shell`. The seam is a no-op when the shell is not running.
var RestartShell = func() error {
	if !ShellRunning() {
		return nil
	}
	return Run("omarchy", "restart", "shell")
}

// SetPinned writes the pinned array directly into shell.json (atomic write)
// so the live shell's FileView (watchChanges+atomicWrites, onFileChanged:
// reload) auto-applies it — no `omarchy bar set` needed for the pin itself.
// The array hot-reloads, but the fcitx5 SNI item's display refresh still needs
// the shell restarted (see RestartShell).
//
// Why not the CLI/IPC: on Omarchy 4.0.1 BOTH `omarchy bar set pinned X
// --json` and `omarchy shell shell setBarWidget` coerce array values into a
// plain string (tested on the host). Tray.qml's pinnedIds is
// `settings.pinned instanceof Array ? ... : []`, so a string leaves the icon
// UNPINNED. Writing the array is the only way to actually pin.
//
// Concurrency: the shell itself and `omarchy bar set` also write this file.
// A plain read-modify-write can therefore drop a change made between our read
// and our rename (评审 P1-3). The write is retried against whatever is on disk
// now — the merge is additive and idempotent, so re-running it on top of a
// concurrent edit converges instead of overwriting.
func SetPinned(shellJSONPath string, pinned []string) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		before, err := readOrNil(shellJSONPath)
		if err != nil {
			return err
		}
		want, err := mergePinnedInto(before, pinned)
		if err != nil {
			return err
		}
		// nothing to do (already exactly our bytes): leave the file alone
		if bytes.Equal(before, want) {
			return nil
		}
		if err := state.WriteAtomic(shellJSONPath, want); err != nil {
			return err
		}
		after, err := os.ReadFile(shellJSONPath)
		if err != nil {
			return err
		}
		if bytes.Equal(after, want) {
			return nil
		}
		lastErr = fmt.Errorf("shell.json changed while ompinyin was writing it (another bar edit?)")
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("cannot converge shell.json")
	}
	return lastErr
}

// RemovePinned drops the Fcitx entry from omarchy.tray.pinned in shell.json
// (atomic write). It is the CLEANUP half of the 4.5 hand-over: Omarchy renders
// the IM indicator itself, so a left-over SNI pin would show the same idea
// twice. changed=false when there was no pin. Same race protection as
// SetPinned: another writer (the shell, `omarchy bar set`) may land between our
// read and our rename, so the write is retried against what is on disk now.
func RemovePinned(shellJSONPath string) (changed bool, err error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		before, rerr := readOrNil(shellJSONPath)
		if rerr != nil {
			return false, rerr
		}
		want, dirty, merr := removePinnedFrom(before)
		if merr != nil {
			return false, merr
		}
		if !dirty {
			return false, nil
		}
		if werr := state.WriteAtomic(shellJSONPath, want); werr != nil {
			return false, werr
		}
		after, aerr := os.ReadFile(shellJSONPath)
		if aerr != nil {
			return false, aerr
		}
		if bytes.Equal(after, want) {
			return true, nil
		}
		lastErr = fmt.Errorf("shell.json changed while ompinyin was writing it (another bar edit?)")
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("cannot converge shell.json")
	}
	return false, lastErr
}

// removePinnedFrom drops Fcitx from the pinned array of shell.json content,
// leaving every other field (and pin) untouched. dirty=false when there was
// nothing to remove.
func removePinnedFrom(shellJSON []byte) (out []byte, dirty bool, err error) {
	if len(shellJSON) == 0 {
		return shellJSON, false, nil
	}
	pinned, err := ReadPinned(shellJSON)
	if err != nil {
		return nil, false, err
	}
	if !HasPin(pinned) {
		return shellJSON, false, nil
	}
	merged, err := mergePinnedInto(shellJSON, RemovePin(pinned))
	if err != nil {
		return nil, false, err
	}
	return merged, true, nil
}

func readOrNil(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return b, nil
}

// mergePinnedInto applies the pinned array to the given shell.json bytes.
func mergePinnedInto(shellJSON []byte, pinned []string) ([]byte, error) {
	var root map[string]any
	if len(shellJSON) > 0 {
		if err := json.Unmarshal(shellJSON, &root); err != nil {
			return nil, fmt.Errorf("shell.json is not valid JSON: %w", err)
		}
	}
	if root == nil {
		root = map[string]any{}
	}
	bar, _ := root["bar"].(map[string]any)
	if bar == nil {
		bar = map[string]any{}
		root["bar"] = bar
	}
	layout, _ := bar["layout"].(map[string]any)
	if layout == nil {
		layout = map[string]any{}
		bar["layout"] = layout
	}
	pinArr := make([]any, 0, len(pinned))
	for _, p := range pinned {
		pinArr = append(pinArr, p)
	}
	setTrayPinned(layout, pinArr)

	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func setTrayPinned(layout map[string]any, pinArr []any) {
	for _, section := range []string{"left", "center", "right"} {
		arr, _ := layout[section].([]any)
		for i, e := range arr {
			entry, _ := e.(map[string]any)
			if entry == nil {
				continue
			}
			if isTrayEntry(entry) {
				arr[i] = mustTrayEntry(entry, pinArr)
				layout[section] = arr
				return
			}
		}
	}
	// shell.json 缺失/空时 omarchy.tray 可能不存在：追加到 right 区(Omarchy 默认)。
	right, _ := layout["right"].([]any)
	layout["right"] = append(right, mustTrayEntry(map[string]any{"id": "omarchy.tray"}, pinArr))
}

func isTrayEntry(entry map[string]any) bool {
	id, _ := entry["id"].(string)
	return id == "omarchy.tray"
}

// mustTrayEntry returns a copy of entry with pinned set to pinArr and default
// id preserved.
func mustTrayEntry(entry map[string]any, pinArr []any) map[string]any {
	next := map[string]any{}
	for k, v := range entry {
		next[k] = v
	}
	id, _ := entry["id"].(string)
	if id == "" {
		id = "omarchy.tray"
	}
	next["id"] = id
	next["pinned"] = pinArr
	if _, ok := next["hidden"]; !ok {
		next["hidden"] = []any{}
	}
	return next
}

// ShellRunning reports whether the Omarchy shell is alive (L4 pre-check
// §6.4). Injectable seam for tests. Omarchy 4.x runs the shell as a
// quickshell instance (`quickshell -n -p /usr/share/omarchy/shell`); the
// `omarchy-shell` name is kept as a fallback for older layouts.
var ShellRunning = func() bool {
	if execcmd.Command("pgrep", "-f", "omarchy-shell").Run() == nil {
		return true
	}
	return execcmd.Command("pgrep", "-f", "/usr/share/omarchy/shell").Run() == nil
}
