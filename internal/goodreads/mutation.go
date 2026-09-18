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

var (
	ErrMutationAmbiguous  = errors.New("Goodreads mutation result is ambiguous")
	ErrVerificationFailed = errors.New("Goodreads mutation verification failed")
)

type VerificationError struct {
	Field  string
	Reason string
}

func (e *VerificationError) Error() string {
	return fmt.Sprintf("%v: %s %s", ErrVerificationFailed, e.Field, e.Reason)
}

func (e *VerificationError) Unwrap() error {
	return ErrVerificationFailed
}

type ratingCandidate struct {
	Book      domain.Book
	PageURL   string
	RowID     string
	ReviewURL string
}

var reviewEditPath = regexp.MustCompile(`^/review/edit/[0-9]+/?$`)
var reviewRowID = regexp.MustCompile(`^review_[0-9]+$`)

var ratingTitles = map[int]string{
	1: "did not like it",
	2: "it was ok",
	3: "liked it",
	4: "really liked it",
	5: "it was amazing",
}

// Rate changes one exact rendered edition through the owner shelf's rating
// control, then reloads all preservation fields before reporting success.
func Rate(ctx context.Context, b browser.Browser, isbn domain.ISBN, rating int) (domain.MutationResult, error) {
	if err := domain.ValidateRating(rating); err != nil {
		return domain.MutationResult{}, err
	}
	state, err := Status(ctx, b)
	if err != nil {
		return domain.MutationResult{}, err
	}
	if !state.Connected {
		return domain.MutationResult{}, ErrSessionExpired
	}
	candidate, err := findRatingCandidate(ctx, b, isbn)
	if err != nil {
		return domain.MutationResult{}, err
	}
	page, err := b.NewPage(ctx, candidate.PageURL)
	if err != nil {
		return domain.MutationResult{}, fmt.Errorf("mutation.rating: target page unavailable: %w", err)
	}
	before, err := ratingBookOnPage(ctx, page, candidate, isbn)
	if err != nil {
		_ = page.Close()
		return domain.MutationResult{}, err
	}
	before.Review, err = loadFullReview(ctx, b, candidate.ReviewURL, "mutation.rating")
	if err != nil {
		_ = page.Close()
		return domain.MutationResult{}, err
	}
	if before.Rating == rating {
		_ = page.Close()
		return VerifyRatingMutation(before, before, rating)
	}
	selector := fmt.Sprintf("#%s td.field.rating div.stars[data-rating] a.star[title='%s']", candidate.RowID, ratingTitles[rating])
	clickErr := page.Click(ctx, selector)
	var completionErr error
	if clickErr == nil {
		completionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		completionErr = waitForRating(completionCtx, page, candidate, isbn, rating)
		cancel()
	}
	_ = page.Close()

	afterCandidate, readbackErr := findRatingCandidate(ctx, b, isbn)
	if readbackErr != nil {
		return domain.MutationResult{}, fmt.Errorf("%w at mutation.verify: readback unavailable", ErrMutationAmbiguous)
	}
	after := afterCandidate.Book
	after.Review, readbackErr = loadFullReview(ctx, b, afterCandidate.ReviewURL, "mutation.rating")
	if readbackErr != nil {
		return domain.MutationResult{}, fmt.Errorf("%w at mutation.verify: preservation readback unavailable", ErrMutationAmbiguous)
	}
	result, verifyErr := VerifyRatingMutation(before, after, rating)
	if verifyErr == nil {
		return result, nil
	}
	if clickErr != nil || completionErr != nil {
		return domain.MutationResult{}, fmt.Errorf("%w at mutation.rating: completion unknown", ErrMutationAmbiguous)
	}
	return domain.MutationResult{}, verifyErr
}

func findRatingCandidate(ctx context.Context, b browser.Browser, isbn domain.ISBN) (ratingCandidate, error) {
	return findMutationCandidate(ctx, b, isbn, "mutation.rating", false)
}

