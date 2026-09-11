// Package execcmd builds *exec.Cmd bound to a process-wide cancellation
// context, so SIGINT/SIGTERM terminates the children the convergence shells out
// to (rime_deployer, systemctl, pacman, DBus, sudo, …) instead of leaving them
// orphaned while the stop window's deferred restart races them.
//
// Why a package-level context instead of a context.Context on every seam: the
// exec seams are package-level function variables (T0 stubs them by
// assignment), so adding a parameter would churn every test. The CLI installs
// the run context once at startup; anything that does not (unit tests calling
// the real seams) gets context.Background().
package execcmd

import (
	"context"
	"os/exec"
	"sync"
)

var (
	mu  sync.RWMutex
	ctx context.Context = context.Background()
)

// SetContext installs the cancellation context used by subsequently created
// commands. A nil context is ignored. Call once at startup (cmd/ompinyin sets
// the SIGINT-cancellable run context).
func SetContext(c context.Context) {
	if c == nil {
		return
	}
	mu.Lock()
	ctx = c
	mu.Unlock()
}

// Context returns the installed context (never nil).
func Context() context.Context {
	mu.RLock()
	defer mu.RUnlock()
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// Command is exec.CommandContext bound to the installed context. Exec seam
// defaults use it so cancellation reaches the child process.
func Command(name string, args ...string) *exec.Cmd {
	return exec.CommandContext(Context(), name, args...)
}
