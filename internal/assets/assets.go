// Package assets implements L2: download → sha256 verify → cache → extract.
// Features: mirror fallback chain, same-origin HTTP Range resume for the
// 420MB model, size/magic integrity gate, zip-slip rejection and
// protected-file extraction rules (§5.3).
package assets

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ProjectAILeap/ompinyin/internal/catalog"
	"github.com/ProjectAILeap/ompinyin/internal/state"
)

// CacheDir is ~/.cache/ompinyin (respects XDG_CACHE_HOME; OMPINYIN_TEST_HOME
// takes precedence for hermetic tests).
func CacheDir() string {
	if th := os.Getenv("OMPINYIN_TEST_HOME"); th != "" {
		return filepath.Join(th, ".cache", "ompinyin")
	}
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		base = filepath.Join(state.Home(), ".cache")
	}
	return filepath.Join(base, "ompinyin")
}

// Manager downloads and extracts assets.
type Manager struct {
	HTTP           *http.Client
	MirrorOverride string               // --mirror <URL>: single custom URL for the asset
	LocalDir       string               // --mirror <dir>: offline asset directory (no HTTP)
	MirrorSource   catalog.MirrorSource // --mirror <preset>: named download policy
	Logf           func(format string, args ...any)
}

func (m *Manager) logf(format string, args ...any) {
	if m.Logf != nil {
		m.Logf(format, args...)
	}
}

// download timeouts: a hostile/blocked mirror must fail fast so the fallback
// chain proceeds, while a legitimately slow 420MB model download is never
// killed by an overall body cap. These cover connect / TLS / response-header
// only — the body copy has no deadline.
const (
	dialTimeout   = 10 * time.Second
	headerTimeout = 15 * time.Second
	tlsTimeout    = 10 * time.Second
)

func (m *Manager) client() *http.Client {
	if m.HTTP != nil {
		return m.HTTP
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   dialTimeout,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   tlsTimeout,
			ResponseHeaderTimeout: headerTimeout,
			ForceAttemptHTTP2:     true,
		},
	}
}

// candidateURLs lists the fallback chain for an asset: a local asset dir wins
// outright (offline mode), then a custom --mirror URL, then the named preset
// ordering of upstream + mirrors.
func (m *Manager) candidateURLs(a catalog.Asset) []string {
	if m.LocalDir != "" {
		return []string{filepath.Join(m.LocalDir, a.Name)}
	}
	if m.MirrorOverride != "" {
		return []string{strings.TrimRight(m.MirrorOverride, "/") + "/" + a.Name}
	}
	return catalog.Candidates(a, m.MirrorSource)
}

