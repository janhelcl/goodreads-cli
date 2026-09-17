//go:build liveprobe

// statuscanary performs one explicitly confirmed status change and restores
// the original status. It prints no book, account, ISBN, shelf, or review data.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
	"github.com/janhelcl/goodreads-cli/internal/goodreads"
	"github.com/janhelcl/goodreads-cli/internal/profile"
)

const confirmation = "CONFIRM_REVERSIBLE_STATUS_WRITE"
const restoreConfirmation = "CONFIRM_STATUS_RESTORATION"

func main() {
	if len(os.Args) != 5 || (os.Args[4] != confirmation && os.Args[4] != restoreConfirmation) {
		fail("expected ISBN, original status, temporary status, and an explicit confirmation")
	}
	isbn, err := domain.NormalizeISBN(os.Args[1])
	if err != nil {
		fail("invalid ISBN")
	}
	original := domain.ReadingStatus(os.Args[2])
	temporary := domain.ReadingStatus(os.Args[3])
	restorationOnly := os.Args[4] == restoreConfirmation && original == temporary
	if !original.Valid() || !temporary.Valid() || (original == temporary && !restorationOnly) {
		fail("expected two different valid statuses")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	paths, err := profile.DefaultPaths()
	if err != nil {
		fail("profile unavailable")
	}
	lock, err := paths.Acquire(ctx)
	if err != nil {
		fail("profile busy")
	}
	defer lock.Release()
	b, err := (browser.RodFactory{}).Launch(ctx, browser.LaunchOptions{
		ProfileDir: paths.Browser,
		Headless:   true,
	})
	if err != nil {
		fail("browser unavailable")
	}
	defer b.Close()
	failAfterLaunch := func(message string) {
		_ = b.Close()
		fail(message)
	}
	if restorationOnly {
		_, statusErr := goodreads.SetStatus(ctx, b, isbn, original)
		cleared, err := goodreads.ClearFinishDate(ctx, b, isbn)
		if err != nil || !cleared.Verified {
			failAfterLaunch(fmt.Sprintf("status restoration (%v) and finish-date cleanup could not be verified (%v)", statusErr, err))
		}
		restored, err := goodreads.SetStatus(ctx, b, isbn, original)
		if err != nil || !restored.Verified {
			failAfterLaunch(fmt.Sprintf("final restoration could not be verified (%v)", err))
		}
		fmt.Println("restoration_verified", true)
		fmt.Println("finish_date_clear_verified", true)
		return
	}

	forward, err := goodreads.SetStatus(ctx, b, isbn, temporary)
	if err != nil {
		if _, restoreErr := goodreads.SetStatus(ctx, b, isbn, original); restoreErr != nil {
			failAfterLaunch(fmt.Sprintf("temporary change failed (%v) and restoration could not be verified (%v)", err, restoreErr))
		}
		failAfterLaunch(fmt.Sprintf("temporary change failed (%v); original status was restored", err))
	}
	if forward.Before.Status != original {
		if _, restoreErr := goodreads.SetStatus(ctx, b, isbn, forward.Before.Status); restoreErr != nil {
			failAfterLaunch("observed original status differed and restoration could not be verified")
		}
		failAfterLaunch("observed original status differed; observed state was restored")
	}
	fmt.Println("temporary_change_verified", forward.Verified)

	restored, err := goodreads.SetStatus(ctx, b, isbn, original)
	if forward.Before.DateRead == nil {
		cleared, clearErr := goodreads.ClearFinishDate(ctx, b, isbn)
		if clearErr != nil || !cleared.Verified {
			failAfterLaunch(fmt.Sprintf("restoration finish-date cleanup could not be verified (%v)", clearErr))
		}
		restored, err = goodreads.SetStatus(ctx, b, isbn, original)
	}
	if err != nil {
		failAfterLaunch(fmt.Sprintf("restoration could not be verified (%v)", err))
	}
	if !restored.Verified || !sameSnapshot(forward.Before, restored.After) {
		failAfterLaunch("restoration snapshot did not match")
	}
	fmt.Println("restoration_verified", true)
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

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
