package profile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

var ErrBusy = errors.New("Goodreads browser profile is in use")

// Paths names only data owned by this application. The lock remains outside the
// Chromium directory so logout can remove the profile while holding the lock.
type Paths struct {
	Root    string
	Browser string
	Lock    string
	Owner   string
}

func DefaultPaths() (Paths, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("locate application data: %w", err)
	}
	return PathsForRoot(filepath.Join(base, "goodreads-cli")), nil
}

// PathsForRoot is intended for tests and internal callers, not a public
// profile override. A release command always uses DefaultPaths.
func PathsForRoot(root string) Paths {
	return Paths{
		Root:    root,
		Browser: filepath.Join(root, "chromium-profile"),
		Lock:    filepath.Join(root, "profile.lock"),
		Owner:   filepath.Join(root, "profile.owner"),
	}
}

func (p Paths) ensureRoot() error {
	if err := refuseSymlink(p.Root); err != nil {
		return err
	}
	if err := os.MkdirAll(p.Root, 0700); err != nil {
		return fmt.Errorf("create application data directory: %w", err)
	}
	return os.Chmod(p.Root, 0700)
}

func (p Paths) EnsureBrowser() error {
	if err := p.ensureRoot(); err != nil {
		return err
	}
	if err := refuseSymlink(p.Browser); err != nil {
		return err
	}
	if err := os.MkdirAll(p.Browser, 0700); err != nil {
		return fmt.Errorf("create dedicated browser profile: %w", err)
	}
	return os.Chmod(p.Browser, 0700)
}

// RemoveBrowser deletes only the exact CLI-owned browser directory. The caller
// must hold the profile lock; an absent profile is a successful no-op.
func (p Paths) HasBrowser() (bool, error) {
	if err := refuseSymlink(p.Root); err != nil {
		return false, err
	}
	info, err := os.Lstat(p.Browser)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, errors.New("refusing unexpected browser profile path")
	}
	return true, nil
}

func (p Paths) RemoveBrowser() error {
	if filepath.Base(p.Browser) != "chromium-profile" || filepath.Dir(p.Browser) != p.Root {
		return errors.New("refusing to remove an unexpected browser profile path")
	}
	if err := refuseSymlink(p.Root); err != nil {
		return err
	}
	if err := refuseSymlink(p.Browser); err != nil {
		return err
	}
	if err := os.RemoveAll(p.Browser); err != nil {
		return err
	}
	marker := filepath.Join(p.Root, "browser-product")
	if err := os.Remove(marker); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func refuseSymlink(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("refusing unexpected application data path")
	}
	return nil
}

func refuseUnsafeFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("refusing unexpected profile coordination file")
	}
	return nil
}

type Lock struct {
	file  *flock.Flock
	owner string
}

// Acquire uses an OS lock, not a timestamp heuristic. A cancelled or timed-out
// wait returns ErrBusy when another process still owns the lock.
func (p Paths) Acquire(ctx context.Context) (*Lock, error) {
	if err := p.ensureRoot(); err != nil {
		return nil, err
	}
	if err := refuseUnsafeFile(p.Lock); err != nil {
		return nil, err
	}
	if err := refuseUnsafeFile(p.Owner); err != nil {
		return nil, err
	}
	callerCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	f := flock.New(p.Lock, flock.SetPermissions(0600))
	locked, err := f.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("lock browser profile: %w", err)
	}
	if !locked {
		if errors.Is(callerCtx.Err(), context.DeadlineExceeded) {
			return nil, ErrBusy
		}
		if callerCtx.Err() != nil {
			return nil, callerCtx.Err()
		}
		return nil, ErrBusy
	}
	owner := fmt.Sprintf("pid=%d\nstarted=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(p.Owner, []byte(owner), 0600); err != nil {
		_ = f.Unlock()
		return nil, fmt.Errorf("record profile lock owner: %w", err)
	}
	return &Lock{file: f, owner: p.Owner}, nil
}

func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	_ = os.Remove(l.owner)
	err := l.file.Unlock()
	l.file = nil
	return err
}
