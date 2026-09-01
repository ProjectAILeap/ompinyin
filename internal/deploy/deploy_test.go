package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBuildUsesTheFullThreeArgForm locks the shape that makes --build compile
// every enabled schema (§3 the deploy seam): userDir + shared data dir +
// buildDir. A truncated form produces only scaffolding, which is the root
// cause of "no Chinese can be typed".
func TestBuildUsesTheFullThreeArgForm(t *testing.T) {
	dir := t.TempDir()
	var got [][]string
	orig := Run
	Run = func(workDir, name string, args ...string) error {
		got = append(got, append([]string{name}, args...))
		return nil
	}
	defer func() { Run = orig }()

	if err := Build(dir, []string{"rime_ice"}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want one invocation, got %d", len(got))
	}
	args := got[0]
	want := []string{"rime_deployer", "--build", dir, SharedData, filepath.Join(dir, "build")}
	if len(args) != len(want) {
		t.Fatalf("arg count wrong: %v", args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("arg %d = %q, want %q", i, args[i], want[i])
		}
	}
}

// TestBuildRefusesMissingDataDir: running --build against an absent user dir
// would "succeed" while compiling nothing.
func TestBuildRefusesMissingDataDir(t *testing.T) {
	orig := Run
	Run = func(string, string, ...string) error { return nil }
	defer func() { Run = orig }()

	err := Build(filepath.Join(t.TempDir(), "nope"), []string{"rime_ice"})
	if err == nil {
		t.Fatal("missing rime dir must fail before shelling out")
	}
	if !strings.Contains(err.Error(), "L2 must run first") {
		t.Errorf("error must point at the layer that populates the dir: %v", err)
	}
}

func TestBuildPropagatesDeployerError(t *testing.T) {
	dir := t.TempDir()
	orig := Run
	Run = func(string, string, ...string) error { return errors.New("exit 1") }
	defer func() { Run = orig }()

	err := Build(dir, []string{"rime_ice"})
	if err == nil {
		t.Fatal("deployer failure must surface")
	}
	for _, hint := range []string{"rime_deployer", "fcitx5-diagnose"} {
		if !strings.Contains(err.Error(), hint) {
			t.Errorf("error must help diagnosis with %q: %v", hint, err)
		}
	}
}

func TestBuildArtifactsExist(t *testing.T) {
	dir := t.TempDir()
	schemas := []string{"rime_ice", "double_pinyin"}
	if got := BuildArtifactsExist(dir, schemas); len(got) != 2 {
		t.Errorf("fresh dir should report both missing, got %v", got)
	}
	p := filepath.Join(dir, "build", "rime_ice.schema.yaml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("compiled"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := BuildArtifactsExist(dir, schemas)
	if len(got) != 1 || got[0] != "double_pinyin" {
		t.Errorf("missing set = %v, want [double_pinyin]", got)
	}
}

// TestWaitForBuildReturnsQuicklyOnSuccess guards the "don't sleep the full
// timeout when the artifacts are already there" path — the converge run pays
// for this on every deploy.
func TestWaitForBuildReturnsQuicklyOnSuccess(t *testing.T) {
	dir := t.TempDir()
	schemas := []string{"rime_ice"}
	p := filepath.Join(dir, "build", "rime_ice.schema.yaml")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("compiled"), 0o644)

	start := time.Now()
	if missing := WaitForBuild(dir, schemas, 5*time.Second); len(missing) != 0 {
		t.Fatalf("want no missing, got %v", missing)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("WaitForBuild should return on the first poll, took %v", d)
	}
}

func TestWaitForBuildTimesOutWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	missing := WaitForBuild(dir, []string{"rime_ice"}, 1200*time.Millisecond)
	if len(missing) != 1 {
		t.Fatalf("absent artifact must be reported after the deadline, got %v", missing)
	}
}

// TestCompileSchemasIsATestSeamOnly: production relies on --build (map-form
// schema_list) compiling everything; this hook exists so T0 can synthesize the
// lazy-deploy result. If it ever starts doing real work, --compile would race
// the deployer.
func TestCompileSchemasIsATestSeamOnly(t *testing.T) {
	if err := CompileSchemas(t.TempDir(), []string{"rime_ice"}); err != nil {
		t.Errorf("production CompileSchemas must stay a no-op: %v", err)
	}
}