// Fetch ensures the asset is present in the cache and verified.
// hintTag/hintSHA come from the state ledger. A cache hit avoids any network
// round-trip; when a hint SHA is known it is re-verified against the cached
// bytes so a corrupted/tampered cache cannot silently pass. Returns
// (cachePath, sha256hex, tag).
//
// Cache keys are per-tag (channelName suffixes the pinned tag), so the stable
// and nightly builds never collide in the cache (P2-4).
func (m *Manager) Fetch(ctx context.Context, a catalog.Asset, hintTag, hintSHA string) (string, string, string, error) {
	if err := os.MkdirAll(CacheDir(), 0o755); err != nil {
		return "", "", "", err
	}
	cachePath := filepath.Join(CacheDir(), cacheKey(a))
	partPath := cachePath + partSuffix
	srcPath := cachePath + partSrcSuffix

	if _, err := os.Stat(cachePath); err == nil {
		sha := state.HashFile(cachePath)
		switch {
		case sha == "":
			// unreadable cache entry: it cannot be verified, so it cannot be
			// trusted — drop it and re-download (评审 P0-8: this used to panic on
			// sha[:12] of the empty string)
			m.logf("[警告] L2 缓存 %s 不可读/为空，丢弃后重下", a.Name)
			if err := os.Remove(cachePath); err != nil {
				return "", "", "", err
			}
		case hintSHA != "" && hintSHA != sha:
			m.logf("[警告] L2 缓存中 %s 的 sha256 与上次记账不符（缓存损坏或 tag 被上游重推）；重新下载", a.Name)
		default:
			m.logf("[跳过] L2 %s 缓存命中 (sha256 %s…)", a.Name, trunc12(sha))
			return cachePath, sha, hintTag, nil
		}
	}

	var lastErr error
	for _, u := range m.candidateURLs(a) {
		// Resume is only safe from the SAME origin: splicing a partial written
		// by one mirror with bytes from another produced a silently corrupt
		// 420MB model (评审 P0-1). Any other case drops the partial.
		if !resumable(srcPath, u, partPath) {
			os.Remove(partPath)
			os.Remove(srcPath)
		}
		tag, err := m.fetchOne(ctx, u, partPath, srcPath, a)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return "", "", "", err
			}
			lastErr = err
			m.logf("[失败] L2 %s from %s: %v (trying next mirror)", a.Name, u, err)
			continue
		}
		if tag == "" {
			tag = a.Tag // URL may not reveal the tag (mirrors, CDNs)
		}
		// integrity gate: size/magic sniffing catches a captive-portal page or
		// an error document served with HTTP 200; the ledger cross-check catches
		// a re-pushed or wrong-mirror build of an immutable tag.
		if err := verifyShape(a, partPath); err != nil {
			os.Remove(partPath)
			os.Remove(srcPath)
			lastErr = err
			m.logf("[失败] L2 %s from %s: %v (trying next mirror)", a.Name, u, err)
			continue
		}
		sha := state.HashFile(partPath)
		if sha == "" {
			os.Remove(partPath)
			lastErr = fmt.Errorf("cannot read the downloaded file %s", partPath)
			continue
		}
		if hintSHA != "" && hintTag != "" && a.Tag != "" && hintTag == a.Tag && sha != hintSHA {
			if a.ImmutableTag {
				os.Remove(partPath)
				os.Remove(srcPath)
				lastErr = fmt.Errorf("sha256 mismatch for immutable tag %s (recorded %s…, got %s…)", a.Tag, trunc12(hintSHA), trunc12(sha))
				m.logf("[失败] L2 %s tag %s 的下载内容与记账 sha256 不符（镜像内容异常？）", a.Name, a.Tag)
				continue
			}
			m.logf("[警告] L2 %s 移动 tag %s 内容更新（sha256 %s… → %s…）", a.Name, a.Tag, trunc12(hintSHA), trunc12(sha))
		}
		if err := os.Rename(partPath, cachePath); err != nil {
			return "", "", "", err
		}
		os.Remove(srcPath)
		m.logf("[完成] L2 %s (sha256 %s…)", a.Name, trunc12(sha))
		return cachePath, sha, tag, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no candidate URL for %s", a.Name)
	}
	return "", "", "", fmt.Errorf("download %s failed: %w", a.Name, lastErr)
}

const (
	partSuffix    = ".part"
	partSrcSuffix = ".part.src"
	// partMaxAge bounds how long an abandoned partial is kept for resume.
	// `ompinyin clean` removes the whole cache dir (partials included).
	partMaxAge = 24 * time.Hour
)

// resumable reports whether the existing partial may be continued from url:
// it must exist, have been written by this very URL, and be recent.
func resumable(srcPath, url, partPath string) bool {
	b, err := os.ReadFile(srcPath)
	if err != nil || strings.TrimSpace(string(b)) != url {
		return false
	}
	info, err := os.Stat(partPath)
	if err != nil || info.Size() == 0 {
		return false
	}
	return time.Since(info.ModTime()) <= partMaxAge
}

func trunc12(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}

