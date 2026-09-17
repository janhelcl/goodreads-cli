//go:build liveprobe

package goodreads

import (
	"context"

	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
)

// ReviewSnapshotForLiveProbe captures the exact target and its complete review
// for reversible compatibility probes. Callers must not print the returned
// review or persist it outside the process.
func ReviewSnapshotForLiveProbe(
	ctx context.Context,
	b browser.Browser,
	isbn domain.ISBN,
) (domain.Book, error) {
	state, err := Status(ctx, b)
	if err != nil {
		return domain.Book{}, err
	}
	if !state.Connected {
		return domain.Book{}, ErrSessionExpired
	}
	candidate, err := findMutationCandidate(ctx, b, isbn, reviewMutationStage, false)
	if err != nil {
		return domain.Book{}, err
	}
	book := candidate.Book
	book.Review, err = loadFullReview(ctx, b, candidate.ReviewURL, reviewMutationStage)
	if err != nil {
		return domain.Book{}, err
	}
	return book, nil
}
