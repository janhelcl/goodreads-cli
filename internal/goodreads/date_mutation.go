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

const (
	calendarPartRetry     = 20
	calendarPartRetryWait = 50 * time.Millisecond
)

var sessionSelectName = regexp.MustCompile(`^(.+)\[(start|end)\]\[(year|month|day)\]$`)

type readingSession struct {
	prefix string
	start  map[string]string
	end    map[string]string
	delete string
}

type calendarParts struct {
	date     string
	nonempty int
}

// SetFinishDate updates the one authoritative completed reading session in
// the ordinary review editor and verifies the exact date from a fresh shelf
// read. The book must already be on the read shelf.
//
// Goodreads rejects a finish date that precedes that session's start date and
// then keeps the previous end date. When the start date is unset or later than
// the requested finish date, this also moves the start date so the end date can
// stick. Start dates are otherwise left alone.
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
	session, err := settableFinishDateSession(ctx, page, doc, before.DateRead)
	if err != nil {
		_ = page.Close()
		return domain.MutationResult{}, err
	}
	start, err := readCalendarParts(ctx, page, session.start)
	if err != nil {
		_ = page.Close()
		return domain.MutationResult{}, err
	}
	if start.nonempty == 0 || start.date > wanted {
		if err := selectCalendarDate(ctx, page, session.start, date); err != nil {
			_ = page.Close()
			return domain.MutationResult{}, err
		}
	}
	if err := selectCalendarDate(ctx, page, session.end, date); err != nil {
		_ = page.Close()
		return domain.MutationResult{}, err
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
		return domain.MutationResult{}, fmt.Errorf("%w at mutation.verify: readback unavailable: %v", ErrMutationAmbiguous, readbackErr)
	}
	after := afterCandidate.Book
	after.Review, readbackErr = loadFullReview(ctx, b, afterCandidate.ReviewURL, finishDateStage)
	if readbackErr != nil {
		return domain.MutationResult{}, fmt.Errorf("%w at mutation.verify: preservation readback unavailable: %v", ErrMutationAmbiguous, readbackErr)
	}
	result, verifyErr := VerifyFinishDateMutation(before, after, wanted)
	if verifyErr == nil {
		return result, nil
	}
	if clickErr != nil || completionErr != nil {
		return domain.MutationResult{}, fmt.Errorf("%w at %s: completion unknown (click=%v completion=%v verify=%v)", ErrMutationAmbiguous, finishDateStage, clickErr, completionErr, verifyErr)
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
	matches := 0
	for _, session := range parseReadingSessions(doc) {
		if len(session.end) != 3 {
			continue
		}
		parts, err := readCalendarParts(ctx, page, session.end)
		if err != nil || parts.nonempty != 3 {
			continue
		}
		if parts.date == wanted {
			matches++
		}
	}
	return matches == 1, nil
}

