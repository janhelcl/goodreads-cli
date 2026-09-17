//go:build liveprobe

// reviewcanary writes one temporary Unicode review and restores the exact
// original review. It prints no book, account, ISBN, or review values.
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

const confirmation = "CONFIRM_REVERSIBLE_REVIEW_WRITE"

const temporaryReview = "goodreads-cli reversible compatibility canary ✓\nTemporary review; restored immediately."

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 3 || os.Args[2] != confirmation {
		return errors.New("expected one exact ISBN and explicit reversible-review confirmation")
	}
	isbn, err := domain.NormalizeISBN(os.Args[1])
	if err != nil {
		return errors.New("invalid ISBN")
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

	fmt.Fprintln(os.Stderr, "phase capture")
	original, err := goodreads.ReviewSnapshotForLiveProbe(ctx, b, isbn)
	if err != nil || original.Review == nil {
		return errors.New("original review snapshot could not be captured")
	}
	if *original.Review == temporaryReview {
		return errors.New("canary already contains the temporary review; restore it manually")
	}

	fmt.Fprintln(os.Stderr, "phase temporary-review")
	changed, err := goodreads.Review(ctx, b, isbn, ptr(temporaryReview))
	if err != nil || !changed.Verified {
		restoreErr := restore(ctx, b, isbn, original)
		if restoreErr != nil {
			return errors.New("temporary review was not verified and restoration failed")
		}
		return errors.New("temporary review was not verified; original snapshot was restored")
	}
	fmt.Println("temporary_review_verified", true)

	fmt.Fprintln(os.Stderr, "phase restore")
	if err := restore(ctx, b, isbn, original); err != nil {
		return err
	}
	fmt.Println("restoration_verified", true)
	return nil
}

func restore(ctx context.Context, b browser.Browser, isbn domain.ISBN, original domain.Book) error {
	if original.Review == nil {
		return errors.New("original review was unavailable")
	}
	restored, err := goodreads.Review(ctx, b, isbn, original.Review)
	if err != nil || !restored.Verified || !sameSnapshot(original, restored.After) {
		return errors.New("original review snapshot could not be restored")
	}
	return nil
}

func sameSnapshot(left, right domain.Book) bool {
	return left.BookID == right.BookID &&
		left.ISBN10 == right.ISBN10 &&
		left.ISBN13 == right.ISBN13 &&
		left.Rating == right.Rating &&
		left.Status == right.Status &&
		equalOptional(left.DateRead, right.DateRead) &&
		equalStrings(left.Bookshelves, right.Bookshelves) &&
		equalOptional(left.Review, right.Review)
}

func equalOptional(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, value := range left {
		counts[value]++
	}
	for _, value := range right {
		counts[value]--
		if counts[value] < 0 {
			return false
		}
	}
	return true
}

func ptr(value string) *string {
	return &value
}