// verifyShape rejects content that cannot be the requested asset: an error
// page or portal HTML served with 200, or a truncated body. This is the
// first-run trust anchor — the ledger cross-check in Fetch can only compare
// against a sha256 we already recorded.
func verifyShape(a catalog.Asset, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if a.MinBytes > 0 && info.Size() < a.MinBytes {
		return fmt.Errorf("downloaded %s is %d bytes, expected ≥ %d (wrong mirror / error page?)", a.Name, info.Size(), a.MinBytes)
	}
	if len(a.Magic) == 0 {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	head := make([]byte, len(a.Magic))
	if _, err := io.ReadFull(f, head); err != nil {
		return fmt.Errorf("cannot read the header of %s: %w", a.Name, err)
	}
	if !bytes.Equal(head, a.Magic) {
		return fmt.Errorf("%s is not a %q file (magic % x); refusing to cache it", a.Name, a.Name, head)
	}
	return nil
}

// cacheKey returns the cache file name for an asset. The pinned tag is part
// of the key ONLY for immutable tags (stable@2026.06.30 vs nightly never
// collide). Moving tags (wanxiang "LTS") keep the plain name: the ledger
// cross-check in Fetch warns on content changes, and update deletes the cache
// by name.
func cacheKey(a catalog.Asset) string {
	if a.Tag != "" && a.ImmutableTag {
		return a.Name + "@" + a.Tag
	}
	return a.Name
}

// fetchOne materialises one candidate into partPath: a local asset directory
// is copied (offline install/VM testing, no HTTP server needed), anything else
// is streamed over HTTP.
func (m *Manager) fetchOne(ctx context.Context, candidate, partPath, srcPath string, a catalog.Asset) (string, error) {
	if m.LocalDir != "" {
		return m.copyLocal(candidate, partPath, a)
	}
	return m.download(ctx, candidate, partPath, srcPath)
}

// copyLocal installs from a pre-downloaded asset directory. The tag cannot be
// read from a filesystem path, so it stays "" unless the asset pins one.
func (m *Manager) copyLocal(candidate, partPath string, a catalog.Asset) (string, error) {
	src := filepath.Join(m.LocalDir, a.Name)
	info, err := os.Stat(src)
	if err != nil {
		return "", fmt.Errorf("local asset %s missing (expected %s): %w", a.Name, src, err)
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.OpenFile(partPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	m.logf("[完成] L2 %s 取自本地目录 %s (%d bytes)", a.Name, m.LocalDir, info.Size())
	return a.Tag, nil
}

// download streams url into partPath with Range resume (continued only when
// resumable() vouches for the partial, i.e. same origin + fresh). It records
// the origin in partPath+".src" before writing so an interrupted download can
// be resumed later without ever mixing two sources. Returns the release tag
// parsed from the final redirect URL ("" if not parseable).
func (m *Manager) download(ctx context.Context, url, partPath, srcPath string) (string, error) {
	// Resume: seek to the end of any existing partial file.
	offset := int64(0)
	if st, err := os.Stat(partPath); err == nil {
		offset = st.Size()
	}
	if offset > 0 {
		m.logf("[计划] L2 从 %d 字节续传 %s", offset, url)
	}
	if err := os.WriteFile(srcPath, []byte(url), 0o644); err != nil {
		return "", err
	}

	var resp *http.Response
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		req, err2 := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err2 != nil {
			return "", err2
		}
		req.Header.Set("User-Agent", "ompinyin/"+catalog.Version)
		if offset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}
		resp, err = m.client().Do(req)
		if err == nil {
			break
		}
	}
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusPartialContent && offset > 0:
		// resume OK
	case resp.StatusCode == http.StatusPartialContent:
		// a 206 without a Range request is a broken origin; restart
		return "", fmt.Errorf("HTTP 206 without a Range request from %s", url)
	case resp.StatusCode == http.StatusOK:
		// server ignored Range (or offset 0); restart from scratch
		offset = 0
	default:
		return "", fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	f, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			f.Close()
			return "", err
		}
	} else if err := f.Truncate(0); err != nil {
		f.Close()
		return "", err
	}
	copied, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	// explicit truncation check: a short body must fail here, not rely on the
	// transport noticing it (and never trust a Content-Length-less 200 either)
	if resp.ContentLength > 0 && copied != resp.ContentLength {
		return "", fmt.Errorf("truncated download from %s: got %d of %d bytes", url, copied, resp.ContentLength)
	}
	if resp.ContentLength <= 0 && copied == 0 {
		return "", fmt.Errorf("empty response body from %s", url)
	}
	// sha256 校验失败则中止、不落半截 .gram：完整性由 Fetch 的 verifyShape +
	// 记账比对把关，坏字节不会被 rename 进缓存。
	return TagFromURL(resp.Request.URL), nil
}

var (
	tagFromDownload = regexp.MustCompile(`/releases/download/([^/]+)/`)
	tagFromTag      = regexp.MustCompile(`/releases/tag/([^/]+)`)
	// NJU github-release mirror: /github-release/<owner>/<repo>/<tag>/<file>
	// ("LatestRelease" is NJU's alias for the last stable snapshot, not a tag)
	tagFromGithubReleaseMirror = regexp.MustCompile(`/github-release/[^/]+/[^/]+/([^/]+)/`)
)

// TagFromURL extracts the release tag from a resolved (post-redirect) URL.
func TagFromURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	s := u.Path
	for _, re := range []*regexp.Regexp{tagFromDownload, tagFromTag, tagFromGithubReleaseMirror} {
		if mm := re.FindStringSubmatch(s); mm != nil {
			return mm[1]
		}
	}
	return ""
}