func findMutationCandidate(
	ctx context.Context,
	b browser.Browser,
	isbn domain.ISBN,
	stage string,
	requireShelfChooser bool,
) (ratingCandidate, error) {
	return resolveOwnedEdition(ctx, b, isbn, stage, requireShelfChooser, false)
}

// resolveOwnedEdition is the single exact owner-library resolver used by Get
// and every mutation. An empty stage requests identity only; mutation callers
// additionally validate the controls they need on the matched row.
func resolveOwnedEdition(
	ctx context.Context,
	b browser.Browser,
	isbn domain.ISBN,
	stage string,
	requireShelfChooser bool,
	rejectUnidentifiedMatch bool,
) (ratingCandidate, error) {
	target, _ := url.Parse(libraryURL)
	visited := map[string]bool{}
	var found ratingCandidate
	matches := 0
	unidentified := false
	for pageNumber := 0; pageNumber < maxExactShelfPages; pageNumber++ {
		if err := ctx.Err(); err != nil {
			return ratingCandidate{}, err
		}
		if visited[target.String()] {
			return ratingCandidate{}, fmt.Errorf("%w at book.resolve: pagination loop", ErrCompatibility)
		}
		visited[target.String()] = true
		page, err := b.NewPage(ctx, target.String())
		if err != nil {
			return ratingCandidate{}, fmt.Errorf("book.resolve: navigation failed: %w", err)
		}
		current, err := page.URL(ctx)
		if err != nil {
			_ = page.Close()
			return ratingCandidate{}, fmt.Errorf("book.resolve: URL unavailable: %w", err)
		}
		currentURL, err := url.Parse(current)
		if err != nil || !isGoodreadsPage(current) ||
			(currentURL.Path != "/review/list" && !strings.HasPrefix(currentURL.Path, "/review/list/")) {
			_ = page.Close()
			return ratingCandidate{}, fmt.Errorf("%w at book.resolve: unexpected page", ErrCompatibility)
		}
		raw, err := page.HTML(ctx)
		_ = page.Close()
		if err != nil {
			return ratingCandidate{}, fmt.Errorf("book.resolve: DOM unavailable: %w", err)
		}
		parsed, err := parseShelfPage(raw)
		if err != nil {
			return ratingCandidate{}, err
		}
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(raw))
		if err != nil {
			return ratingCandidate{}, fmt.Errorf("%w at book.resolve: invalid DOM", ErrCompatibility)
		}
		var rowErr error
		doc.Find("#booksBody > tr").EachWithBreak(func(_ int, row *goquery.Selection) bool {
			book, err := parseShelfRow(row)
			if err != nil {
				rowErr = err
				return false
			}
			if book.ISBN10 == "" && book.ISBN13 == "" {
				unidentified = true
			}
			if !exactISBN(book, isbn) {
				return true
			}
			candidate := ratingCandidate{Book: book}
			if stage != "" {
				candidate, err = candidateFromRow(currentURL, row, book, stage, requireShelfChooser)
				if err != nil {
					rowErr = err
					return false
				}
			}
			found = candidate
			matches++
			return true
		})
		if rowErr != nil {
			return ratingCandidate{}, rowErr
		}
		if parsed.Next == "" {
			switch {
			case matches > 1:
				return ratingCandidate{}, ErrBookAmbiguous
			case matches == 0:
				if unidentified && rejectUnidentifiedMatch {
					return ratingCandidate{}, fmt.Errorf("%w at library.row: ISBN missing; exact lookup incomplete", ErrCompatibility)
				}
				return ratingCandidate{}, ErrBookNotFound
			case unidentified && rejectUnidentifiedMatch:
				return ratingCandidate{}, fmt.Errorf("%w at library.row: ISBN missing; exact lookup incomplete", ErrCompatibility)
			default:
				return found, nil
			}
		}
		next, err := url.Parse(parsed.Next)
		if err != nil {
			return ratingCandidate{}, fmt.Errorf("%w at book.resolve: invalid next link", ErrCompatibility)
		}
		target = currentURL.ResolveReference(next)
		if !isGoodreadsPage(target.String()) || target.Path != currentURL.Path ||
			!samePaginationScope(currentURL, target) {
			return ratingCandidate{}, fmt.Errorf("%w at book.resolve: next link left shelf", ErrCompatibility)
		}
	}
	return ratingCandidate{}, ErrScanIncomplete
}

