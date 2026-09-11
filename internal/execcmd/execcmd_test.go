package execcmd

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCommandHonorsContext(t *testing.T) {
	orig := currentContext()
	defer SetContext(orig)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	SetContext(ctx)
	if err := Command("true").Run(); !errors.Is(err, context.Canceled) {
		t.Errorf("a canceled context must abort the child, got %v", err)
	}
}

func TestSetContextInstallsAndGuardsNil(t *testing.T) {
	orig := currentContext()
	defer SetContext(orig)

	custom, cancel := context.WithCancel(context.Background())
	defer cancel()
	SetContext(custom)
	if currentContext() != custom {
		t.Fatalf("SetContext must install the given context")
	}

	// A nil context is ignored: Command must never build a cmd with a nil
	// context (exec.CommandContext panics). Typed nil — never a literal.
	var none context.Context
	SetContext(none)
	if currentContext() != custom {
		t.Error("a nil context must not replace the installed context")
	}
}

// TestSudoArgs: without a controlling terminal (agent/CI) sudo must get -n so
// it fails fast instead of hanging on a password prompt nobody answers.
func TestSudoArgs(t *testing.T) {
	if got := strings.Join(SudoArgs(false, "pacman", "-S", "fcitx5"), " "); got != "sudo -n pacman -S fcitx5" {
		t.Errorf("no tty: want 'sudo -n pacman -S fcitx5', got %q", got)
	}
	if got := strings.Join(SudoArgs(true, "pacman", "-S", "fcitx5"), " "); got != "sudo pacman -S fcitx5" {
		t.Errorf("tty: want 'sudo pacman -S fcitx5', got %q", got)
	}
}
