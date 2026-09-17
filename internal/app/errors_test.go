package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/janhelcl/goodreads-cli/internal/domain"
)

func TestDescribeError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		kind ErrorKind
	}{
		{"invalid arguments", domain.ErrInvalidISBN, ErrorInvalidArguments},
		{"busy", ErrBusy, ErrorBusy},
		{"browser unavailable", ErrBrowserUnavailable, ErrorBrowserUnavailable},
		{"browser launch", ErrBrowserLaunch, ErrorBrowserLaunch},
		{"session", ErrSessionExpired, ErrorNotAuthenticated},
		{"login cancelled", ErrLoginCancelled, ErrorLoginCancelled},
		{"book not found", ErrBookNotFound, ErrorBookNotFound},
		{"book ambiguous", ErrBookAmbiguous, ErrorBookAmbiguous},
		{"mutation ambiguous", ErrMutationAmbiguous, ErrorMutationAmbiguous},
		{"verification", ErrVerificationFailed, ErrorVerificationFailed},
		{"compatibility", ErrCompatibility, ErrorCompatibility},
		{"page limit", ErrPageLimit, ErrorCompatibility},
		{"export", ErrExportFailed, ErrorExportFailed},
		{"timeout", context.DeadlineExceeded, ErrorTimeout},
		{"cancelled", context.Canceled, ErrorCancelled},
		{"internal", errors.New("private detail"), ErrorInternal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			description := DescribeError(test.err)
			if description.Kind != test.kind || description.Message == "" {
				t.Fatalf("description=%+v", description)
			}
			if strings.Contains(description.Message, "private detail") {
				t.Fatalf("unsafe message=%q", description.Message)
			}
		})
	}
}