// releasesAPIURL is the endpoint ResolveStableTag queries; a var so unit
// tests can point it at an httptest server.
var releasesAPIURL = catalog.RimeIceReleasesAPI

// stableTagCandidates builds the ordered API endpoints to try (direct first,
// then ghproxy accelerators); injectable so tests stay hermetic.
var stableTagCandidates = func(api string) []string {
	return append([]string{api}, catalog.Ghproxy(api)...)
}

// releaseInfo mirrors the fields of the GitHub releases API we consume.
type releaseInfo struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// ResolveStableTag resolves the newest STABLE release tag of rime-ice via the
// releases API (catalog.RimeIceReleasesAPI, newest first; the rolling nightly
// release is tagged "nightly" and skipped). Best-effort: tries the API
// directly, then via the ghproxy accelerators (CN reachability); returns ""
// when resolution is impossible — callers must fall back to the unpinned
// releases/latest form. Verified against the live API: the list is ordered
// newest-first and its first non-nightly entry is the latest stable tag.
//
// Package var so T0 tests can stub it (CI must never touch the network).
var ResolveStableTag = func(ctx context.Context) string {
	m := &Manager{}
	for _, u := range stableTagCandidates(releasesAPIURL) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "ompinyin/"+catalog.Version)
		resp, err := m.client().Do(req)
		if err != nil {
			continue
		}
		var releases []releaseInfo
		err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&releases)
		resp.Body.Close()
		if err != nil {
			continue
		}
		for _, r := range releases {
			if r.Draft || r.Prerelease || r.TagName == "" || r.TagName == "nightly" {
				continue
			}
			return r.TagName
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Extraction rules (§5.3 L2 解压规则)
// ---------------------------------------------------------------------------

// protectedNames are never overwritten if they already exist at the
// destination: user customizations and runtime data.
var protectedNames = map[string]bool{
	"user.yaml":         true,
	"installation.yaml": true,
	"custom_phrase.txt": true,
}

// ExtractZip extracts zipPath into destDir following the L2 rules:
//   - zip-slip rejected (.. or absolute paths)
//   - same-name *.custom.yaml skipped with a warning (managed-file guard)
//   - user.yaml / installation.yaml / *.userdb / custom_phrase.txt NEVER
//     overwritten (even with overwrite=true)
//   - with overwrite=false (plain install): any existing file is skipped —
//     a converged host re-run must not rewrite the whole data dir (P1-1b);
//     with overwrite=true (update): existing non-protected files are refreshed
func (m *Manager) ExtractZip(zipPath, destDir string, overwrite bool) (extracted, skipped []string, err error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, nil, err
	}

	// Pass 1: full manifest scan before touching disk (§5.3 解压前扫描).
	for _, f := range zr.File {
		name := f.Name
		clean := filepath.Clean(name)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) || strings.Contains(clean, ".."+string(filepath.Separator)) {
			return nil, nil, fmt.Errorf("zip-slip rejected: entry %q escapes the destination", name)
		}
	}

	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		clean := filepath.Clean(f.Name)
		base := filepath.Base(clean)

		if strings.HasSuffix(clean, ".custom.yaml") {
			// rime-ice full.zip must not contain custom.yaml; if upstream
			// packaging changes, guard the managed namespace (§5.3, ADR 5).
			m.logf("[跳过] L2 zip 内的 %s（受管命名空间，跳过 + 警告）", clean)
			skipped = append(skipped, clean)
			continue
		}

		dest := filepath.Join(destDir, clean)
		if _, err := os.Lstat(dest); err == nil {
			if protectedNames[base] || strings.HasSuffix(base, ".userdb") {
				m.logf("[跳过] L2 %s（已存在，永不覆盖）", clean)
				skipped = append(skipped, clean)
				continue
			}
			if !overwrite {
				m.logf("[跳过] L2 %s（已存在）", clean)
				skipped = append(skipped, clean)
				continue
			}
		}

		if err := extractEntry(f, dest); err != nil {
			return nil, nil, fmt.Errorf("extract %s: %w", clean, err)
		}
		extracted = append(extracted, clean)
	}
	return extracted, skipped, nil
}

func extractEntry(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if f.Mode()&0o111 != 0 {
		mode = 0o755
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".ompinyin-zx-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, rc); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dest)
}
