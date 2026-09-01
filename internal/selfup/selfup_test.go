package selfup

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v1.2.0", "v1.1.0", true},
		{"v1.1.0", "v1.2.0", false},
		{"v1.0.0", "v1.0.0", false},
		{"v1.10.0", "v1.9.0", true}, // numeric, not string, compare
		{"v1.0.0", "a1b2c3", true},  // current unparseable (dev build) → upgrade offered
		{"v1.0.0", "v1.0.0-dirty", false},
		{"2.0.0", "1.9.9", true},
	}
	for _, c := range cases {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%q,%q)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestCheck(t *testing.T) {
	orig := Version
	defer func() { Version = orig }()
	Version = func() (string, error) { return "v1.1.0", nil }
	r, err := Check("1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Newer || r.Latest != "v1.1.0" || r.Asset == "" {
		t.Errorf("check wrong: %+v", r)
	}
}

func TestApplyIdentity(t *testing.T) {
	orig := Version
	defer func() { Version = orig }()
	Version = func() (string, error) { return "v1.0.0", nil }
	r, err := Apply("1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if r.Applied {
		t.Errorf("identical version must not apply: %+v", r)
	}
}

// TestApplyUpgrades drives a complete self-upgrade with the network / exec
// seams stubbed: an older binary is backed up and atomically replaced only
// after its sha256 matches the fetched checksums.txt.
func TestApplyUpgrades(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "ompinyin")
	old := []byte("old-binary")
	if err := os.WriteFile(exe, old, 0o755); err != nil {
		t.Fatal(err)
	}
	asset := "ompinyin_linux_" + runtime.GOARCH
	newBin := []byte("new-binary-content")
	sum := sha256.Sum256(newBin)
	want := hex.EncodeToString(sum[:])

	origV, origF, origE := Version, Fetch, Executable
	defer func() { Version, Fetch, Executable = origV, origF, origE }()

	Version = func() (string, error) { return "v1.1.0", nil }
	Executable = func() (string, error) { return exe, nil }
	Fetch = func(url, dest string) error {
		switch {
		case strings.HasSuffix(url, "checksums.txt"):
			return os.WriteFile(dest, []byte(want+"  "+asset+"\n"), 0o644)
		case strings.HasSuffix(url, asset):
			return os.WriteFile(dest, newBin, 0o755)
		default:
			return os.WriteFile(dest, nil, 0o644)
		}
	}

	r, err := Apply("1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Applied {
		t.Fatalf("expect applied: %+v", r)
	}
	if !strings.Contains(r.Message, "已升级") {
		t.Errorf("message=%q", r.Message)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newBin) {
		t.Errorf("exe not replaced: got %q", got)
	}
	bak := filepath.Join(dir, "."+filepath.Base(exe)+".ompinyin.bak")
	gotBak, err := os.ReadFile(bak)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotBak) != string(old) {
		t.Errorf("backup wrong: got %q", gotBak)
	}
}
