package goodreads

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/janhelcl/goodreads-cli/internal/browser"
)

const signOutCSS = "a[href*='/user/sign_out']"

var (
	signInURL               = "https://www.goodreads.com/user/sign_in"
	libraryURL              = "https://www.goodreads.com/review/list"
	allowedGoodreadsOrigins = []string{"https://www.goodreads.com", "https://goodreads.com"}
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
	if knownRemoteFailurePath(u.Path) {
		return ConnectionStatus{}, fmt.Errorf("%w at auth.private-library", browser.ErrNetwork)
	}
	if u.Path != "/review/list" && !strings.HasPrefix(u.Path, "/review/list/") {
		return ConnectionStatus{}, fmt.Errorf("%w at auth.private-library: unexpected page", ErrCompatibility)
	}
	html, err := p.HTML(ctx)
	if err != nil {
		return ConnectionStatus{}, fmt.Errorf("auth.private-library: DOM unavailable: %w", err)
	}
	if remoteFailureDocument(html) {
		return ConnectionStatus{}, fmt.Errorf("%w at auth.private-library", browser.ErrNetwork)
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

func knownRemoteFailurePath(path string) bool {
	return path == "/error" ||
		strings.HasPrefix(path, "/error/") ||
		path == "/maintenance" ||
		strings.HasPrefix(path, "/maintenance/")
}

func remoteFailureDocument(raw string) bool {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(raw))
	if err != nil {
		return false
	}
	failed := false
	doc.Find("title,h1").EachWithBreak(func(_ int, selection *goquery.Selection) bool {
		text := strings.ToLower(strings.Join(strings.Fields(selection.Text()), " "))
		for _, marker := range []string{
			"service unavailable",
			"too many requests",
			"internal server error",
			"temporarily unavailable",
			"goodreads is over capacity",
		} {
			if strings.Contains(text, marker) {
				failed = true
				return false
			}
		}
		return true
	})
	return failed
}

func isGoodreadsPage(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host == "" {
		return false
	}
	for _, rawOrigin := range allowedGoodreadsOrigins {
		origin, err := url.Parse(rawOrigin)
		if err == nil && u.Scheme == origin.Scheme &&
			strings.EqualFold(u.Hostname(), origin.Hostname()) &&
			goodreadsPort(u) == goodreadsPort(origin) {
			return true
		}
	}
	return false
}

func goodreadsPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if u.Scheme == "https" {
		return "443"
	}
	if u.Scheme == "http" {
		return "80"
	}
	return ""
}
