// Package catalog holds the pure, I/O-free terminal-state definitions:
// the double-pinyin layout catalog, the official grammar constants (§5.2),
// asset URLs and mirrors (§5.3), and the Desired terminal state (§2).
package catalog

import (
	"fmt"
	"strings"
)

// Version is the BUILD version, injected by the linker:
//
//	-ldflags "-X …/internal/catalog.Version=$(git describe)"
//
// It is reported by `ompinyin version` and sent as the download User-Agent.
// It is deliberately NOT used in ManagedHeader(): a build stamp changes on
// every compile, and putting it in the header would make every managed file's
// bytes differ per build — which forces an L3 rewrite + a full rime rebuild on
// each run and destroys the "converged re-run is a no-op" guarantee (真机实测
// 到：dev 构建后 plan.need.l3 恒为 true).
var Version = "1.0.0"

// ManagedFormat is the version of the MANAGED FILE FORMAT, and the only thing
// stamped into owned files. Bump it when the generated content changes shape,
// so existing hosts converge to the new bytes exactly once.
const ManagedFormat = "1.0.0"

// ManagedHeader is the first line of every file ompinyin owns.
func ManagedHeader() string {
	return fmt.Sprintf("# managed by ompinyin v%s — hand edits will be overwritten", ManagedFormat)
}

// ---------------------------------------------------------------------------
// Desired terminal state (§2)
// ---------------------------------------------------------------------------

// Desired is the declarative terminal state shared by install / update /
// switch / status / doctor.
type Desired struct {
	Primary string   `json:"primary"` // layout ID, schema_list[0]
	Extra   []string `json:"extra,omitempty"`
	Model   bool     `json:"model"`   // wanxiang LMDG
	Channel string   `json:"channel"` // stable | nightly
}

// IsZero reports whether the desired state was never persisted.
func (d Desired) IsZero() bool {
	return d.Primary == "" && len(d.Extra) == 0 && !d.Model && d.Channel == ""
}

// DefaultDesired is the zero-flag terminal state (§1.2).
func DefaultDesired() Desired {
	// stable: the release-tagged build (default). NOTE: an earlier observation
	// that "stable does not compile" was actually the bare `- <id>` schema_list
	// short-form bug; with the correct `- schema: <id>` map form the stable
	// release compiles fine (verified: rime_deployer --build emits
	// rime_ice.table.bin).
	return Desired{Primary: "quanpin", Extra: nil, Model: true, Channel: "stable"}
}

// Validate enforces the §2.2 flag rules.
func (d Desired) Validate() error {
	if _, ok := Lookup(d.Primary); !ok {
		return fmt.Errorf("unknown layout id %q", d.Primary)
	}
	if d.Channel != "stable" && d.Channel != "nightly" {
		return fmt.Errorf("channel must be stable|nightly, got %q", d.Channel)
	}
	seen := map[string]bool{}
	for _, e := range d.Extra {
		l, ok := Lookup(e)
		if !ok {
			return fmt.Errorf("unknown layout id %q", e)
		}
		if l.DoublePinyin {
			// ok
		} else if e == "quanpin" && d.Primary != "quanpin" {
			// §2.2 --dsp-default: quanpin kept in Extra as the fallback schema
		} else {
			return fmt.Errorf("extra layout %q must be a double pinyin layout", e)
		}
		if seen[e] {
			return fmt.Errorf("duplicate layout %q", e)
		}
		seen[e] = true
	}
	return nil
}

// SchemaList returns the deduplicated schema list: primary first (§2.2).
func (d Desired) SchemaList() []string {
	var out []string
	add := func(id string) {
		l, ok := Lookup(id)
		if !ok {
			return
		}
		for _, s := range out {
			if s == l.Schema {
				return
			}
		}
		out = append(out, l.Schema)
	}
	add(d.Primary)
	for _, e := range d.Extra {
		add(e)
	}
	return out
}

// ---------------------------------------------------------------------------
// Layout catalog (§2.1)
// ---------------------------------------------------------------------------

