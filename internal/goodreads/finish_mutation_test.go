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
			libraryURL: {owner(), owner()},
		},
	}
	result, err := Finish(context.Background(), b, isbn, date, nil)
	if err != nil || !result.Verified || result.Operation != "finish" {
		t.Fatalf("result=%+v err=%v calls=%v", result, err, b.calls)
	}
}

func TestFinishReconcilesFailuresAfterVerifiedSteps(t *testing.T) {
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	date, _ := time.Parse("2006-01-02", "2026-09-18")
	rating := 4
	before := ratingSnapshot()
	before.Status = domain.StatusCurrentlyReading
	before.DateRead = nil
	afterStatus := before
	afterStatus.Status = domain.StatusRead
	afterDate := afterStatus
	dateText := date.Format("2006-01-02")
	afterDate.DateRead = &dateText
	stepFailure := errors.New("injected failure")
	partialFailure := errors.New("reconciled partial failure")

	for _, test := range []struct {
		name          string
		failStatus    bool
		failDate      bool
		failRating    bool
		wantCompleted []string
		wantFailed    string
		wantReconcile int
	}{
		{"before any write", true, false, false, nil, "", 0},
		{"after status", false, true, false, []string{"status"}, "date", 1},
		{"after date", false, false, true, []string{"status", "date"}, "rating", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			reconcileCalls := 0
			operations := finishOperations{
				setStatus: func(context.Context, browser.Browser, domain.ISBN, domain.ReadingStatus, bool) (domain.MutationResult, error) {
					if test.failStatus {
						return domain.MutationResult{}, stepFailure
					}
					return domain.MutationResult{Before: before, After: afterStatus, Verified: true}, nil
				},
				setDate: func(context.Context, browser.Browser, domain.ISBN, time.Time) (domain.MutationResult, error) {
					if test.failDate {
						return domain.MutationResult{}, stepFailure
					}
					return domain.MutationResult{Before: afterStatus, After: afterDate, Verified: true}, nil
				},
				rate: func(context.Context, browser.Browser, domain.ISBN, int) (domain.MutationResult, error) {
					if test.failRating {
						return domain.MutationResult{}, stepFailure
					}
					afterRating := afterDate
					afterRating.Rating = rating
					return domain.MutationResult{Before: afterDate, After: afterRating, Verified: true}, nil
				},
				reconcile: func(_ context.Context, _ browser.Browser, _ domain.ISBN, operation string, completed []string, failed string, cause error) error {
					reconcileCalls++
					if operation != "finish" || !equalStrings(completed, test.wantCompleted) ||
						failed != test.wantFailed || !errors.Is(cause, stepFailure) {
						t.Fatalf("operation=%q completed=%v failed=%q cause=%v", operation, completed, failed, cause)
					}
					return partialFailure
				},
			}
			_, gotErr := finishWithOperations(context.Background(), &fakeBrowser{}, isbn, date, &rating, operations)
			if test.wantReconcile == 0 {
				if !errors.Is(gotErr, stepFailure) {
					t.Fatalf("error=%v", gotErr)
				}
			} else if !errors.Is(gotErr, partialFailure) {
				t.Fatalf("error=%v", gotErr)
			}
			if reconcileCalls != test.wantReconcile {
				t.Fatalf("reconcile calls=%d", reconcileCalls)
			}
		})
	}
}

func TestFinishOperationsSucceedWithoutReconciliation(t *testing.T) {
	isbn, _ := domain.NormalizeISBN("9780306406157")
	date, _ := time.Parse("2006-01-02", "2026-09-18")
	rating := 4
	before := ratingSnapshot()
	before.Status = domain.StatusCurrentlyReading
	before.DateRead = nil
	afterStatus := before
	afterStatus.Status = domain.StatusRead
	afterDate := afterStatus
	dateText := date.Format("2006-01-02")
	afterDate.DateRead = &dateText
	afterRating := afterDate
	afterRating.Rating = rating
	operations := finishOperations{
		setStatus: func(context.Context, browser.Browser, domain.ISBN, domain.ReadingStatus, bool) (domain.MutationResult, error) {
			return domain.MutationResult{Before: before, After: afterStatus, Verified: true}, nil
		},
		setDate: func(context.Context, browser.Browser, domain.ISBN, time.Time) (domain.MutationResult, error) {
			return domain.MutationResult{Before: afterStatus, After: afterDate, Verified: true}, nil
		},
		rate: func(context.Context, browser.Browser, domain.ISBN, int) (domain.MutationResult, error) {
			return domain.MutationResult{Before: afterDate, After: afterRating, Verified: true}, nil
		},
		reconcile: func(context.Context, browser.Browser, domain.ISBN, string, []string, string, error) error {
			t.Fatal("successful finish attempted reconciliation")
			return nil
		},
	}
	result, err := finishWithOperations(context.Background(), &fakeBrowser{}, isbn, date, &rating, operations)
	if err != nil || !result.Verified {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestCompoundFailurePerformsOneSafeFinalReadback(t *testing.T) {
	isbn, _ := domain.NormalizeISBN("9780306406157")
	const pageURL = "https://www.goodreads.com/review/list/123"
	b := &fakeBrowser{pages: map[string]browser.Page{
		libraryURL: libraryTestPage(exactScanFixture(1, true, 0), pageURL),
	}}
	err := reconcileCompoundFailure(
		context.Background(), b, isbn, "finish", []string{"status", "date"}, "rating", errors.New("private cause"),
	)
	var partial *domain.PartialMutationError
	if !errors.As(err, &partial) || len(b.calls) != 1 ||
		!equalStrings(partial.Completed, []string{"status", "date"}) ||
		partial.Failed != "rating" || partial.Observed.Status != domain.StatusRead ||
		partial.Observed.Rating == nil || *partial.Observed.Rating != 2 ||
		partial.RetryAutomatically {
		t.Fatalf("partial=%+v err=%v calls=%v", partial, err, b.calls)
	}

	failed := &fakeBrowser{pages: map[string]browser.Page{
		libraryURL: libraryTestPage("", pageURL),
	}}
	err = reconcileCompoundFailure(
		context.Background(), failed, isbn, "finish", []string{"status"}, "date", errors.New("private cause"),
	)
	if !errors.Is(err, ErrMutationAmbiguous) || len(failed.calls) != 1 {
		t.Fatalf("failed reconciliation err=%v calls=%v", err, failed.calls)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
