// Package deploy runs the rime deploy seam between L3 and the L4 stop window
// (§3). It must run while fcitx5 is stopped, otherwise lazy deployment races
// and half-built artifacts appear.
//
// Deploy mirrors the upstream reference that produced the known-good
// pre-ompinyin configuration on this host. Its deploy runs
// `rime_deployer --build <userDir> /usr/share/rime-data <buildDir>` (full
// three-arg form, with the system shared-data dir). The userDir must carry a
// `default.yaml`-merged `default.custom.yaml` whose `schema_list` uses the
// `- schema: <id>` map form; only then does `--build` compile the enabled
// schemas and write the tables. With the bare `- <id>` string form the
// deployer ignores the list, compiles nothing, and leaves only a `build/default.yaml`
// scaffolding — which then prompts fcitx5-rime to skip its own lazy deploy, so
// no schema is ever compiled/activated and the input method produces nothing.
// That short-list-form bug was THE root cause of “no Chinese can be typed”
// (verified: the known-good upstream output and the pre-ompinyin backup both
// use `- schema:`).
//
// `rime_deployer --compile` is therefore never needed: `--build` (with a
// map-form schema_list) performs the full compile and yields the correct
// tables (rime_ice.table.bin 60658388) that fcitx5-rime loads. Do NOT delete
// the build dir or rely on fcitx5-rime lazily compiling it later.
package deploy

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Run is the exec seam for tests (fake rime_deployer in T0). dir is the
// working directory ("" = current).
var Run = func(dir, name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Dir = dir
	return c.Run()
}

// CompileSchemas is the exec seam for the rime build step. In production it is
// a no-op: Build() runs rime_deployer --build (build_info) and fcitx5-rime
// recompiles the schemas lazily on the next activation, so no explicit compile
// is needed. Tests override this to synthesize the grammar-compiled build
// schemas immediately.
var CompileSchemas = func(rimeDir string, schemas []string) error {
	return nil
}

// SharedData is the system-wide rime data dir.
const SharedData = "/usr/share/rime-data"

// Build runs:
//
//	rime_deployer --build $RIME /usr/share/rime-data $RIME/build
//
// where $RIME is the XDG data rime dir (~/.local/share/fcitx5/rime). With a
// map-form schema_list (`- schema: <id>`) in default.custom.yaml this compiles
// every enabled schema and writes the tables (the correct behavior). With the
// bare `- <id>` form the deployer compiles nothing — that was the root cause of
// “no Chinese”. See the package comment. The full three-arg form (with the
// system shared-data dir) is required so the deployer can resolve the base
// schemas; a truncated form yields only scaffolding.
func Build(rimeDir string, schemas []string) error {
	if _, err := os.Stat(rimeDir); err != nil {
		return fmt.Errorf("rime data dir %s missing (L2 must run first)", rimeDir)
	}
	staged := filepath.Join(rimeDir, "build")
	if err := Run("", "rime_deployer", "--build", rimeDir, SharedData, staged); err != nil {
		return fmt.Errorf("rime_deployer --build failed: %w (inspect %s and run fcitx5-diagnose)", err, rimeDir)
	}
	return nil
}

// WaitForBuild polls build/<schema>.schema.yaml for every schema until they
// appear (fcitx5-rime lazy deploy after the service starts) or until timeout.
// It returns the schemas still missing at timeout (empty slice = all built).
func WaitForBuild(rimeDir string, schemas []string, timeout time.Duration) []string {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if missing := BuildArtifactsExist(rimeDir, schemas); len(missing) == 0 {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return BuildArtifactsExist(rimeDir, schemas)
}

// BuildArtifactsExist checks that build/<schema>.schema.yaml exists for every
// schema (L5 read-only probe).
func BuildArtifactsExist(rimeDir string, schemas []string) []string {
	var missing []string
	for _, s := range schemas {
		p := filepath.Join(rimeDir, "build", s+".schema.yaml")
		if _, err := os.Stat(p); err != nil {
			missing = append(missing, s)
		}
	}
	return missing
}
