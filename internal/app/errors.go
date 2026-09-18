package app

import (
	"context"
	"errors"

	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
	"github.com/janhelcl/goodreads-cli/internal/goodreads"
	"github.com/janhelcl/goodreads-cli/internal/profile"
)

// Application error aliases keep transport adapters dependent on the
// application boundary rather than concrete browser and Goodreads packages.
var (
	ErrBusy               = profile.ErrBusy
	ErrBrowserUnavailable = browser.ErrUnavailable
	ErrBrowserLaunch      = browser.ErrLaunch
	ErrNetwork            = browser.ErrNetwork
	ErrSessionExpired     = goodreads.ErrSessionExpired
	ErrLoginCancelled     = goodreads.ErrLoginCancelled
	ErrBookNotFound       = goodreads.ErrBookNotFound
	ErrBookAmbiguous      = goodreads.ErrBookAmbiguous
	ErrMutationAmbiguous  = goodreads.ErrMutationAmbiguous
	ErrPartialMutation    = domain.ErrPartialMutation
	ErrVerificationFailed = goodreads.ErrVerificationFailed
	ErrCompatibility      = goodreads.ErrCompatibility
	ErrPageLimit          = goodreads.ErrPageLimit
	ErrScanIncomplete     = goodreads.ErrScanIncomplete
	ErrExportFailed       = goodreads.ErrExportFailed
)

type ErrorKind string

const (
	ErrorInternal           ErrorKind = "internal_error"
	ErrorInvalidArguments   ErrorKind = "invalid_arguments"
	ErrorBusy               ErrorKind = "busy"
	ErrorBrowserUnavailable ErrorKind = "browser_unavailable"
	ErrorBrowserLaunch      ErrorKind = "browser_launch"
	ErrorNetwork            ErrorKind = "network"
	ErrorNotAuthenticated   ErrorKind = "not_authenticated"
	ErrorLoginCancelled     ErrorKind = "login_cancelled"
	ErrorBookNotFound       ErrorKind = "book_not_found"
	ErrorBookAmbiguous      ErrorKind = "book_ambiguous"
	ErrorMutationAmbiguous  ErrorKind = "mutation_ambiguous"
	ErrorPartialMutation    ErrorKind = "partial_mutation"
	ErrorVerificationFailed ErrorKind = "verification_failed"
	ErrorCompatibility      ErrorKind = "compatibility"
	ErrorScanIncomplete     ErrorKind = "scan_incomplete"
	ErrorExportFailed       ErrorKind = "export_failed"
	ErrorTimeout            ErrorKind = "timeout"
	ErrorCancelled          ErrorKind = "cancelled"
)

type ErrorDescriptor struct {
	Kind    ErrorKind
	Message string
}

// DescribeError is the shared safe error boundary for CLI and MCP transports.
func DescribeError(err error) ErrorDescriptor {
	switch {
	case errors.Is(err, domain.ErrInvalidISBN):
		return ErrorDescriptor{ErrorInvalidArguments, "isbn must be a valid ISBN-10 or ISBN-13."}
	case errors.Is(err, domain.ErrInvalidStatus):
		return ErrorDescriptor{ErrorInvalidArguments, "status must be to-read, currently-reading, or read."}
	case errors.Is(err, domain.ErrInvalidRating):
		return ErrorDescriptor{ErrorInvalidArguments, "rating must be 1 through 5."}
	case errors.Is(err, domain.ErrInvalidDate):
		return ErrorDescriptor{ErrorInvalidArguments, "date must be YYYY-MM-DD."}
	case errors.Is(err, domain.ErrInvalidLimit):
		return ErrorDescriptor{ErrorInvalidArguments, "limit must be 1 through 200."}
	case errors.Is(err, domain.ErrInvalidReview):
		return ErrorDescriptor{ErrorInvalidArguments, "provide a non-empty review or explicitly clear it."}
	case errors.Is(err, domain.ErrExportExists):
		return ErrorDescriptor{ErrorInvalidArguments, "Export destination already exists; use --force to replace it."}
	case errors.Is(err, domain.ErrExportDestination):
		return ErrorDescriptor{ErrorInvalidArguments, "Invalid export destination."}
	case errors.Is(err, ErrBusy):
		return ErrorDescriptor{ErrorBusy, "Goodreads browser profile is busy; retry after the other command finishes."}
	case errors.Is(err, ErrBrowserUnavailable):
		return ErrorDescriptor{ErrorBrowserUnavailable, "No supported Chrome, Chromium, or Edge browser found. Set GOODREADS_CLI_BROWSER to its executable."}
	case errors.Is(err, ErrBrowserLaunch):
		return ErrorDescriptor{ErrorBrowserLaunch, "Could not launch the dedicated browser."}
	case errors.Is(err, ErrNetwork):
		return ErrorDescriptor{ErrorNetwork, "Goodreads could not be reached; check the network and try again."}
	case errors.Is(err, ErrCompatibility):
		return ErrorDescriptor{ErrorCompatibility, "Goodreads UI changed; retry with --headed for diagnosis."}
	case errors.Is(err, ErrPageLimit):
		return ErrorDescriptor{ErrorScanIncomplete, "Library scan reached its safety budget before the requested results could be completed."}
	case errors.Is(err, ErrScanIncomplete):
		return ErrorDescriptor{ErrorScanIncomplete, "Exact-edition scan reached its safety budget before identity could be confirmed; no mutation was attempted."}
	case errors.Is(err, ErrBookNotFound):
		return ErrorDescriptor{ErrorBookNotFound, "No library entry has that exact ISBN."}
	case errors.Is(err, ErrBookAmbiguous):
		return ErrorDescriptor{ErrorBookAmbiguous, "Multiple library entries have that ISBN; exact edition is ambiguous."}
	case errors.Is(err, ErrMutationAmbiguous):
		return ErrorDescriptor{ErrorMutationAmbiguous, "Goodreads may have changed the book, but the result could not be confirmed; do not retry automatically."}
	case errors.Is(err, ErrPartialMutation):
		var partial *domain.PartialMutationError
		if errors.As(err, &partial) {
			return ErrorDescriptor{ErrorPartialMutation, partial.Error()}
		}
		return ErrorDescriptor{ErrorPartialMutation, "Goodreads mutation partially completed; do not retry automatically."}
	case errors.Is(err, ErrVerificationFailed):
		return ErrorDescriptor{ErrorVerificationFailed, "Goodreads did not match the requested change after readback."}
	case errors.Is(err, ErrSessionExpired):
		return ErrorDescriptor{ErrorNotAuthenticated, "Goodreads session expired; run gr login."}
	case errors.Is(err, ErrLoginCancelled):
		return ErrorDescriptor{ErrorLoginCancelled, "Goodreads login was cancelled or timed out."}
	case errors.Is(err, ErrExportFailed):
		return ErrorDescriptor{ErrorExportFailed, "Goodreads could not generate or download a valid export."}
	case errors.Is(err, context.DeadlineExceeded):
		return ErrorDescriptor{ErrorTimeout, "Goodreads operation timed out."}
	case errors.Is(err, context.Canceled):
		return ErrorDescriptor{ErrorCancelled, "Goodreads operation was cancelled."}
	default:
		return ErrorDescriptor{ErrorInternal, "Goodreads operation failed."}
	}
}
