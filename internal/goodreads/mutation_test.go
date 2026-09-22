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

func ratingSnapshot() domain.Book {
	date := "2026-09-12"
	review := "private review text"
	return domain.Book{
		BookID:      "42",
		ISBN10:      "0306406152",
		ISBN13:      "9780306406157",
		Rating:      2,
		Status:      domain.StatusRead,
		DateRead:    &date,
		Bookshelves: []string{"speculative"},
		Review:      &review,
	}
}

func TestVerifyRatingMutation(t *testing.T) {
	before := ratingSnapshot()
	after := ratingSnapshot()
	after.Rating = 4
	after.Bookshelves = []string{"reference", "speculative"}
	before.Bookshelves = []string{"speculative", "reference"}

	result, err := VerifyRatingMutation(before, after, 4)
	if err != nil || !result.Verified || result.Operation != "rate" ||
		result.Changes.Rating == nil || *result.Changes.Rating != 4 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestVerifyRatingMutationAcceptsAlreadySatisfiedState(t *testing.T) {
	before := ratingSnapshot()
	before.Rating = 4
	result, err := VerifyRatingMutation(before, before, 4)
	if err != nil || !result.Verified {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestVerifyRatingMutationFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		alter func(*domain.Book, *domain.Book)
		field string
	}{
		{"identity", func(_ *domain.Book, after *domain.Book) { after.BookID = "43" }, "book_id"},
		{"rating", func(_ *domain.Book, after *domain.Book) { after.Rating = 3 }, "rating"},
		{"status", func(_ *domain.Book, after *domain.Book) { after.Status = domain.StatusToRead }, "status"},
		{"date", func(_ *domain.Book, after *domain.Book) { after.DateRead = nil }, "date_read"},
		{"shelves", func(_ *domain.Book, after *domain.Book) { after.Bookshelves = nil }, "bookshelves"},
		{"review changed", func(_ *domain.Book, after *domain.Book) {
			changed := "different private text"
			after.Review = &changed
		}, "review"},
		{"review unavailable before", func(before *domain.Book, _ *domain.Book) { before.Review = nil }, "review"},
		{"review unavailable after", func(_ *domain.Book, after *domain.Book) { after.Review = nil }, "review"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := ratingSnapshot()
			after := ratingSnapshot()
			after.Rating = 4
			tc.alter(&before, &after)
			result, err := VerifyRatingMutation(before, after, 4)
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

func TestVerifyRatingMutationValidatesRating(t *testing.T) {
	for _, rating := range []int{0, 6} {
		if _, err := VerifyRatingMutation(ratingSnapshot(), ratingSnapshot(), rating); !errors.Is(err, domain.ErrInvalidRating) {
			t.Fatalf("rating %d: %v", rating, err)
		}
	}
}

func TestRateClicksOnceAndVerifiesFreshState(t *testing.T) {
	b, action, isbn := ratingFlowBrowser(t, 2, 4)
	clicks := 0
	action.click = func(selector string) error {
		clicks++
		if !strings.Contains(selector, "title='really liked it'") {
			t.Fatalf("unexpected rating selector %q", selector)
		}
		action.html = ownerRatingFixture(4)
		return nil
	}
	result, err := Rate(context.Background(), b, isbn, 4)
	if err != nil || !result.Verified || result.Before.Rating != 2 || result.After.Rating != 4 || clicks != 1 {
		t.Fatalf("result=%+v err=%v clicks=%d calls=%v", result, err, clicks, b.calls)
	}
	if n := countCalls(b.calls, shelfTarget("", exactShelfPageSize).String()); n != 1 {
		t.Fatalf("rate ran %d terminating scans; want one: %v", n, b.calls)
	}
}

func TestRateAlreadySatisfiedDoesNotClick(t *testing.T) {
	b, action, isbn := ratingFlowBrowser(t, 4, 4)
	action.click = func(string) error {
		t.Fatal("already-satisfied rating was clicked")
		return nil
	}
	result, err := Rate(context.Background(), b, isbn, 4)
	if err != nil || !result.Verified {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestMutationReadbackFallsBackWhenKnownRowMoved(t *testing.T) {
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	const pageURL = "https://www.goodreads.com/review/list/123"
	before := libraryTestPage(ownerRatingFixture(2), pageURL)
	after := libraryTestPage(ownerRatingFixture(4), pageURL)
	b := &fakeBrowser{
		pages: map[string]browser.Page{
			pageURL: libraryTestPage(emptyOwnerLibrary, pageURL),
		},
		pagesQueue: map[string][]browser.Page{
			libraryURL: {before, after},
		},
	}
	candidate, err := findRatingCandidate(context.Background(), b, isbn)
	if err != nil {
		t.Fatal(err)
	}
	readback, err := readbackMutationCandidate(
		context.Background(), b, isbn, candidate, "mutation.rating", false,
	)
	if err != nil || readback.Book.Rating != 4 {
		t.Fatalf("readback=%+v err=%v calls=%v", readback, err, b.calls)
	}
	if countCalls(b.calls, shelfTarget("", exactShelfPageSize).String()) != 2 ||
		countCalls(b.calls, pageURL) != 1 {
		t.Fatalf("known-page fallback calls=%v", b.calls)
	}
}

func TestRateDoesNotReplayAmbiguousClick(t *testing.T) {
	b, action, isbn := ratingFlowBrowser(t, 2, 2)
	clicks := 0
	action.click = func(string) error {
		clicks++
		return errors.New("click result unknown")
	}
	_, err := Rate(context.Background(), b, isbn, 4)
	if !errors.Is(err, ErrMutationAmbiguous) || clicks != 1 {
		t.Fatalf("err=%v clicks=%d calls=%v", err, clicks, b.calls)
	}
}

func TestRateTreatsSignInRedirectAsExpired(t *testing.T) {
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	b := &fakeBrowser{pages: map[string]browser.Page{
		libraryURL: &fakePage{url: signInURL},
	}}
	_, err = Rate(context.Background(), b, isbn, 4)
	if !errors.Is(err, ErrSessionExpired) || len(b.calls) != 1 {
		t.Fatalf("err=%v calls=%v", err, b.calls)
	}
}

func TestRateMissingInteractiveStarsIsCompatibility(t *testing.T) {
	b, action, isbn := ratingFlowBrowser(t, 2, 4)
	action.html = strings.ReplaceAll(ownerRatingFixture(2), `<a class="star off"`, `<span class="star off"`)
	clicks := 0
	action.click = func(string) error {
		clicks++
		return nil
	}
	original := ratingControlTimeout
	ratingControlTimeout = 200 * time.Millisecond
	defer func() { ratingControlTimeout = original }()
	_, err := Rate(context.Background(), b, isbn, 4)
	if !errors.Is(err, ErrCompatibility) || clicks != 0 {
		t.Fatalf("err=%v clicks=%d calls=%v", err, clicks, b.calls)
	}
}

func ratingFlowBrowser(t *testing.T, beforeRating, readbackRating int) (*fakeBrowser, *fakePage, domain.ISBN) {
	t.Helper()
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	const pageURL = "https://www.goodreads.com/review/list/123"
	const reviewURL = "https://www.goodreads.com/review/edit/7"
	before := libraryTestPage(ownerRatingFixture(beforeRating), pageURL)
	action := libraryTestPage(ownerRatingFixture(beforeRating), pageURL)
	after := libraryTestPage(ownerRatingFixture(readbackRating), pageURL)
	reviewPage := &fakePage{
		url:  reviewURL,
		html: `<form><textarea id="review_review_usertext" name="review[review]"></textarea></form>`,
	}
	b := &fakeBrowser{
		pages: map[string]browser.Page{reviewURL: reviewPage},
		pagesQueue: map[string][]browser.Page{
			libraryURL: {before, after},
			pageURL:   {action, after},
		},
	}
	return b, action, isbn
}

func countCalls(calls []string, target string) int {
	n := 0
	for _, call := range calls {
		if call == target {
			n++
		}
	}
	return n
}

func ownerRatingFixture(rating int) string {
	html := strings.Replace(shelfFixture, `<a class="next_page" href="?page=2">next</a>`, "", 1)
	start := `<span class="staticStars">
<span class="staticStar p10"></span><span class="staticStar p10"></span><span class="staticStar p0"></span><span class="staticStar p0"></span><span class="staticStar p0"></span>
</span>`
	titles := []string{"did not like it", "it was ok", "liked it", "really liked it", "it was amazing"}
	var links strings.Builder
	for _, title := range titles {
		fmt.Fprintf(&links, `<a class="star off" title="%s" href="#"></a>`, title)
	}
	control := fmt.Sprintf(`<div class="stars" data-rating="%d.0">%s</div>`, rating, links.String())
	html = strings.Replace(html, start, control, 1)
	return strings.Replace(html, `<td class="field shelves">`, `<td class="field review"><div class="value"><a href="/review/edit/7">edit</a></div></td><td class="field shelves">`, 1)
}
