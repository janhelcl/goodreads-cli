package goodreads

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
)

var ErrPageLimit = errors.New("library page scan limit reached")
var ErrScanIncomplete = errors.New("exact-edition scan incomplete")
var ErrBookNotFound = errors.New("book ISBN not found in library")
var ErrBookAmbiguous = errors.New("book ISBN matches multiple library entries")

const (
	maxListShelfPages  = 10
	maxExactShelfPages = 100
)

// Library reads rendered Goodreads shelf pages for this invocation only.
func Library(ctx context.Context, b browser.Browser, filter domain.LibraryFilter) ([]domain.Book, error) {
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	books := make([]domain.Book, 0, filter.Limit)
	err := scanShelf(ctx, b, filter.Shelf, maxListShelfPages, ErrPageLimit, func(book domain.Book) bool {
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
// The full bounded scan is required to detect duplicate matches. An unidentified
// sibling row does not hide a unique ISBN match with a different book ID.
func Get(ctx context.Context, b browser.Browser, isbn domain.ISBN) (domain.Book, error) {
	state, err := Status(ctx, b)
	if err != nil {
		return domain.Book{}, err
	}
	if !state.Connected {
		return domain.Book{}, ErrSessionExpired
	}
	candidate, err := resolveOwnedEdition(ctx, b, isbn, "", false, true)
	if err != nil {
		return domain.Book{}, err
	}
	return candidate.Book, nil
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

func scanShelf(
	ctx context.Context,
	b browser.Browser,
	shelf domain.ReadingStatus,
	maxPages int,
	incompleteError error,
	visit func(domain.Book) bool,
) error {
	target, _ := url.Parse(libraryURL)
	if shelf != "" {
		query := target.Query()
		query.Set("shelf", string(shelf))
		target.RawQuery = query.Encode()
	}
	visited := map[string]bool{}
	for pageNumber := 0; pageNumber < maxPages; pageNumber++ {
		if visited[target.String()] {
			return fmt.Errorf("%w at library.page: pagination loop", ErrCompatibility)
		}
		visited[target.String()] = true
		page, err := b.NewPage(ctx, target.String())
		if errors.Is(err, browser.ErrOrigin) {
			return ErrSessionExpired
		}
		if err != nil {
			return fmt.Errorf("library.page: navigation failed: %w", err)
		}
		parsed, currentURL, err := readLibraryShelfPage(ctx, page, shelf)
		_ = page.Close()
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
			target.Query().Get("shelf") != string(shelf) ||
			!samePaginationScope(currentURL, target) {
			return fmt.Errorf("%w at library.page: next link left shelf", ErrCompatibility)
		}
	}
	return incompleteError
}

func readLibraryShelfPage(ctx context.Context, page browser.Page, shelf domain.ReadingStatus) (shelfPage, *url.URL, error) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var lastHTML string
	haveHTML := false
	for {
		current, err := page.URL(ctx)
		if err != nil {
			return shelfPage{}, nil, fmt.Errorf("library.page: URL unavailable: %w", err)
		}
		currentURL, err := url.Parse(current)
		if err != nil || !isGoodreadsPage(current) {
			return shelfPage{}, nil, fmt.Errorf("%w at library.page: unexpected origin", ErrCompatibility)
		}
		if strings.HasPrefix(currentURL.Path, "/user/sign_in") {
			return shelfPage{}, nil, ErrSessionExpired
		}
		if knownRemoteFailurePath(currentURL.Path) {
			return shelfPage{}, nil, fmt.Errorf("%w at library.page", browser.ErrNetwork)
		}
		if currentURL.Path != "/review/list" && !strings.HasPrefix(currentURL.Path, "/review/list/") {
			return shelfPage{}, nil, fmt.Errorf("%w at library.page: unexpected page", ErrCompatibility)
		}
		if shelf != "" && currentURL.Query().Get("shelf") != string(shelf) {
			return shelfPage{}, nil, fmt.Errorf("%w at library.page: requested shelf was not applied", ErrCompatibility)
		}
		html, err := page.HTML(ctx)
		if err != nil {
			return shelfPage{}, nil, fmt.Errorf("library.page: DOM unavailable: %w", err)
		}
		if remoteFailureDocument(html) {
			return shelfPage{}, nil, fmt.Errorf("%w at library.page", browser.ErrNetwork)
		}
		parsed, parseErr := parseShelfPage(html)
		if parseErr == nil {
			return parsed, currentURL, nil
		}
		if haveHTML && html == lastHTML {
			return shelfPage{}, nil, parseErr
		}
		lastHTML = html
		haveHTML = true
		select {
		case <-ctx.Done():
			if parseErr != nil && ctx.Err() == nil {
				return shelfPage{}, nil, parseErr
			}
			return shelfPage{}, nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func samePaginationScope(current, next *url.URL) bool {
	currentQuery := current.Query()
	nextQuery := next.Query()
	currentQuery.Del("page")
	nextQuery.Del("page")
	return currentQuery.Encode() == nextQuery.Encode()
}