func candidateFromRow(
	pageURL *url.URL,
	row *goquery.Selection,
	book domain.Book,
	stage string,
	requireShelfChooser bool,
) (ratingCandidate, error) {
	rowID := row.AttrOr("id", "")
	if !reviewRowID.MatchString(rowID) {
		return ratingCandidate{}, fmt.Errorf("%w at %s: row identity changed", ErrCompatibility, stage)
	}
	if row.Find("td.field.rating div.stars[data-rating] a.star").Length() != 5 {
		return ratingCandidate{}, fmt.Errorf("%w at %s: owner rating control missing", ErrCompatibility, stage)
	}
	if row.Find("td.field.date_read .value").Length() != 1 {
		return ratingCandidate{}, fmt.Errorf("%w at %s: finish date field missing", ErrCompatibility, stage)
	}
	if requireShelfChooser && row.Find("td.field.shelves a.shelfChooserLink").Length() != 1 {
		return ratingCandidate{}, fmt.Errorf("%w at %s: shelf chooser missing", ErrCompatibility, stage)
	}
	reviewLinks := row.Find("a[href*='/review/edit']")
	if reviewLinks.Length() == 0 {
		return ratingCandidate{}, fmt.Errorf("%w at %s: review editor missing", ErrCompatibility, stage)
	}
	reviewURL := ""
	var reviewErr error
	reviewLinks.EachWithBreak(func(_ int, link *goquery.Selection) bool {
		href, ok := link.Attr("href")
		if !ok {
			reviewErr = fmt.Errorf("%w at %s: review editor target missing", ErrCompatibility, stage)
			return false
		}
		resolved, err := resolveReviewEditURL(pageURL, href, stage)
		if err != nil {
			reviewErr = err
			return false
		}
		if reviewURL != "" && resolved != reviewURL {
			reviewErr = fmt.Errorf("%w at %s: review editor is ambiguous", ErrCompatibility, stage)
			return false
		}
		reviewURL = resolved
		return true
	})
	if reviewErr != nil {
		return ratingCandidate{}, reviewErr
	}
	return ratingCandidate{Book: book, PageURL: pageURL.String(), RowID: rowID, ReviewURL: reviewURL}, nil
}

func resolveReviewEditURL(base *url.URL, href, stage string) (string, error) {
	reference, err := url.Parse(href)
	if err != nil {
		return "", fmt.Errorf("%w at %s: invalid review URL", ErrCompatibility, stage)
	}
	target := base.ResolveReference(reference)
	if !isGoodreadsPage(target.String()) || !reviewEditPath.MatchString(target.Path) {
		return "", fmt.Errorf("%w at %s: unsafe review URL", ErrCompatibility, stage)
	}
	target.RawQuery = ""
	target.Fragment = ""
	return target.String(), nil
}

func loadFullReview(ctx context.Context, b browser.Browser, target, stage string) (*string, error) {
	page, err := b.NewPage(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("%s: review page unavailable: %w", stage, err)
	}
	defer page.Close()
	current, err := page.URL(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: review URL unavailable: %w", stage, err)
	}
	currentURL, err := url.Parse(current)
	if err != nil || !isGoodreadsPage(current) || !reviewEditPath.MatchString(currentURL.Path) {
		return nil, fmt.Errorf("%w at %s: unexpected review page", ErrCompatibility, stage)
	}
	raw, err := page.HTML(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: review DOM unavailable: %w", stage, err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%w at %s: invalid review DOM", ErrCompatibility, stage)
	}
	textarea := doc.Find("textarea[name='review[review]'], textarea#review_review_usertext")
	if textarea.Length() != 1 || textarea.First().Closest("form").Length() != 1 {
		return nil, fmt.Errorf("%w at %s: full review field missing", ErrCompatibility, stage)
	}
	review := textarea.First().Text()
	return &review, nil
}

