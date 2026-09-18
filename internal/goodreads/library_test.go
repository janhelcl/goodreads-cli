package goodreads

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
)

func libraryTestPage(html, url string) *fakePage {
	return &fakePage{
		url:       url,
		html:      html,
		selectors: map[string]bool{signOutCSS: true, "#books": true, "#booksBody": true},
		text:      map[string]bool{"h1|^My Books$": true},
	}
}

func TestLibraryPaginatesOnlyAsNeeded(t *testing.T) {
	pageTwo := strings.Replace(shelfFixture, `<a class="next_page" href="?page=2">next</a>`, "", 1)
	pageTwo = strings.Replace(pageTwo, "book/show/42.Invented_Book", "book/show/43.Another_Book", 1)
	pageTwo = strings.Replace(pageTwo, "staticStar p0", "staticStar p10", 1)
	b := &fakeBrowser{pages: map[string]browser.Page{
		libraryURL: libraryTestPage(shelfFixture, "https://www.goodreads.com/review/list/123"),
		"https://www.goodreads.com/review/list/123?page=2": libraryTestPage(pageTwo, "https://www.goodreads.com/review/list/123?page=2"),
	}}
	books, err := Library(context.Background(), b, domain.LibraryFilter{Limit: 1})
	if err != nil || len(books) != 1 || len(b.calls) != 2 {
		t.Fatalf("early-stop books=%+v err=%v calls=%v", books, err, b.calls)
	}
	b.calls = nil
	books, err = Library(context.Background(), b, domain.LibraryFilter{Rating: 3, Limit: 1})
	if err != nil || len(books) != 1 || books[0].BookID != "43" || len(b.calls) != 3 {
		t.Fatalf("filtered pagination books=%+v err=%v calls=%v", books, err, b.calls)
	}
}

func TestLibraryRejectsUnsafePagination(t *testing.T) {
	for _, next := range []string{"https://example.com/", "/review/list/999?page=2"} {
		html := strings.Replace(shelfFixture, "?page=2", next, 1)
		b := &fakeBrowser{pages: map[string]browser.Page{
			libraryURL: libraryTestPage(html, "https://www.goodreads.com/review/list/123"),
		}}
		_, err := Library(context.Background(), b, domain.LibraryFilter{Rating: 5, Limit: 2})
		if !errors.Is(err, ErrCompatibility) {
			t.Fatalf("next=%q: expected compatibility error, got %v", next, err)
		}
	}
	loop := &fakeBrowser{pages: map[string]browser.Page{
		libraryURL: libraryTestPage(shelfFixture, "https://www.goodreads.com/review/list/123"),
		"https://www.goodreads.com/review/list/123?page=2": libraryTestPage(shelfFixture, "https://www.goodreads.com/review/list/123?page=2"),
	}}
	if _, err := Library(context.Background(), loop, domain.LibraryFilter{Rating: 5, Limit: 2}); !errors.Is(err, ErrCompatibility) {
		t.Fatalf("pagination loop accepted: %v", err)
	}
}

func TestGetScansForExactISBNAndRejectsAmbiguity(t *testing.T) {
	isbn, err := domain.NormalizeISBN("0-306-40615-2")
	if err != nil {
		t.Fatal(err)
	}
	pageOne := strings.Replace(shelfFixture, "0-306-40615-2", "1603580557", 1)
	pageOne = strings.Replace(pageOne, "9780306406157", "9781603580557", 1)
	pageTwo := strings.Replace(shelfFixture, `<a class="next_page" href="?page=2">next</a>`, "", 1)
	pageTwo = strings.Replace(pageTwo, "book/show/42.Invented_Book", "book/show/43.Another_Book", 1)
	b := &fakeBrowser{pages: map[string]browser.Page{
		libraryURL: libraryTestPage(pageOne, "https://www.goodreads.com/review/list/123"),
		"https://www.goodreads.com/review/list/123?page=2": libraryTestPage(pageTwo, "https://www.goodreads.com/review/list/123?page=2"),
	}}
	book, err := Get(context.Background(), b, isbn)
	if err != nil || book.BookID != "43" || len(b.calls) != 3 {
		t.Fatalf("get book=%+v err=%v calls=%v", book, err, b.calls)
	}
	b.pages[libraryURL] = libraryTestPage(shelfFixture, "https://www.goodreads.com/review/list/123")
	if _, err := Get(context.Background(), b, isbn); !errors.Is(err, ErrBookAmbiguous) {
		t.Fatalf("duplicate ISBN accepted: %v", err)
	}
	noMatch := strings.Replace(pageOne, `<a class="next_page" href="?page=2">next</a>`, "", 1)
	b.pages[libraryURL] = libraryTestPage(noMatch, "https://www.goodreads.com/review/list/123")
	if _, err := Get(context.Background(), b, isbn); !errors.Is(err, ErrBookNotFound) {
		t.Fatalf("missing ISBN resolved: %v", err)
	}
	unidentified := strings.Replace(noMatch, "1603580557", "", 1)
	unidentified = strings.Replace(unidentified, "9781603580557", "", 1)
	b.pages[libraryURL] = libraryTestPage(unidentified, "https://www.goodreads.com/review/list/123")
	if _, err := Get(context.Background(), b, isbn); !errors.Is(err, ErrCompatibility) {
		t.Fatalf("unidentified row was treated as not-found: %v", err)
	}
}

func TestGetResolvesBeyondTenPagesAndChecksToTermination(t *testing.T) {
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	b := exactScanBrowser(12, map[int]bool{11: true})
	book, err := Get(context.Background(), b, isbn)
	if err != nil || book.BookID != "1011" || len(b.calls) != 13 {
		t.Fatalf("book=%+v err=%v calls=%d", book, err, len(b.calls))
	}

	b = exactScanBrowser(12, map[int]bool{1: true})
	book, err = Get(context.Background(), b, isbn)
	if err != nil || book.BookID != "1001" || len(b.calls) != 13 {
		t.Fatalf("first-page book=%+v err=%v calls=%d", book, err, len(b.calls))
	}
}