// Layout describes one input layout entry.
type Layout struct {
	ID           string // short ID used by CLI and state.json
	Name         string // human name
	Schema       string // rime schema name on disk
	Algebra      string // radical/melt algebra recipe name
	DoublePinyin bool
}

// Layouts is the fixed catalog; algebra names come from the official
// others/recipes/config.recipe.yaml (algebra_${schema}).
var Layouts = []Layout{
	{ID: "quanpin", Name: "全拼", Schema: "rime_ice", Algebra: "algebra_rime_ice", DoublePinyin: false},
	{ID: "zrm", Name: "自然码", Schema: "double_pinyin", Algebra: "algebra_double_pinyin", DoublePinyin: true},
	{ID: "flypy", Name: "小鹤", Schema: "double_pinyin_flypy", Algebra: "algebra_double_pinyin_flypy", DoublePinyin: true},
	{ID: "mspy", Name: "微软", Schema: "double_pinyin_mspy", Algebra: "algebra_double_pinyin_mspy", DoublePinyin: true},
	{ID: "sogou", Name: "搜狗", Schema: "double_pinyin_sogou", Algebra: "algebra_double_pinyin_sogou", DoublePinyin: true},
	{ID: "abc", Name: "智能 ABC", Schema: "double_pinyin_abc", Algebra: "algebra_double_pinyin_abc", DoublePinyin: true},
	{ID: "ziguang", Name: "紫光", Schema: "double_pinyin_ziguang", Algebra: "algebra_double_pinyin_ziguang", DoublePinyin: true},
	{ID: "jiajia", Name: "拼音加加", Schema: "double_pinyin_jiajia", Algebra: "algebra_double_pinyin_jiajia", DoublePinyin: true},
}

// Lookup finds a layout by short ID.
func Lookup(id string) (Layout, bool) {
	for _, l := range Layouts {
		if l.ID == id {
			return l, true
		}
	}
	return Layout{}, false
}

