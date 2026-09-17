package goodreads

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
)

const finishDateStage = "mutation.finish-date"

var endDateSelectName = regexp.MustCompile(`^(.+)\[end\]\[(year|month|day)\]$`)

// SetFinishDate updates the one authoritative completed reading session in
// the ordinary review editor and verifies the exact date from a fresh shelf
// read. The book must already be on the read shelf.
func SetFinishDate(
	ctx context.Context,
	b browser.Browser,
	isbn domain.ISBN,
	date time.Time,
) (domain.MutationResult, error) {
	if err := domain.ValidateDate(date); err != nil {
		return domain.MutationResult{}, err
	}
	wanted := date.Format("2006-01-02")
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
	if before.Status != domain.StatusRead {
		return domain.MutationResult{}, fmt.Errorf("%w at %s: book is not on the read shelf", ErrCompatibility, finishDateStage)
	}
	if before.DateRead != nil && *before.DateRead == wanted {
		return VerifyFinishDateMutation(before, before, wanted)
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
	selectors, err := settableFinishDateSelectors(ctx, page, doc, before.DateRead)
	if err != nil {
		_ = page.Close()
		return domain.MutationResult{}, err
	}
	values := map[string]string{
		"year":  fmt.Sprintf("%d", date.Year()),
		"month": fmt.Sprintf("%d", int(date.Month())),
		"day":   fmt.Sprintf("%d", date.Day()),
	}
	for _, unit := range []string{"year", "month", "day"} {
		if err := page.SelectValue(ctx, selectors[unit], values[unit]); err != nil {
			_ = page.Close()
			return domain.MutationResult{}, fmt.Errorf("%w at %s: %s option unavailable", ErrCompatibility, finishDateStage, unit)
		}
		value, err := page.Value(ctx, selectors[unit])
		if err != nil || value != values[unit] {
			_ = page.Close()
			return domain.MutationResult{}, fmt.Errorf("%w at %s: %s selection did not stick", ErrCompatibility, finishDateStage, unit)
		}
	}
	editsMade, err := page.Value(ctx, "input[name='readingEditsMade']")
	if err != nil || editsMade == "" || editsMade == "false" || editsMade == "0" {
		_ = page.Close()
		return domain.MutationResult{}, fmt.Errorf("%w at %s: visible date changes were not armed", ErrCompatibility, finishDateStage)
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
	result, verifyErr := VerifyFinishDateMutation(before, after, wanted)
	if verifyErr == nil {
		return result, nil
	}
	if clickErr != nil || completionErr != nil {
		return domain.MutationResult{}, fmt.Errorf("%w at %s: completion unknown", ErrMutationAmbiguous, finishDateStage)
	}
	retained, evidenceErr := editorRetainedFinishDate(ctx, b, afterCandidate.ReviewURL, wanted)
	if evidenceErr == nil && retained {
		return domain.MutationResult{}, fmt.Errorf("%w: date_read shelf readback differed from the saved review editor", ErrVerificationFailed)
	}
	return domain.MutationResult{}, verifyErr
}

func editorRetainedFinishDate(ctx context.Context, b browser.Browser, target, wanted string) (bool, error) {
	page, err := b.NewPage(ctx, target)
	if err != nil {
		return false, err
	}
	defer page.Close()
	raw, err := page.HTML(ctx)
	if err != nil {
		return false, err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(raw))
	if err != nil {
		return false, err
	}
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
	matches := 0
	for _, selectors := range groups {
		if len(selectors) != 3 {
			continue
		}
		values := make([]int, 3)
		complete := true
		for index, unit := range []string{"year", "month", "day"} {
			value, valueErr := page.Value(ctx, selectors[unit])
			if valueErr != nil || value == finishDatePlaceholder(unit) {
				complete = false
				break
			}
			values[index], valueErr = strconv.Atoi(value)
			if valueErr != nil {
				complete = false
				break
			}
		}
		if complete && fmt.Sprintf("%04d-%02d-%02d", values[0], values[1], values[2]) == wanted {
			matches++
		}
	}
	return matches == 1, nil
}

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
	for _, unit := range []string{"day", "month", "year"} {
		placeholder := finishDatePlaceholder(unit)
		if err := page.SelectValue(ctx, selectors[unit], placeholder); err != nil {
			_ = page.Close()
			return domain.MutationResult{}, fmt.Errorf("%w at %s: %s clear option unavailable", ErrCompatibility, finishDateStage, unit)
		}
		value, err := page.Value(ctx, selectors[unit])
		if err != nil || value != placeholder {
			_ = page.Close()
			return domain.MutationResult{}, fmt.Errorf("%w at %s: %s clear did not stick", ErrCompatibility, finishDateStage, unit)
		}
	}
	editsMade, err := page.Value(ctx, "input[name='readingEditsMade']")
	if err != nil || editsMade == "" || editsMade == "false" || editsMade == "0" {
		_ = page.Close()
		return domain.MutationResult{}, fmt.Errorf("%w at %s: visible date clearing was not armed", ErrCompatibility, finishDateStage)
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
	for _, selectors := range groups {
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

func settableFinishDateSelectors(
	ctx context.Context,
	page browser.Page,
	doc *goquery.Document,
	current *string,
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
	for _, selectors := range groups {
		if len(selectors) != 3 {
			continue
		}
		values := map[string]string{}
		nonempty := 0
		for _, unit := range []string{"year", "month", "day"} {
			value, err := page.Value(ctx, selectors[unit])
			if err != nil {
				return nil, fmt.Errorf("%w at %s: finish date value unavailable", ErrCompatibility, finishDateStage)
			}
			values[unit] = value
			if value != finishDatePlaceholder(unit) {
				nonempty++
			}
		}
		if nonempty != 0 && nonempty != 3 {
			return nil, fmt.Errorf("%w at %s: partial finish date", ErrCompatibility, finishDateStage)
		}
		matches := current == nil && nonempty == 0
		if current != nil && nonempty == 3 {
			year, yearErr := strconv.Atoi(values["year"])
			month, monthErr := strconv.Atoi(values["month"])
			day, dayErr := strconv.Atoi(values["day"])
			if yearErr != nil || monthErr != nil || dayErr != nil {
				return nil, fmt.Errorf("%w at %s: invalid finish date values", ErrCompatibility, finishDateStage)
			}
			matches = fmt.Sprintf("%04d-%02d-%02d", year, month, day) == *current
		}
		if matches {
			if found != nil {
				return nil, fmt.Errorf("%w at %s: finish date session is ambiguous", ErrCompatibility, finishDateStage)
			}
			found = selectors
		}
	}
	if found == nil {
		return nil, fmt.Errorf("%w at %s: expected one settable finish date", ErrCompatibility, finishDateStage)
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

func VerifyFinishDateMutation(before, after domain.Book, date string) (domain.MutationResult, error) {
	if before.BookID == "" || after.BookID != before.BookID {
		return domain.MutationResult{}, verificationError("book_id", "changed")
	}
	if before.ISBN10 != "" && after.ISBN10 != before.ISBN10 {
		return domain.MutationResult{}, verificationError("isbn10", "changed")
	}
	if before.ISBN13 != "" && after.ISBN13 != before.ISBN13 {
		return domain.MutationResult{}, verificationError("isbn13", "changed")
	}
	if after.DateRead == nil || *after.DateRead != date {
		return domain.MutationResult{}, verificationError("date_read", "did not match")
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
	return domain.MutationResult{
		Operation: "set-finish-date",
		Before:    before,
		After:     after,
		Changes:   domain.BookUpdate{DateRead: &date},
		Verified:  true,
	}, nil
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
