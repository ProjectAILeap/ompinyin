package tray

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProjectAILeap/ompinyin/internal/service"
)

func TestDropInContent(t *testing.T) {
	// ADR 12/13: dedicated file, removes --disable notificationitem
	if !strings.Contains(DropInContent, "ExecStart=") {
		t.Fatal("ExecStart= reset line missing")
	}
	if !strings.Contains(DropInContent, "ExecStart=/usr/bin/fcitx5") {
		t.Fatal("clean ExecStart missing")
	}
	if strings.Contains(DropInContent, "notificationitem") {
		// the flag must be gone; only a comment may mention it
		for _, line := range strings.Split(DropInContent, "\n") {
			if strings.HasPrefix(line, "ExecStart") && strings.Contains(line, "notificationitem") {
				t.Fatalf("disable flag still present: %s", line)
			}
		}
	}
	if !strings.HasSuffix(DropInRelPath(""), "ompinyin-notificationitem.conf") {
		t.Fatalf("drop-in must use the dedicated filename, got %s", DropInRelPath(""))
	}
	// the path follows the discovered unit, not a hardcoded one (评审 P1-4)
	if got, want := DropInRelPath("fcitx5.service"), ".config/systemd/user/fcitx5.service.d/ompinyin-notificationitem.conf"; got != want {
		t.Errorf("drop-in path must follow the unit:\n got %s\nwant %s", got, want)
	}
}

func TestDropInEnabled(t *testing.T) {
	if !DropInEnabled(DropInContent) {
		t.Errorf("the managed drop-in must count as enabling notificationitem:\n%s", DropInContent)
	}
	cases := map[string]string{
		"still disables":  "[Service]\nExecStart=/usr/bin/fcitx5 --disable notificationitem\n",
		"empty ExecStart": "[Service]\nExecStart=\n",
		"no Service":      "# managed by ompinyin\n",
	}
	for name, body := range cases {
		if DropInEnabled(body) {
			t.Errorf("%s must not be reported as enabled", name)
		}
	}
}

const shellJSON = `{
  "bar": {
    "layout": {
      "left": [{"id": "omarchy.workspaces"}],
      "center": [{"id": "omarchy.clock"}],
      "right": [
        {"id": "omarchy.keyboard-layout"},
        {"id": "omarchy.tray", "pinned": ["Foo", "Bar"]}
      ]
    }
  }
}`

func TestReadPinned(t *testing.T) {
	pinned, err := ReadPinned([]byte(shellJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(pinned) != 2 || pinned[0] != "Foo" || pinned[1] != "Bar" {
		t.Fatalf("pinned = %v", pinned)
	}
	// missing structure → empty, no error
	if p, err := ReadPinned([]byte(`{}`)); err != nil || p != nil {
		t.Fatalf("empty json: %v %v", p, err)
	}
	if _, err := ReadPinned([]byte("not json")); err == nil {
		t.Fatal("invalid json must error")
	}
}

func TestMergePinKeepsExistingPins(t *testing.T) {
	// 读→并→set：绝不能吞掉用户已有 pin（§6.4）
	merged := MergePin([]string{"Foo", "Bar"})
	want := []string{"Foo", "Bar", FcitxId}
	if len(merged) != 3 {
		t.Fatalf("merged = %v", merged)
	}
	for i := range want {
		if merged[i] != want[i] {
			t.Fatalf("merged = %v, want %v", merged, want)
		}
	}
	// idempotent
	if again := MergePin(merged); len(again) != 3 {
		t.Fatalf("not idempotent: %v", again)
	}
}

func TestRemovePin(t *testing.T) {
	got := RemovePin([]string{FcitxId, "Foo"})
	if len(got) != 1 || got[0] != "Foo" {
		t.Fatalf("got %v", got)
	}
	if empty := RemovePin([]string{FcitxId}); empty == nil || len(empty) != 0 {
		t.Fatalf("empty pin must still be settable: %v", empty)
	}
}

func TestSetPinnedWritesArrayToFile(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "shell.json")
	os.WriteFile(p, []byte(shellJSON), 0o644)

	if err := SetPinned(p, []string{"Foo", FcitxId}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadPinned(b)
	if err != nil {
		t.Fatal(err)
	}
	// the value must be an ARRAY (Tray.qml needs an array), with the user's
	// existing pins preserved
	if len(got) != 2 || got[0] != "Foo" || got[1] != FcitxId {
		t.Fatalf("pinned after set = %v", got)
	}
	// assert persisted pinned is a JSON array, not a string
	var node map[string]any
	if err := json.Unmarshal(b, &node); err != nil {
		t.Fatal(err)
	}
	right := node["bar"].(map[string]any)["layout"].(map[string]any)["right"].([]any)
	found := false
	for _, e := range right {
		em := e.(map[string]any)
		if em["id"] == "omarchy.tray" {
			if _, ok := em["pinned"].([]any); !ok {
				t.Fatalf("pinned is not an array: %T", em["pinned"])
			}
			found = true
		}
	}
	if !found {
		t.Fatal("omarchy.tray entry not found")
	}
}

func TestSetPinnedCreatesMissingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "shell.json")
	if err := SetPinned(p, []string{FcitxId}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	got, err := ReadPinned(b)
	if err != nil || len(got) != 1 || got[0] != FcitxId {
		t.Fatalf("readback = %v err=%v", got, err)
	}
}

func TestSetPinnedRejectsInvalidJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "shell.json")
	os.WriteFile(p, []byte("not json"), 0o644)
	if err := SetPinned(p, []string{FcitxId}); err == nil {
		t.Fatal("invalid shell.json must error")
	}
}

