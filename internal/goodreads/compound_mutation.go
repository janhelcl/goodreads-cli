package goodreads

import (
	"context"
	"errors"
	"fmt"

	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
)

func reconcileCompoundFailure(
	ctx context.Context,
	b browser.Browser,
	isbn domain.ISBN,
	operation string,
	completed []string,
	failed string,
	cause error,
) error {
	if len(completed) == 0 {
		return cause
	}
	candidate, err := resolveOwnedEdition(ctx, b, isbn, "", false, false)
	if err != nil {
		return fmt.Errorf("%w at mutation.verify: final state unavailable", ErrMutationAmbiguous)
	}
	rating := candidate.Book.Rating
	observed := domain.ObservedMutationState{
		Status: candidate.Book.Status,
		Rating: &rating,
	}
	if candidate.Book.DateRead != nil {
		date := *candidate.Book.DateRead
		observed.DateRead = &date
	}
	return &domain.PartialMutationError{
		Operation:          operation,
		Completed:          append([]string(nil), completed...),
		Failed:             failed,
		Observed:           observed,
		RetryAutomatically: false,
	}
}

func statusChanged(result domain.MutationResult) bool {
	return result.Before.Status != result.After.Status
}

func dateChanged(result domain.MutationResult) bool {
	return !equalOptionalString(result.Before.DateRead, result.After.DateRead)
}

func ratingChanged(result domain.MutationResult) bool {
	return result.Before.Rating != result.After.Rating
}

func prependPartialStep(err error, step string) error {
	var partial *domain.PartialMutationError
	if !errors.As(err, &partial) {
		return err
	}
	copyOfError := *partial
	copyOfError.Completed = append([]string{step}, partial.Completed...)
	return &copyOfError
}
