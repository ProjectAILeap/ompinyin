package catalog

import (
	"reflect"
	"strings"
	"testing"
)

func TestLookupLayoutCatalog(t *testing.T) {
	want := map[string]Layout{
		"quanpin": {Schema: "rime_ice", Algebra: "algebra_rime_ice"},
		"zrm":     {Schema: "double_pinyin", Algebra: "algebra_double_pinyin"},
		"flypy":   {Schema: "double_pinyin_flypy"},
		"mspy":    {Schema: "double_pinyin_mspy"},
		"sogou":   {Schema: "double_pinyin_sogou"},
		"abc":     {Schema: "double_pinyin_abc"},
		"ziguang": {Schema: "double_pinyin_ziguang"},
		"jiajia":  {Schema: "double_pinyin_jiajia"},
	}
	for id, w := range want {
		l, ok := Lookup(id)
		if !ok {
			t.Fatalf("layout %s missing", id)
		}
		if l.Schema != w.Schema || (w.Algebra != "" && l.Algebra != w.Algebra) {
			t.Errorf("layout %s = %+v, want schema=%s algebra=%s", id, l, w.Schema, w.Algebra)
		}
	}
	if _, ok := Lookup("nope"); ok {
		t.Error("unknown id should not resolve")
	}
}

func TestSchemaListRules(t *testing.T) {
	cases := []struct {
		name string
		d    Desired
		want []string
	}{
		{"default", DefaultDesired(), []string{"rime_ice"}},
		{"dsp zrm", Desired{Primary: "quanpin", Extra: []string{"zrm"}, Model: true, Channel: "stable"}, []string{"rime_ice", "double_pinyin"}},
		{"dsp flypy default", Desired{Primary: "flypy", Extra: []string{"quanpin"}, Model: true, Channel: "stable"}, []string{"double_pinyin_flypy", "rime_ice"}},
		{"no quanpin", Desired{Primary: "zrm", Model: true, Channel: "stable"}, []string{"double_pinyin"}},
	}
	for _, tc := range cases {
		got := tc.d.SchemaList()
		if len(got) != len(tc.want) {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
			}
		}
	}
}

func TestValidateRules(t *testing.T) {
	if err := (Desired{Primary: "flypy", Extra: []string{"quanpin"}, Channel: "stable"}).Validate(); err != nil {
		t.Errorf("valid state rejected: %v", err)
	}
	if err := (Desired{Primary: "quanpin", Channel: "stable"}).Validate(); err != nil {
		t.Errorf("default rejected: %v", err)
	}
	if err := (Desired{Primary: "quanpin", Channel: "bogus"}).Validate(); err == nil {
		t.Error("bad channel accepted")
	}
	// extra must be double pinyin
	if err := (Desired{Primary: "quanpin", Extra: []string{"quanpin"}, Channel: "stable"}).Validate(); err == nil {
		t.Error("quanpin as extra accepted")
	}
}

