package goodreads

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
)

const emptyOwnerLibrary = `<html><body><h1>My Books</h1><a href="/user/sign_out">Sign out</a>
<table id="books"><thead><tr><th class="field title">title</th><th class="field author">author</th></tr></thead>
<tbody id="booksBody"></tbody></table></body></html>`

func TestTextHasExactISBN(t *testing.T) {
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{
		"9780306406157 (ISBN10: 0306406152)",
		"ISBN 0-306-40615-2",
	} {
		if !textHasExactISBN(text, isbn) {
			t.Fatalf("exact ISBN not found in %q", text)
		}
	}
	if textHasExactISBN("9781603580557", isbn) {
		t.Fatal("mismatched ISBN accepted")
	}
}

func TestResolveExactBookUsesVisibleDetails(t *testing.T) {
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	searchURL := "https://www.goodreads.com/search?q=9780306406157&search_type=books"
	bookURL := "https://www.goodreads.com/book/show/42.Invented_Book"
	search := &fakePage{url: searchURL, html: addSearchFixture("/book/show/42.Invented_Book")}
	book := &fakePage{url: bookURL, html: addBookFixture(false)}
	clicks := 0
	book.click = func(selector string) error {
		clicks++
		if !strings.Contains(selector, "BookPageMetadataSection") {
			t.Fatalf("unexpected selector %q", selector)
		}
		book.html = addBookFixture(true)
		return nil
	}
	b := &fakeBrowser{pages: map[string]browser.Page{
		searchURL: search,
		bookURL:   book,
	}}
	resolved, page, err := resolveExactBook(context.Background(), b, isbn)
	if err != nil || resolved.BookID != "42" || resolved.URL != bookURL || page != book || clicks != 1 {
		t.Fatalf("resolved=%+v page=%T err=%v clicks=%d calls=%v", resolved, page, err, clicks, b.calls)
	}
}

func TestResolveExactBookRejectsAmbiguousSearch(t *testing.T) {
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	searchURL := "https://www.goodreads.com/search?q=9780306406157&search_type=books"
	search := &fakePage{
		url: searchURL,
		html: addSearchFixture("/book/show/42.Invented_Book") +
			`<a href="/book/show/43.Other_Edition">other</a>`,
	}
	b := &fakeBrowser{pages: map[string]browser.Page{searchURL: search}}
	if _, _, err := resolveExactBook(context.Background(), b, isbn); !errors.Is(err, ErrBookAmbiguous) {
		t.Fatalf("err=%v calls=%v", err, b.calls)
	}
}

