package goodreads

import (
	"context"
	"errors"
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
