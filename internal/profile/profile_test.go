package profile

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestLockAndProfileLifecycle(t *testing.T) {
	p := PathsForRoot(filepath.Join(t.TempDir(), "app"))
	first, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	if err := p.EnsureBrowser(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(p.Browser); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0700) {
		t.Fatalf("profile permissions: info=%v err=%v", info, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	second, err := p.Acquire(ctx)
	if second != nil || !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent acquire: lock=%v err=%v", second, err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	expired, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Acquire(expired); !errors.Is(err, context.Canceled) || errors.Is(err, ErrBusy) {
		t.Fatalf("expired acquire remapped: %v", err)
	}
	third, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer third.Release()
	if err := p.RemoveBrowser(); err != nil {
		t.Fatal(err)
	}
	if err := p.RemoveBrowser(); err != nil {
		t.Fatalf("repeat removal: %v", err)
	}
	if _, err := os.Stat(p.Browser); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("profile still exists: %v", err)
	}
}

func TestRefusesSymlinkProfile(t *testing.T) {
	target := filepath.Join(t.TempDir(), "personal-profile")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	p := PathsForRoot(filepath.Join(t.TempDir(), "app"))
	if err := os.Mkdir(p.Root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p.Browser); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := p.EnsureBrowser(); err == nil {
		t.Fatal("accepted a symlink as the browser profile")
	}
	if err := p.RemoveBrowser(); err == nil {
		t.Fatal("removed through a symlink")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("symlink target changed: %v", err)
	}
}

func TestLockAcrossProcesses(t *testing.T) {
	p := PathsForRoot(filepath.Join(t.TempDir(), "app"))
	first, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestLockHelperProcess")
	cmd.Env = append(os.Environ(), "GOODREADS_LOCK_HELPER_ROOT="+p.Root)
	if err := cmd.Run(); err == nil {
		_ = first.Release()
		t.Fatal("second process acquired held profile lock")
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 8 {
		_ = first.Release()
		t.Fatalf("unexpected helper result: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(os.Args[0], "-test.run=TestLockHelperProcess")
	cmd.Env = append(os.Environ(), "GOODREADS_LOCK_HELPER_ROOT="+p.Root)
	if err := cmd.Run(); err != nil {
		t.Fatalf("lock remained after release: %v", err)
	}
}

func TestLockHelperProcess(t *testing.T) {
	root := os.Getenv("GOODREADS_LOCK_HELPER_ROOT")
	if root == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	lock, err := PathsForRoot(root).Acquire(ctx)
	if errors.Is(err, ErrBusy) {
		os.Exit(8)
	}
	if err != nil {
		os.Exit(1)
	}
	_ = lock.Release()
}
