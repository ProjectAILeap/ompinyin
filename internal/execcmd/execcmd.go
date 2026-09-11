// Package execcmd builds *exec.Cmd bound to a process-wide cancellation
// context, so SIGINT/SIGTERM terminates the children the convergence shells out
// to (rime_deployer, systemctl, pacman, DBus, sudo, …) instead of leaving them
// orphaned while the stop window's deferred restart races them. It also owns
// the two process-level shell-out policies every privileged step shares: the
// tty probe and the `sudo` argv shape.
//
// Why a package-level context instead of a context.Context on every seam: the
// exec seams are package-level function variables (T0 stubs them by
// assignment), so adding a parameter would churn every test. The CLI installs
// the run context once at startup; anything that does not (unit tests calling
// the real seams) gets context.Background().
package execcmd

import (
	"context"
	"os"
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

// currentContext returns the installed context (never nil).
func currentContext() context.Context {
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
	return exec.CommandContext(currentContext(), name, args...)
}

// RunInteractive runs name with the caller's terminal wired in, so a sudo
// password prompt is visible and pacman/systemctl progress stays on screen.
func RunInteractive(name string, args ...string) error {
	c := Command(name, args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// HasTTY reports whether a controlling terminal exists that sudo can prompt on.
// Without one (CI, agent, pipe) sudo must use -n so it fails fast with "a
// password is required" instead of hanging on a prompt nobody answers.
func HasTTY() bool {
	f, err := os.Open("/dev/tty")
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// SudoArgs builds the sudo argv for a non-root command, inserting -n when there
// is no controlling terminal. hasTTY is passed in (instead of probed here) so
// callers can keep their own test seam.
func SudoArgs(hasTTY bool, args ...string) []string {
	argv := []string{"sudo"}
	if !hasTTY {
		argv = append(argv, "-n")
	}
	return append(argv, args...)
}

// Cleanup runs fn with a cancellation-immune exec context, restoring the
// previously installed one afterwards. The long children (rime_deployer,
// pacman, a 420MB download) must stay cancellable — but teardown must not be:
// the first SIGINT cancels the run context, so a restart or daemon-reload
// issued from a defer would return context.Canceled without spawning anything
// and leave the user with no input method (§16 invariant 14, 评审 P0-4).
func Cleanup(fn func()) {
	prev := currentContext()
	SetContext(context.Background())
	defer SetContext(prev)
	fn()
}
