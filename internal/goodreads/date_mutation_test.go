package goodreads

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
)

func TestVerifyFinishDateMutation(t *testing.T) {
	before := ratingSnapshot()
	after := ratingSnapshot()
	date := "2026-09-17"
	after.DateRead = &date
	result, err := VerifyFinishDateMutation(before, after, date)
	if err != nil || !result.Verified || result.Operation != "set-finish-date" ||
		result.Changes.DateRead == nil || *result.Changes.DateRead != date {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestSetFinishDateSelectsCompletedSessionAndVerifies(t *testing.T) {
	action, selected := dateActionPage(dateReviewFixture("2026", "9", "12", "2026", "9", "12"), nil)
	result, err := setFinishDateOnPages(
		t,
		ownerStatusFixture(domain.StatusRead, false),
		strings.Replace(ownerStatusFixture(domain.StatusRead, false), "Sep 12, 2026", "Sep 17, 2026", 1),
		action,
		"2026-09-17",
	)
	if err != nil || !result.Verified || result.After.DateRead == nil || *result.After.DateRead != "2026-09-17" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if joined := strings.Join(*selected, ","); strings.Contains(joined, "[start]") {
		t.Fatalf("moved an earlier start date: %v", *selected)
	}
	if !strings.Contains(strings.Join(*selected, ","), "pickerA[end][day]=17") {
		t.Fatalf("did not change the matching end date: %v", *selected)
	}
}

func TestSetFinishDateMovesStartWhenItBlocksFinishDate(t *testing.T) {
	action, selected := dateActionPage(dateReviewFixture("2026", "9", "20", "2026", "9", "20"), nil)
	beforeHTML := strings.Replace(ownerStatusFixture(domain.StatusRead, false), "Sep 12, 2026", "Sep 20, 2026", 1)
	afterHTML := strings.Replace(ownerStatusFixture(domain.StatusRead, false), "Sep 12, 2026", "Sep 18, 2026", 1)
	result, err := setFinishDateOnPages(t, beforeHTML, afterHTML, action, "2026-09-18")
	if err != nil || !result.Verified || result.After.DateRead == nil || *result.After.DateRead != "2026-09-18" {
		t.Fatalf("result=%+v err=%v selected=%v", result, err, *selected)
	}
	joined := strings.Join(*selected, ",")
	startDay := strings.Index(joined, "pickerA[start][day]=18")
	endDay := strings.Index(joined, "pickerA[end][day]=18")
	if startDay < 0 || endDay < 0 || startDay > endDay {
		t.Fatalf("start date was not moved before the finish date: %v", *selected)
	}
}

func TestSetFinishDateFillsBlankStartOnUnsetSession(t *testing.T) {
	action, selected := dateActionPage(unsetDateReviewFixture(), nil)
	unset := strings.Replace(
		ownerStatusFixture(domain.StatusRead, false),
		`<div class="value">Sep 12, 2026</div>`,
		`<div class="value"><div class="date_row"><span class="greyText">Not set</span></div></div>`,
		1,
	)
	afterHTML := strings.Replace(ownerStatusFixture(domain.StatusRead, false), "Sep 12, 2026", "Sep 18, 2026", 1)
	result, err := setFinishDateOnPages(t, unset, afterHTML, action, "2026-09-18")
	if err != nil || !result.Verified {
		t.Fatalf("result=%+v err=%v selected=%v", result, err, *selected)
	}
	joined := strings.Join(*selected, ",")
	if !strings.Contains(joined, "pickerA[start][day]=18") || !strings.Contains(joined, "pickerA[end][day]=18") {
		t.Fatalf("blank session did not receive start and end dates: %v", *selected)
	}
}

func TestVerifyFinishDateClear(t *testing.T) {
	before := ratingSnapshot()
	after := ratingSnapshot()
	after.DateRead = nil
	result, err := VerifyFinishDateClear(before, after)
	if err != nil || !result.Verified || result.Operation != "clear-finish-date" ||
		result.Changes.DateRead == nil || *result.Changes.DateRead != "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestVerifyFinishDateClearFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		alter func(*domain.Book)
		field string
	}{
		{"date", func(after *domain.Book) { after.DateRead = ratingSnapshot().DateRead }, "date_read"},
		{"status", func(after *domain.Book) { after.Status = domain.StatusToRead }, "status"},
		{"rating", func(after *domain.Book) { after.Rating++ }, "rating"},
		{"shelves", func(after *domain.Book) { after.Bookshelves = nil }, "bookshelves"},
		{"review", func(after *domain.Book) {
			changed := "different private text"
			after.Review = &changed
		}, "review"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := ratingSnapshot()
			after := ratingSnapshot()
			after.DateRead = nil
			tc.alter(&after)
			result, err := VerifyFinishDateClear(before, after)
			if !errors.Is(err, ErrVerificationFailed) || result.Verified ||
				!strings.Contains(err.Error(), tc.field) {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestClearFinishDateRemovesDatedSessionAndVerifies(t *testing.T) {
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	const pageURL = "https://www.goodreads.com/review/list/123"
	const reviewURL = "https://www.goodreads.com/review/edit/7"
	before := libraryTestPage(ownerStatusFixture(domain.StatusRead, false), pageURL)
	afterHTML := strings.Replace(
		ownerStatusFixture(domain.StatusRead, false),
		`<div class="value">Sep 12, 2026</div>`,
		`<div class="value"><div class="date_row"><span class="greyText">Not set</span></div><div class="date_row"><span class="greyText">Not set</span></div></div>`,
		1,
	)
	after := libraryTestPage(afterHTML, pageURL)
	reviewHTML := dateReviewFixture("2026", "9", "12", "2026", "9", "12")
	reviewBefore := &fakePage{url: reviewURL, html: reviewHTML}
	action := &fakePage{url: reviewURL, html: reviewHTML}
	reviewAfter := &fakePage{url: reviewURL, html: reviewHTML}
	deleted := false
	action.value = func(selector string) (string, error) {
		switch {
		case strings.Contains(selector, "[delete]"):
			if deleted {
				return "true", nil
			}
			return "false", nil
		case strings.Contains(selector, "pickerA[end][year]"), strings.Contains(selector, "pickerA[start][year]"):
			return "2026", nil
		case strings.Contains(selector, "pickerA[end][month]"), strings.Contains(selector, "pickerA[start][month]"):
			return "9", nil
		case strings.Contains(selector, "pickerA[end][day]"), strings.Contains(selector, "pickerA[start][day]"):
			return "12", nil
		case strings.Contains(selector, "[year]"):
			return "Year", nil
		case strings.Contains(selector, "[month]"):
			return "Month", nil
		case strings.Contains(selector, "[day]"):
			return "Day", nil
		default:
			return "", nil
		}
	}
	action.click = func(selector string) error {
		switch {
		case strings.Contains(selector, "deleteReadingSession"):
			if !strings.Contains(selector, `id="delete-pickerA"`) {
				t.Fatalf("deleted wrong reading session: %q", selector)
			}
			deleted = true
		case strings.Contains(selector, "input[type='submit']"):
			if !deleted {
				t.Fatal("review form submitted before the extra session was removed")
			}
			action.url = "https://www.goodreads.com/review/show/7"
		default:
			t.Fatalf("unexpected click %q", selector)
		}
		return nil
	}
	b := &fakeBrowser{
		pagesQueue: map[string][]browser.Page{
			libraryURL: {before, after},
			reviewURL:  {reviewBefore, action, reviewAfter},
		},
	}
	result, err := ClearFinishDate(context.Background(), b, isbn)
	if err != nil || !result.Verified || result.Before.DateRead == nil || result.After.DateRead != nil {
		t.Fatalf("result=%+v err=%v calls=%v", result, err, b.calls)
	}
}

func TestClearFinishDateRemovesSoleDatedSession(t *testing.T) {
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	const pageURL = "https://www.goodreads.com/review/list/123"
	const reviewURL = "https://www.goodreads.com/review/edit/7"
	before := libraryTestPage(ownerStatusFixture(domain.StatusRead, false), pageURL)
	afterHTML := strings.Replace(
		ownerStatusFixture(domain.StatusRead, false),
		`<div class="value">Sep 12, 2026</div>`,
		`<div class="value"><span class="greyText">Not set</span></div>`,
		1,
	)
	after := libraryTestPage(afterHTML, pageURL)
	reviewHTML := dateReviewForm(readingSessionRow("pickerA", "2026", "9", "12", "2026", "9", "12"))
	reviewBefore := &fakePage{url: reviewURL, html: reviewHTML}
	action := &fakePage{url: reviewURL, html: reviewHTML}
	reviewAfter := &fakePage{url: reviewURL, html: reviewHTML}
	deleted := false
	added := false
	action.htmlFunc = func() string { return action.html }
	action.value = func(selector string) (string, error) {
		switch {
		case strings.Contains(selector, "[delete]"):
			if deleted {
				return "true", nil
			}
			return "false", nil
		case strings.Contains(selector, "[year]"):
			if strings.Contains(selector, "pickerA") {
				return "2026", nil
			}
			return "Year", nil
		case strings.Contains(selector, "[month]"):
			if strings.Contains(selector, "pickerA") {
				return "9", nil
			}
			return "Month", nil
		case strings.Contains(selector, "[day]"):
			if strings.Contains(selector, "pickerA") {
				return "12", nil
			}
			return "Day", nil
		default:
			return "", nil
		}
	}
	action.click = func(selector string) error {
		switch {
		case strings.Contains(selector, `id="add-session"`):
			action.html = dateReviewForm(
				readingSessionRow("pickerA", "2026", "9", "12", "2026", "9", "12") +
					readingSessionRow("pickerB", "Year", "Month", "Day", "Year", "Month", "Day"),
			)
			added = true
		case strings.Contains(selector, "deleteReadingSession"):
			if !added {
				t.Fatal("deleted the sole session before adding a replacement row")
			}
			if !strings.Contains(selector, `id="delete-pickerA"`) {
				t.Fatalf("deleted wrong reading session: %q", selector)
			}
			deleted = true
		case strings.Contains(selector, "input[type='submit']"):
			if !deleted {
				t.Fatal("review form submitted before the session was removed")
			}
			action.url = "https://www.goodreads.com/review/show/7"
		default:
			t.Fatalf("unexpected click %q", selector)
		}
		return nil
	}
	b := &fakeBrowser{
		pagesQueue: map[string][]browser.Page{
			libraryURL: {before, after},
			reviewURL:  {reviewBefore, action, reviewAfter},
		},
	}
	result, err := ClearFinishDate(context.Background(), b, isbn)
	if err != nil || !result.Verified || result.After.DateRead != nil {
		t.Fatalf("result=%+v err=%v calls=%v", result, err, b.calls)
	}
}

func setFinishDateOnPages(t *testing.T, beforeHTML, afterHTML string, action *fakePage, wanted string) (domain.MutationResult, error) {
	t.Helper()
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	date, err := time.Parse("2006-01-02", wanted)
	if err != nil {
		t.Fatal(err)
	}
	const pageURL = "https://www.goodreads.com/review/list/123"
	const reviewURL = "https://www.goodreads.com/review/edit/7"
	reviewHTML := action.html
	b := &fakeBrowser{
		pagesQueue: map[string][]browser.Page{
			libraryURL: {libraryTestPage(beforeHTML, pageURL), libraryTestPage(afterHTML, pageURL)},
			reviewURL: {
				&fakePage{url: reviewURL, html: reviewHTML},
				action,
				&fakePage{url: reviewURL, html: reviewHTML},
			},
		},
	}
	return SetFinishDate(context.Background(), b, isbn, date)
}

func dateActionPage(html string, extraClick func(string) error) (*fakePage, *[]string) {
	selected := []string{}
	values := map[string]string{}
	page := &fakePage{url: "https://www.goodreads.com/review/edit/7", html: html}
	boundUnit := func(selector string) (string, string) {
		for _, bound := range []string{"start", "end"} {
			for _, unit := range []string{"year", "month", "day"} {
				if strings.Contains(selector, "["+bound+"]["+unit+"]") {
					return bound, unit
				}
			}
		}
		return "", ""
	}
	page.value = func(selector string) (string, error) {
		if strings.Contains(selector, "readingEditsMade") {
			return "true", nil
		}
		bound, unit := boundUnit(selector)
		if bound == "" {
			return "", nil
		}
		key := bound + "." + unit
		if value, ok := values[key]; ok {
			return value, nil
		}
		if strings.Contains(selector, "pickerA") {
			switch unit {
			case "year":
				if bound == "start" {
					return startYearFromFixture(html), nil
				}
				return endYearFromFixture(html), nil
			case "month":
				if bound == "start" {
					return startMonthFromFixture(html), nil
				}
				return endMonthFromFixture(html), nil
			case "day":
				if bound == "start" {
					return startDayFromFixture(html), nil
				}
				return endDayFromFixture(html), nil
			}
		}
		return finishDatePlaceholder(unit), nil
	}
	page.selectVal = func(selector, value string) error {
		if !strings.Contains(selector, "pickerA") {
			panic("selected wrong reading session: " + selector)
		}
		bound, unit := boundUnit(selector)
		values[bound+"."+unit] = value
		selected = append(selected, "pickerA["+bound+"]["+unit+"]="+value)
		return nil
	}
	page.click = func(selector string) error {
		if extraClick != nil {
			if err := extraClick(selector); err != nil {
				return err
			}
		}
		if !strings.Contains(selector, "input[type='submit']") {
			panic("unexpected click " + selector)
		}
		page.url = "https://www.goodreads.com/review/show/7"
		return nil
	}
	return page, &selected
}

func dateReviewFixture(startYear, startMonth, startDay, endYear, endMonth, endDay string) string {
	return dateReviewForm(readingSessionRow("pickerA", startYear, startMonth, startDay, endYear, endMonth, endDay) +
		readingSessionRow("pickerB", "Year", "Month", "Day", "Year", "Month", "Day"))
}

func unsetDateReviewFixture() string {
	return dateReviewForm(readingSessionRow("pickerA", "Year", "Month", "Day", "Year", "Month", "Day"))
}

func dateReviewForm(rows string) string {
	return `<form><textarea name="review[review]"></textarea><input type="hidden" name="readingEditsMade" value=""><table class="rereadingDatesTable">` +
		rows +
		`</table><a id="add-session" class="gr-button" href="#">Add Date Read</a><input type="submit" name="next"></form>`
}

func readingSessionRow(prefix, startY, startM, startD, endY, endM, endD string) string {
	return `<tr class="js-readingSessionRow readingSessionRow"><td>` +
		`<input type="hidden" name="` + prefix + `[delete]" value="false">` +
		`<select name="` + prefix + `[start][year]"><option>` + startY + `</option></select>` +
		`<select name="` + prefix + `[start][month]"><option>` + startM + `</option></select>` +
		`<select name="` + prefix + `[start][day]"><option>` + startD + `</option></select>` +
		`<select name="` + prefix + `[end][year]"><option>` + endY + `</option></select>` +
		`<select name="` + prefix + `[end][month]"><option>` + endM + `</option></select>` +
		`<select name="` + prefix + `[end][day]"><option>` + endD + `</option></select>` +
		`<a class="deleteReadingSession" href="#" id="delete-` + prefix + `"></a></td></tr>`
}

func startYearFromFixture(html string) string {
	return fixtureSelectValue(html, "pickerA[start][year]")
}

func startMonthFromFixture(html string) string {
	return fixtureSelectValue(html, "pickerA[start][month]")
}

func startDayFromFixture(html string) string {
	return fixtureSelectValue(html, "pickerA[start][day]")
}

func endYearFromFixture(html string) string {
	return fixtureSelectValue(html, "pickerA[end][year]")
}

func endMonthFromFixture(html string) string {
	return fixtureSelectValue(html, "pickerA[end][month]")
}

func endDayFromFixture(html string) string {
	return fixtureSelectValue(html, "pickerA[end][day]")
}

func fixtureSelectValue(html, name string) string {
	marker := `name="` + name + `"><option>`
	index := strings.Index(html, marker)
	if index < 0 {
		return ""
	}
	rest := html[index+len(marker):]
	end := strings.Index(rest, "</option>")
	if end < 0 {
		return ""
	}
	return rest[:end]
}