// TestDeriveDropInContent (评审 2.2): the drop-in must strip ONLY
// `--disable notificationitem` and preserve every other ExecStart token, so
// an upstream flag/path change is not silently clobbered by a hardcoded line.
func TestDeriveDropInContent(t *testing.T) {
	got := DeriveDropInContent("/usr/bin/fcitx5 --disable notificationitem --replace")
	for _, want := range []string{"ExecStart=/usr/bin/fcitx5 --replace", "ExecStart="} {
		if !strings.Contains(got, want) {
			t.Errorf("derived drop-in missing %q:\n%s", want, got)
		}
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "ExecStart") && strings.Contains(line, "notificationitem") {
			t.Errorf("disable flag survived: %s", line)
		}
	}
	// different binary path must be preserved, not reset to /usr/bin/fcitx5
	got2 := DeriveDropInContent("/usr/local/bin/fcitx5 --disable notificationitem")
	if !strings.Contains(got2, "ExecStart=/usr/local/bin/fcitx5") {
		t.Errorf("binary path clobbered:\n%s", got2)
	}
	// empty → fallback
	if !strings.Contains(DeriveDropInContent(""), "ExecStart=/usr/bin/fcitx5") {
		t.Error("empty ExecStart must fall back to /usr/bin/fcitx5")
	}
}

// TestSetPinnedConcurrentEdit locks 评审 P1-3: the shell and `omarchy bar set`
// also write shell.json. A read-modify-write that lands on top of a concurrent
// edit must converge (re-merge), not silently drop the other writer's change.
func TestSetPinnedConcurrentEdit(t *testing.T) {
	home := t.TempDir()
	p := ShellJSONPath(home)
	os.MkdirAll(filepath.Dir(p), 0o755)
	// start with a tray entry pinned to Foo
	os.WriteFile(p, []byte(`{"bar":{"layout":{"right":[{"id":"omarchy.tray","pinned":["Foo"]}]}}}`), 0o644)

	if err := SetPinned(p, []string{"Foo", "Fcitx"}); err != nil {
		t.Fatal(err)
	}
	// a competing writer replaces the file with a different widget config that
	// already carries Fcitx; our next write must not resurrect the old bytes
	competing := `{"bar":{"layout":{"right":[{"id":"omarchy.tray","pinned":["Fcitx","Bar"]}]}}}`
	go func() { _ = os.WriteFile(p, []byte(competing), 0o644) }()
	if err := SetPinned(p, []string{"Fcitx", "Bar"}); err != nil {
		t.Fatalf("concurrent write should converge: %v", err)
	}
	b, _ := os.ReadFile(p)
	pinned, err := ReadPinned(b)
	if err != nil {
		t.Fatal(err)
	}
	if !HasPin(pinned) {
		t.Errorf("Fcitx pin lost under concurrency: %v", pinned)
	}
	if len(pinned) != 2 || pinned[1] != "Bar" {
		t.Errorf("competing writer's pin was dropped: %v", pinned)
	}
}