// DoublePinyinIDs returns all double pinyin layout IDs.
func DoublePinyinIDs() []string {
	var out []string
	for _, l := range Layouts {
		if l.DoublePinyin {
			out = append(out, l.ID)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Managed patch bodies (§5.1) — whole files, never YAML merges
// ---------------------------------------------------------------------------

// GrammarPatch renders the managed <schema>.custom.yaml carrying the official
// grammar block (§5.2, from rime-ice others/recipes/grammar.recipe.yaml).
// Must be applied to EVERY enabled schema in schema_list (ADR 9).
func GrammarPatch() string {
	var b strings.Builder
	b.WriteString(ManagedHeader())
	b.WriteString("\npatch:\n")
	b.WriteString("  grammar:\n")
	b.WriteString("    language: " + GrammarLanguage + "\n")
	fmt.Fprintf(&b, "    collocation_max_length: %d\n", GrammarCollocationMaxLength)
	fmt.Fprintf(&b, "    collocation_min_length: %d\n", GrammarCollocationMinLength)
	fmt.Fprintf(&b, "    collocation_penalty: %d\n", GrammarCollocationPenalty)
	fmt.Fprintf(&b, "    non_collocation_penalty: %d\n", GrammarNonCollocationPenalty)
	fmt.Fprintf(&b, "    weak_collocation_penalty: %d\n", GrammarWeakCollocationPenalty)
	fmt.Fprintf(&b, "    rear_penalty: %d\n", GrammarRearPenalty)
	fmt.Fprintf(&b, "  translator/contextual_suggestions: %v\n", GrammarContextualSuggestions)
	fmt.Fprintf(&b, "  translator/max_homophones: %d\n", GrammarMaxHomophones)
	return b.String()
}

// Official grammar constants (§5.2).
const (
	GrammarLanguage               = "wanxiang-lts-zh-hans"
	GrammarCollocationMaxLength   = 6
	GrammarCollocationMinLength   = 3
	GrammarCollocationPenalty     = -14
	GrammarNonCollocationPenalty  = -6
	GrammarWeakCollocationPenalty = -100
	GrammarRearPenalty            = -20
	GrammarContextualSuggestions  = false
	GrammarMaxHomophones          = 8
)

// DefaultPatch renders the managed default.custom.yaml (schema_list only).
// IMPORTANT: schema_list entries MUST use the `- schema: <id>` map form.
// rime_deployer --build only compiles schemas listed that way; the bare
// `- <id>` string form is ignored by the deployer, so --build writes only the
// build/default.yaml scaffolding and never compiles the schemas — and the
// leftover build/ prompts fcitx5-rime to skip its lazy deploy, so no schema
// is ever compiled/activated and the input method produces nothing. This was
// the root cause of "no Chinese can be typed" (the known-good pre-ompinyin
// backup uses `- schema:`).
// DefaultPageSize is the managed candidate count per page. The upstream
// rime-ice default is 5; ompinyin manages a 9-candidate page (the host's
// known-good feel, §5.4).
const DefaultPageSize = 9

// DefaultPatch renders the managed default.custom.yaml: the schema_list
// (map-form, §5.1) plus the two managed "feel" settings — the candidate count
// and `,.` paging (§5.4). The schema_list MUST use the `- schema: <id>` map
// form.
func DefaultPatch(schemaList []string) string {
	var b strings.Builder
	b.WriteString(ManagedHeader())
	b.WriteString("\npatch:\n")
	b.WriteString("  schema_list:\n")
	for _, s := range schemaList {
		b.WriteString("    - schema: " + s + "\n")
	}
	// §5.4 managed feel: 9 candidates + `,`/`.` paging.
	fmt.Fprintf(&b, "  menu/page_size: %d\n", DefaultPageSize)
	b.WriteString("  key_binder/bindings/+:\n")
	b.WriteString("    - accept: comma\n")
	b.WriteString("      send: Page_Up\n")
	b.WriteString("      when: has_menu\n")
	b.WriteString("    - accept: period\n")
	b.WriteString("      send: Page_Down\n")
	b.WriteString("      when: has_menu\n")
	return b.String()
}

// AlgebraPatch renders the managed radical_pinyin/melt_eng custom.yaml that
// redirects the reverse-lookup / English algebra to the Primary layout (§2.2).
// schemaName is the schema being patched (radical_pinyin / melt_eng); the
// include must reference the algebra DEFINED in that schema file
// (<schema>.schema.yaml:/<algebra>). A bare top-level `__include: <algebra>`
// resolves to a non-existent `<algebra>.yaml` and breaks the sub-schema build,
// which double_pinyin depends on (table_translator@melt_eng / @radical_lookup)
// → no composition. (Verified against the known-good pre-ompinyin setup.)
func AlgebraPatch(schemaName, algebra string) string {
	var b strings.Builder
	b.WriteString(ManagedHeader())
	b.WriteString("\npatch:\n")
	b.WriteString("  speller/algebra:\n")
	b.WriteString("    __include: " + schemaName + ".schema.yaml:/" + algebra + "\n")
	return b.String()
}

// ---------------------------------------------------------------------------
// Assets (§5.3)
// ---------------------------------------------------------------------------

// Asset is one downloadable data asset. URL is the official upstream; CN and
// Proxy are China-friendly candidate mirrors, ordered by preference.
type Asset struct {
	Name  string   // local file name in the cache / destination
	URL   string   // official upstream
	CN    []string // China mirrors (大学镜像 / 代码托管镜像)
	Proxy []string // GitHub 加速代理 (ghproxy family)
	// MinBytes and Magic are the first-run integrity anchors: an error page or
	// a captive-portal document served with HTTP 200 is rejected before it can
	// be cached. They are not a substitute for the ledger sha256 cross-check,
	// they just close the "nothing trusted yet" gap (评审 P0-1 remainder).
	MinBytes int64  // 0 = unchecked
	Magic    []byte // required leading bytes; nil = unchecked
	// Tag is the release tag this asset URL pins ("" = rolling/unpinned).
	// When set, the downloader can verify a re-download against the previously
	// recorded sha256 for the same tag (see assets.Fetch).
	Tag string
	// ImmutableTag marks tags that upstream never re-pushes (date-tagged
	// releases). For such tags a sha256 mismatch against the state ledger on
	// re-download is a hard error (tampered/wrong mirror). Mutable moving tags
	// (e.g. wanxiang "LTS", nightly) only warn.
	ImmutableTag bool
}

// MirrorSource names a download-policy preset selected via --mirror.
type MirrorSource string

const (
	MirrorAuto     MirrorSource = "auto"     // 官方优先，失败自动回退镜像/代理
	MirrorChina    MirrorSource = "cn"       // 国内镜像优先，官方兜底
	MirrorGhproxy  MirrorSource = "ghproxy"  // GitHub 加速代理优先，官方兜底
	MirrorUpstream MirrorSource = "upstream" // 仅官方上游，不回退
)

var mirrorSources = []MirrorSource{MirrorAuto, MirrorChina, MirrorGhproxy, MirrorUpstream}

// Valid reports whether s is a known preset.
func (s MirrorSource) Valid() bool {
	for _, m := range mirrorSources {
		if s == m {
			return true
		}
	}
	return false
}

// DefaultMirrorSource is the default download policy: 国内镜像优先.
func DefaultMirrorSource() MirrorSource { return MirrorChina }

// ParseMirrorSource maps a --mirror value to a named preset.
func ParseMirrorSource(v string) (MirrorSource, bool) {
	s := MirrorSource(v)
	return s, s.Valid()
}

// Candidates returns the ordered candidate URLs for an asset under a source
// policy. Names are appended by the downloader (which knows the cache name).
func Candidates(a Asset, src MirrorSource) []string {
	us := []string{a.URL}
	switch src {
	case MirrorChina:
		return dedupe(append(append(append([]string{}, a.CN...), a.Proxy...), us...))
	case MirrorGhproxy:
		return dedupe(append(append(append([]string{}, a.Proxy...), a.CN...), us...))
	case MirrorUpstream:
		return dedupe(us)
	case MirrorAuto, "":
		return dedupe(append(append(append([]string{}, us...), a.CN...), a.Proxy...))
	default: // unknown preset: treat as auto
		return dedupe(append(append(append([]string{}, us...), a.CN...), a.Proxy...))
	}
}

func dedupe(xs []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, x := range xs {
		if x == "" || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}

// ghproxy wraps an upstream GitHub URL with common China accelerators.
// gh-proxy.com is the most stable of the ones verified reachable; ghfast.top
// is flaky (intermittent timeouts) so it stays as a lower-priority fallback.
// Ghproxy wraps an arbitrary upstream URL with the common China accelerators
// (same order as ghproxy). Exported for non-asset uses like the releases API.
func Ghproxy(raw string) []string { return ghproxy(raw) }

func ghproxy(raw string) []string {
	return []string{
		"https://gh-proxy.com/" + raw,
		"https://ghfast.top/" + raw,
	}
}

// ZipMagic are the leading bytes of a real (uncompressed-header) zip archive.
var ZipMagic = []byte{'P', 'K', 0x03, 0x04}

// Integrity anchors. Sizes are deliberately far below the real ones (NJU
// serves full.zip at 16,050,491 B; the wanxiang LTS gram is 420,248,620 B) so
// a legitimate upstream shrink never bricks install, while an HTML/JSON error
// document — always kilobytes — is rejected outright.
const (
	rimeIceMinBytes  = 1 << 20  // 1 MiB
	wanxiangMinBytes = 50 << 20 // 50 MiB
)

// RimeIceReleasesAPI lists rime-ice releases (newest first) and is used to
// resolve the concrete latest STABLE tag: the repo's rolling nightly release
// is tagged "nightly" and must be skipped (verified against the live API).
const RimeIceReleasesAPI = "https://api.github.com/repos/iDvel/rime-ice/releases?per_page=100"

// RimeIce returns the rime-ice full.zip asset for the channel (unpinned
// fallback form; prefer resolving a concrete tag via RimeIceTagged on stable).
//
// WARNING (why RimeIceTagged exists): GitHub `releases/latest` on this repo
// redirects to the rolling `nightly` tag, while the NJU `LatestRelease` mirror
// serves the last STABLE snapshot — the two are NOT the same bytes. Pinning to
// a resolved tag (RimeIceTagged) removes that ambiguity; this unpinned form is
// only the graceful fallback when tag resolution is impossible.
//   - `stable`: `releases/latest` (rolling nightly, see above) + NJU
//     `LatestRelease` (last stable). Both compile under the `- schema: <id>`
//     map form (verified: rime_deployer --build emits rime_ice.table.bin).
//   - `nightly`: the rolling build `.../download/nightly/full.zip`. The NJU
//     `LatestRelease` mirror is EXCLUDED here — it serves stable bytes, which
//     would silently defeat the channel.
//
// NOTE: an earlier "stable does not compile" claim was in fact the bare
// `- <id>` schema_list short-form bug — with the correct map form the stable
// compiles fine.
func RimeIce(channel string) Asset {
	if channel == "nightly" {
		upstream := "https://github.com/iDvel/rime-ice/releases/download/nightly/full.zip"
		return Asset{
			Name:     "rime-ice-full-nightly.zip",
			URL:      upstream,
			Proxy:    ghproxy(upstream),
			MinBytes: rimeIceMinBytes,
			Magic:    ZipMagic,
		}
	}
	upstream := "https://github.com/iDvel/rime-ice/releases/latest/download/full.zip"
	return Asset{
		Name:     "rime-ice-full-stable.zip",
		URL:      upstream,
		CN:       []string{"https://mirror.nju.edu.cn/github-release/iDvel/rime-ice/LatestRelease/full.zip"},
		Proxy:    ghproxy(upstream),
		MinBytes: rimeIceMinBytes,
		Magic:    ZipMagic,
	}
}

// RimeIceTagged pins the stable channel to a concrete release tag (e.g.
// "2026.06.30"), resolved via the releases API (assets.ResolveStableTag).
// All three source families then serve byte-identical files for the same tag:
//   - upstream: github.com/.../releases/download/<tag>/full.zip
//   - CN: mirror.nju.edu.cn/github-release/<owner>/<repo>/<tag>/full.zip
//     (verified: NJU supports per-tag paths, not just LatestRelease)
//   - Proxy: ghproxy family wrapping the upstream URL
//
// Date tags are practically immutable upstream, so the downloader hard-rejects
// a re-download whose sha256 differs from the state ledger for the same tag.
func RimeIceTagged(tag string) Asset {
	upstream := "https://github.com/iDvel/rime-ice/releases/download/" + tag + "/full.zip"
	return Asset{
		Name:     "rime-ice-full-stable.zip",
		URL:      upstream,
		CN:       []string{"https://mirror.nju.edu.cn/github-release/iDvel/rime-ice/" + tag + "/full.zip"},
		Proxy:    ghproxy(upstream),
		MinBytes: rimeIceMinBytes,
		Magic:    ZipMagic,
		Tag:      tag,
		// date-tagged releases are never re-pushed upstream
		ImmutableTag: true,
	}
}

// Wanxiang returns the wanxiang LMDG model asset (~420MB).
// Version relationship (confirmed vs upstream): GitHub `RIME-LMDG` `LTS` and
// the CNB `rime-wanxiang` `model` release carry a BYTE-IDENTICAL
// `wanxiang-lts-zh-hans.gram` (both 420248620 B), so the CN mirror matches the
// upstream model exactly (only the release tag name differs: "LTS" vs "model").
//
// The "LTS" tag is a MOVING tag (upstream re-pushes updated models under the
// same tag), so ImmutableTag stays false: a sha256 change against the ledger
// is surfaced as a warning, never a hard failure that would brick `update`.
func Wanxiang() Asset {
	upstream := "https://github.com/amzxyz/RIME-LMDG/releases/download/LTS/" + GrammarLanguage + ".gram"
	return Asset{
		Name:     GrammarLanguage + ".gram",
		URL:      upstream,
		CN:       []string{"https://cnb.cool/amzxyz/rime-wanxiang/-/releases/download/model/" + GrammarLanguage + ".gram"},
		Proxy:    ghproxy(upstream),
		Tag:      "LTS",
		MinBytes: wanxiangMinBytes,
		// no Magic: the OSS model container has no documented signature, the
		// 50 MiB floor is what separates it from an error page
	}
}
