package patches

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProjectAILeap/ompinyin/internal/catalog"
	"github.com/ProjectAILeap/ompinyin/internal/state"
)

// TestManagedFilesGoldenQuanpinModel covers the default terminal state.
func TestManagedFilesGoldenQuanpinModel(t *testing.T) {
	d := catalog.DefaultDesired()
	files := map[string]string{}
	for _, f := range ManagedFiles(d) {
		files[f.RelPath] = f.Content
	}

	for _, name := range []string{"default.custom.yaml", "radical_pinyin.custom.yaml", "melt_eng.custom.yaml", "rime_ice.custom.yaml"} {
		if _, ok := files[name]; !ok {
			t.Fatalf("missing managed file %s", name)
		}
	}
	if got := files["default.custom.yaml"]; !strings.Contains(got, "- schema: rime_ice\n") || strings.Contains(got, "double_pinyin") {
		t.Errorf("default.custom.yaml wrong:\n%s", got)
	}
	// header present on every managed file
	for name, content := range files {
		if !strings.HasPrefix(content, "# managed by ompinyin v") {
			t.Errorf("%s missing managed header", name)
		}
	}
}

// TestManagedFilesDSPModel asserts the installer pitfall fix: the grammar must
// be applied to EVERY enabled schema, including the double pinyin one.
func TestManagedFilesDSPModel(t *testing.T) {
	d := catalog.Desired{Primary: "quanpin", Extra: []string{"zrm"}, Model: true, Channel: "stable"}
	files := map[string]string{}
	for _, f := range ManagedFiles(d) {
		files[f.RelPath] = f.Content
	}
	for _, schema := range []string{"rime_ice", "double_pinyin"} {
		c, ok := files[schema+".custom.yaml"]
		if !ok {
			t.Fatalf("schema %s has no grammar custom.yaml", schema)
		}
		if !strings.Contains(c, "wanxiang-lts-zh-hans") || !strings.Contains(c, "collocation_penalty: -14") {
			t.Errorf("schema %s grammar not official:\n%s", schema, c)
		}
	}
}

// TestAlgebraFollowsPrimary asserts §2.2/§16 invariant 6.
func TestAlgebraFollowsPrimary(t *testing.T) {
	d := catalog.Desired{Primary: "flypy", Extra: []string{"quanpin"}, Channel: "stable"}
	found := 0
	for _, f := range ManagedFiles(d) {
		if f.RelPath == "radical_pinyin.custom.yaml" || f.RelPath == "melt_eng.custom.yaml" {
			found++
			if !strings.Contains(f.Content, "algebra_double_pinyin_flypy") {
				t.Errorf("%s algebra does not follow primary:\n%s", f.RelPath, f.Content)
			}
		}
	}
	if found != 2 {
		t.Fatalf("radical/melt files missing")
	}
}

func TestManagedFilesNoModel(t *testing.T) {
	d := catalog.Desired{Primary: "quanpin", Model: false, Channel: "stable"}
	for _, f := range ManagedFiles(d) {
		if f.RelPath == "rime_ice.custom.yaml" {
			t.Error("Model=false must not generate schema grammar files")
		}
		if strings.Contains(f.Content, "grammar") {
			t.Errorf("%s contains grammar without model", f.RelPath)
		}
	}
}

// TestOwnershipProtocol covers the hash ledger: absent → write, managed →
// rewrite, foreign/unrecorded → treated as external.
func TestOwnershipProtocol(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMPINYIN_TEST_HOME", home)
	rimeDir := filepath.Join(home, ".local", "share", "fcitx5", "rime")
	abs := filepath.Join(rimeDir, "default.custom.yaml")

	st := state.New()
	content := catalog.DefaultPatch([]string{"rime_ice"})

	if got := Classify(abs, st.ManagedFiles["default.custom.yaml"]); got != StatusAbsent {
		t.Fatalf("want StatusAbsent, got %v", got)
	}
	if err := WriteFile(abs, content, st); err != nil {
		t.Fatal(err)
	}
	if got := Classify(abs, st.ManagedFiles["default.custom.yaml"]); got != StatusManaged {
		t.Fatalf("want StatusManaged, got %v", got)
	}
	// user edits
	os.WriteFile(abs, []byte("patch:\n  menu/page_size: 9\n"), 0o644)
	if got := Classify(abs, st.ManagedFiles["default.custom.yaml"]); got != StatusUserModified {
		t.Fatalf("want StatusUserModified, got %v", got)
	}
	// foreign (unrecorded)
	os.WriteFile(abs, []byte("x: 1\n"), 0o644)
	if got := Classify(abs, ""); got != StatusForeign {
		t.Fatalf("want StatusForeign, got %v", got)
	}
	// removal drops the ledger entry
	if err := RemoveFile(abs, st); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.ManagedFiles["default.custom.yaml"]; ok {
		t.Error("ledger entry not dropped")
	}
}

func TestOrphanFiles(t *testing.T) {
	st := state.New()
	st.ManagedFiles["default.custom.yaml"] = "x"
	st.ManagedFiles["radical_pinyin.custom.yaml"] = "x"
	st.ManagedFiles["melt_eng.custom.yaml"] = "x"
	st.ManagedFiles["rime_ice.custom.yaml"] = "x"
	st.ManagedFiles["double_pinyin.custom.yaml"] = "x"

	// Model=false: every schema-level grammar file is an orphan
	if got := OrphanFiles(st, catalog.Desired{Primary: "quanpin", Model: false, Channel: "stable"}); len(got) != 2 {
		t.Fatalf("Model=false want 2 orphans, got %v", got)
	}

	// 评审 P1-5: switching layouts must not leave the previous schema's file.
	// Model stays on, only the enabled set changes → double_pinyin is orphaned.
	got := OrphanFiles(st, catalog.Desired{Primary: "quanpin", Model: true, Channel: "stable"})
	if len(got) != 1 || got[0] != "double_pinyin.custom.yaml" {
		t.Errorf("layout switch should orphan double_pinyin.custom.yaml, got %v", got)
	}

	// A desired state that includes both orphans nothing
	both := catalog.Desired{Primary: "quanpin", Extra: []string{"zrm"}, Model: true, Channel: "stable"}
	if got := OrphanFiles(st, both); len(got) != 0 {
		t.Errorf("nothing should be orphaned when both schemas are enabled, got %v", got)
	}

	// Files ompinyin never wrote are never returned (not ours to delete)
	if got := OrphanFiles(state.New(), catalog.DefaultDesired()); len(got) != 0 {
		t.Errorf("empty ledger must yield no orphans, got %v", got)
	}
}