// TestDropInPathFollowsUnit locks 评审 P1-4: the drop-in must live under the
// unit that actually runs, otherwise notificationitem stays disabled while
// every existence check still passes.
func TestDropInPathFollowsUnit(t *testing.T) {
	home := t.TempDir()
	generic := DropInPath(home, "fcitx5.service")
	omarchy := DropInPath(home, "omarchy-fcitx5.service")
	if generic == omarchy {
		t.Fatal("drop-in path ignores the unit")
	}
	if filepath.Base(filepath.Dir(generic)) != "fcitx5.service.d" {
		t.Errorf("generic unit path wrong: %s", generic)
	}
	// empty unit falls back to the Omarchy default
	if DropInPath(home, "") != omarchy {
		t.Errorf("empty unit must fall back to %s", omarchy)
	}
	// writing + removing for a non-default unit works and cleans its own dir
	if created, err := WriteDropIn(home, "fcitx5.service", DropInContent); err != nil || !created {
		t.Fatalf("WriteDropIn: created=%v err=%v", created, err)
	}
	if _, err := os.Stat(generic); err != nil {
		t.Fatalf("drop-in not written for the generic unit: %v", err)
	}
	if err := RemoveDropIn(home, "fcitx5.service"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(generic); !os.IsNotExist(err) {
		t.Error("drop-in not removed")
	}
	if _, err := os.Stat(filepath.Dir(generic)); !os.IsNotExist(err) {
		t.Error("empty .d directory left behind")
	}
	// a user drop-in in the same dir must survive
	os.MkdirAll(filepath.Dir(omarchy), 0o755)
	os.WriteFile(filepath.Join(filepath.Dir(omarchy), "user.conf"), []byte("[Service]\n"), 0o644)
	os.WriteFile(omarchy, []byte(DropInContent), 0o644)
	if err := RemoveDropIn(home, "omarchy-fcitx5.service"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(omarchy), "user.conf")); err != nil {
		t.Error("user drop-in deleted (ADR 13 violation)")
	}
}

// RemovePinned is the 4.5+ cleanup half: the SNI pin must go while every other
// pin the user made survives.
func TestRemovePinned(t *testing.T) {
	home := t.TempDir()
	p := ShellJSONPath(home)
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(shellJSON), 0o644)

	if err := SetPinned(p, []string{"Foo", "Bar", FcitxId}); err != nil {
		t.Fatal(err)
	}
	changed, err := RemovePinned(p)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("removing an existing pin must report changed")
	}
	b, _ := os.ReadFile(p)
	pinned, err := ReadPinned(b)
	if err != nil {
		t.Fatal(err)
	}
	if HasPin(pinned) {
		t.Errorf("Fcitx pin survived: %v", pinned)
	}
	if len(pinned) != 2 || pinned[0] != "Foo" || pinned[1] != "Bar" {
		t.Errorf("user pins damaged: %v", pinned)
	}
	// idempotent: nothing left to remove, and the bytes stay put
	before := string(b)
	changed, err = RemovePinned(p)
	if err != nil || changed {
		t.Errorf("second call changed=%v err=%v", changed, err)
	}
	if b2, _ := os.ReadFile(p); string(b2) != before {
		t.Error("an idempotent call must not rewrite the file")
	}
	// no shell.json at all: not an error, nothing to do
	if changed, err := RemovePinned(filepath.Join(home, "nope.json")); err != nil || changed {
		t.Errorf("missing file: changed=%v err=%v", changed, err)
	}
	// invalid JSON is a real error (never silently dropped)
	os.WriteFile(p, []byte("not json"), 0o644)
	if _, err := RemovePinned(p); err == nil {
		t.Error("invalid shell.json must error")
	}
}

