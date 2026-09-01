//go:build !windows

package facts

import "os/exec"

func defaultRun(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

func defaultLookPath(name string) (string, error) {
	return exec.LookPath(name)
}