func TestResolveExactBookRequiresWantToRead(t *testing.T) {
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	searchURL := "https://www.goodreads.com/search?q=9780306406157&search_type=books"
	bookURL := "https://www.goodreads.com/book/show/42.Invented_Book"
	b := &fakeBrowser{pages: map[string]browser.Page{
		searchURL: &fakePage{url: searchURL, html: addSearchFixture("/book/show/42.Invented_Book")},
		bookURL:   &fakePage{url: bookURL, html: ownedBookFixture(true)},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if _, _, err := resolveExactBook(ctx, b, isbn); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("add path accepted a book page without Want to Read: %v", err)
	}
}

func TestLookupPublicBookIDAcceptsOwnedBookPage(t *testing.T) {
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	searchURL := "https://www.goodreads.com/search?q=9780306406157&search_type=books"
	bookURL := "https://www.goodreads.com/book/show/42.Invented_Book"
	b := &fakeBrowser{pages: map[string]browser.Page{
		searchURL: &fakePage{url: searchURL, html: addSearchFixture("/book/show/42.Invented_Book")},
		bookURL:   &fakePage{url: bookURL, html: ownedBookFixture(true)},
	}}
	bookID, err := lookupPublicBookID(context.Background(), b, isbn)
	if err != nil || bookID != "42" {
		t.Fatalf("bookID=%q err=%v calls=%v", bookID, err, b.calls)
	}
}

func TestResolvePublicBookReportsEmptySearch(t *testing.T) {
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	searchURL := "https://www.goodreads.com/search?q=9780306406157&search_type=books"
	b := &fakeBrowser{pages: map[string]browser.Page{
		searchURL: &fakePage{url: searchURL, html: emptyPublicSearch},
	}}
	if _, _, err := resolvePublicBook(context.Background(), b, isbn, false); !errors.Is(err, ErrBookNotFound) {
		t.Fatalf("empty search=%v calls=%v", err, b.calls)
	}
}

func TestResolvePublicBookPreservesSearchTimeout(t *testing.T) {
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	searchURL := "https://www.goodreads.com/search?q=9780306406157&search_type=books"
	b := &fakeBrowser{pages: map[string]browser.Page{
		searchURL: &fakePage{url: searchURL, html: `<html><body><p>loading</p></body></html>`},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	_, _, err = resolvePublicBook(ctx, b, isbn, false)
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrCompatibility) {
		t.Fatalf("search timeout remapped: %v", err)
	}
}

func TestAddNewEditionClicksOnceAndVerifiesFreshOwnerRow(t *testing.T) {
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	searchURL := "https://www.goodreads.com/search?q=9780306406157&search_type=books"
	bookURL := "https://www.goodreads.com/book/show/42.Invented_Book"
	search := &fakePage{url: searchURL, html: addSearchFixture("/book/show/42.Invented_Book")}
	book := &fakePage{url: bookURL, html: addBookFixture(true)}
	clicks := 0
	book.click = func(selector string) error {
		clicks++
		if !strings.Contains(selector, "Button--wtr") {
			t.Fatalf("unexpected add selector %q", selector)
		}
		return nil
	}
	empty := libraryTestPage(emptyOwnerLibrary, "https://www.goodreads.com/review/list/123")
	added := libraryTestPage(ownerStatusFixture(domain.StatusToRead, false), "https://www.goodreads.com/review/list/123")
	review := &fakePage{
		url:  "https://www.goodreads.com/review/edit/7",
		html: `<form><textarea id="review_review_usertext" name="review[review]"></textarea></form>`,
	}
	b := &fakeBrowser{
		pages: map[string]browser.Page{
			searchURL: search,
			bookURL:   book,
			"https://www.goodreads.com/review/edit/7": review,
		},
		pagesQueue: map[string][]browser.Page{
			libraryURL: {empty, empty, added},
		},
	}
	result, err := Add(context.Background(), b, isbn, domain.StatusToRead)
	if err != nil || !result.Verified || result.Operation != "add" ||
		result.After.BookID != "42" || result.After.Status != domain.StatusToRead || clicks != 1 {
		t.Fatalf("result=%+v err=%v clicks=%d calls=%v", result, err, clicks, b.calls)
	}
}

func TestAddExistingEditionUsesOwnerISBNWithoutPublicResolution(t *testing.T) {
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	const pageURL = "https://www.goodreads.com/review/list/123"
	const reviewURL = "https://www.goodreads.com/review/edit/7"
	owner := func() browser.Page {
		return libraryTestPage(ownerStatusFixture(domain.StatusRead, false), pageURL)
	}
	action := owner().(*fakePage)
	action.click = func(selector string) error {
		t.Fatalf("idempotent add clicked %q", selector)
		return nil
	}
	review := &fakePage{
		url:  reviewURL,
		html: `<form><textarea id="review_review_usertext" name="review[review]"></textarea></form>`,
	}
	b := &fakeBrowser{
		pages: map[string]browser.Page{
			pageURL:   action,
			reviewURL: review,
		},
		pagesQueue: map[string][]browser.Page{
			libraryURL: {owner(), owner()},
		},
	}
	result, err := Add(context.Background(), b, isbn, domain.StatusRead)
	if err != nil || !result.Verified || result.Operation != "add" {
		t.Fatalf("result=%+v err=%v calls=%v", result, err, b.calls)
	}
	for _, call := range b.calls {
		if strings.Contains(call, "/search") || strings.Contains(call, "/book/show/") {
			t.Fatalf("existing exact owner row triggered public resolution: %v", b.calls)
		}
	}
}

func TestAddUsesResolvedBookIDWhenOwnerRowHasNoISBN(t *testing.T) {
	isbn, _ := domain.NormalizeISBN("9780306406157")
	searchURL := "https://www.goodreads.com/search?q=9780306406157&search_type=books"
	bookURL := "https://www.goodreads.com/book/show/42.Invented_Book"
	search := &fakePage{url: searchURL, html: addSearchFixture("/book/show/42.Invented_Book")}
	book := &fakePage{url: bookURL, html: addBookFixture(true)}
	book.click = func(string) error {
		t.Fatal("existing unidentified owner row was added again")
		return nil
	}
	unknown := ownerStatusFixture(domain.StatusRead, false)
	unknown = strings.Replace(unknown, "0-306-40615-2", "", 1)
	unknown = strings.Replace(unknown, "9780306406157", "", 1)
	owner := func() browser.Page {
		return libraryTestPage(unknown, "https://www.goodreads.com/review/list/123")
	}
	b := &fakeBrowser{
		pages: map[string]browser.Page{
			searchURL: search,
			bookURL:   book,
		},
		pagesQueue: map[string][]browser.Page{
			libraryURL: {owner(), owner()},
		},
	}
	if _, err := Add(context.Background(), b, isbn, domain.StatusToRead); !errors.Is(err, ErrCompatibility) {
		t.Fatalf("unidentified existing edition was not rejected safely: %v", err)
	}
}

func TestAddReportsUnrecognizedPublicISBN(t *testing.T) {
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	searchURL := "https://www.goodreads.com/search?q=9780306406157&search_type=books"
	empty := libraryTestPage(emptyOwnerLibrary, "https://www.goodreads.com/review/list/123")
	b := &fakeBrowser{
		pages: map[string]browser.Page{
			searchURL: &fakePage{url: searchURL, html: emptyPublicSearch},
		},
		pagesQueue: map[string][]browser.Page{
			libraryURL: {empty},
		},
	}
	if _, err := Add(context.Background(), b, isbn, domain.StatusToRead); !errors.Is(err, ErrBookNotFound) {
		t.Fatalf("unrecognized ISBN=%v calls=%v", err, b.calls)
	}
}

func TestAddRejectsInvalidStatusBeforeBrowserWork(t *testing.T) {
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	b := &fakeBrowser{}
	if _, err := Add(context.Background(), b, isbn, "paused"); !errors.Is(err, domain.ErrInvalidStatus) {
		t.Fatalf("err=%v calls=%v", err, b.calls)
	}
}

func TestVerifyAddMutation(t *testing.T) {
	after := ratingSnapshot()
	after.Status = domain.StatusToRead
	result, err := VerifyAddMutation(domain.Book{}, after, domain.StatusToRead, false)
	if err != nil || !result.Verified || result.Operation != "add" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	after.Status = domain.StatusRead
	if _, err := VerifyAddMutation(domain.Book{}, after, domain.StatusToRead, false); !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("status mismatch accepted: %v", err)
	}
}

func TestNewAddReconcilesStatusFailure(t *testing.T) {
	isbn, _ := domain.NormalizeISBN("9780306406157")
	stepFailure := errors.New("status failed")
	partialFailure := errors.New("partial")
	reconcileCalls := 0
	_, err := completeNewAdd(
		context.Background(),
		&fakeBrowser{},
		isbn,
		domain.StatusCurrentlyReading,
		addOperations{
			setStatus: func(context.Context, browser.Browser, domain.ISBN, domain.ReadingStatus) (domain.MutationResult, error) {
				return domain.MutationResult{}, stepFailure
			},
			reconcile: func(_ context.Context, _ browser.Browser, _ domain.ISBN, operation string, completed []string, failed string, cause error) error {
				reconcileCalls++
				if operation != "add" || !equalStrings(completed, []string{"add"}) ||
					failed != "status" || !errors.Is(cause, stepFailure) {
					t.Fatalf("operation=%q completed=%v failed=%q cause=%v", operation, completed, failed, cause)
				}
				return partialFailure
			},
		},
	)
	if !errors.Is(err, partialFailure) || reconcileCalls != 1 {
		t.Fatalf("err=%v reconcile calls=%d", err, reconcileCalls)
	}
}

func TestNewAddCompletesWithoutReconciliation(t *testing.T) {
	isbn, _ := domain.NormalizeISBN("9780306406157")
	before := ratingSnapshot()
	before.Status = domain.StatusToRead
	after := before
	after.Status = domain.StatusCurrentlyReading
	result, err := completeNewAdd(
		context.Background(),
		&fakeBrowser{},
		isbn,
		domain.StatusCurrentlyReading,
		addOperations{
			setStatus: func(context.Context, browser.Browser, domain.ISBN, domain.ReadingStatus) (domain.MutationResult, error) {
				return domain.MutationResult{Before: before, After: after, Verified: true}, nil
			},
			reconcile: func(context.Context, browser.Browser, domain.ISBN, string, []string, string, error) error {
				t.Fatal("successful add attempted reconciliation")
				return nil
			},
		},
	)
	if err != nil || !result.Verified || result.Operation != "add" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestNewAddPreservesNestedStatusPartialDetails(t *testing.T) {
	isbn, _ := domain.NormalizeISBN("9780306406157")
	nested := &domain.PartialMutationError{
		Operation: "add",
		Completed: []string{"status"},
		Failed:    "date",
	}
	result, err := completeNewAdd(
		context.Background(),
		&fakeBrowser{},
		isbn,
		domain.StatusRead,
		addOperations{
			setStatus: func(context.Context, browser.Browser, domain.ISBN, domain.ReadingStatus) (domain.MutationResult, error) {
				return domain.MutationResult{}, nested
			},
			reconcile: func(context.Context, browser.Browser, domain.ISBN, string, []string, string, error) error {
				t.Fatal("nested partial mutation was reconciled twice")
				return nil
			},
		},
	)
	var partial *domain.PartialMutationError
	if result.Verified || !errors.As(err, &partial) ||
		!equalStrings(partial.Completed, []string{"add", "status"}) ||
		partial.Failed != "date" {
		t.Fatalf("result=%+v partial=%+v err=%v", result, partial, err)
	}
}

const emptyPublicSearch = `<html><body><form action="/search"></form></body></html>`

func addSearchFixture(route string) string {
	return fmt.Sprintf(`<html><body><form action="/search"></form><a href="%s">result</a></body></html>`, route)
}

func addBookFixture(details bool) string {
	return bookPageFixture(details, true)
}

func ownedBookFixture(details bool) string {
	return bookPageFixture(details, false)
}

func bookPageFixture(details, wantToRead bool) string {
	metadata := ""
	if details {
		metadata = `<div class="TruncatedContent__text TruncatedContent__text--small">9780306406157 <span>(ISBN10: 0306406152)</span></div>`
	}
	action := `<button class="Button Button--medium Button--block">Read</button>`
	if wantToRead {
		action = `<button class="Button Button--wtr Button--block">Want to Read</button>`
	}
	return `<html><body>
	<div class="Sticky"><div class="BookActions"><div class="BookActions__button">` + action + `</div></div></div>
	<div class="BookPageMetadataSection"><button class="Button Button--inline">Book details</button>` +
		metadata + `</div></body></html>`
}
