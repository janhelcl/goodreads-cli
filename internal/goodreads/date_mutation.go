package goodreads

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
)

const finishDateStage = "mutation.finish-date"

var endDateSelectName = regexp.MustCompile(`^(.+)\[end\]\[(year|month|day)\]$`)

// ClearFinishDate clears one exact edition's sole rendered finish date through
// the ordinary review editor. It exists to support verified restoration and
// the later finish flow; callers still have to opt into the mutation.
func ClearFinishDate(
	ctx context.Context,
	b browser.Browser,
	isbn domain.ISBN,
) (domain.MutationResult, error) {
	state, err := Status(ctx, b)
	if err != nil {
		return domain.MutationResult{}, err
	}
	if !state.Connected {
		return domain.MutationResult{}, ErrSessionExpired
	}
	candidate, err := findMutationCandidate(ctx, b, isbn, finishDateStage, false)
	if err != nil {
		return domain.MutationResult{}, err
	}
	before := candidate.Book
	before.Review, err = loadFullReview(ctx, b, candidate.ReviewURL, finishDateStage)
	if err != nil {
		return domain.MutationResult{}, err
	}
	if before.DateRead == nil {
		return VerifyFinishDateClear(before, before)
	}

	page, err := b.NewPage(ctx, candidate.ReviewURL)
	if err != nil {
		return domain.MutationResult{}, fmt.Errorf("%s: review page unavailable: %w", finishDateStage, err)
	}
	raw, err := page.HTML(ctx)
	if err != nil {
		_ = page.Close()
		return domain.MutationResult{}, fmt.Errorf("%s: review DOM unavailable: %w", finishDateStage, err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(raw))
	if err != nil {
		_ = page.Close()
		return domain.MutationResult{}, fmt.Errorf("%w at %s: invalid review DOM", ErrCompatibility, finishDateStage)
	}
	selectors, err := clearableFinishDateSelectors(ctx, page, doc)
	if err != nil {
		_ = page.Close()
		return domain.MutationResult{}, err
	}
	deleteSelector := "tr.js-readingSessionRow:has(" + selectors["year"] + ") a.deleteReadingSession"
	if doc.Find(deleteSelector).Length() != 1 {
		_ = page.Close()
		return domain.MutationResult{}, fmt.Errorf("%w at %s: reading-session removal control changed", ErrCompatibility, finishDateStage)
	}
	if err := page.Click(ctx, deleteSelector); err != nil {
		_ = page.Close()
		return domain.MutationResult{}, fmt.Errorf("%w at %s: reading-session removal unavailable", ErrCompatibility, finishDateStage)
	}
	deleteValue, err := page.Value(ctx, selectors["delete"])
	if err != nil || (deleteValue != "true" && deleteValue != "1") {
		_ = page.Close()
		return domain.MutationResult{}, fmt.Errorf("%w at %s: reading-session removal was not armed", ErrCompatibility, finishDateStage)
	}
	submitSelector := "form:has(textarea[name='review[review]']) input[type='submit'][name='next']"
	beforeSubmitURL, _ := page.URL(ctx)
	clickErr := page.Click(ctx, submitSelector)
	var completionErr error
	if clickErr == nil {
		completionCtx, cancelCompletion := context.WithTimeout(ctx, 10*time.Second)
		completionErr = waitForReviewSubmission(completionCtx, page, beforeSubmitURL)
		cancelCompletion()
	}
	_ = page.Close()

	afterCandidate, readbackErr := findMutationCandidate(ctx, b, isbn, finishDateStage, false)
	if readbackErr != nil {
		return domain.MutationResult{}, fmt.Errorf("%w at mutation.verify: readback unavailable", ErrMutationAmbiguous)
	}
	after := afterCandidate.Book
	after.Review, readbackErr = loadFullReview(ctx, b, afterCandidate.ReviewURL, finishDateStage)
	if readbackErr != nil {
		return domain.MutationResult{}, fmt.Errorf("%w at mutation.verify: preservation readback unavailable", ErrMutationAmbiguous)
	}
	result, verifyErr := VerifyFinishDateClear(before, after)
	if verifyErr == nil {
		return result, nil
	}
	if clickErr != nil || completionErr != nil {
		return domain.MutationResult{}, fmt.Errorf("%w at %s: completion unknown", ErrMutationAmbiguous, finishDateStage)
	}
	return domain.MutationResult{}, verifyErr
}

func waitForReviewSubmission(ctx context.Context, page browser.Page, beforeURL string) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		current, urlErr := page.URL(ctx)
		if urlErr == nil && current != beforeURL {
			return nil
		}
		raw, htmlErr := page.HTML(ctx)
		if htmlErr == nil {
			doc, parseErr := goquery.NewDocumentFromReader(strings.NewReader(raw))
			if parseErr == nil && doc.Find("form:has(textarea[name='review[review]'])").Length() == 0 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func clearableFinishDateSelectors(
	ctx context.Context,
	page browser.Page,
	doc *goquery.Document,
) (map[string]string, error) {
	groups := map[string]map[string]string{}
	doc.Find("select[name*='[end]']").Each(func(_ int, selection *goquery.Selection) {
		name := selection.AttrOr("name", "")
		match := endDateSelectName.FindStringSubmatch(name)
		if len(match) != 3 {
			return
		}
		if groups[match[1]] == nil {
			groups[match[1]] = map[string]string{}
		}
		groups[match[1]][match[2]] = fmt.Sprintf(`select[name="%s"]`, name)
	})
	var found map[string]string
	for prefix, selectors := range groups {
		if len(selectors) != 3 {
			continue
		}
		nonempty := 0
		for _, unit := range []string{"year", "month", "day"} {
			value, err := page.Value(ctx, selectors[unit])
			if err != nil {
				return nil, fmt.Errorf("%w at %s: finish date value unavailable", ErrCompatibility, finishDateStage)
			}
			if value != finishDatePlaceholder(unit) {
				nonempty++
			}
		}
		switch nonempty {
		case 0:
			continue
		case 3:
			if found != nil {
				return nil, fmt.Errorf("%w at %s: multiple finish dates are not clearable", ErrCompatibility, finishDateStage)
			}
			selectors["delete"] = fmt.Sprintf(`input[name="%s[delete]"]`, prefix)
			found = selectors
		default:
			return nil, fmt.Errorf("%w at %s: partial finish date", ErrCompatibility, finishDateStage)
		}
	}
	if found == nil {
		return nil, fmt.Errorf("%w at %s: expected one clearable finish date", ErrCompatibility, finishDateStage)
	}
	form := doc.Find("form:has(textarea[name='review[review]'])")
	if form.Length() != 1 || form.Find("input[type='submit'][name='next']").Length() != 1 {
		return nil, fmt.Errorf("%w at %s: review submit control changed", ErrCompatibility, finishDateStage)
	}
	return found, nil
}

func finishDatePlaceholder(unit string) string {
	switch unit {
	case "year":
		return "Year"
	case "month":
		return "Month"
	case "day":
		return "Day"
	default:
		return ""
	}
}

func VerifyFinishDateClear(before, after domain.Book) (domain.MutationResult, error) {
	if before.BookID == "" || after.BookID != before.BookID {
		return domain.MutationResult{}, verificationError("book_id", "changed")
	}
	if before.ISBN10 != "" && after.ISBN10 != before.ISBN10 {
		return domain.MutationResult{}, verificationError("isbn10", "changed")
	}
	if before.ISBN13 != "" && after.ISBN13 != before.ISBN13 {
		return domain.MutationResult{}, verificationError("isbn13", "changed")
	}
	if after.DateRead != nil {
		return domain.MutationResult{}, verificationError("date_read", "was not cleared")
	}
	if after.Status != before.Status {
		return domain.MutationResult{}, verificationError("status", "changed")
	}
	if after.Rating != before.Rating {
		return domain.MutationResult{}, verificationError("rating", "changed")
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
	empty := ""
	return domain.MutationResult{
		Operation: "clear-finish-date",
		Before:    before,
		After:     after,
		Changes:   domain.BookUpdate{DateRead: &empty},
		Verified:  true,
	}, nil
}
