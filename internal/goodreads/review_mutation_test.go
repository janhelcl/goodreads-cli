package goodreads

import (
	"context"
	"errors"
	"html"
	"strings"
	"testing"

	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
)

func TestVerifyReviewMutation(t *testing.T) {
	before := ratingSnapshot()
	after := ratingSnapshot()
	wanted := "Replaced private text ✓"
	after.Review = &wanted

	result, err := VerifyReviewMutation(before, after, wanted)
	if err != nil || !result.Verified || result.Operation != "review" ||
		result.Changes.Review == nil || *result.Changes.Review != wanted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestVerifyReviewMutationFailsClosedWithoutLeakingText(t *testing.T) {
	const wanted = "new secret review"
	for _, tc := range []struct {
		name  string
		alter func(*domain.Book, *domain.Book)
		field string
	}{
		{"identity", func(_ *domain.Book, after *domain.Book) { after.BookID = "43" }, "book_id"},
		{"review mismatch", func(_ *domain.Book, after *domain.Book) {
			other := "other secret review"
			after.Review = &other
		}, "review"},
		{"review unavailable before", func(before *domain.Book, _ *domain.Book) { before.Review = nil }, "review"},
		{"review unavailable after", func(_ *domain.Book, after *domain.Book) { after.Review = nil }, "review"},
		{"status", func(_ *domain.Book, after *domain.Book) { after.Status = domain.StatusToRead }, "status"},
		{"rating", func(_ *domain.Book, after *domain.Book) { after.Rating++ }, "rating"},
		{"date", func(_ *domain.Book, after *domain.Book) { after.DateRead = nil }, "date_read"},
		{"shelves", func(_ *domain.Book, after *domain.Book) { after.Bookshelves = nil }, "bookshelves"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := ratingSnapshot()
			after := ratingSnapshot()
			after.Review = ptrString(wanted)
			tc.alter(&before, &after)
			result, err := VerifyReviewMutation(before, after, wanted)
			if !errors.Is(err, ErrVerificationFailed) || result.Verified || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if strings.Contains(err.Error(), wanted) || strings.Contains(err.Error(), "other secret") ||
				strings.Contains(err.Error(), *ratingSnapshot().Review) {
				t.Fatalf("verification error leaked review text: %v", err)
			}
		})
	}
}

func TestReviewReplacesUnicodeTextAndVerifiesFreshState(t *testing.T) {
	const before = "Old private text"
	const wanted = "Precise <review> & café 📚\nSecond line."
	b, action, isbn := reviewFlowBrowser(t, before, wanted)
	inputs := 0
	action.input = func(selector, value string) error {
		inputs++
		if !strings.Contains(selector, "textarea[name='review[review]']") || value != wanted {
			t.Fatalf("unexpected review input selector=%q value=%q", selector, value)
		}
		action.value = func(string) (string, error) { return wanted, nil }
		return nil
	}
	clicks := 0
	action.click = func(selector string) error {
		clicks++
		if !strings.Contains(selector, "input[type='submit'][name='next']") {
			t.Fatalf("unexpected submit selector %q", selector)
		}
		action.url = "https://www.goodreads.com/review/show/7"
		return nil
	}
	result, err := Review(context.Background(), b, isbn, ptrString(wanted))
	if err != nil || !result.Verified || result.After.Review == nil || *result.After.Review != wanted ||
		inputs != 1 || clicks != 1 {
		t.Fatalf("result=%+v err=%v inputs=%d clicks=%d calls=%v", result, err, inputs, clicks, b.calls)
	}
}

func TestReviewExplicitClearAndAlreadySatisfiedAreVerified(t *testing.T) {
	b, action, isbn := reviewFlowBrowser(t, "remove me", "")
	action.input = func(_ string, value string) error {
		if value != "" {
			t.Fatalf("clear entered %q", value)
		}
		action.value = func(string) (string, error) { return "", nil }
		return nil
	}
	action.click = func(string) error {
		action.url = "https://www.goodreads.com/review/show/7"
		return nil
	}
	result, err := Review(context.Background(), b, isbn, ptrString(""))
	if err != nil || !result.Verified || result.After.Review == nil || *result.After.Review != "" {
		t.Fatalf("clear result=%+v err=%v", result, err)
	}

	b, _, isbn = reviewFlowBrowser(t, "same text", "same text")
	result, err = Review(context.Background(), b, isbn, ptrString("same text"))
	if err != nil || !result.Verified || len(b.calls) != 3 {
		t.Fatalf("idempotent result=%+v err=%v calls=%v", result, err, b.calls)
	}
}

func TestReviewDoesNotReplayAmbiguousSubmission(t *testing.T) {
	b, action, isbn := reviewFlowBrowser(t, "old", "old")
	action.input = func(_ string, value string) error {
		action.value = func(string) (string, error) { return value, nil }
		return nil
	}
	clicks := 0
	action.click = func(string) error {
		clicks++
		return errors.New("submission result unknown")
	}
	_, err := Review(context.Background(), b, isbn, ptrString("new"))
	if !errors.Is(err, ErrMutationAmbiguous) || clicks != 1 {
		t.Fatalf("err=%v clicks=%d calls=%v", err, clicks, b.calls)
	}
}

func TestReviewRejectsNilBeforeBrowserWork(t *testing.T) {
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	b := &fakeBrowser{}
	if _, err := Review(context.Background(), b, isbn, nil); !errors.Is(err, domain.ErrInvalidReview) {
		t.Fatalf("err=%v calls=%v", err, b.calls)
	}
}

func reviewFlowBrowser(t *testing.T, beforeReview, afterReview string) (*fakeBrowser, *fakePage, domain.ISBN) {
	t.Helper()
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	const pageURL = "https://www.goodreads.com/review/list/123"
	const reviewURL = "https://www.goodreads.com/review/edit/7"
	before := libraryTestPage(ownerRatingFixture(2), pageURL)
	after := libraryTestPage(ownerRatingFixture(2), pageURL)
	reviewBefore := &fakePage{url: reviewURL, html: reviewFixture(beforeReview)}
	action := &fakePage{url: reviewURL, html: reviewFixture(beforeReview)}
	action.value = func(string) (string, error) { return beforeReview, nil }
	reviewAfter := &fakePage{url: reviewURL, html: reviewFixture(afterReview)}
	b := &fakeBrowser{
		pagesQueue: map[string][]browser.Page{
			libraryURL: {privatePage(), before, after},
			reviewURL:  {reviewBefore, action, reviewAfter},
		},
	}
	return b, action, isbn
}

func reviewFixture(review string) string {
	return `<form><textarea id="review_review_usertext" name="review[review]">` +
		html.EscapeString(review) +
		`</textarea><input type="submit" name="next" value="Save"></form>`
}

func ptrString(value string) *string {
	return &value
}
