//go:build !windows

package facts

import (
	"os/exec"

	"github.com/ProjectAILeap/ompinyin/internal/execcmd"
)

func defaultRun(name string, args ...string) error {
	return execcmd.Command(name, args...).Run()
}

func defaultLookPath(name string) (string, error) {
	return exec.LookPath(name)
}
