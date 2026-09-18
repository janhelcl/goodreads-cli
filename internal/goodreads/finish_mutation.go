package goodreads

import (
	"context"
	"time"

	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
)

// Finish moves one exact edition to read, sets its requested finish date, and
// optionally rates it. Each UI action is independently read back before the
// final preservation check.
func Finish(
	ctx context.Context,
	b browser.Browser,
	isbn domain.ISBN,
	date time.Time,
	rating *int,
) (domain.MutationResult, error) {
	if err := domain.ValidateDate(date); err != nil {
		return domain.MutationResult{}, err
	}
	if rating != nil {
		if err := domain.ValidateRating(*rating); err != nil {
			return domain.MutationResult{}, err
		}
	}
	return finishWithOperations(ctx, b, isbn, date, rating, finishOperations{
		setStatus: setStatus,
		setDate:   SetFinishDate,
		rate:      Rate,
		reconcile: reconcileCompoundFailure,
	})
}

type finishOperations struct {
	setStatus func(context.Context, browser.Browser, domain.ISBN, domain.ReadingStatus, bool) (domain.MutationResult, error)
	setDate   func(context.Context, browser.Browser, domain.ISBN, time.Time) (domain.MutationResult, error)
	rate      func(context.Context, browser.Browser, domain.ISBN, int) (domain.MutationResult, error)
	reconcile func(context.Context, browser.Browser, domain.ISBN, string, []string, string, error) error
}

func finishWithOperations(
	ctx context.Context,
	b browser.Browser,
	isbn domain.ISBN,
	date time.Time,
	rating *int,
	operations finishOperations,
) (domain.MutationResult, error) {
	completed := []string{}
	statusResult, err := operations.setStatus(ctx, b, isbn, domain.StatusRead, true)
	if err != nil {
		return domain.MutationResult{}, err
	}
	if statusChanged(statusResult) {
		completed = append(completed, "status")
	}
	before := statusResult.Before

	dateResult, err := operations.setDate(ctx, b, isbn, date)
	if err != nil {
		return domain.MutationResult{}, operations.reconcile(ctx, b, isbn, "finish", completed, "date", err)
	}
	if dateChanged(dateResult) {
		completed = append(completed, "date")
	}
	after := dateResult.After
	if rating != nil {
		ratingResult, err := operations.rate(ctx, b, isbn, *rating)
		if err != nil {
			return domain.MutationResult{}, operations.reconcile(ctx, b, isbn, "finish", completed, "rating", err)
		}
		if ratingChanged(ratingResult) {
			completed = append(completed, "rating")
		}
		after = ratingResult.After
	}
	result, err := VerifyFinishMutation(before, after, date.Format("2006-01-02"), rating)
	if err != nil {
		return domain.MutationResult{}, operations.reconcile(ctx, b, isbn, "finish", completed, "verify", err)
	}
	return result, nil
}

func VerifyFinishMutation(
	before domain.Book,
	after domain.Book,
	date string,
	rating *int,
) (domain.MutationResult, error) {
	if rating != nil {
		if err := domain.ValidateRating(*rating); err != nil {
			return domain.MutationResult{}, err
		}
	}
	if before.BookID == "" || after.BookID != before.BookID {
		return domain.MutationResult{}, verificationError("book_id", "changed")
	}
	if before.ISBN10 != "" && after.ISBN10 != before.ISBN10 {
		return domain.MutationResult{}, verificationError("isbn10", "changed")
	}
	if before.ISBN13 != "" && after.ISBN13 != before.ISBN13 {
		return domain.MutationResult{}, verificationError("isbn13", "changed")
	}
	if after.Status != domain.StatusRead {
		return domain.MutationResult{}, verificationError("status", "did not match")
	}
	if after.DateRead == nil || *after.DateRead != date {
		return domain.MutationResult{}, verificationError("date_read", "did not match")
	}
	if rating == nil {
		if after.Rating != before.Rating {
			return domain.MutationResult{}, verificationError("rating", "changed")
		}
	} else if after.Rating != *rating {
		return domain.MutationResult{}, verificationError("rating", "did not match")
	}
	if !equalStringSet(after.Bookshelves, before.Bookshelves) {
		return domain.MutationResult{}, verificationError("bookshelves", "changed")
	}
	if before.Review == nil || after.Review == nil {
		return domain.MutationResult{}, verificationError("review", "was unavailable")
	}
	if *after.Review != *before.Review {
		return domain.MutationResult{}, verificationError("review", "changed")
	}
	status := domain.StatusRead
	changes := domain.BookUpdate{Status: &status, DateRead: &date, Rating: rating}
	return domain.MutationResult{
		Operation: "finish",
		Before:    before,
		After:     after,
		Changes:   changes,
		Verified:  true,
	}, nil
}
