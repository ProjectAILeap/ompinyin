package bar

import (
	"testing"

	"github.com/ProjectAILeap/ompinyin/internal/tray"
)

func TestIndicatorPresent(t *testing.T) {
	withWidget := `{"bar":{"layout":{"left":[{"id":"omarchy.workspaces"}],
		"right":[{"id":"omarchy.tray","pinned":[]},{"id":"omarchy.keyboard-layout"}]}}}`
	if !IndicatorPresent([]byte(withWidget)) {
		t.Error("widget in the right section must be detected")
	}
	// the widget may sit in any section
	if !IndicatorPresent([]byte(`{"bar":{"layout":{"center":[{"id":"omarchy.keyboard-layout"}]}}}`)) {
		t.Error("widget in the center section must be detected")
	}
	// a substring match (another keyboard widget) is not the indicator
	for name, body := range map[string]string{
		"absent":            `{"bar":{"layout":{"right":[{"id":"omarchy.tray"}]}}}`,
		"prefix only":       `{"bar":{"layout":{"right":[{"id":"omarchy.keyboard-layout-2"}]}}}`,
		"no bar":            `{}`,
		"bad json":          `not json`,
		"empty":             ``,
		"layout not a list": `{"bar":{"layout":{"right":"omarchy.keyboard-layout"}}}`,
	} {
		if IndicatorPresent([]byte(body)) {
			t.Errorf("%s must read as absent", name)
		}
	}
}

func TestEnsureIndicator(t *testing.T) {
	origShell, origRun := tray.ShellRunning, Run
	defer func() { tray.ShellRunning = origShell; Run = origRun }()

	var calls [][]string
	Run = func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		return nil
	}

	tray.ShellRunning = func() bool { return false }
	if err := EnsureIndicator(); err != nil {
		t.Errorf("a stopped shell is not a failure: %v", err)
	}
	if len(calls) != 0 {
		t.Error("no exec may run while the shell is not running")
	}

	tray.ShellRunning = func() bool { return true }
	if err := EnsureIndicator(); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || len(calls[0]) != 4 || calls[0][0] != "omarchy" ||
		calls[0][1] != "bar" || calls[0][2] != "put" || calls[0][3] != WidgetID {
		t.Errorf("unexpected exec: %v", calls)
	}
}
