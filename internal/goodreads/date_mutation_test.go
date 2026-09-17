package goodreads

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
)

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
	reviewBefore := &fakePage{url: reviewURL, html: dateReviewFixture()}
	action := &fakePage{url: reviewURL, html: dateReviewFixture()}
	reviewAfter := &fakePage{url: reviewURL, html: dateReviewFixture()}
	deleteArmed := false
	action.value = func(selector string) (string, error) {
		switch {
		case strings.Contains(selector, "pickerA") && strings.Contains(selector, "[year]"):
			return "2026", nil
		case strings.Contains(selector, "pickerA") && strings.Contains(selector, "[month]"):
			return "9", nil
		case strings.Contains(selector, "pickerA") && strings.Contains(selector, "[day]"):
			return "12", nil
		case strings.Contains(selector, "pickerA") && strings.Contains(selector, "[delete]"):
			if deleteArmed {
				return "true", nil
			}
			return "false", nil
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
			deleteArmed = true
		case strings.Contains(selector, "input[type='submit']"):
			if !deleteArmed {
				t.Fatal("review form submitted before removal was armed")
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
	return `<form><textarea name="review[review]"></textarea><table>` +
		session("pickerA", "2026", "9", "12") +
		session("pickerB", "Year", "Month", "Day") +
		`</table><input type="submit" name="next"></form>`
}