// The v1.0 override.conf is name-agnostic: only its CONTENT says it is ours.
// Legacy body → delete; derived body (written by an early v1.3 under the generic
// name) → delete; anything else → HAND OFF (it is someone else's file).
func TestRemoveLegacyOverride(t *testing.T) {
	const unit = "omarchy-fcitx5.service"
	derived := DeriveDropInContent("/usr/bin/fcitx5 --replace --disable notificationitem")

	cases := []struct {
		name    string
		content string
		remove  bool
	}{
		{"v1.0 literal body", LegacyOverrideBody, true},
		{"derived drop-in body", derived, true},
		{"trailing newline noise", LegacyOverrideBody + "\n", true},
		{"user-authored override", "[Service]\nEnvironment=FOO=1\n", false},
		{"user-authored ExecStart", "[Service]\nExecStart=\nExecStart=/usr/local/bin/fcitx5 --replace\n", false},
		{"empty file", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := LegacyOverridePath(dir, unit)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			// an unrelated drop-in in the same dir must always survive
			other := filepath.Join(filepath.Dir(p), "user.conf")
			os.WriteFile(other, []byte("[Service]\n"), 0o644)

			removed, err := RemoveLegacyOverride(dir, unit, derived)
			if err != nil {
				t.Fatal(err)
			}
			if removed != tc.remove {
				t.Fatalf("removed=%v, want %v", removed, tc.remove)
			}
			if _, err := os.Stat(p); (err != nil) != tc.remove {
				t.Errorf("file state wrong after remove (err=%v, want removed=%v)", err, tc.remove)
			}
			if _, err := os.Stat(other); err != nil {
				t.Error("unrelated drop-in was deleted")
			}
			// the .d dir stays when the user still has a drop-in in it
			if _, err := os.Stat(filepath.Dir(p)); err != nil {
				t.Errorf(".d directory holding a user drop-in was removed: %v", err)
			}
		})
	}

	// idempotent / absent
	if removed, err := RemoveLegacyOverride(t.TempDir(), unit, derived); err != nil || removed {
		t.Errorf("missing file: removed=%v err=%v", removed, err)
	}
}

// TestLegacyOverridePathFollowsUnit: like the drop-in, the override path must
// follow the unit that actually runs.
func TestLegacyOverridePathFollowsUnit(t *testing.T) {
	home := t.TempDir()
	if got, want := LegacyOverrideRelPath("fcitx5.service"), ".config/systemd/user/fcitx5.service.d/override.conf"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if LegacyOverridePath(home, "") != LegacyOverridePath(home, DefaultUnit) {
		t.Error("empty unit must fall back to the Omarchy default unit")
	}
}

// TestDerivedDropInBody: the derived body follows the unit that RUNS (generic
// fcitx5.service included) and falls back to the static body when unreadable.
func TestDerivedDropInBody(t *testing.T) {
	// hermetic: a dev machine really has /usr/lib/systemd/user/omarchy-fcitx5.service
	dirs := service.SystemUnitDirs
	service.SystemUnitDirs = nil
	t.Cleanup(func() { service.SystemUnitDirs = dirs })

	home := t.TempDir()
	if got := DerivedDropInBody(home, "omarchy-fcitx5.service"); got != DropInContent {
		t.Errorf("no unit file must fall back to the static body:\n%s", got)
	}
	unitFile := filepath.Join(home, ".config", "systemd", "user", "fcitx5.service")
	os.MkdirAll(filepath.Dir(unitFile), 0o755)
	os.WriteFile(unitFile, []byte("[Service]\nExecStart=/usr/local/bin/fcitx5 --disable notificationitem --replace\n"), 0o644)
	got := DerivedDropInBody(home, "fcitx5.service")
	if !strings.Contains(got, "ExecStart=/usr/local/bin/fcitx5 --replace") {
		t.Errorf("derived body must keep the unit's other flags:\n%s", got)
	}
}
