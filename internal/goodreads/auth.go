package goodreads

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/janhelcl/goodreads-cli/internal/browser"
)

const (
	signInURL  = "https://www.goodreads.com/user/sign_in"
	libraryURL = "https://www.goodreads.com/review/list"
	signOutCSS = "a[href*='/user/sign_out']"
)

var (
	ErrCompatibility  = errors.New("Goodreads UI changed")
	ErrSessionExpired = errors.New("Goodreads session expired")
	ErrLoginCancelled = errors.New("Goodreads login was cancelled or timed out")
)

type ConnectionStatus struct {
	Connected    bool `json:"connected"`
	SessionValid bool `json:"session_valid"`
}

// Login waits for the user to complete sign-in in the headed browser. It never
// reads or interacts with credential fields, then validates a private page.
func Login(ctx context.Context, b browser.Browser) (ConnectionStatus, error) {
	// A saved session can redirect the sign-in URL to a home page whose header
	// differs from the private library. Validate the private page first.
	state, err := Status(ctx, b)
	if err != nil && !errors.Is(err, ErrSessionExpired) {
		return ConnectionStatus{}, err
	}
	if state.Connected {
		return state, nil
	}
	p, err := b.NewPage(ctx, signInURL)
	if err != nil {
		return ConnectionStatus{}, fmt.Errorf("auth.page: %w", err)
	}
	defer p.Close()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	lastCheckedURL := ""
	lastCheck := time.Time{}
	accountSeen := false
	for {
		if ctx.Err() != nil {
			return ConnectionStatus{}, ErrLoginCancelled
		}
		raw, err := p.URL(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ConnectionStatus{}, ErrLoginCancelled
			}
			return ConnectionStatus{}, fmt.Errorf("auth.page: browser page unavailable: %w", err)
		}
		if isGoodreadsPage(raw) {
			found, err := p.Has(ctx, signOutCSS)
			if err != nil {
				if ctx.Err() != nil {
					return ConnectionStatus{}, ErrLoginCancelled
				}
				return ConnectionStatus{}, fmt.Errorf("auth.page: account marker unavailable: %w", err)
			}
			u, _ := url.Parse(raw)
			if !strings.HasPrefix(u.Path, "/user/sign_in") &&
				(raw != lastCheckedURL || time.Since(lastCheck) >= 5*time.Second || (found && !accountSeen)) {
				state, err := Status(ctx, b)
				if err != nil {
					return ConnectionStatus{}, err
				}
				if state.Connected {
					return state, nil
				}
				lastCheckedURL = raw
				lastCheck = time.Now()
			}
			accountSeen = found
		}
		select {
		case <-ctx.Done():
			return ConnectionStatus{}, ErrLoginCancelled
		case <-ticker.C:
		}
	}
}

// Status visits the private library during this invocation. A sign-in redirect
// is a disconnected result; an unexpected Goodreads page is compatibility drift.
func Status(ctx context.Context, b browser.Browser) (ConnectionStatus, error) {
	p, err := b.NewPage(ctx, libraryURL)
	if errors.Is(err, browser.ErrOrigin) {
		return ConnectionStatus{}, ErrSessionExpired
	}
	if err != nil {
		return ConnectionStatus{}, fmt.Errorf("auth.private-library: %w", err)
	}
	defer p.Close()
	raw, err := p.URL(ctx)
	if err != nil {
		return ConnectionStatus{}, fmt.Errorf("auth.private-library: page unavailable: %w", err)
	}
	u, err := url.Parse(raw)
	if err != nil || !isGoodreadsPage(raw) {
		return ConnectionStatus{}, fmt.Errorf("%w at auth.private-library: unexpected origin", ErrCompatibility)
	}
	if strings.HasPrefix(u.Path, "/user/sign_in") {
		return ConnectionStatus{}, nil
	}
	if u.Path != "/review/list" && !strings.HasPrefix(u.Path, "/review/list/") {
		return ConnectionStatus{}, fmt.Errorf("%w at auth.private-library: unexpected page", ErrCompatibility)
	}
	heading, err := p.HasText(ctx, "h1", "^My Books$")
	if err != nil {
		return ConnectionStatus{}, fmt.Errorf("auth.private-library: heading check failed: %w", err)
	}
	account, err := p.Has(ctx, signOutCSS)
	if err != nil {
		return ConnectionStatus{}, fmt.Errorf("auth.private-library: account check failed: %w", err)
	}
	table, err := p.Has(ctx, "#books")
	if err != nil {
		return ConnectionStatus{}, fmt.Errorf("auth.private-library: table check failed: %w", err)
	}
	body, err := p.Has(ctx, "#booksBody")
	if err != nil {
		return ConnectionStatus{}, fmt.Errorf("auth.private-library: body check failed: %w", err)
	}
	if !heading || !account || !table || !body {
		return ConnectionStatus{}, fmt.Errorf("%w at auth.private-library: required markers missing", ErrCompatibility)
	}
	return ConnectionStatus{Connected: true, SessionValid: true}, nil
}

func isGoodreadsPage(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && (u.Hostname() == "www.goodreads.com" || u.Hostname() == "goodreads.com") && u.User == nil && (u.Port() == "" || u.Port() == "443")
}