// ClearFinishDate clears one exact edition's sole rendered finish date through
// the ordinary review editor. It exists to support verified restoration and
// the later finish flow; callers still have to opt into the mutation.
//
// Returning a book to `read` creates an extra completed session whose end date
// Goodreads stamps as today. Clearing the dropdowns leaves that session in
// place and the stamp often returns; removing the extra session is the path
// that actually restores an unset finish date.
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
	session, err := clearableFinishDateSession(ctx, page, doc)
	if err != nil {
		_ = page.Close()
		return domain.MutationResult{}, err
	}
	if !extraReadingSession(doc) {
		if err := addBlankReadingSession(ctx, page, doc); err != nil {
			_ = page.Close()
			return domain.MutationResult{}, err
		}
		raw, err = page.HTML(ctx)
		if err != nil {
			_ = page.Close()
			return domain.MutationResult{}, fmt.Errorf("%s: review DOM unavailable: %w", finishDateStage, err)
		}
		doc, err = goquery.NewDocumentFromReader(strings.NewReader(raw))
		if err != nil {
			_ = page.Close()
			return domain.MutationResult{}, fmt.Errorf("%w at %s: invalid review DOM", ErrCompatibility, finishDateStage)
		}
		session, err = clearableFinishDateSession(ctx, page, doc)
		if err != nil {
			_ = page.Close()
			return domain.MutationResult{}, err
		}
	}
	if err := deleteReadingSession(ctx, page, doc, session); err != nil {
		_ = page.Close()
		return domain.MutationResult{}, err
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

func parseReadingSessions(doc *goquery.Document) []*readingSession {
	byPrefix := map[string]*readingSession{}
	var order []string
	doc.Find("select[name]").Each(func(_ int, selection *goquery.Selection) {
		name := selection.AttrOr("name", "")
		match := sessionSelectName.FindStringSubmatch(name)
		if len(match) != 4 {
			return
		}
		prefix, bound, unit := match[1], match[2], match[3]
		session, ok := byPrefix[prefix]
		if !ok {
			session = &readingSession{
				prefix: prefix,
				start:  map[string]string{},
				end:    map[string]string{},
				delete: fmt.Sprintf(`input[name="%s[delete]"]`, prefix),
			}
			byPrefix[prefix] = session
			order = append(order, prefix)
		}
		selector := fmt.Sprintf(`select[name="%s"]`, name)
		if bound == "start" {
			session.start[unit] = selector
		} else {
			session.end[unit] = selector
		}
	})
	sessions := make([]*readingSession, 0, len(order))
	for _, prefix := range order {
		sessions = append(sessions, byPrefix[prefix])
	}
	return sessions
}

func extraReadingSession(doc *goquery.Document) bool {
	return doc.Find("table.rereadingDatesTable tr.js-readingSessionRow").Length() > 1
}

func addBlankReadingSession(ctx context.Context, page browser.Page, doc *goquery.Document) error {
	selector, err := addReadingSessionSelector(doc)
	if err != nil {
		return err
	}
	if err := page.Click(ctx, selector); err != nil {
		return fmt.Errorf("%w at %s: add-session control unavailable", ErrCompatibility, finishDateStage)
	}
	return selectUntil(ctx, func() error {
		raw, htmlErr := page.HTML(ctx)
		if htmlErr != nil {
			return fmt.Errorf("%s: review DOM unavailable: %w", finishDateStage, htmlErr)
		}
		current, parseErr := goquery.NewDocumentFromReader(strings.NewReader(raw))
		if parseErr != nil || !extraReadingSession(current) {
			return fmt.Errorf("%w at %s: extra reading session was not created", ErrCompatibility, finishDateStage)
		}
		return nil
	}, calendarPartRetry)
}

func addReadingSessionSelector(doc *goquery.Document) (string, error) {
	form := doc.Find("form:has(textarea[name='review[review]'])")
	if form.Length() != 1 {
		return "", fmt.Errorf("%w at %s: review submit control changed", ErrCompatibility, finishDateStage)
	}
	var id string
	matches := 0
	form.Find("a.gr-button").Each(func(_ int, link *goquery.Selection) {
		class := link.AttrOr("class", "")
		if strings.Contains(class, "Today") || strings.Contains(class, "gr-button--small") {
			return
		}
		candidate := strings.TrimSpace(link.AttrOr("id", ""))
		if candidate == "" || strings.ContainsAny(candidate, `"\]`) {
			return
		}
		matches++
		id = candidate
	})
	if matches != 1 {
		return "", fmt.Errorf("%w at %s: add-session control changed", ErrCompatibility, finishDateStage)
	}
	selector := fmt.Sprintf(`a.gr-button[id="%s"]`, id)
	if doc.Find(selector).Length() != 1 {
		return "", fmt.Errorf("%w at %s: add-session control changed", ErrCompatibility, finishDateStage)
	}
	return selector, nil
}

func deleteReadingSession(
	ctx context.Context,
	page browser.Page,
	doc *goquery.Document,
	session *readingSession,
) error {
	deleteSelector, err := deleteLinkSelector(doc, session)
	if err != nil {
		return err
	}
	if err := page.ClickDOM(ctx, deleteSelector); err != nil {
		return fmt.Errorf("%w at %s: reading-session removal unavailable", ErrCompatibility, finishDateStage)
	}
	return selectUntil(ctx, func() error {
		deleteValue, err := page.Value(ctx, session.delete)
		if err == nil && (deleteValue == "true" || deleteValue == "1") {
			return nil
		}
		return fmt.Errorf("%w at %s: reading-session removal was not armed", ErrCompatibility, finishDateStage)
	}, calendarPartRetry)
}

func sessionRowIndex(doc *goquery.Document, session *readingSession) int {
	for index, candidate := range parseReadingSessions(doc) {
		if candidate.prefix == session.prefix {
			return index + 1
		}
	}
	return 0
}

func deleteLinkSelector(doc *goquery.Document, session *readingSession) (string, error) {
	index := sessionRowIndex(doc, session)
	rows := doc.Find("table.rereadingDatesTable tr.js-readingSessionRow")
	if index == 0 || rows.Length() < index {
		return "", fmt.Errorf("%w at %s: reading-session removal control changed", ErrCompatibility, finishDateStage)
	}
	link := rows.Eq(index - 1).Find("a.deleteReadingSession")
	if link.Length() != 1 {
		return "", fmt.Errorf("%w at %s: reading-session removal control changed", ErrCompatibility, finishDateStage)
	}
	id := strings.TrimSpace(link.AttrOr("id", ""))
	if id != "" {
		if strings.ContainsAny(id, `"\]`) {
			return "", fmt.Errorf("%w at %s: reading-session removal control changed", ErrCompatibility, finishDateStage)
		}
		selector := fmt.Sprintf(`a.deleteReadingSession[id="%s"]`, id)
		if doc.Find(selector).Length() != 1 {
			return "", fmt.Errorf("%w at %s: reading-session removal control changed", ErrCompatibility, finishDateStage)
		}
		return selector, nil
	}
	unique := "table.rereadingDatesTable a.deleteReadingSession"
	if rows.Length() == 1 && doc.Find(unique).Length() == 1 {
		return unique, nil
	}
	return "", fmt.Errorf("%w at %s: reading-session removal control changed", ErrCompatibility, finishDateStage)
}

func selectUntil(ctx context.Context, attempt func() error, retries int) error {
	var last error
	for i := 0; i <= retries; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		last = attempt()
		if last == nil {
			return nil
		}
		if i == retries {
			return last
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(calendarPartRetryWait):
		}
	}
	return last
}

