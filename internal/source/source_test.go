package source

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParsePreset(t *testing.T) {
	for _, ok := range []string{"cn", "upstream"} {
		if _, valid := ParsePreset(ok); !valid {
			t.Errorf("preset %q should parse", ok)
		}
	}
	for _, bad := range []string{"", "auto", "CN", "github", "random"} {
		if _, valid := ParsePreset(bad); valid {
			t.Errorf("preset %q should be rejected", bad)
		}
	}
}

func TestContentPresetCN(t *testing.T) {
	c := Content(PresetCN)
	if !strings.Contains(c, "mirrors.aliyun.com/archlinux/$repo/os/$arch") {
		t.Errorf("cn content missing aliyun:\n%s", c)
	}
	if !strings.Contains(c, "mirrors.tuna.tsinghua.edu.cn/archlinux/$repo/os/$arch") {
		t.Errorf("cn content missing tuna:\n%s", c)
	}
	if !strings.Contains(c, "stable-mirror.omarchy.org/$repo/os/$arch") {
		t.Errorf("cn content missing omarchy fallback:\n%s", c)
	}
}

func TestMatchClassify(t *testing.T) {
	cn := "# cn\nServer = https://mirrors.aliyun.com/archlinux/$repo/os/$arch\nServer = https://stable-mirror.omarchy.org/$repo/os/$arch\n"
	up := "Server = https://stable-mirror.omarchy.org/$repo/os/$arch\n"
	if !matched(cn, PresetCN) {
		t.Errorf("aliyun-first must classify as cn")
	}
	if matched(cn, PresetUpstream) {
		t.Errorf("aliyun-first must not classify as upstream")
	}
	if !matched(up, PresetUpstream) {
		t.Errorf("single omarchy must classify as upstream")
	}
	if matched(up, PresetCN) {
		t.Errorf("single omarchy must not classify as cn")
	}
}

func TestEnsureDryRunWritesNothing(t *testing.T) {
	origRF, origSudo := ReadFile, RunSudo
	defer func() { ReadFile, RunSudo = origRF, origSudo }()

	ReadFile = func(string) ([]byte, error) {
		return []byte("Server = https://stable-mirror.omarchy.org/$repo/os/$arch\n"), nil
	}
	sudoCalled := false
	RunSudo = func(args ...string) error { sudoCalled = true; return nil }

	var out bytes.Buffer
	res, err := Ensure(EnsureArgs{Preset: PresetCN, DryRun: true, Stdout: &out, Stdin: strings.NewReader("")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed || res.Skipped {
		t.Errorf("dry-run must neither change nor skip: %+v", res)
	}
	if sudoCalled {
		t.Error("dry-run must not run sudo")
	}
	if !strings.Contains(out.String(), "mirrors.aliyun.com") {
		t.Errorf("dry-run should print the target content:\n%s", out.String())
	}
}

func TestEnsureAlreadyMatchedSkips(t *testing.T) {
	origRF, origSudo := ReadFile, RunSudo
	defer func() { ReadFile, RunSudo = origRF, origSudo }()

	ReadFile = func(string) ([]byte, error) {
		return []byte("Server = https://mirrors.aliyun.com/archlinux/$repo/os/$arch\nServer = https://stable-mirror.omarchy.org/$repo/os/$arch\n"), nil
	}
	sudoCalled := false
	RunSudo = func(args ...string) error { sudoCalled = true; return nil }

	var out bytes.Buffer
	res, err := Ensure(EnsureArgs{Preset: PresetCN, Yes: true, Stdout: &out, Stdin: strings.NewReader("")})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Skipped || res.Changed {
		t.Errorf("already-cn should skip: %+v", res)
	}
	if sudoCalled {
		t.Error("already matched must not write")
	}
}

func TestEnsureBackupAndWrite(t *testing.T) {
	origRF, origSudo, origNow := ReadFile, RunSudo, Now
	defer func() { ReadFile, RunSudo, Now = origRF, origSudo, origNow }()

	ReadFile = func(string) ([]byte, error) {
		return []byte("Server = https://stable-mirror.omarchy.org/$repo/os/$arch\n"), nil
	}
	Now = func() time.Time { return time.Unix(1700000000, 0) }

	var calls []string
	RunSudo = func(args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}

	var out bytes.Buffer
	res, err := Ensure(EnsureArgs{Preset: PresetCN, Yes: true, Stdout: &out, Stdin: strings.NewReader("")})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || res.Skipped {
		t.Errorf("expected a change: %+v", res)
	}
	if res.BackedUp == "" {
		t.Errorf("expected a backup path recorded")
	}
	// backup cp and write cp both happened
	var backup, write bool
	for _, c := range calls {
		if strings.Contains(c, "cp") && strings.Contains(c, "mirrorlist.bak-1700000000") && strings.Contains(c, MirrorlistPath) {
			backup = true
		}
		if strings.Contains(c, "cp") && strings.Contains(c, "ompinyin-mirrorlist-") && strings.Contains(c, MirrorlistPath) {
			write = true
		}
	}
	if !backup {
		t.Errorf("backup cp not invoked: %v", calls)
	}
	if !write {
		t.Errorf("write cp not invoked: %v", calls)
	}
	if !strings.Contains(out.String(), "提示：仅用") {
		t.Errorf("should warn against full -Syu:\n%s", out.String())
	}
}

func TestEnsureCanceledWithoutYes(t *testing.T) {
	origRF, origSudo := ReadFile, RunSudo
	defer func() { ReadFile, RunSudo = origRF, origSudo }()

	ReadFile = func(string) ([]byte, error) {
		return []byte("Server = https://stable-mirror.omarchy.org/$repo/os/$arch\n"), nil
	}
	sudoCalled := false
	RunSudo = func(args ...string) error { sudoCalled = true; return nil }

	var out bytes.Buffer
	_, err := Ensure(EnsureArgs{Preset: PresetCN, Yes: false, Stdout: &out, Stdin: strings.NewReader("n\n")})
	if err == nil || !strings.Contains(err.Error(), "已取消") {
		t.Fatalf("expected cancellation error, got %v", err)
	}
	if sudoCalled {
		t.Error("canceled run must not run sudo")
	}
}

func TestEnsureReadErrorFatal(t *testing.T) {
	origRF, origSudo := ReadFile, RunSudo
	defer func() { ReadFile, RunSudo = origRF, origSudo }()

	ReadFile = func(string) ([]byte, error) { return nil, errors.New("permission denied") }
	sudoCalled := false
	RunSudo = func(args ...string) error { sudoCalled = true; return nil }

	var out bytes.Buffer
	_, err := Ensure(EnsureArgs{Preset: PresetCN, Yes: true, Stdout: &out, Stdin: strings.NewReader("")})
	if err == nil || !strings.Contains(err.Error(), "读取") {
		t.Fatalf("expected read error, got %v", err)
	}
	if sudoCalled {
		t.Error("read error must abort before sudo")
	}
}
