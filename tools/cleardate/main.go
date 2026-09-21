//go:build liveprobe

// cleardate clears one exact edition's finish date for restoration after a
// reversible public finish canary. It prints no book, account, ISBN, or date
// values.
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

const confirmation = "CONFIRM_CLEAR_FINISH_DATE"

func main() {
	if len(os.Args) != 3 || os.Args[2] != confirmation {
		fail("expected ISBN and explicit confirmation")
	}
	isbn, err := domain.NormalizeISBN(os.Args[1])
	if err != nil {
		fail("invalid ISBN")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
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

	result, err := goodreads.ClearFinishDate(ctx, b, isbn)
	if err != nil || !result.Verified || result.After.DateRead != nil {
		fail(fmt.Sprintf("finish date could not be cleared (%v)", err))
	}
	fmt.Println("cleared", true)
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