func requireReviewSubmit(doc *goquery.Document) error {
	form := doc.Find("form:has(textarea[name='review[review]'])")
	if form.Length() != 1 || form.Find("input[type='submit'][name='next']").Length() != 1 {
		return fmt.Errorf("%w at %s: review submit control changed", ErrCompatibility, finishDateStage)
	}
	return nil
}

func settableFinishDateSession(
	ctx context.Context,
	page browser.Page,
	doc *goquery.Document,
	current *string,
) (*readingSession, error) {
	var found *readingSession
	for _, session := range parseReadingSessions(doc) {
		if len(session.end) != 3 || len(session.start) != 3 {
			continue
		}
		end, err := readCalendarParts(ctx, page, session.end)
		if err != nil {
			return nil, err
		}
		if end.nonempty != 0 && end.nonempty != 3 {
			return nil, fmt.Errorf("%w at %s: partial finish date", ErrCompatibility, finishDateStage)
		}
		matches := current == nil && end.nonempty == 0
		if current != nil && end.nonempty == 3 {
			matches = end.date == *current
		}
		if matches {
			if found != nil {
				return nil, fmt.Errorf("%w at %s: finish date session is ambiguous", ErrCompatibility, finishDateStage)
			}
			found = session
		}
	}
	if found == nil {
		return nil, fmt.Errorf("%w at %s: expected one settable finish date", ErrCompatibility, finishDateStage)
	}
	if err := requireReviewSubmit(doc); err != nil {
		return nil, err
	}
	return found, nil
}

