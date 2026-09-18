//go:build liveprobe

package goodreads

import (
	"context"
	"errors"

	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
)

// ReconcilePartialMutationLiveProbe is available only to the explicitly
// confirmed live canary. It injects a failure after a separately verified
// semantic step and performs the production final-state reconciliation.
func ReconcilePartialMutationLiveProbe(
	ctx context.Context,
	b browser.Browser,
	isbn domain.ISBN,
	operation string,
	completed []string,
	failed string,
) error {
	return reconcileCompoundFailure(
		ctx,
		b,
		isbn,
		operation,
		completed,
		failed,
		errors.New("injected live canary failure"),
	)
}