// TestGrammarPatchGolden locks the official recipe constants (§5.2, ADR 9).
func TestGrammarPatchGolden(t *testing.T) {
	got := GrammarPatch()
	want := strings.Join([]string{
		"# managed by ompinyin v" + Version + " — hand edits will be overwritten",
		"patch:",
		"  grammar:",
		"    language: wanxiang-lts-zh-hans",
		"    collocation_max_length: 6",
		"    collocation_min_length: 3",
		"    collocation_penalty: -14",
		"    non_collocation_penalty: -6",
		"    weak_collocation_penalty: -100",
		"    rear_penalty: -20",
		"  translator/contextual_suggestions: false",
		"  translator/max_homophones: 8",
		"",
	}, "\n")
	if got != want {
		t.Errorf("grammar patch mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestDefaultPatchGolden(t *testing.T) {
	got := DefaultPatch([]string{"rime_ice", "double_pinyin"})
	want := ManagedHeader() + "\npatch:\n  schema_list:\n    - schema: rime_ice\n    - schema: double_pinyin\n  menu/page_size: 9\n  key_binder/bindings/+:\n    - accept: comma\n      send: Page_Up\n      when: has_menu\n    - accept: period\n      send: Page_Down\n      when: has_menu\n"
	if got != want {
		t.Errorf("default patch mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestAssets(t *testing.T) {
	// stable 默认：unpinned 回退形态是 releases/latest（该 repo 实际指向滚动
	// nightly）；生产路径优先 RimeIceTagged(具体 tag)，见 TestRimeIceTagged。
	s := RimeIce("stable")
	if !strings.HasPrefix(s.URL, "https://github.com/iDvel/rime-ice/releases/latest/download/full.zip") {
		t.Errorf("stable url = %s", s.URL)
	}
	n := RimeIce("nightly")
	if !strings.Contains(n.URL, "nightly") {
		t.Errorf("nightly url = %s", n.URL)
	}
	// nightly 不得混用 NJU LatestRelease（那是 stable 快照，会静默击穿通道）
	for _, cn := range n.CN {
		if strings.Contains(cn, "LatestRelease") {
			t.Errorf("nightly must not use the NJU LatestRelease mirror: %s", cn)
		}
	}
	// stable/nightly 缓存名必须不同（P2-4）
	if s.Name == n.Name {
		t.Errorf("stable/nightly cache names collide: %s", s.Name)
	}
	// NJU LatestRelease 镜像是 stable 快照，只允许出现在 stable 候选里
	if len(s.CN) == 0 || !strings.Contains(s.CN[0], "LatestRelease") {
		t.Errorf("stable CN mirror = %v", s.CN)
	}
	w := Wanxiang()
	if !strings.HasSuffix(w.URL, "wanxiang-lts-zh-hans.gram") {
		t.Errorf("wanxiang url = %s", w.URL)
	}
	if len(w.CN) == 0 || !strings.Contains(strings.Join(w.CN, ""), "wanxiang-lts-zh-hans.gram") {
		t.Errorf("wanxiang CN mirror = %v", w.CN)
	}
}

// TestRimeIceTagged locks the tagged stable form: per-tag upstream URL, per-tag
// NJU mirror path (verified reachable), immutable tag semantics.
func TestRimeIceTagged(t *testing.T) {
	a := RimeIceTagged("2026.06.30")
	if a.Tag != "2026.06.30" || !a.ImmutableTag {
		t.Errorf("tagged asset meta wrong: %+v", a)
	}
	if a.URL != "https://github.com/iDvel/rime-ice/releases/download/2026.06.30/full.zip" {
		t.Errorf("tagged upstream url = %s", a.URL)
	}
	if len(a.CN) != 1 || a.CN[0] != "https://mirror.nju.edu.cn/github-release/iDvel/rime-ice/2026.06.30/full.zip" {
		t.Errorf("tagged CN mirror = %v", a.CN)
	}
	for _, u := range Candidates(a, MirrorChina) {
		if strings.Contains(u, "LatestRelease") {
			t.Errorf("tagged candidates must not include LatestRelease: %s", u)
		}
	}
	// wanxiang: moving tag, byte-identical mirrors, pinned tag recorded
	w := Wanxiang()
	if w.Tag != "LTS" || w.ImmutableTag {
		t.Errorf("wanxiang tag meta wrong: %+v", w)
	}
	// 加速代理：较稳定的 gh-proxy.com 优先，ghfast.top 作为兜底（实测 ghfast 间歇性超时）。
	if len(w.Proxy) < 2 || !strings.HasPrefix(w.Proxy[0], "https://gh-proxy.com/") {
		t.Errorf("gh-proxy.com must be first, got %v", w.Proxy)
	}
	if !strings.HasPrefix(w.Proxy[1], "https://ghfast.top/") {
		t.Errorf("ghfast.top should be second, got %v", w.Proxy)
	}
}

func TestMirrorSource(t *testing.T) {
	for _, s := range []MirrorSource{MirrorAuto, MirrorChina, MirrorGhproxy, MirrorUpstream} {
		if !s.Valid() {
			t.Errorf("%s should be valid", s)
		}
	}
	if (MirrorSource("bogus")).Valid() {
		t.Error("bogus mirror source accepted")
	}
	if _, ok := ParseMirrorSource("cn"); !ok || DefaultMirrorSource() != MirrorChina {
		t.Error("DefaultMirrorSource should be cn and parse")
	}
	if _, ok := ParseMirrorSource("https://x/y"); ok {
		t.Error("a URL must not parse as a preset")
	}
}

func TestCandidatesOrdering(t *testing.T) {
	a := Asset{Name: "x", URL: "https://up/x", CN: []string{"https://cn/x"}, Proxy: []string{"https://gh/x"}}
	// auto: upstream → cn → proxy
	if got := Candidates(a, MirrorAuto); !reflect.DeepEqual(got, []string{"https://up/x", "https://cn/x", "https://gh/x"}) {
		t.Errorf("auto = %v", got)
	}
	// cn: cn → proxy → upstream
	if got := Candidates(a, MirrorChina); !reflect.DeepEqual(got, []string{"https://cn/x", "https://gh/x", "https://up/x"}) {
		t.Errorf("cn = %v", got)
	}
	// ghproxy: proxy → cn → upstream
	if got := Candidates(a, MirrorGhproxy); !reflect.DeepEqual(got, []string{"https://gh/x", "https://cn/x", "https://up/x"}) {
		t.Errorf("ghproxy = %v", got)
	}
	// upstream: only upstream
	if got := Candidates(a, MirrorUpstream); !reflect.DeepEqual(got, []string{"https://up/x"}) {
		t.Errorf("upstream = %v", got)
	}
}
