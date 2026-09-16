package goodreads

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
)

var ErrPageLimit = errors.New("library page scan limit reached")
var ErrBookNotFound = errors.New("book ISBN not found in library")
var ErrBookAmbiguous = errors.New("book ISBN matches multiple library entries")

const maxShelfPages = 10

// Library reads rendered Goodreads shelf pages for this invocation only.
func Library(ctx context.Context, b browser.Browser, filter domain.LibraryFilter) ([]domain.Book, error) {
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	books := make([]domain.Book, 0, filter.Limit)
	err := scanShelf(ctx, b, filter.Shelf, func(book domain.Book) bool {
		if filter.Rating == 0 || book.Rating == filter.Rating {
			books = append(books, book)
		}
		return len(books) >= filter.Limit
	})
	if err != nil {
		return nil, err
	}
	return books, nil
}

// Get resolves one exact edition ISBN from the user's rendered library rows.
// The full bounded scan is required to detect duplicate matches.
func Get(ctx context.Context, b browser.Browser, isbn domain.ISBN) (domain.Book, error) {
	var found domain.Book
	matches := 0
	unidentified := false
	err := scanShelf(ctx, b, "", func(book domain.Book) bool {
		if book.ISBN10 == "" && book.ISBN13 == "" {
			unidentified = true
		}
		if exactISBN(book, isbn) {
			found = book
			matches++
		}
		return false
	})
	if err != nil {
		return domain.Book{}, err
	}
	if matches > 1 {
		return domain.Book{}, ErrBookAmbiguous
	}
	if unidentified {
		return domain.Book{}, fmt.Errorf("%w at library.row: ISBN missing; exact lookup incomplete", ErrCompatibility)
	}
	if matches == 0 {
		return domain.Book{}, ErrBookNotFound
	}
	return found, nil
}

func exactISBN(book domain.Book, isbn domain.ISBN) bool {
	if book.ISBN13 == isbn.ISBN13 || (isbn.ISBN10 != "" && book.ISBN10 == isbn.ISBN10) {
		return true
	}
	// Goodreads may render only the ISBN-10 column for an edition.
	if book.ISBN10 != "" {
		parsed, err := domain.NormalizeISBN(book.ISBN10)
		return err == nil && parsed.ISBN13 == isbn.ISBN13
	}
	return false
}

func scanShelf(ctx context.Context, b browser.Browser, shelf domain.ReadingStatus, visit func(domain.Book) bool) error {
	state, err := Status(ctx, b)
	if err != nil {
		return err
	}
	if !state.Connected {
		return ErrSessionExpired
	}
	target, _ := url.Parse(libraryURL)
	if shelf != "" {
		query := target.Query()
		query.Set("shelf", string(shelf))
		target.RawQuery = query.Encode()
	}
	visited := map[string]bool{}
	for pageNumber := 0; pageNumber < maxShelfPages; pageNumber++ {
		if visited[target.String()] {
			return fmt.Errorf("%w at library.page: pagination loop", ErrCompatibility)
		}
		visited[target.String()] = true
		page, err := b.NewPage(ctx, target.String())
		if err != nil {
			return fmt.Errorf("library.page: navigation failed: %w", err)
		}
		current, err := page.URL(ctx)
		if err != nil {
			_ = page.Close()
			return fmt.Errorf("library.page: URL unavailable: %w", err)
		}
		currentURL, err := url.Parse(current)
		if err != nil || !isGoodreadsPage(current) ||
			(currentURL.Path != "/review/list" && !strings.HasPrefix(currentURL.Path, "/review/list/")) {
			_ = page.Close()
			return fmt.Errorf("%w at library.page: unexpected page", ErrCompatibility)
		}
		if shelf != "" && currentURL.Query().Get("shelf") != string(shelf) {
			_ = page.Close()
			return fmt.Errorf("%w at library.page: requested shelf was not applied", ErrCompatibility)
		}
		html, err := page.HTML(ctx)
		_ = page.Close()
		if err != nil {
			return fmt.Errorf("library.page: DOM unavailable: %w", err)
		}
		parsed, err := parseShelfPage(html)
		if err != nil {
			return err
		}
		for _, book := range parsed.Books {
			if visit(book) {
				return nil
			}
		}
		if parsed.Next == "" {
			return nil
		}
		next, err := url.Parse(parsed.Next)
		if err != nil {
			return fmt.Errorf("%w at library.page: invalid next link", ErrCompatibility)
		}
		target = currentURL.ResolveReference(next)
		if !isGoodreadsPage(target.String()) || target.Path != currentURL.Path ||
			target.Query().Get("shelf") != string(shelf) {
			return fmt.Errorf("%w at library.page: next link left shelf", ErrCompatibility)
		}
	}
	return ErrPageLimit
}
