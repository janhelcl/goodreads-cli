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

const addMutationStage = "mutation.add"

var isbnTextCandidate = regexp.MustCompile(`[0-9Xx][0-9Xx -]{8,20}[0-9Xx]`)

type resolvedBook struct {
	BookID string
	URL    string
}

// Add ensures that one exact Goodreads edition is in the library with the
// requested exclusive shelf. New editions are added through the visible
// Want-to-Read control, then any requested status change uses the same
// verified owner-shelf flow as Start.
func Add(
	ctx context.Context,
	b browser.Browser,
	isbn domain.ISBN,
	status domain.ReadingStatus,
) (domain.MutationResult, error) {
	if !status.Valid() {
		return domain.MutationResult{}, domain.ErrInvalidStatus
	}
	// Session state comes from the owner-library scan; a separate Status page
	// load must not consume the command deadline before the add or status click.
	existing, err := findMutationCandidate(ctx, b, isbn, addMutationStage, true)
	switch {
	case err == nil:
		result, err := setStatusPreservingFinishDateUsingCandidate(ctx, b, isbn, status, existing)
		if err != nil {
			return domain.MutationResult{}, err
		}
		result.Operation = "add"
		return result, nil
	case !errors.Is(err, ErrBookNotFound):
		return domain.MutationResult{}, err
	}
	resolved, page, err := resolveExactBook(ctx, b, isbn)
	if err != nil {
		return domain.MutationResult{}, err
	}
	defer page.Close()
	present, err := libraryContainsBookID(ctx, b, resolved.BookID)
	if err != nil {
		return domain.MutationResult{}, err
	}
	if present {
		return domain.MutationResult{}, fmt.Errorf("%w at book.resolve: target edition lacks a verifiable ISBN row", ErrCompatibility)
	}

	const addSelector = "div.Sticky div.BookActions div.BookActions__button button.Button--wtr.Button--block"
	raw, err := page.HTML(ctx)
	if err != nil {
		return domain.MutationResult{}, fmt.Errorf("%s: book DOM unavailable: %w", addMutationStage, err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(raw))
	if err != nil {
		return domain.MutationResult{}, fmt.Errorf("%w at %s: invalid book DOM", ErrCompatibility, addMutationStage)
	}
	button := doc.Find(addSelector)
	if button.Length() != 1 || normalizedVisibleText(button.Text()) != "want to read" {
		return domain.MutationResult{}, fmt.Errorf("%w at %s: add control changed", ErrCompatibility, addMutationStage)
	}
	requestCtx, cancelRequest := context.WithTimeout(ctx, 15*time.Second)
	clickErr := page.ClickAndWaitForRequest(requestCtx, addSelector)
	cancelRequest()
	_ = page.Close()

	added, readbackErr := findMutationCandidate(ctx, b, isbn, addMutationStage, true)
	if readbackErr != nil {
		return domain.MutationResult{}, fmt.Errorf("%w at mutation.verify: added edition unavailable", ErrMutationAmbiguous)
	}
	if added.Book.BookID != resolved.BookID {
		return domain.MutationResult{}, verificationError("book_id", "did not match")
	}
	added.Book.Review, readbackErr = loadFullReview(ctx, b, added.ReviewURL, addMutationStage)
	if readbackErr != nil {
		return domain.MutationResult{}, fmt.Errorf("%w at mutation.verify: added state unavailable", ErrMutationAmbiguous)
	}
	toReadResult, verifyErr := VerifyAddMutation(domain.Book{}, added.Book, domain.StatusToRead, false)
	if verifyErr != nil {
		if clickErr != nil {
			return domain.MutationResult{}, fmt.Errorf("%w at %s: completion unknown", ErrMutationAmbiguous, addMutationStage)
		}
		return domain.MutationResult{}, verifyErr
	}
	if status == domain.StatusToRead {
		return toReadResult, nil
	}
	completed := []string{"add"}
	statusResult, err := setStatusPreservingFinishDateUsingCandidate(ctx, b, isbn, status, added)
	if err != nil {
		if errors.Is(err, domain.ErrPartialMutation) {
			return domain.MutationResult{}, prependPartialStep(err, "add")
		}
		return domain.MutationResult{}, reconcileCompoundFailure(ctx, b, isbn, "add", completed, "status", err)
	}
	if statusChanged(statusResult) {
		completed = append(completed, "status")
	}
	result, err := VerifyAddMutation(domain.Book{}, statusResult.After, status, false)
	if err != nil {
		return domain.MutationResult{}, reconcileCompoundFailure(ctx, b, isbn, "add", completed, "verify", err)
	}
	return result, nil
}

type addOperations struct {
	setStatus func(context.Context, browser.Browser, domain.ISBN, domain.ReadingStatus) (domain.MutationResult, error)
	reconcile func(context.Context, browser.Browser, domain.ISBN, string, []string, string, error) error
}

func completeNewAdd(
	ctx context.Context,
	b browser.Browser,
	isbn domain.ISBN,
	status domain.ReadingStatus,
	operations addOperations,
) (domain.MutationResult, error) {
	completed := []string{"add"}
	statusResult, err := operations.setStatus(ctx, b, isbn, status)
	if err != nil {
		if errors.Is(err, domain.ErrPartialMutation) {
			return domain.MutationResult{}, prependPartialStep(err, "add")
		}
		return domain.MutationResult{}, operations.reconcile(ctx, b, isbn, "add", completed, "status", err)
	}
	if statusChanged(statusResult) {
		completed = append(completed, "status")
	}
	result, err := VerifyAddMutation(domain.Book{}, statusResult.After, status, false)
	if err != nil {
		return domain.MutationResult{}, operations.reconcile(ctx, b, isbn, "add", completed, "verify", err)
	}
	return result, nil
}

func resolveExactBook(
	ctx context.Context,
	b browser.Browser,
	isbn domain.ISBN,
) (resolvedBook, browser.Page, error) {
	return resolvePublicBook(ctx, b, isbn, true)
}

func resolvePublicBook(
	ctx context.Context,
	b browser.Browser,
	isbn domain.ISBN,
	requireAddControl bool,
) (resolvedBook, browser.Page, error) {
	searchTarget := "https://www.goodreads.com/search?q=" +
		url.QueryEscape(isbn.ISBN13) + "&search_type=books"
	searchPage, err := b.NewPage(ctx, searchTarget)
	if err != nil {
		return resolvedBook{}, nil, fmt.Errorf("book.resolve: search unavailable: %w", err)
	}
	if searchPage == nil {
		return resolvedBook{}, nil, fmt.Errorf("%w at book.resolve: search results unavailable", ErrCompatibility)
	}
	// A completed search may have zero book routes. Waiting for a
	// /book/show/ link would turn that legitimate absence into a timeout
	// for unrecognized ISBNs and the unidentified-row public fallback.
	searchDoc, err := waitForDocument(ctx, searchPage, func(doc *goquery.Document) bool {
		return doc.Find("form[action='/search']").Length() == 1
	})
	if err != nil {
		_ = searchPage.Close()
		return resolvedBook{}, nil, err
	}
	searchURL, err := searchPage.URL(ctx)
	if err != nil {
		_ = searchPage.Close()
		return resolvedBook{}, nil, fmt.Errorf("book.resolve: search URL unavailable: %w", err)
	}
	parsedSearch, err := url.Parse(searchURL)
	if err != nil || !isGoodreadsPage(searchURL) || parsedSearch.Path != "/search" {
		_ = searchPage.Close()
		return resolvedBook{}, nil, fmt.Errorf("%w at book.resolve: unexpected search page", ErrCompatibility)
	}
	routes := map[string]string{}
	var routeErr error
	searchDoc.Find("a[href*='/book/show/']").EachWithBreak(func(_ int, link *goquery.Selection) bool {
		href := link.AttrOr("href", "")
		reference, err := url.Parse(href)
		if err != nil {
			routeErr = err
			return false
		}
		target := parsedSearch.ResolveReference(reference)
		match := bookPath.FindStringSubmatch(target.Path)
		if !isGoodreadsPage(target.String()) || len(match) != 2 {
			return true
		}
		target.RawQuery = ""
		target.Fragment = ""
		routes[match[1]] = target.String()
		return true
	})
	_ = searchPage.Close()
	if routeErr != nil {
		return resolvedBook{}, nil, fmt.Errorf("%w at book.resolve: invalid search result", ErrCompatibility)
	}
	if len(routes) == 0 {
		return resolvedBook{}, nil, ErrBookNotFound
	}
	if len(routes) != 1 {
		return resolvedBook{}, nil, ErrBookAmbiguous
	}
	var resolved resolvedBook
	for id, target := range routes {
		resolved = resolvedBook{BookID: id, URL: target}
	}
	page, err := b.NewPage(ctx, resolved.URL)
	if err != nil {
		return resolvedBook{}, nil, fmt.Errorf("book.resolve: book page unavailable: %w", err)
	}
	doc, err := waitForDocument(ctx, page, func(doc *goquery.Document) bool {
		if doc.Find("div.BookPageMetadataSection button.Button--inline").Length() != 1 {
			return false
		}
		// Already-owned book pages replace Want to Read with a shelf-status
		// control. Identity proof only needs the metadata section.
		if requireAddControl &&
			doc.Find("div.Sticky div.BookActions button.Button--wtr.Button--block").Length() != 1 {
			return false
		}
		return true
	})
	if err != nil {
		_ = page.Close()
		return resolvedBook{}, nil, err
	}
	metadata := doc.Find("div.BookPageMetadataSection").Clone()
	metadata.Find("script,style").Remove()
	if textHasExactISBN(metadata.Text(), isbn) {
		return resolved, page, nil
	}
	detailsSelector := "div.BookPageMetadataSection button.Button--inline"
	if !strings.Contains(normalizedVisibleText(doc.Find(detailsSelector).Text()), "detail") {
		_ = page.Close()
		return resolvedBook{}, nil, fmt.Errorf("%w at book.resolve: details control changed", ErrCompatibility)
	}
	if err := page.Click(ctx, detailsSelector); err != nil {
		_ = page.Close()
		return resolvedBook{}, nil, fmt.Errorf("%w at book.resolve: details unavailable", ErrCompatibility)
	}
	detailsCtx, cancelDetails := context.WithTimeout(ctx, 20*time.Second)
	_, err = waitForDocument(detailsCtx, page, func(doc *goquery.Document) bool {
		metadata := doc.Find("div.BookPageMetadataSection").Clone()
		metadata.Find("script,style").Remove()
		return textHasExactISBN(metadata.Text(), isbn)
	})
	cancelDetails()
	if err != nil {
		_ = page.Close()
		if ctx.Err() != nil {
			return resolvedBook{}, nil, ctx.Err()
		}
		return resolvedBook{}, nil, fmt.Errorf("%w at book.resolve: exact ISBN was not visible", ErrCompatibility)
	}
	return resolved, page, nil
}

func waitForDocument(
	ctx context.Context,
	page browser.Page,
	ready func(*goquery.Document) bool,
) (*goquery.Document, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		raw, err := page.HTML(ctx)
		if err == nil {
			doc, parseErr := goquery.NewDocumentFromReader(strings.NewReader(raw))
			if parseErr == nil && ready(doc) {
				return doc, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func textHasExactISBN(text string, isbn domain.ISBN) bool {
	if strings.Contains(text, isbn.ISBN13) ||
		(isbn.ISBN10 != "" && strings.Contains(text, isbn.ISBN10)) {
		return true
	}
	for _, candidate := range isbnTextCandidate.FindAllString(text, -1) {
		parsed, err := domain.NormalizeISBN(candidate)
		if err == nil && parsed.ISBN13 == isbn.ISBN13 {
			return true
		}
	}
	return false
}

func normalizedVisibleText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func lookupPublicBookID(ctx context.Context, b browser.Browser, isbn domain.ISBN) (string, error) {
	resolved, page, err := resolvePublicBook(ctx, b, isbn, false)
	if page != nil {
		_ = page.Close()
	}
	if err != nil {
		return "", err
	}
	if resolved.BookID == "" {
		return "", ErrBookNotFound
	}
	return resolved.BookID, nil
}

func libraryContainsBookID(ctx context.Context, b browser.Browser, bookID string) (bool, error) {
	found := false
	err := scanShelf(ctx, b, "", exactShelfPageSize, maxExactShelfPages, ErrScanIncomplete, func(book domain.Book) bool {
		if book.BookID == bookID {
			found = true
			return true
		}
		return false
	})
	return found, err
}

func setStatusPreservingFinishDateUsingCandidate(
	ctx context.Context,
	b browser.Browser,
	isbn domain.ISBN,
	status domain.ReadingStatus,
	candidate ratingCandidate,
) (domain.MutationResult, error) {
	if status != domain.StatusRead {
		result, _, err := setStatusResolved(ctx, b, isbn, status, false, candidate)
		return result, err
	}
	result, candidate, err := setStatusResolved(ctx, b, isbn, status, true, candidate)
	if err != nil {
		return domain.MutationResult{}, err
	}
	before := result.Before
	completed := []string{}
	if statusChanged(result) {
		completed = append(completed, "status")
	}
	if equalOptionalString(before.DateRead, result.After.DateRead) {
		return verifyStatusMutation(before, result.After, status, false)
	}
	if before.DateRead == nil {
		dateResult, err := clearFinishDateUsingCandidate(ctx, b, isbn, candidate)
		if err != nil {
			return domain.MutationResult{}, reconcileCompoundFailure(ctx, b, isbn, "add", completed, "date", err)
		}
		candidate.Book = dateResult.After
	} else {
		date, err := time.Parse("2006-01-02", *before.DateRead)
		if err != nil {
			parseErr := fmt.Errorf("%w at %s: prior finish date invalid", ErrCompatibility, addMutationStage)
			return domain.MutationResult{}, reconcileCompoundFailure(ctx, b, isbn, "add", completed, "date", parseErr)
		}
		dateResult, err := setFinishDateUsingCandidate(ctx, b, isbn, date, candidate)
		if err != nil {
			return domain.MutationResult{}, reconcileCompoundFailure(ctx, b, isbn, "add", completed, "date", err)
		}
		candidate.Book = dateResult.After
	}
	afterCandidate, err := readbackMutationCandidate(ctx, b, isbn, candidate, addMutationStage, true)
	if err != nil {
		readbackErr := fmt.Errorf("%w at mutation.verify: restored finish date unavailable", ErrMutationAmbiguous)
		return domain.MutationResult{}, reconcileCompoundFailure(ctx, b, isbn, "add", completed, "verify", readbackErr)
	}
	after := afterCandidate.Book
	after.Review, err = loadFullReview(ctx, b, afterCandidate.ReviewURL, addMutationStage)
	if err != nil {
		readbackErr := fmt.Errorf("%w at mutation.verify: restored state unavailable", ErrMutationAmbiguous)
		return domain.MutationResult{}, reconcileCompoundFailure(ctx, b, isbn, "add", completed, "verify", readbackErr)
	}
	verified, err := verifyStatusMutation(before, after, status, false)
	if err != nil {
		return domain.MutationResult{}, reconcileCompoundFailure(ctx, b, isbn, "add", completed, "verify", err)
	}
	return verified, nil
}

func VerifyAddMutation(
	before domain.Book,
	after domain.Book,
	status domain.ReadingStatus,
	existed bool,
) (domain.MutationResult, error) {
	if !status.Valid() {
		return domain.MutationResult{}, domain.ErrInvalidStatus
	}
	if after.BookID == "" || (after.ISBN10 == "" && after.ISBN13 == "") {
		return domain.MutationResult{}, verificationError("book_id", "was unavailable")
	}
	if after.Status != status {
		return domain.MutationResult{}, verificationError("status", "did not match")
	}
	if existed {
		result, err := verifyStatusMutation(before, after, status, false)
		if err != nil {
			return domain.MutationResult{}, err
		}
		result.Operation = "add"
		return result, nil
	}
	return domain.MutationResult{
		Operation: "add",
		Before:    before,
		After:     after,
		Changes:   domain.BookUpdate{Status: &status},
		Verified:  true,
	}, nil
}
