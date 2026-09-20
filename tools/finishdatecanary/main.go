//go:build liveprobe

// finishdatecanary creates one completed reading session, changes its finish
// date, and restores the exact original semantic snapshot. It prints no book,
// account, ISBN, shelf, date, or review values.
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

const confirmation = "CONFIRM_REVERSIBLE_FINISH_DATE_WRITE"

func main() {
	if len(os.Args) != 4 || os.Args[3] != confirmation {
		fail("expected ISBN, temporary YYYY-MM-DD date, and explicit confirmation")
	}
	isbn, err := domain.NormalizeISBN(os.Args[1])
	if err != nil {
		fail("invalid ISBN")
	}
	date, err := time.Parse("2006-01-02", os.Args[2])
	if err != nil || date.Format("2006-01-02") != os.Args[2] {
		fail("invalid date")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
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

	fmt.Fprintln(os.Stderr, "phase capture")
	original, err := goodreads.SetStatus(ctx, b, isbn, domain.StatusRead)
	if err != nil {
		failAfterLaunch("could not capture an already-read canary")
	}
	if original.Before.Status != domain.StatusRead || original.Before.DateRead != nil {
		failAfterLaunch("canary requires an originally read book with no finish date")
	}

	fmt.Fprintln(os.Stderr, "phase set-finish-date")
	changed, err := goodreads.SetFinishDate(ctx, b, isbn, date)
	if err != nil || !changed.Verified {
		restoreErr := restore(b, ctx, isbn, original.Before)
		failAfterLaunch(fmt.Sprintf("temporary finish date could not be verified (%v); restoration: %v", err, restoreErr))
	}
	fmt.Println("temporary_finish_date_verified", true)

	fmt.Fprintln(os.Stderr, "phase restore")
	if err := restore(b, ctx, isbn, original.Before); err != nil {
		failAfterLaunch(err.Error())
	}
	fmt.Println("restoration_verified", true)
}

func restore(b browser.Browser, ctx context.Context, isbn domain.ISBN, original domain.Book) error {
	_, _ = goodreads.SetStatus(ctx, b, isbn, original.Status)
	if original.DateRead == nil {
		if _, err := goodreads.ClearFinishDate(ctx, b, isbn); err != nil {
			return fmt.Errorf("finish-date restoration could not be verified (%v)", err)
		}
	}
	restored, err := goodreads.SetStatus(ctx, b, isbn, original.Status)
	if err != nil || !restored.Verified || !sameSnapshot(original, restored.After) {
		return fmt.Errorf("original snapshot could not be restored")
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

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
