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
var errLibraryTableNotReady = errors.New("required table markers missing")

const (
	maxListShelfPages  = 10
	maxExactShelfPages = 100
	exactShelfPageSize = 100
)

// pageReadyTimeout bounds how long Status and shelf reads wait for the
// private library table after NewPage sees a document body. A hanging
// window.onload must not consume the command deadline, but a brief first
// paint without #books is not a compatibility failure.
var pageReadyTimeout = 15 * time.Second

// Library reads rendered Goodreads shelf pages for this invocation only.
func Library(ctx context.Context, b browser.Browser, filter domain.LibraryFilter) ([]domain.Book, error) {
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	books := make([]domain.Book, 0, filter.Limit)
	err := scanShelf(ctx, b, filter.Shelf, 0, maxListShelfPages, ErrPageLimit, func(book domain.Book) bool {
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
	pageSize int,
	maxPages int,
	incompleteError error,
	visit func(domain.Book) bool,
) error {
	target := shelfTarget(shelf, pageSize)
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
		currentScope := paginationScopeWithPageSize(currentURL, pageSize)
		target = paginationScopeWithPageSize(target, pageSize)
		if !isGoodreadsPage(target.String()) || target.Path != currentURL.Path ||
			target.Query().Get("shelf") != string(shelf) ||
			!samePaginationScope(currentScope, target) {
			return fmt.Errorf("%w at library.page: next link left shelf", ErrCompatibility)
		}
	}
	return incompleteError
}

func shelfTarget(shelf domain.ReadingStatus, pageSize int) *url.URL {
	target, _ := url.Parse(libraryURL)
	query := target.Query()
	if shelf != "" {
		query.Set("shelf", string(shelf))
	}
	if pageSize > 0 {
		query.Set("per_page", fmt.Sprintf("%d", pageSize))
	}
	target.RawQuery = query.Encode()
	return target
}

func paginationScopeWithPageSize(target *url.URL, pageSize int) *url.URL {
	clone := *target
	if pageSize <= 0 {
		return &clone
	}
	query := clone.Query()
	query.Set("per_page", fmt.Sprintf("%d", pageSize))
	clone.RawQuery = query.Encode()
	return &clone
}

func readLibraryShelfPage(ctx context.Context, page browser.Page, shelf domain.ReadingStatus) (shelfPage, *url.URL, error) {
	readyCtx, cancel := context.WithTimeout(ctx, pageReadyTimeout)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
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
		if !errors.Is(parseErr, errLibraryTableNotReady) {
			return shelfPage{}, nil, parseErr
		}
		lastErr = parseErr
		select {
		case <-readyCtx.Done():
			if ctx.Err() != nil {
				return shelfPage{}, nil, ctx.Err()
			}
			if lastErr != nil {
				return shelfPage{}, nil, lastErr
			}
			return shelfPage{}, nil, readyCtx.Err()
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
