package goodreads

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
)

func TestVerifyStatusMutation(t *testing.T) {
	before := ratingSnapshot()
	after := ratingSnapshot()
	after.Status = domain.StatusCurrentlyReading
	after.Bookshelves = []string{"reference", "speculative"}
	before.Bookshelves = []string{"speculative", "reference"}

	result, err := VerifyStatusMutation(before, after, domain.StatusCurrentlyReading)
	if err != nil || !result.Verified || result.Operation != "status" ||
		result.Changes.Status == nil || *result.Changes.Status != domain.StatusCurrentlyReading {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestVerifyStatusMutationAcceptsAlreadySatisfiedState(t *testing.T) {
	before := ratingSnapshot()
	result, err := VerifyStatusMutation(before, before, before.Status)
	if err != nil || !result.Verified {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestVerifyStatusMutationFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		alter func(*domain.Book, *domain.Book)
		field string
	}{
		{"identity", func(_ *domain.Book, after *domain.Book) { after.BookID = "43" }, "book_id"},
		{"status", func(_ *domain.Book, after *domain.Book) { after.Status = domain.StatusRead }, "status"},
		{"rating", func(_ *domain.Book, after *domain.Book) { after.Rating++ }, "rating"},
		{"date", func(_ *domain.Book, after *domain.Book) { after.DateRead = nil }, "date_read"},
		{"shelves", func(_ *domain.Book, after *domain.Book) { after.Bookshelves = nil }, "bookshelves"},
		{"review", func(_ *domain.Book, after *domain.Book) {
			changed := "different private text"
			after.Review = &changed
		}, "review"},
		{"review unavailable", func(before *domain.Book, _ *domain.Book) { before.Review = nil }, "review"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := ratingSnapshot()
			after := ratingSnapshot()
			after.Status = domain.StatusCurrentlyReading
			tc.alter(&before, &after)
			result, err := VerifyStatusMutation(before, after, domain.StatusCurrentlyReading)
			if !errors.Is(err, ErrVerificationFailed) || result.Verified ||
				!strings.Contains(err.Error(), tc.field) {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "different") {
				t.Fatalf("verification error leaked review text: %v", err)
			}
		})
	}
}

func TestSetStatusClicksOnceAndVerifiesFreshState(t *testing.T) {
	b, action, isbn := statusFlowBrowser(t, domain.StatusRead, domain.StatusCurrentlyReading)
	clicks := 0
	action.click = func(selector string) error {
		clicks++
		switch clicks {
		case 1:
			if !strings.Contains(selector, "shelfChooserLink") {
				t.Fatalf("unexpected chooser selector %q", selector)
			}
			action.html = ownerStatusFixture(domain.StatusRead, true)
		case 2:
			if !strings.Contains(selector, "alt='currently-reading'") {
				t.Fatalf("unexpected status selector %q", selector)
			}
			action.html = ownerStatusFixture(domain.StatusCurrentlyReading, false)
		default:
			t.Fatalf("unexpected extra click %q", selector)
		}
		return nil
	}

	result, err := SetStatus(context.Background(), b, isbn, domain.StatusCurrentlyReading)
	if err != nil || !result.Verified || result.Before.Status != domain.StatusRead ||
		result.After.Status != domain.StatusCurrentlyReading || clicks != 2 {
		t.Fatalf("result=%+v err=%v clicks=%d calls=%v", result, err, clicks, b.calls)
	}
}

func TestSetStatusRetriesChooserOpenUntilContractHolds(t *testing.T) {
	originalWait := chooserRetryWait
	chooserRetryWait = 20 * time.Millisecond
	t.Cleanup(func() { chooserRetryWait = originalWait })

	b, action, isbn := statusFlowBrowser(t, domain.StatusRead, domain.StatusCurrentlyReading)
	clicks := 0
	action.click = func(selector string) error {
		clicks++
		switch {
		case strings.Contains(selector, "shelfChooserLink"):
			if clicks >= 2 {
				action.html = ownerStatusFixture(domain.StatusRead, true)
			}
		case strings.Contains(selector, "alt='currently-reading'"):
			action.html = ownerStatusFixture(domain.StatusCurrentlyReading, false)
		default:
			t.Fatalf("unexpected selector %q", selector)
		}
		return nil
	}

	result, err := SetStatus(context.Background(), b, isbn, domain.StatusCurrentlyReading)
	if err != nil || !result.Verified || clicks != 3 {
		t.Fatalf("result=%+v err=%v clicks=%d", result, err, clicks)
	}
}

func TestSetStatusAlreadySatisfiedDoesNotClick(t *testing.T) {
	b, action, isbn := statusFlowBrowser(t, domain.StatusRead, domain.StatusRead)
	action.click = func(string) error {
		t.Fatal("already-satisfied status was clicked")
		return nil
	}
	result, err := SetStatus(context.Background(), b, isbn, domain.StatusRead)
	if err != nil || !result.Verified {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestStartUsesCurrentlyReadingSemanticOperation(t *testing.T) {
	b, action, isbn := statusFlowBrowser(t, domain.StatusCurrentlyReading, domain.StatusCurrentlyReading)
	action.click = func(string) error {
		t.Fatal("already-satisfied start was clicked")
		return nil
	}
	result, err := Start(context.Background(), b, isbn)
	if err != nil || !result.Verified || result.Operation != "start" ||
		result.Changes.Status == nil || *result.Changes.Status != domain.StatusCurrentlyReading {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestSetStatusDoesNotReplayAmbiguousClick(t *testing.T) {
	b, action, isbn := statusFlowBrowser(t, domain.StatusRead, domain.StatusRead)
	clicks := 0
	action.click = func(selector string) error {
		clicks++
		if clicks == 1 {
			action.html = ownerStatusFixture(domain.StatusRead, true)
			return nil
		}
		return errors.New("click result unknown")
	}
	_, err := SetStatus(context.Background(), b, isbn, domain.StatusCurrentlyReading)
	if !errors.Is(err, ErrMutationAmbiguous) || clicks != 2 {
		t.Fatalf("err=%v clicks=%d calls=%v", err, clicks, b.calls)
	}
}

func TestSetStatusRejectsInvalidStatusBeforeBrowserWork(t *testing.T) {
	b := &fakeBrowser{}
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(context.Background(), b, isbn, "paused"); !errors.Is(err, domain.ErrInvalidStatus) {
		t.Fatalf("err=%v calls=%v", err, b.calls)
	}
}

func statusFlowBrowser(
	t *testing.T,
	beforeStatus domain.ReadingStatus,
	readbackStatus domain.ReadingStatus,
) (*fakeBrowser, *fakePage, domain.ISBN) {
	t.Helper()
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	const pageURL = "https://www.goodreads.com/review/list/123"
	const reviewURL = "https://www.goodreads.com/review/edit/7"
	before := libraryTestPage(ownerStatusFixture(beforeStatus, false), pageURL)
	action := libraryTestPage(ownerStatusFixture(beforeStatus, false), pageURL)
	after := libraryTestPage(ownerStatusFixture(readbackStatus, false), pageURL)
	reviewPage := &fakePage{
		url:  reviewURL,
		html: `<form><textarea id="review_review_usertext" name="review[review]"></textarea></form>`,
	}
	b := &fakeBrowser{
		pages: map[string]browser.Page{reviewURL: reviewPage},
		pagesQueue: map[string][]browser.Page{
			libraryURL: {before, after},
			pageURL:    {action, after},
		},
	}
	return b, action, isbn
}

func ownerStatusFixture(status domain.ReadingStatus, open bool) string {
	html := ownerRatingFixture(2)
	oldCell := `<td class="field shelves"><div class="value"><a href="?shelf=read">read</a>, <a href="?shelf=speculative">speculative</a></div></td>`
	openClass := ""
	if open {
		openClass = " open"
	}
	options := ""
	for _, option := range []domain.ReadingStatus{
		domain.StatusToRead,
		domain.StatusCurrentlyReading,
		domain.StatusRead,
	} {
		class := "visible exclusive"
		if option == status {
			class += " exclusive_chosen"
		}
		options += fmt.Sprintf(`<li alt="%s" class="%s"><span>%s</span></li>`, option, class, option)
	}
	newCell := fmt.Sprintf(
		`<td class="field shelves"><div class="value"><a href="?shelf=%s">%s</a>, <a href="?shelf=speculative">speculative</a><a class="shelfChooserLink" href="#">edit</a><div class="shelfChooserWrapper%s"><ul>%s</ul></div></div></td>`,
		status, status, openClass, options,
	)
	if !strings.Contains(html, oldCell) {
		panic("owner fixture shelf cell changed")
	}
	return strings.Replace(html, oldCell, newCell, 1)
}
