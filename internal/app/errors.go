package app

import (
	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/goodreads"
	"github.com/janhelcl/goodreads-cli/internal/profile"
)

// Application error aliases keep transport adapters dependent on the
// application boundary rather than concrete browser and Goodreads packages.
var (
	ErrBusy               = profile.ErrBusy
	ErrBrowserUnavailable = browser.ErrUnavailable
	ErrBrowserLaunch      = browser.ErrLaunch
	ErrSessionExpired     = goodreads.ErrSessionExpired
	ErrBookNotFound       = goodreads.ErrBookNotFound
	ErrBookAmbiguous      = goodreads.ErrBookAmbiguous
	ErrMutationAmbiguous  = goodreads.ErrMutationAmbiguous
	ErrVerificationFailed = goodreads.ErrVerificationFailed
	ErrCompatibility      = goodreads.ErrCompatibility
	ErrPageLimit          = goodreads.ErrPageLimit
)
