package assets

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ProjectAILeap/ompinyin/internal/catalog"
)

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func mustURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}

func setCacheDir(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", dir)
}

// TestFetchAndMirrorFallback: primary fails → mirror succeeds; sha256 returned;
// second fetch is a cache hit with no network.
func TestFetchAndMirrorFallback(t *testing.T) {
	root := t.TempDir()
	setCacheDir(t, filepath.Join(root, "cache"))
	content := []byte("fake full zip payload")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "fail") {
			w.WriteHeader(500)
			return
		}
		w.Write(content)
	}))
	defer srv.Close()

	m := &Manager{HTTP: srv.Client(), Logf: func(string, ...any) {}}
	a := catalog.Asset{
		Name: "test.bin",
		URL:  srv.URL + "/fail/test.bin",
		CN:   []string{srv.URL + "/ok/test.bin"},
	}
	path, sha, _, err := m.Fetch(context.Background(), a, "", "")
	if err != nil {
		t.Fatalf("mirror fallback failed: %v", err)
	}
	if sha != hashBytes(content) {
		t.Errorf("sha mismatch: %s", sha)
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, content) {
		t.Error("cache content mismatch")
	}

	// cache hit: second fetch must not touch the network
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no network expected on cache hit")
	}))
	dead.Close() // closed immediately: any request errors
	m.HTTP = dead.Client()
	path2, sha2, _, err := m.Fetch(context.Background(), a, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if path2 != path || sha2 != sha {
		t.Error("cache hit inconsistent")
	}
}

// TestShortBodyIntegrity: a truncated download must fail and NOT leave a
// half file in the cache (校验通过再 rename, §8).
func TestShortBodyIntegrity(t *testing.T) {
	root := t.TempDir()
	setCacheDir(t, filepath.Join(root, "cache"))
	content := []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "32")
		w.Write(content[:10])
	}))
	defer srv.Close()

	m := &Manager{HTTP: srv.Client(), Logf: func(string, ...any) {}}
	_, _, _, err := m.Fetch(context.Background(), catalog.Asset{Name: "short.bin", URL: srv.URL}, "", "")
	if err == nil {
		t.Fatal("truncated download must error")
	}
	if _, statErr := os.Stat(filepath.Join(CacheDir(), "short.bin")); statErr == nil {
		t.Error("half file must not land in the cache")
	}
}

