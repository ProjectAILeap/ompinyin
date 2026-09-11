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
	// CopyFile writes 0644, so a missing chmod would ship a non-executable
	// binary: the failure would only show up as `ompinyin: Permission denied`.
	if fi, err := os.Stat(exe); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("replaced binary must stay executable, mode=%v", fi.Mode())
	}
	if litter, _ := filepath.Glob(filepath.Join(dir, ".*.new")); len(litter) > 0 {
		t.Errorf("staging litter left behind: %v", litter)
	}
}

// crossDeviceDir returns a directory on a different filesystem than $TMPDIR, or
// "" when the host has none (there rename can never cross a mount, so EXDEV is
// unreproducible). Detection is behavioral — try the rename that rename(2)
// would refuse — so it needs no syscall constants and no platform guards.
func crossDeviceDir(t *testing.T) string {
	t.Helper()
	for _, cand := range []string{"/dev/shm", "/run", "/var/tmp", "/"} {
		if st, err := os.Stat(cand); err != nil || !st.IsDir() {
			continue
		}
		f, err := os.CreateTemp(cand, "probe-*")
		if err != nil {
			continue // not writable by this user
		}
		src := f.Name()
		f.Close()
		dst := filepath.Join(os.TempDir(), "probe-xfs-"+filepath.Base(src))
		err = os.Rename(src, dst)
		if err != nil {
			_ = os.Remove(src)
			return cand // the rename crossed a mount
		}
		_ = os.Remove(dst)
	}
	return ""
}

// TestApplyAcrossFilesystems locks the EXDEV regression. The download is staged
// in $TMPDIR while the binary lives on another mount — on Omarchy /tmp is tmpfs
// and $HOME is btrfs — and rename(2) cannot cross mounts. The old code renamed
// straight from the staging dir, so `update --self` failed with a misleading
// "无权限" on exactly the hosts it ships to, while TestApplyUpgrades above kept
// passing: it creates the exe AND the staging dir under the same $TMPDIR, the
// one layout where the rename is legal.
func TestApplyAcrossFilesystems(t *testing.T) {
	exeDir := t.TempDir() // created under the default $TMPDIR, before we move it
	other := crossDeviceDir(t)
	if other == "" {
		t.Skip("host has a single filesystem — a cross-mount rename is unreproducible here")
	}
	// Recreate the Omarchy layout: staging on the other mount, binary on this one.
	t.Setenv("TMPDIR", other)

	exe := filepath.Join(exeDir, "ompinyin")
	old := []byte("old-binary")
	if err := os.WriteFile(exe, old, 0o755); err != nil {
		t.Fatal(err)
	}
	newBin := []byte("new-binary-content")
	sum := sha256.Sum256(newBin)
	want := hex.EncodeToString(sum[:])
	asset := "ompinyin_linux_" + runtime.GOARCH

	origV, origF, origE, origS := Version, Fetch, Executable, Sudo
	defer func() { Version, Fetch, Executable, Sudo = origV, origF, origE, origS }()
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
	Sudo = func(...string) error {
		t.Error("a writable directory must not need sudo")
		return nil
	}

	r, err := Apply("1.0.0")
	if err != nil {
		t.Fatalf("self-upgrade across filesystems failed: %v", err)
	}
	if !r.Applied {
		t.Fatalf("expect applied: %+v", r)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newBin) {
		t.Errorf("exe not replaced: got %q", got)
	}
	if fi, err := os.Stat(exe); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("replaced binary must stay executable, mode=%v", fi.Mode())
	}
	if litter, _ := filepath.Glob(filepath.Join(exeDir, ".*.new")); len(litter) > 0 {
		t.Errorf("staging litter left next to the binary: %v", litter)
	}
}

// TestApplyFallsBackToSudo: an unwritable directory (the /usr/local/bin layout)
// must still get the pre-upgrade backup and the replacement, via the Sudo seam.
func TestApplyFallsBackToSudo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the read-only directory mode")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "ompinyin")
	old := []byte("old-binary")
	if err := os.WriteFile(exe, old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) // let TempDir cleanup remove it

	newBin := []byte("new-binary-content")
	sum := sha256.Sum256(newBin)
	want := hex.EncodeToString(sum[:])
	asset := "ompinyin_linux_" + runtime.GOARCH

	origV, origF, origE, origS := Version, Fetch, Executable, Sudo
	defer func() { Version, Fetch, Executable, Sudo = origV, origF, origE, origS }()
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
	var escalated []string
	Sudo = func(args ...string) error {
		escalated = append(escalated, args[0])
		// emulate `cp -p <src> <dst>` / `install -m 755 <src> <dst>`: root can
		// write into a directory this user cannot, which is the whole point.
		b, err := os.ReadFile(args[len(args)-2])
		if err != nil {
			return err
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return err
		}
		defer func() { _ = os.Chmod(dir, 0o500) }()
		return os.WriteFile(args[len(args)-1], b, 0o755)
	}

	r, err := Apply("1.0.0")
	if err != nil {
		t.Fatalf("sudo fallback failed: %v", err)
	}
	if !r.Applied {
		t.Fatalf("expect applied: %+v", r)
	}
	if strings.Join(escalated, ",") != "cp,install" {
		t.Errorf("escalations = %v, want the backup then the install", escalated)
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".ompinyin.ompinyin.bak")); err != nil || string(b) != string(old) {
		t.Errorf("pre-upgrade backup missing or wrong: %q %v", b, err)
	}
	if b, err := os.ReadFile(exe); err != nil || string(b) != string(newBin) {
		t.Errorf("exe not replaced: %q %v", b, err)
	}
}
