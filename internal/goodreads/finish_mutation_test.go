package goodreads

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
)

func TestVerifyFinishMutation(t *testing.T) {
	before := ratingSnapshot()
	before.Status = domain.StatusCurrentlyReading
	before.DateRead = nil
	after := ratingSnapshot()
	date := "2026-09-17"
	rating := 4
	after.DateRead = &date
	after.Rating = rating

	result, err := VerifyFinishMutation(before, after, date, &rating)
	if err != nil || !result.Verified || result.Operation != "finish" ||
		result.Changes.Status == nil || *result.Changes.Status != domain.StatusRead ||
		result.Changes.DateRead == nil || *result.Changes.DateRead != date ||
		result.Changes.Rating == nil || *result.Changes.Rating != rating {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestFinishRejectsInvalidArgumentsBeforeBrowserWork(t *testing.T) {
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	b := &fakeBrowser{}
	if _, err := Finish(context.Background(), b, isbn, time.Time{}, nil); !errors.Is(err, domain.ErrInvalidDate) {
		t.Fatalf("zero date err=%v calls=%v", err, b.calls)
	}
	rating := 6
	if _, err := Finish(context.Background(), b, isbn, time.Now(), &rating); !errors.Is(err, domain.ErrInvalidRating) {
		t.Fatalf("rating err=%v calls=%v", err, b.calls)
	}
}

func TestVerifyFinishMutationPreservesRatingWhenOmitted(t *testing.T) {
	before := ratingSnapshot()
	after := ratingSnapshot()
	date := "2026-09-17"
	after.DateRead = &date
	result, err := VerifyFinishMutation(before, after, date, nil)
	if err != nil || !result.Verified || result.Changes.Rating != nil {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	after.Rating++
	if _, err := VerifyFinishMutation(before, after, date, nil); !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("rating change accepted: %v", err)
	}
}

func TestVerifyFinishMutationFailsClosed(t *testing.T) {
	date := "2026-09-17"
	for _, tc := range []struct {
		name  string
		alter func(*domain.Book)
		field string
	}{
		{"status", func(after *domain.Book) { after.Status = domain.StatusCurrentlyReading }, "status"},
		{"date", func(after *domain.Book) { after.DateRead = nil }, "date_read"},
		{"shelves", func(after *domain.Book) { after.Bookshelves = nil }, "bookshelves"},
		{"review", func(after *domain.Book) {
			changed := "different private text"
			after.Review = &changed
		}, "review"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := ratingSnapshot()
			after := ratingSnapshot()
			after.DateRead = &date
			tc.alter(&after)
			result, err := VerifyFinishMutation(before, after, date, nil)
			if !errors.Is(err, ErrVerificationFailed) || result.Verified || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "different") {
				t.Fatalf("verification error leaked review text: %v", err)
			}
		})
	}
}

func TestFinishAlreadySatisfiedDoesNotMutate(t *testing.T) {
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	date, err := time.Parse("2006-01-02", "2026-09-12")
	if err != nil {
		t.Fatal(err)
	}
	const pageURL = "https://www.goodreads.com/review/list/123"
	const reviewURL = "https://www.goodreads.com/review/edit/7"
	owner := func() browser.Page {
		return libraryTestPage(ownerStatusFixture(domain.StatusRead, false), pageURL)
	}
	action := owner().(*fakePage)
	action.click = func(string) error {
		t.Fatal("already-satisfied finish clicked a mutation control")
		return nil
	}
	reviewPage := &fakePage{
		url:  reviewURL,
		html: `<form><textarea id="review_review_usertext" name="review[review]"></textarea></form>`,
	}
	b := &fakeBrowser{
		pages: map[string]browser.Page{
			pageURL:   action,
			reviewURL: reviewPage,
		},
		pagesQueue: map[string][]browser.Page{
			libraryURL: {privatePage(), owner(), privatePage(), owner()},
		},
	}
	result, err := Finish(context.Background(), b, isbn, date, nil)
	if err != nil || !result.Verified || result.Operation != "finish" {
		t.Fatalf("result=%+v err=%v calls=%v", result, err, b.calls)
	}
}