// TestZipSlipRejected (§16 invariant 12).
func TestZipSlipRejected(t *testing.T) {
	root := t.TempDir()
	setCacheDir(t, filepath.Join(root, "cache"))
	dest := filepath.Join(root, "rime")
	os.MkdirAll(dest, 0o755)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("../../evil.yaml")
	f.Write([]byte("evil: true"))
	zw.Close()

	m := &Manager{Logf: func(string, ...any) {}}
	zipPath := filepath.Join(root, "evil.zip")
	os.WriteFile(zipPath, buf.Bytes(), 0o644)

	_, _, err := m.ExtractZip(zipPath, dest, false)
	if err == nil || !strings.Contains(err.Error(), "zip-slip") {
		t.Fatalf("zip slip not rejected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "evil.yaml")); err == nil {
		t.Fatal("file escaped the destination")
	}
}

// TestExtractRules: custom.yaml skipped + protected files never overwritten.
func TestExtractRules(t *testing.T) {
	root := t.TempDir()
	setCacheDir(t, filepath.Join(root, "cache"))
	dest := filepath.Join(root, "rime")
	os.MkdirAll(dest, 0o755)
	// pre-create the protected set: 永不覆盖 only applies when they exist
	for name, body := range map[string]string{
		"user.yaml":         "my old user data",
		"installation.yaml": "old installation",
		"custom_phrase.txt": "old phrase",
		"mydict.userdb":     "old userdb",
	} {
		os.WriteFile(filepath.Join(dest, name), []byte(body), 0o644)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, body string) {
		f, _ := zw.Create(name)
		f.Write([]byte(body))
	}
	add("default.yaml", "schema data")
	add("evil.custom.yaml", "upstream custom must be skipped")
	add("user.yaml", "upstream user.yaml must not overwrite")
	add("installation.yaml", "must not overwrite")
	add("custom_phrase.txt", "must not overwrite")
	add("mydict.userdb", "must not overwrite")
	zw.Close()

	m := &Manager{Logf: func(string, ...any) {}}
	zipPath := filepath.Join(root, "full.zip")
	os.WriteFile(zipPath, buf.Bytes(), 0o644)

	extracted, skipped, err := m.ExtractZip(zipPath, dest, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(extracted) != 1 || extracted[0] != "default.yaml" {
		t.Errorf("extracted = %v", extracted)
	}
	if len(skipped) != 5 {
		t.Errorf("skipped = %v", skipped)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "user.yaml")); string(b) != "my old user data" {
		t.Errorf("user.yaml overwritten: %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "installation.yaml")); string(b) != "old installation" {
		t.Errorf("installation.yaml overwritten: %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "mydict.userdb")); string(b) != "old userdb" {
		t.Errorf("userdb overwritten: %s", b)
	}
	if _, err := os.Stat(filepath.Join(dest, "evil.custom.yaml")); err == nil {
		t.Error("custom.yaml from zip must be skipped")
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "default.yaml")); string(b) != "schema data" {
		t.Error("data file not extracted")
	}
}

func TestTagFromURL(t *testing.T) {
	cases := map[string]string{
		"/iDvel/rime-ice/releases/download/v2026.08.15/full.zip": "v2026.08.15",
		"/iDvel/rime-ice/releases/tag/v2026.08.15":               "v2026.08.15",
		"/releases/download/LTS/wanxiang-lts-zh-hans.gram":       "LTS",
		"/some/other/path": "",
	}
	for path, want := range cases {
		if got := TagFromURL(mustURL("https://github.com" + path)); got != want {
			t.Errorf("TagFromURL(%s) = %q, want %q", path, got, want)
		}
	}
}

// TestExtractOverwriteSemantics (P1-1b): plain install skips existing files;
// update (overwrite=true) refreshes them; protected names are never touched.
func TestExtractOverwriteSemantics(t *testing.T) {
	root := t.TempDir()
	setCacheDir(t, filepath.Join(root, "cache"))
	dest := filepath.Join(root, "rime")
	os.MkdirAll(dest, 0o755)
	os.WriteFile(filepath.Join(dest, "default.yaml"), []byte("old"), 0o644)
	os.WriteFile(filepath.Join(dest, "user.yaml"), []byte("mine"), 0o644)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"default.yaml", "newdict.yaml", "user.yaml"} {
		f, _ := zw.Create(name)
		f.Write([]byte("new"))
	}
	zw.Close()
	zipPath := filepath.Join(root, "full.zip")
	os.WriteFile(zipPath, buf.Bytes(), 0o644)
	m := &Manager{Logf: func(string, ...any) {}}

	// plain install: existing files skipped, missing extracted, protected kept
	extracted, skipped, err := m.ExtractZip(zipPath, dest, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 2 {
		t.Errorf("install skipped = %v, want default.yaml + user.yaml", skipped)
	}
	if len(extracted) != 1 || extracted[0] != "newdict.yaml" {
		t.Errorf("install extracted = %v", extracted)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "default.yaml")); string(b) != "old" {
		t.Errorf("existing file overwritten without overwrite=true: %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "user.yaml")); string(b) != "mine" {
		t.Error("protected user.yaml overwritten")
	}

	// update: non-protected refreshed, protected still never overwritten
	extracted, _, err = m.ExtractZip(zipPath, dest, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(extracted) != 2 {
		t.Errorf("update extracted = %v", extracted)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "default.yaml")); string(b) != "new" {
		t.Errorf("update did not refresh default.yaml: %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "user.yaml")); string(b) != "mine" {
		t.Error("protected user.yaml overwritten on update")
	}
}

// TestCacheKeyPerTag (P2-4): an immutable-tag asset gets a per-tag cache key;
// moving tags and unpinned assets share the plain name.
func TestCacheKeyPerTag(t *testing.T) {
	if got := cacheKey(catalog.Asset{Name: "x.zip", Tag: "2026.06.30", ImmutableTag: true}); got != "x.zip@2026.06.30" {
		t.Errorf("immutable cache key = %s", got)
	}
	if got := cacheKey(catalog.Asset{Name: "x.zip", Tag: "LTS"}); got != "x.zip" {
		t.Errorf("moving-tag cache key = %s", got)
	}
	if got := cacheKey(catalog.Asset{Name: "x.zip"}); got != "x.zip" {
		t.Errorf("unpinned cache key = %s", got)
	}
}

// TestFetchLedgerMismatch: for an immutable tag whose sha256 matches the
// ledger, a redownload serving DIFFERENT bytes must be rejected (hard) and
// the next mirror tried; for a moving tag it must only warn and accept.
func TestFetchLedgerMismatch(t *testing.T) {
	good, bad := []byte("good-bytes"), []byte("bad--bytes")
	var mode atomic.Value
	mode.Store("bad")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "primary") {
			if mode.Load().(string) == "bad" {
				w.Write(bad)
				return
			}
			w.Write(good)
			return
		}
		w.Write(good)
	}))
	defer srv.Close()

	asset := func(immutable bool) catalog.Asset {
		return catalog.Asset{Name: "a.bin", URL: srv.URL + "/primary/a.bin",
			CN: []string{srv.URL + "/mirror/a.bin"}, Tag: "v1", ImmutableTag: immutable}
	}
	shaGood := hashBytes(good)

	// immutable: primary serves tampered bytes → rejected, mirror accepted
	m := &Manager{HTTP: srv.Client(), Logf: func(string, ...any) {}}
	_, sha, tag, err := m.Fetch(context.Background(), asset(true), "v1", shaGood)
	if err != nil {
		t.Fatalf("immutable mismatch not recovered via mirror: %v", err)
	}
	if sha != shaGood || tag != "v1" {
		t.Errorf("sha/tag = %s/%s", sha, tag)
	}
	if b, _ := os.ReadFile(filepath.Join(CacheDir(), "a.bin@v1")); !bytes.Equal(b, good) {
		t.Error("tampered bytes landed in cache")
	}

	// moving tag: changed bytes are accepted with a warning (model updates)
	mode.Store("good")
	os.Remove(filepath.Join(CacheDir(), "b.bin"))
	m2 := &Manager{HTTP: srv.Client(), Logf: func(string, ...any) {}}
	mov := catalog.Asset{Name: "b.bin", URL: srv.URL + "/primary/b.bin", Tag: "LTS"}
	if _, _, _, err := m2.Fetch(context.Background(), mov, "LTS", hashBytes(bad)); err != nil {
		t.Fatalf("moving-tag update rejected: %v", err)
	}
}

