package goodreads

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
	"github.com/janhelcl/goodreads-cli/internal/profile"
)

// This test executes the production Rod wrapper and Goodreads contracts
// together against local synthetic pages. It never contacts Goodreads.
func TestRodGoodreadsFlows(t *testing.T) {
	if os.Getenv("GOODREADS_BROWSER_TESTS") != "1" {
		t.Skip("set GOODREADS_BROWSER_TESTS=1 for local Chromium Goodreads flow test")
	}

	var stateMu sync.Mutex
	rating := 2
	currentStatus := domain.StatusRead
	ambiguousRating := false
	var ratingRequests atomic.Int32
	var statusRequests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/review/list":
			if r.URL.Query().Get("page") != "2" {
				fmt.Fprint(w, exactScanFixture(1, false, 2))
				return
			}
			stateMu.Lock()
			html := rodOwnerFixture(rating, currentStatus)
			stateMu.Unlock()
			fmt.Fprint(w, html)
		case "/review/edit/7":
			fmt.Fprint(w, `<html><body><form action="/review/edit/7"><textarea id="review_review_usertext" name="review[review]">fixture review</textarea></form></body></html>`)
		case "/rating":
			ratingRequests.Add(1)
			stateMu.Lock()
			if !ambiguousRating {
				rating = 4
			}
			stateMu.Unlock()
			fmt.Fprint(w, "ok")
		case "/status":
			statusRequests.Add(1)
			stateMu.Lock()
			currentStatus = domain.ReadingStatus(r.URL.Query().Get("value"))
			stateMu.Unlock()
			fmt.Fprint(w, "ok")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	originalSignIn, originalLibrary := signInURL, libraryURL
	originalOrigins := append([]string(nil), allowedGoodreadsOrigins...)
	signInURL = server.URL + "/user/sign_in"
	libraryURL = server.URL + "/review/list"
	allowedGoodreadsOrigins = []string{server.URL}
	originalRatingCompletionTimeout := ratingCompletionTimeout
	ratingCompletionTimeout = 250 * time.Millisecond
	t.Cleanup(func() {
		signInURL = originalSignIn
		libraryURL = originalLibrary
		allowedGoodreadsOrigins = originalOrigins
		ratingCompletionTimeout = originalRatingCompletionTimeout
	})

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	paths := profile.PathsForRoot(filepath.Join(t.TempDir(), "app"))
	if err := paths.EnsureBrowser(); err != nil {
		t.Fatal(err)
	}
	origin, _ := url.Parse(server.URL)
	b, err := (browser.RodFactory{}).Launch(ctx, browser.LaunchOptions{
		ProfileDir:     paths.Browser,
		Headless:       true,
		AllowedOrigins: []string{origin.Scheme + "://" + origin.Host},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	isbn, _ := domain.NormalizeISBN("9780306406157")
	books, err := Library(ctx, b, domain.LibraryFilter{Limit: 2})
	if err != nil || len(books) != 2 {
		t.Fatalf("paginated library books=%d err=%v", len(books), err)
	}
	book, err := Get(ctx, b, isbn)
	if err != nil || book.BookID != "42" {
		t.Fatalf("exact book=%+v err=%v", book, err)
	}

	result, err := Rate(ctx, b, isbn, 4)
	if err != nil || !result.Verified || result.After.Rating != 4 || ratingRequests.Load() != 1 {
		t.Fatalf("rating result=%+v err=%v requests=%d", result, err, ratingRequests.Load())
	}

	stateMu.Lock()
	rating = 2
	ambiguousRating = true
	stateMu.Unlock()
	_, err = Rate(ctx, b, isbn, 4)
	if !errors.Is(err, ErrMutationAmbiguous) || ratingRequests.Load() != 2 {
		t.Fatalf("ambiguous rating err=%v requests=%d", err, ratingRequests.Load())
	}

	stateMu.Lock()
	ambiguousRating = false
	currentStatus = domain.StatusCurrentlyReading
	stateMu.Unlock()
	stepFailure := errors.New("injected date failure")
	_, err = finishWithOperations(ctx, b, isbn, time.Now(), nil, finishOperations{
		setStatus: setStatus,
		setDate: func(context.Context, browser.Browser, domain.ISBN, time.Time) (domain.MutationResult, error) {
			return domain.MutationResult{}, stepFailure
		},
		rate:      Rate,
		reconcile: reconcileCompoundFailure,
	})
	var partial *domain.PartialMutationError
	if !errors.As(err, &partial) ||
		!equalStrings(partial.Completed, []string{"status"}) ||
		partial.Failed != "date" ||
		partial.Observed.Status != domain.StatusRead ||
		statusRequests.Load() != 1 {
		t.Fatalf("partial=%+v err=%v status requests=%d", partial, err, statusRequests.Load())
	}
}

func rodOwnerFixture(rating int, status domain.ReadingStatus) string {
	html := ownerStatusFixture(status, false)
	html = strings.Replace(
		html,
		`data-rating="2.0"`,
		fmt.Sprintf(`data-rating="%d.0"`, rating),
		1,
	)
	for value, title := range ratingTitles {
		class := "off"
		if value <= rating {
			class = "on"
		}
		old := fmt.Sprintf(`<a class="star off" title="%s" href="#"></a>`, title)
		replacement := fmt.Sprintf(`<a class="star %s" title="%s" href="#" onclick="var request=new XMLHttpRequest(); request.open('POST','/rating',false); request.send(); location.reload(); return false">★</a>`, class, title)
		html = strings.Replace(html, old, replacement, 1)
	}
	html = strings.Replace(
		html,
		`<a class="shelfChooserLink" href="#">edit</a>`,
		`<a class="shelfChooserLink" href="#" onclick="document.querySelector('.shelfChooserWrapper').classList.add('open'); return false">edit</a>`,
		1,
	)
	html = strings.Replace(
		html,
		`<span>read</span>`,
		`<span onclick="fetch('/status?value=read', {method:'POST'}).then(() => location.reload())">read</span>`,
		1,
	)
	return html
}