func TestExactResolutionBudgetIsIncompleteAndPreventsAdd(t *testing.T) {
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	b := exactScanBrowser(maxExactShelfPages+1, nil)
	_, err = Add(context.Background(), b, isbn, domain.StatusToRead)
	if !errors.Is(err, ErrScanIncomplete) {
		t.Fatalf("expected incomplete scan, got %v", err)
	}
	for _, call := range b.calls {
		if strings.Contains(call, "/search") || strings.Contains(call, "/book/show/") {
			t.Fatalf("mutation resolution continued after incomplete scan: %v", b.calls)
		}
	}
}

func TestExactResolutionRejectsPaginationLoop(t *testing.T) {
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	b := exactScanBrowser(2, nil)
	loopURL := "https://www.goodreads.com/review/list/123?page=2"
	b.pages[loopURL] = libraryTestPage(exactScanFixture(2, false, 2), loopURL)
	if _, err := Get(context.Background(), b, isbn); !errors.Is(err, ErrCompatibility) {
		t.Fatalf("pagination loop was accepted: %v", err)
	}
}

func TestExactResolutionRejectsPaginationScopeChange(t *testing.T) {
	isbn, _ := domain.NormalizeISBN("9780306406157")
	b := exactScanBrowser(2, nil)
	first := strings.Replace(exactScanFixture(1, false, 2), "?page=2", "?page=2&shelf=read", 1)
	b.pagesQueue[libraryURL][1] = libraryTestPage(first, "https://www.goodreads.com/review/list/123")
	if _, err := Get(context.Background(), b, isbn); !errors.Is(err, ErrCompatibility) {
		t.Fatalf("pagination scope change was accepted: %v", err)
	}
}

func TestBookIDResolutionUsesExactScanBudget(t *testing.T) {
	b := exactScanBrowser(12, nil)
	found, err := libraryContainsBookID(context.Background(), b, "1011")
	if err != nil || !found || len(b.calls) != 12 {
		t.Fatalf("found=%t err=%v calls=%d", found, err, len(b.calls))
	}
}

func TestUnidentifiedRowsBlockGetButNotProvenMutationTarget(t *testing.T) {
	isbn, _ := domain.NormalizeISBN("9780306406157")
	const pageTwoURL = "https://www.goodreads.com/review/list/123?page=2"
	unknown := exactScanFixture(1, false, 2)
	unknown = strings.Replace(unknown, "1-60358-055-7", "", 1)
	unknown = strings.Replace(unknown, "9781603580557", "", 1)
	match := ownerStatusFixture(domain.StatusRead, false)

	getBrowser := &fakeBrowser{
		pages: map[string]browser.Page{
			pageTwoURL: libraryTestPage(match, pageTwoURL),
		},
		pagesQueue: map[string][]browser.Page{
			libraryURL: {
				privatePage(),
				libraryTestPage(unknown, "https://www.goodreads.com/review/list/123"),
			},
		},
	}
	if _, err := Get(context.Background(), getBrowser, isbn); !errors.Is(err, ErrCompatibility) {
		t.Fatalf("get accepted incomplete uniqueness proof: %v", err)
	}

	mutationBrowser := &fakeBrowser{pages: map[string]browser.Page{
		libraryURL: libraryTestPage(unknown, "https://www.goodreads.com/review/list/123"),
		pageTwoURL: libraryTestPage(match, pageTwoURL),
	}}
	candidate, err := findMutationCandidate(
		context.Background(), mutationBrowser, isbn, statusMutationStage, true,
	)
	if err != nil || candidate.Book.BookID != "42" {
		t.Fatalf("proven mutation target=%+v err=%v", candidate, err)
	}
}

func exactScanBrowser(pageCount int, matches map[int]bool) *fakeBrowser {
	b := &fakeBrowser{
		pages: map[string]browser.Page{},
		pagesQueue: map[string][]browser.Page{
			libraryURL: {privatePage()},
		},
	}
	for pageNumber := 1; pageNumber <= pageCount; pageNumber++ {
		requestURL := libraryURL
		currentURL := "https://www.goodreads.com/review/list/123"
		if pageNumber > 1 {
			requestURL = fmt.Sprintf("%s?page=%d", currentURL, pageNumber)
			currentURL = requestURL
		}
		next := pageNumber + 1
		html := exactScanFixture(pageNumber, matches[pageNumber], next)
		if pageNumber == pageCount {
			html = exactScanFixture(pageNumber, matches[pageNumber], 0)
		}
		page := libraryTestPage(html, currentURL)
		if pageNumber == 1 {
			b.pagesQueue[libraryURL] = append(b.pagesQueue[libraryURL], page)
		} else {
			b.pages[requestURL] = page
		}
	}
	return b
}

func exactScanFixture(pageNumber int, match bool, next int) string {
	html := strings.Replace(shelfFixture, "review_7", fmt.Sprintf("review_%d", 1000+pageNumber), 1)
	html = strings.Replace(html, "book/show/42.Invented_Book", fmt.Sprintf("book/show/%d.Invented_Book", 1000+pageNumber), 1)
	if !match {
		html = strings.Replace(html, "0-306-40615-2", "1-60358-055-7", 1)
		html = strings.Replace(html, "9780306406157", "9781603580557", 1)
	}
	if next == 0 {
		return strings.Replace(html, `<a class="next_page" href="?page=2">next</a>`, "", 1)
	}
	return strings.Replace(html, "?page=2", fmt.Sprintf("?page=%d", next), 1)
}