// TestResolveStableTag (P2-7): picks the newest non-nightly release; nightly
// first-in-list, drafts and prereleases are skipped.
func TestResolveStableTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[
		 {"tag_name":"nightly","draft":false,"prerelease":false},
		 {"tag_name":"2026.06.30","draft":false,"prerelease":false},
		 {"tag_name":"2026.06.03","draft":false,"prerelease":false}
		]`))
	}))
	defer srv.Close()
	oldURL := releasesAPIURL
	releasesAPIURL = srv.URL
	oldCands := stableTagCandidates
	stableTagCandidates = func(api string) []string { return []string{api} } // hermetic: no proxy fallback
	t.Cleanup(func() { releasesAPIURL = oldURL; stableTagCandidates = oldCands })

	ctx := context.Background()
	if got := ResolveStableTag(ctx); got != "2026.06.30" {
		t.Errorf("ResolveStableTag = %q, want 2026.06.30", got)
	}
	// API unreachable → "" (callers fall back to unpinned releases/latest)
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer dead.Close()
	releasesAPIURL = dead.URL
	if got := ResolveStableTag(ctx); got != "" {
		t.Errorf("ResolveStableTag on broken API = %q, want \"\"", got)
	}
}

// ---------------------------------------------------------------------------
// 评审 P0-1 / P0-8: integrity anchors, resume safety, no panic on bad cache
// ---------------------------------------------------------------------------

// TestVerifyShapeRejectsErrorPage: an HTML error document served with 200 must
// never reach the cache — there is no recorded sha256 to compare against on a
// first run, so size/magic is the only trust anchor.
func TestVerifyShapeRejectsErrorPage(t *testing.T) {
	root := t.TempDir()
	setCacheDir(t, filepath.Join(root, "cache"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>portal</body></html>")
	}))
	defer srv.Close()

	a := catalog.Asset{Name: "full.zip", URL: srv.URL, MinBytes: 1 << 20, Magic: []byte{'P', 'K', 0x03, 0x04}}
	m := &Manager{HTTP: srv.Client(), Logf: func(string, ...any) {}}
	if _, _, _, err := m.Fetch(context.Background(), a, "", ""); err == nil {
		t.Fatal("error page accepted as an asset")
	}
	if _, err := os.Stat(filepath.Join(CacheDir(), a.Name)); err == nil {
		t.Error("error page landed in the cache")
	}
	if _, err := os.Stat(filepath.Join(CacheDir(), a.Name+".part")); err == nil {
		t.Error("rejected download left a .part behind")
	}
}

// TestResumeSameSourceOnly: a partial may only be continued from the origin
// that wrote it. Cross-source resume spliced two byte streams together (the
// 420MB corruption bug); same-source resume must still work.
func TestResumeSameSourceOnly(t *testing.T) {
	full := bytes.Repeat([]byte("0123456789abcdef"), 64) // 1 KiB
	var mu sync.Mutex
	var sawRange string
	recordRange := func(r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		sawRange = r.Header.Get("Range")
	}
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordRange(r)
		http.ServeContent(w, r, "model.bin", time.Now(), bytes.NewReader(full))
	}))
	defer good.Close()
	// dies after 20 bytes on the first attempt, then behaves
	var attempts int32
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordRange(r)
		if atomic.AddInt32(&attempts, 1) == 1 {
			w.Header().Set("Content-Length", fmt.Sprint(len(full)))
			fmt.Fprint(w, string(full[:20]))
			return
		}
		http.ServeContent(w, r, "model.bin", time.Now(), bytes.NewReader(full))
	}))
	defer flaky.Close()

	root := t.TempDir()
	setCacheDir(t, filepath.Join(root, "cache"))
	m := &Manager{HTTP: flaky.Client(), Logf: func(string, ...any) {}}
	a := catalog.Asset{Name: "model.bin", URL: flaky.URL}

	// attempt 1: truncated → failure, but the partial is kept for a same-URL retry
	if _, _, _, err := m.Fetch(context.Background(), a, "", ""); err == nil {
		t.Fatal("truncated download must fail")
	}
	part := filepath.Join(CacheDir(), "model.bin.part")
	if st, err := os.Stat(part); err != nil || st.Size() != 20 {
		t.Fatalf("partial should be kept at 20 bytes, got %v %v", st, err)
	}
	// attempt 2, same URL: resumes from byte 20 and completes
	path, sha, _, err := m.Fetch(context.Background(), a, "", "")
	if err != nil {
		t.Fatalf("resume fetch failed: %v", err)
	}
	if !strings.HasPrefix(sawRange, "bytes=20-") {
		t.Errorf("resume did not send a Range header: %q", sawRange)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, full) {
		t.Errorf("resumed content mismatch (%d bytes)", len(got))
	}
	if sha != hashBytes(full) {
		t.Errorf("sha = %s, want %s", sha, hashBytes(full))
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Error(".part must be gone after a successful fetch")
	}

	// cross-source: a partial from origin A must never be continued at B
	atomic.StoreInt32(&attempts, 5) // flaky now behaves; make a fresh stale partial
	os.WriteFile(part, []byte("XXXXXXXXXXXXXXXXXXXX"), 0o644)
	os.WriteFile(part+".src", []byte(flaky.URL), 0o644)
	m2 := &Manager{HTTP: good.Client(), Logf: func(string, ...any) {}}
	p2, _, _, err := m2.Fetch(context.Background(), catalog.Asset{Name: "model.bin", URL: good.URL}, "", "")
	if err != nil {
		t.Fatalf("fetch from a different origin failed: %v", err)
	}
	b2, _ := os.ReadFile(p2)
	if !bytes.Equal(b2, full) {
		t.Errorf("CROSS-SOURCE SPLICE: got %d bytes, prefix %q", len(b2), b2[:min(24, len(b2))])
	}
}

// TestCacheHitUnreadableDoesNotPanic: HashFile returns "" for an unreadable
// file; the old sha[:12] slice panicked (评审 P0-8).
func TestCacheHitUnreadableDoesNotPanic(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod 000 is not enforced for root")
	}
	root := t.TempDir()
	setCacheDir(t, filepath.Join(root, "cache"))
	os.MkdirAll(CacheDir(), 0o755)
	name := "model.bin"
	p := filepath.Join(CacheDir(), name)
	if err := os.WriteFile(p, []byte("cached bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o000); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "fresh")
	}))
	defer srv.Close()
	_ = os.Chmod(p, 0o644) // allow the removal path to work
	// make it unreadable again right before Fetch, via a wrapper that re-chmods
	m := &Manager{HTTP: srv.Client(), Logf: func(string, ...any) {}}
	if err := os.Chmod(p, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(p, 0o644)
	// must not panic; the unreadable entry is dropped and re-downloaded
	path, sha, _, err := m.Fetch(context.Background(), catalog.Asset{Name: name, URL: srv.URL}, "", "")
	if err != nil {
		t.Fatalf("Fetch should recover by re-downloading: %v", err)
	}
	if sha != hashBytes([]byte("fresh")) {
		t.Errorf("sha = %s, want the freshly downloaded value", sha[:12])
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "fresh" {
		t.Errorf("cache not refreshed: %q %v", b, err)
	}
}

// TestLocalAssetDir: --mirror <dir> installs fully offline (the workaround in
// the test notes used to be a python http.server; 评审 P2).
func TestLocalAssetDir(t *testing.T) {
	root := t.TempDir()
	setCacheDir(t, filepath.Join(root, "cache"))
	assetsDir := filepath.Join(root, "assets")
	os.MkdirAll(assetsDir, 0o755)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("default.yaml")
	f.Write([]byte("data"))
	zw.Close()
	if err := os.WriteFile(filepath.Join(assetsDir, "full.zip"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	m := &Manager{LocalDir: assetsDir, Logf: func(string, ...any) {}}
	a := catalog.Asset{Name: "full.zip", MinBytes: 1, Magic: []byte{'P', 'K', 0x03, 0x04}}
	path, sha, _, err := m.Fetch(context.Background(), a, "", "")
	if err != nil {
		t.Fatalf("offline fetch failed: %v", err)
	}
	if sha != hashBytes(buf.Bytes()) {
		t.Errorf("sha mismatch: %s", sha)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("not cached: %v", err)
	}

	// missing file must name the expected path so a typo is obvious
	_, _, _, err = m.Fetch(context.Background(), catalog.Asset{Name: "nope.gram"}, "", "")
	if err == nil || !strings.Contains(err.Error(), "nope.gram") {
		t.Errorf("missing local asset error = %v", err)
	}

	// the shape gate still applies to local files (a truncated hand-copy is
	// not trusted just because it came from disk)
	os.WriteFile(filepath.Join(assetsDir, "tiny.zip"), []byte("PK"), 0o644)
	if _, _, _, err := m.Fetch(context.Background(),
		catalog.Asset{Name: "tiny.zip", MinBytes: 1 << 20, Magic: []byte{'P', 'K', 0x03, 0x04}}, "", ""); err == nil {
		t.Error("undersized local asset must be rejected")
	}
}
