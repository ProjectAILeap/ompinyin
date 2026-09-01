// Package patches implements L3: whole-file generation of managed
// *.custom.yaml plus the ownership hash ledger protocol (§5.1).
//
// Red lines (§16): never merge YAML, never silently overwrite user-modified
// managed files, grammar applied to EVERY enabled schema.
package patches

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ProjectAILeap/ompinyin/internal/catalog"
	"github.com/ProjectAILeap/ompinyin/internal/state"
)

// File is one managed file to write: content is generated whole.
type File struct {
	RelPath string // relative to the rime data dir
	Content string
}

// ManagedFiles returns the full set of managed files for the desired state:
//   - default.custom.yaml (always; schema_list only)
//   - <schema>.custom.yaml for every schema in schema_list when Model=true
//   - radical_pinyin.custom.yaml / melt_eng.custom.yaml (always; algebra
//     follows Primary, §2.2)
func ManagedFiles(d catalog.Desired) []File {
	schemas := d.SchemaList()
	files := []File{
		{RelPath: "default.custom.yaml", Content: catalog.DefaultPatch(schemas)},
		{RelPath: "radical_pinyin.custom.yaml", Content: catalog.AlgebraPatch("radical_pinyin", primaryAlgebra(d))},
		{RelPath: "melt_eng.custom.yaml", Content: catalog.AlgebraPatch("melt_eng", primaryAlgebra(d))},
	}
	if d.Model {
		for _, s := range schemas {
			files = append(files, File{RelPath: s + ".custom.yaml", Content: catalog.GrammarPatch()})
		}
	}
	return files
}

func primaryAlgebra(d catalog.Desired) string {
	l, ok := catalog.Lookup(d.Primary)
	if !ok {
		return "algebra_rime_ice"
	}
	return l.Algebra
}

// OrphanFiles returns ledger-recorded managed files that the DESIRED state no
// longer includes — the set convergence must remove.
//
// Computing it as "ledger − ManagedFiles(desired)" covers every leak:
//   - Model=false → all schema-level grammar files
//   - `switch --dsp none` / a different double pinyin → the previous schema's
//     <schema>.custom.yaml (the old `if !d.Model` gate left these behind
//     forever, 评审 P1-5)
//
// Only ledger-recorded paths are returned: a file ompinyin never wrote is not
// ours to delete. Output is sorted for deterministic step output.
func OrphanFiles(st *state.State, d catalog.Desired) []string {
	want := map[string]bool{}
	for _, f := range ManagedFiles(d) {
		want[f.RelPath] = true
	}
	var out []string
	for rel := range st.ManagedFiles {
		if want[rel] || filepath.Base(rel) != rel || !strings.HasSuffix(rel, ".custom.yaml") {
			continue
		}
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// Status classifies a managed file on disk against the ownership ledger (§5.1):
type Status int

const (
	StatusAbsent       Status = iota // 不存在 → 写入
	StatusManaged                    // hash == ledger → 正常重写
	StatusUserModified               // hash != ledger → 用户改过 → 确认/备份
	StatusForeign                    // 无记账哈希 → 视为外来 → 确认/备份
)

// Classify inspects the file at absPath against the ledger record.
func Classify(absPath, ledgerHash string) Status {
	b, err := os.ReadFile(absPath)
	if err != nil {
		return StatusAbsent
	}
	disk := state.HashBytes(b)
	if ledgerHash == "" {
		return StatusForeign
	}
	if disk == ledgerHash {
		return StatusManaged
	}
	return StatusUserModified
}

// StatusString renders the classification in Chinese step output.
func (s Status) String() string {
	switch s {
	case StatusAbsent:
		return "不存在"
	case StatusManaged:
		return "受管"
	case StatusUserModified:
		return "用户已修改"
	default:
		return "外来文件"
	}
}

// WriteFile writes content atomically and updates the ledger record.
func WriteFile(absPath, content string, st *state.State) error {
	if err := state.WriteAtomic(absPath, []byte(content)); err != nil {
		return err
	}
	if st != nil {
		st.ManagedFiles[filepath.Base(absPath)] = state.HashBytes([]byte(content))
	}
	return nil
}

// RemoveFile deletes the file and drops the ledger record.
func RemoveFile(absPath string, st *state.State) error {
	err := os.Remove(absPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if st != nil {
		delete(st.ManagedFiles, filepath.Base(absPath))
	}
	return nil
}
