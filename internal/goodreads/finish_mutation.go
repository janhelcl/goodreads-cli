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
	statusResult, err := setStatus(ctx, b, isbn, domain.StatusRead, true)
	if err != nil {
		return domain.MutationResult{}, err
	}
	before := statusResult.Before

	dateResult, err := SetFinishDate(ctx, b, isbn, date)
	if err != nil {
		return domain.MutationResult{}, err
	}
	after := dateResult.After
	if rating != nil {
		ratingResult, err := Rate(ctx, b, isbn, *rating)
		if err != nil {
			return domain.MutationResult{}, err
		}
		after = ratingResult.After
	}
	return VerifyFinishMutation(before, after, date.Format("2006-01-02"), rating)
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
