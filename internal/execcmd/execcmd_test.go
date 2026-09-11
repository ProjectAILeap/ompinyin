package execcmd

import (
	"context"
	"errors"
	"testing"
)

func TestCommandHonorsContext(t *testing.T) {
	orig := Context()
	defer SetContext(orig)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	SetContext(ctx)
	if err := Command("true").Run(); !errors.Is(err, context.Canceled) {
		t.Errorf("a canceled context must abort the child, got %v", err)
	}
}

func TestSetContextInstallsAndGuardsNil(t *testing.T) {
	orig := Context()
	defer SetContext(orig)

	custom, cancel := context.WithCancel(context.Background())
	defer cancel()
	SetContext(custom)
	if Context() != custom {
		t.Fatalf("SetContext must install the given context")
	}

	// A nil context is ignored: Command must never build a cmd with a nil
	// context (exec.CommandContext panics). Typed nil — never a literal.
	var none context.Context
	SetContext(none)
	if Context() != custom {
		t.Error("a nil context must not replace the installed context")
	}
}