func ratingBookOnPage(ctx context.Context, page browser.Page, candidate ratingCandidate, isbn domain.ISBN) (domain.Book, error) {
	return mutationBookOnPage(ctx, page, candidate, isbn, "mutation.rating")
}

func mutationBookOnPage(
	ctx context.Context,
	page browser.Page,
	candidate ratingCandidate,
	isbn domain.ISBN,
	stage string,
) (domain.Book, error) {
	raw, err := page.HTML(ctx)
	if err != nil {
		return domain.Book{}, fmt.Errorf("%s: target DOM unavailable: %w", stage, err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(raw))
	if err != nil {
		return domain.Book{}, fmt.Errorf("%w at %s: invalid target DOM", ErrCompatibility, stage)
	}
	row := doc.Find("#" + candidate.RowID)
	if row.Length() != 1 {
		return domain.Book{}, fmt.Errorf("%w at %s: target row missing", ErrCompatibility, stage)
	}
	book, err := parseShelfRow(row)
	if err != nil {
		return domain.Book{}, err
	}
	if !exactISBN(book, isbn) || book.BookID != candidate.Book.BookID {
		return domain.Book{}, fmt.Errorf("%w at %s: target identity changed", ErrCompatibility, stage)
	}
	return book, nil
}

func waitForRating(ctx context.Context, page browser.Page, candidate ratingCandidate, isbn domain.ISBN, rating int) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		book, err := ratingBookOnPage(ctx, page, candidate, isbn)
		if err == nil && book.Rating == rating {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// VerifyRatingMutation compares authoritative snapshots taken before and after
// the rating control is used. A nil Review means the source did not expose the
// complete review, so preservation cannot be proved and verification fails.
func VerifyRatingMutation(before, after domain.Book, rating int) (domain.MutationResult, error) {
	if err := domain.ValidateRating(rating); err != nil {
		return domain.MutationResult{}, err
	}
	if before.BookID == "" || after.BookID != before.BookID {
		return domain.MutationResult{}, verificationError("book_id", "changed")
	}
	if before.ISBN10 != "" && after.ISBN10 != before.ISBN10 {
		return domain.MutationResult{}, verificationError("isbn10", "changed")
	}
	if before.ISBN13 != "" && after.ISBN13 != before.ISBN13 {
		return domain.MutationResult{}, verificationError("isbn13", "changed")
	}
	if after.Rating != rating {
		return domain.MutationResult{}, verificationError("rating", "did not match")
	}
	if after.Status != before.Status {
		return domain.MutationResult{}, verificationError("status", "changed")
	}
	if !equalOptionalString(after.DateRead, before.DateRead) {
		return domain.MutationResult{}, verificationError("date_read", "changed")
	}
	if !equalStringSet(after.Bookshelves, before.Bookshelves) {
		return domain.MutationResult{}, verificationError("bookshelves", "changed")
	}
	if before.Review == nil || after.Review == nil {
		return domain.MutationResult{}, verificationError("review", "was unavailable")
	}
	if *after.Review != *before.Review {
		return domain.MutationResult{}, verificationError("review", "changed")
	}
	return domain.MutationResult{
		Operation: "rate",
		Before:    before,
		After:     after,
		Changes:   domain.BookUpdate{Rating: &rating},
		Verified:  true,
	}, nil
}

func equalOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func equalStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, value := range left {
		counts[value]++
	}
	for _, value := range right {
		counts[value]--
		if counts[value] < 0 {
			return false
		}
	}
	return true
}

func verificationError(field, reason string) error {
	return &VerificationError{Field: field, Reason: reason}
}