func clearableFinishDateSession(
	ctx context.Context,
	page browser.Page,
	doc *goquery.Document,
) (*readingSession, error) {
	var found *readingSession
	for _, session := range parseReadingSessions(doc) {
		if len(session.end) != 3 {
			continue
		}
		end, err := readCalendarParts(ctx, page, session.end)
		if err != nil {
			return nil, err
		}
		switch end.nonempty {
		case 0:
			continue
		case 3:
			if found != nil {
				return nil, fmt.Errorf("%w at %s: multiple finish dates are not clearable", ErrCompatibility, finishDateStage)
			}
			found = session
		default:
			return nil, fmt.Errorf("%w at %s: partial finish date", ErrCompatibility, finishDateStage)
		}
	}
	if found == nil {
		return nil, fmt.Errorf("%w at %s: expected one clearable finish date", ErrCompatibility, finishDateStage)
	}
	if err := requireReviewSubmit(doc); err != nil {
		return nil, err
	}
	return found, nil
}

func readCalendarParts(
	ctx context.Context,
	page browser.Page,
	selectors map[string]string,
) (calendarParts, error) {
	values := map[string]string{}
	nonempty := 0
	for _, unit := range []string{"year", "month", "day"} {
		value, err := page.Value(ctx, selectors[unit])
		if err != nil {
			return calendarParts{}, fmt.Errorf("%w at %s: finish date value unavailable", ErrCompatibility, finishDateStage)
		}
		values[unit] = value
		if value != finishDatePlaceholder(unit) {
			nonempty++
		}
	}
	if nonempty == 0 {
		return calendarParts{}, nil
	}
	if nonempty != 3 {
		return calendarParts{nonempty: nonempty}, nil
	}
	year, yearErr := strconv.Atoi(values["year"])
	month, monthErr := strconv.Atoi(values["month"])
	day, dayErr := strconv.Atoi(values["day"])
	if yearErr != nil || monthErr != nil || dayErr != nil {
		return calendarParts{}, fmt.Errorf("%w at %s: invalid finish date values", ErrCompatibility, finishDateStage)
	}
	return calendarParts{
		date:     fmt.Sprintf("%04d-%02d-%02d", year, month, day),
		nonempty: 3,
	}, nil
}

func selectCalendarDate(
	ctx context.Context,
	page browser.Page,
	selectors map[string]string,
	date time.Time,
) error {
	values := map[string]string{
		"year":  fmt.Sprintf("%d", date.Year()),
		"month": fmt.Sprintf("%d", int(date.Month())),
		"day":   fmt.Sprintf("%d", date.Day()),
	}
	for index, unit := range []string{"year", "month", "day"} {
		retries := 0
		if index > 0 {
			retries = calendarPartRetry
		}
		if err := selectCalendarPart(ctx, page, selectors[unit], values[unit], unit, retries); err != nil {
			return err
		}
	}
	return nil
}

func selectCalendarPart(
	ctx context.Context,
	page browser.Page,
	selector, value, unit string,
	retries int,
) error {
	var last error
	for attempt := 0; attempt <= retries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := page.SelectValue(ctx, selector, value)
		if err == nil {
			got, valueErr := page.Value(ctx, selector)
			if valueErr == nil && got == value {
				return nil
			}
			last = fmt.Errorf("%w at %s: %s selection did not stick", ErrCompatibility, finishDateStage, unit)
		} else {
			last = fmt.Errorf("%w at %s: %s option unavailable", ErrCompatibility, finishDateStage, unit)
		}
		if attempt == retries {
			return last
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(calendarPartRetryWait):
		}
	}
	return last
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
