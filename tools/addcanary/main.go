//go:build liveprobe

// addcanary performs one explicitly armed, reversible add-to-read mutation.
// It verifies the exact ISBN from visible Goodreads UI, freshly reads the
// added owner row, removes that row through its visible action, and verifies
// the original absence by stable Goodreads book ID.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
	"github.com/janhelcl/goodreads-cli/internal/goodreads"
	"github.com/janhelcl/goodreads-cli/internal/profile"
)

const (
	confirmation = "CONFIRM_REVERSIBLE_ADD_WRITE"
	restoreOnly  = "CONFIRM_ADD_RESTORATION"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 3 && len(os.Args) != 4 {
		return errors.New("expected one exact ISBN, optional status, and explicit reversible-add confirmation")
	}
	isbn, err := domain.NormalizeISBN(os.Args[1])
	if err != nil {
		return errors.New("invalid ISBN")
	}
	status := domain.StatusToRead
	token := os.Args[len(os.Args)-1]
	if len(os.Args) == 4 {
		status = domain.ReadingStatus(os.Args[2])
		if !status.Valid() {
			return errors.New("status must be to-read, currently-reading, or read")
		}
	}
	if token != confirmation && token != restoreOnly {
		return errors.New("explicit reversible-add confirmation is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	paths, err := profile.DefaultPaths()
	if err != nil {
		return errors.New("profile unavailable")
	}
	lock, err := paths.Acquire(ctx)
	if err != nil {
		return errors.New("profile busy")
	}
	defer lock.Release()
	b, err := (browser.RodFactory{}).Launch(ctx, browser.LaunchOptions{
		ProfileDir: paths.Browser,
		Headless:   true,
	})
	if err != nil {
		return errors.New("browser unavailable")
	}
	defer b.Close()

	if token == restoreOnly {
		if err := goodreads.RemoveForLiveProbe(ctx, b, isbn); err != nil {
			return fmt.Errorf("restoration failed: %w", err)
		}
		present, err := goodreads.IsInLibraryForLiveProbe(ctx, b, isbn)
		if err != nil || present {
			return fmt.Errorf("original absence could not be verified: %w", err)
		}
		fmt.Println("restoration_verified", true)
		return nil
	}
	present, err := goodreads.IsInLibraryForLiveProbe(ctx, b, isbn)
	if err != nil {
		return fmt.Errorf("initial exact-edition check failed: %w", err)
	}
	if present {
		return errors.New("canary requires an edition absent from the library")
	}
	fmt.Fprintln(os.Stderr, "phase add")
	result, err := goodreads.Add(ctx, b, isbn, status)
	if err != nil || !result.Verified || result.After.Status != status {
		return fmt.Errorf("temporary add could not be verified: %w", err)
	}
	fmt.Println("temporary_add_verified", true)

	fmt.Fprintln(os.Stderr, "phase restore")
	if err := goodreads.RemoveForLiveProbe(ctx, b, isbn); err != nil {
		return fmt.Errorf("temporary add was verified but restoration failed: %w", err)
	}
	present, err = goodreads.IsInLibraryForLiveProbe(ctx, b, isbn)
	if err != nil || present {
		return fmt.Errorf("original absence could not be verified: %w", err)
	}
	fmt.Println("restoration_verified", true)
	return nil
}
