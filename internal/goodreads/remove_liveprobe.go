//go:build liveprobe

package goodreads

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
)

var reviewDestroyPath = regexp.MustCompile(`^/review/destroy/[0-9]+/?$`)

// IsInLibraryForLiveProbe resolves the public edition exactly and checks
// owner rows by stable book ID, including rows that do not render an ISBN.
func IsInLibraryForLiveProbe(
	ctx context.Context,
	b browser.Browser,
	isbn domain.ISBN,
) (bool, error) {
	resolved, page, err := resolveExactBook(ctx, b, isbn)
	if err != nil {
		return false, err
	}
	_ = page.Close()
	return libraryContainsBookID(ctx, b, resolved.BookID)
}

// RemoveForLiveProbe removes one exact library edition through its visible
// owner-row action. It exists only in liveprobe builds to restore an add
// canary; removal is not part of the public product API.
func RemoveForLiveProbe(ctx context.Context, b browser.Browser, isbn domain.ISBN) error {
	candidate, err := findMutationCandidate(ctx, b, isbn, "mutation.add.restore", false)
	if err != nil {
		return err
	}
	page, err := b.NewPage(ctx, candidate.PageURL)
	if err != nil {
		return fmt.Errorf("mutation.add.restore: target page unavailable: %w", err)
	}
	raw, err := page.HTML(ctx)
	if err != nil {
		_ = page.Close()
		return fmt.Errorf("mutation.add.restore: DOM unavailable: %w", err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(raw))
	if err != nil {
		_ = page.Close()
		return fmt.Errorf("%w at mutation.add.restore: invalid DOM", ErrCompatibility)
	}
	row := doc.Find("#" + candidate.RowID)
	book, err := parseShelfRow(row)
	if err != nil || row.Length() != 1 || !exactISBN(book, isbn) ||
		book.BookID != candidate.Book.BookID {
		_ = page.Close()
		return fmt.Errorf("%w at mutation.add.restore: target identity changed", ErrCompatibility)
	}
	link := row.Find("td.field.actions a.deleteLink[data-method='post'][data-confirm][rel='nofollow']")
	if link.Length() != 1 {
		_ = page.Close()
		return fmt.Errorf("%w at mutation.add.restore: remove control changed", ErrCompatibility)
	}
	href := link.AttrOr("href", "")
	reference, err := url.Parse(href)
	if err != nil {
		_ = page.Close()
		return fmt.Errorf("%w at mutation.add.restore: invalid remove route", ErrCompatibility)
	}
	base, _ := url.Parse(candidate.PageURL)
	target := base.ResolveReference(reference)
	if !isGoodreadsPage(target.String()) || !reviewDestroyPath.MatchString(target.Path) {
		_ = page.Close()
		return fmt.Errorf("%w at mutation.add.restore: unsafe remove route", ErrCompatibility)
	}
	selector := "#" + candidate.RowID +
		" td.field.actions a.deleteLink[data-method='post'][data-confirm][rel='nofollow']"
	requestCtx, cancelRequest := context.WithTimeout(ctx, 15*time.Second)
	clickErr := page.ClickAndAcceptConfirmAndWaitForRequest(requestCtx, selector)
	cancelRequest()
	_ = page.Close()

	_, readbackErr := findMutationCandidate(ctx, b, isbn, "mutation.add.restore", false)
	present, idErr := libraryContainsBookID(ctx, b, candidate.Book.BookID)
	if errors.Is(readbackErr, ErrBookNotFound) && idErr == nil && !present {
		return nil
	}
	if clickErr != nil {
		return fmt.Errorf("%w at mutation.add.restore: completion unknown", ErrMutationAmbiguous)
	}
	if readbackErr != nil && !errors.Is(readbackErr, ErrBookNotFound) {
		return readbackErr
	}
	if idErr != nil {
		return idErr
	}
	return verificationError("book", "was not removed")
}
