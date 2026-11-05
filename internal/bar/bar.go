// Package bar keeps Omarchy 4.5+'s own input-method indicator on the bar
// (§6.4 after the 4.5 hand-over). Omarchy renders the indicator itself
// (shell/plugins/bar/widgets/KeyboardLayout.qml + default/input-methods/
// indicator.py) and ships `omarchy.keyboard-layout` in the default right
// section, so ompinyin no longer creates an icon — it only makes sure the
// widget is on the bar and lets converge delete the pre-4.5 tray artifacts.
package bar

import (
	"encoding/json"

	"github.com/ProjectAILeap/ompinyin/internal/execcmd"
	"github.com/ProjectAILeap/ompinyin/internal/tray"
)

// WidgetID is the shell widget Omarchy uses for the input-method indicator.
const WidgetID = "omarchy.keyboard-layout"

// Run is the exec seam for tests.
var Run = func(name string, args ...string) error {
	return execcmd.Command(name, args...).Run()
}

// IndicatorPresent reports whether shell.json carries a bar entry with id
// WidgetID in any section. Pure (bad JSON reads as absent), so the whole
// decision stays testable without an exec.
func IndicatorPresent(shellJSON []byte) bool {
	if len(shellJSON) == 0 {
		return false
	}
	var root map[string]any
	if err := json.Unmarshal(shellJSON, &root); err != nil {
		return false
	}
	bar, _ := root["bar"].(map[string]any)
	layout, _ := bar["layout"].(map[string]any)
	for _, section := range []string{"left", "center", "right"} {
		arr, _ := layout[section].([]any)
		for _, e := range arr {
			entry, _ := e.(map[string]any)
			if id, _ := entry["id"].(string); id == WidgetID {
				return true
			}
		}
	}
	return false
}

// EnsureIndicator puts the widget on the bar (`omarchy bar put`, idempotent).
// A shell that is not running has nothing to put into: Omarchy's default
// config already contains the widget, so this is a no-op, not a failure.
func EnsureIndicator() error {
	if !tray.ShellRunning() {
		return nil
	}
	return Run("omarchy", "bar", "put", WidgetID)
}
