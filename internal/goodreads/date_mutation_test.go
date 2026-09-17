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
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	date, err := time.Parse("2006-01-02", "2026-09-17")
	if err != nil {
		t.Fatal(err)
	}
	const pageURL = "https://www.goodreads.com/review/list/123"
	const reviewURL = "https://www.goodreads.com/review/edit/7"
	before := libraryTestPage(ownerStatusFixture(domain.StatusRead, false), pageURL)
	afterHTML := strings.Replace(ownerStatusFixture(domain.StatusRead, false), "Sep 12, 2026", "Sep 17, 2026", 1)
	after := libraryTestPage(afterHTML, pageURL)
	reviewBefore := &fakePage{url: reviewURL, html: dateReviewFixture()}
	action := &fakePage{url: reviewURL, html: dateReviewFixture()}
	reviewAfter := &fakePage{url: reviewURL, html: dateReviewFixture()}
	values := map[string]string{
		"year": "2026", "month": "9", "day": "12",
	}
	unitFor := func(selector string) string {
		for _, unit := range []string{"year", "month", "day"} {
			if strings.Contains(selector, "[end]["+unit+"]") {
				return unit
			}
		}
		return ""
	}
	action.value = func(selector string) (string, error) {
		if strings.Contains(selector, "readingEditsMade") {
			return "true", nil
		}
		unit := unitFor(selector)
		if strings.Contains(selector, "pickerA") {
			return values[unit], nil
		}
		if unit != "" {
			return finishDatePlaceholder(unit), nil
		}
		return "", nil
	}
	action.selectVal = func(selector, value string) error {
		if !strings.Contains(selector, "pickerA") {
			t.Fatalf("selected wrong reading session: %q", selector)
		}
		values[unitFor(selector)] = value
		return nil
	}
	action.click = func(selector string) error {
		if !strings.Contains(selector, "input[type='submit']") {
			t.Fatalf("unexpected click %q", selector)
		}
		action.url = "https://www.goodreads.com/review/show/7"
		return nil
	}
	b := &fakeBrowser{
		pagesQueue: map[string][]browser.Page{
			libraryURL: {privatePage(), before, after},
			reviewURL:  {reviewBefore, action, reviewAfter},
		},
	}
	result, err := SetFinishDate(context.Background(), b, isbn, date)
	if err != nil || !result.Verified || result.After.DateRead == nil || *result.After.DateRead != "2026-09-17" {
		t.Fatalf("result=%+v err=%v calls=%v", result, err, b.calls)
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

func TestClearFinishDateClearsDatedSessionAndVerifies(t *testing.T) {
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
	reviewBefore := &fakePage{url: reviewURL, html: dateReviewFixture()}
	action := &fakePage{url: reviewURL, html: dateReviewFixture()}
	reviewAfter := &fakePage{url: reviewURL, html: dateReviewFixture()}
	values := map[string]string{"year": "2026", "month": "9", "day": "12"}
	editsMade := false
	unitFor := func(selector string) string {
		for _, unit := range []string{"year", "month", "day"} {
			if strings.Contains(selector, "[end]["+unit+"]") {
				return unit
			}
		}
		return ""
	}
	action.value = func(selector string) (string, error) {
		switch {
		case strings.Contains(selector, "readingEditsMade"):
			if editsMade {
				return "true", nil
			}
			return "", nil
		case strings.Contains(selector, "pickerA"):
			return values[unitFor(selector)], nil
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
	action.selectVal = func(selector, value string) error {
		if !strings.Contains(selector, "pickerA") {
			t.Fatalf("cleared wrong reading session: %q", selector)
		}
		values[unitFor(selector)] = value
		editsMade = true
		return nil
	}
	action.click = func(selector string) error {
		switch {
		case strings.Contains(selector, "input[type='submit']"):
			if values["year"] != "Year" || values["month"] != "Month" || values["day"] != "Day" {
				t.Fatal("review form submitted before date fields were cleared")
			}
			action.url = "https://www.goodreads.com/review/show/7"
		default:
			t.Fatalf("unexpected click %q", selector)
		}
		return nil
	}
	b := &fakeBrowser{
		pagesQueue: map[string][]browser.Page{
			libraryURL: {privatePage(), before, after},
			reviewURL:  {reviewBefore, action, reviewAfter},
		},
	}
	result, err := ClearFinishDate(context.Background(), b, isbn)
	if err != nil || !result.Verified || result.Before.DateRead == nil || result.After.DateRead != nil {
		t.Fatalf("result=%+v err=%v calls=%v", result, err, b.calls)
	}
}

func dateReviewFixture() string {
	session := func(prefix, year, month, day string) string {
		return `<tr class="js-readingSessionRow readingSessionRow"><td>` +
			`<input type="hidden" name="` + prefix + `[delete]" value="false">` +
			`<select name="` + prefix + `[end][year]"><option>Year</option><option>` + year + `</option></select>` +
			`<select name="` + prefix + `[end][month]"><option>Month</option><option>` + month + `</option></select>` +
			`<select name="` + prefix + `[end][day]"><option>Day</option><option>` + day + `</option></select>` +
			`<a class="deleteReadingSession" href="#"></a></td></tr>`
	}
	return `<form><textarea name="review[review]"></textarea><input type="hidden" name="readingEditsMade" value=""><table>` +
		session("pickerA", "2026", "9", "12") +
		session("pickerB", "Year", "Month", "Day") +
		`</table><input type="submit" name="next"></form>`
}
