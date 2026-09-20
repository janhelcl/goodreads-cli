//go:build liveprobe

// ratingprobe inspects the owner rating/review controls for one exact ISBN.
// It does not click, submit, or print book/review/account values.
package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
	"github.com/janhelcl/goodreads-cli/internal/profile"
)

func main() {
	if len(os.Args) != 2 {
		fail("expected one exact ISBN")
	}
	isbn, err := domain.NormalizeISBN(os.Args[1])
	if err != nil {
		fail("invalid ISBN")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	paths, err := profile.DefaultPaths()
	if err != nil {
		fail("profile unavailable")
	}
	lock, err := paths.Acquire(ctx)
	if err != nil {
		fail("profile busy")
	}
	defer lock.Release()
	b, err := (browser.RodFactory{}).Launch(ctx, browser.LaunchOptions{ProfileDir: paths.Browser, Headless: true})
	if err != nil {
		fail("browser unavailable")
	}
	defer b.Close()
	page, err := b.NewPage(ctx, "https://www.goodreads.com/review/list")
	if err != nil {
		fail("library page unavailable")
	}
	raw, err := page.HTML(ctx)
	_ = page.Close()
	if err != nil {
		fail("library DOM unavailable")
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(raw))
	if err != nil {
		fail("library DOM invalid")
	}
	var matches []*goquery.Selection
	doc.Find("#booksBody > tr").Each(func(_ int, row *goquery.Selection) {
		if rowMatchesISBN(row, isbn) {
			matches = append(matches, row)
		}
	})
	if len(matches) != 1 {
		fail("exactly one rendered ISBN match required")
	}
	row := matches[0]
	stars := row.Find("td.field.rating div.stars[data-rating]")
	fmt.Println("owner_rating_control", stars.Length() == 1 && stars.Find("a.star").Length() == 5)
	fmt.Println("owner_rating_attribute", stars.Is("[data-rating]"))
	reviewLink := row.Find("a[href*='/review/edit']").First()
	href, ok := reviewLink.Attr("href")
	if !ok {
		fail("review edit link unavailable")
	}
	target, err := safeGoodreadsReviewURL(href)
	if err != nil {
		fail("review edit link unsafe")
	}
	edit, err := b.NewPage(ctx, target)
	if err != nil {
		fail("review edit page unavailable")
	}
	defer edit.Close()
	raw, err = edit.HTML(ctx)
	if err != nil {
		fail("review edit DOM unavailable")
	}
	doc, err = goquery.NewDocumentFromReader(strings.NewReader(raw))
	if err != nil {
		fail("review edit DOM invalid")
	}
	textarea := doc.Find("textarea[name='review[review]'], textarea#review_review")
	fmt.Println("review_textarea_count", textarea.Length())
	if textarea.Length() == 1 {
		fmt.Println("review_text_length", len([]rune(textarea.First().Text())))
	}
	form := textarea.First().Closest("form")
	fmt.Println("review_form_count", form.Length())
	if form.Length() == 1 {
		if current, valueErr := edit.Value(ctx, "input[name='readingEditsMade']"); valueErr == nil {
			fmt.Println("reading_edits_made_pattern", valuePattern(current))
		}
		form.Find("input[name$='[state]']").Each(func(i int, field *goquery.Selection) {
			name := field.AttrOr("name", "")
			current, valueErr := edit.Value(ctx, fmt.Sprintf(`input[name="%s"]`, name))
			fmt.Printf("reading_session_state[%d]=%q value_error=%t\n", i, current, valueErr != nil)
		})
		form.Find("input,select,textarea,button").Each(func(i int, field *goquery.Selection) {
			if i >= 80 {
				return
			}
			fmt.Printf("form_field[%d] tag=%q type=%q name=%q id=%q attrs=%q\n",
				i, goquery.NodeName(field), field.AttrOr("type", ""), safeToken(field.AttrOr("name", "")),
				safeToken(field.AttrOr("id", "")), attributeNames(field))
		})
		form.Find("a,button,label").Each(func(i int, field *goquery.Selection) {
			if i >= 80 {
				return
			}
			fmt.Printf("form_action[%d] tag=%q class=%q attrs=%q text_pattern=%q\n",
				i, goquery.NodeName(field), field.AttrOr("class", ""), attributeNames(field),
				valuePattern(strings.Join(strings.Fields(field.Text()), " ")))
		})
		for _, action := range []string{"delete", "remove", "clear", "save", "cancel"} {
			count := form.Find("a,button,label,input").FilterFunction(func(_ int, field *goquery.Selection) bool {
				text := strings.ToLower(strings.Join(strings.Fields(field.Text()), " "))
				value := strings.ToLower(strings.Join(strings.Fields(field.AttrOr("value", "")), " "))
				return text == action || value == action
			}).Length()
			fmt.Printf("form_action_%s_count %d\n", action, count)
		}
		fmt.Println("rereading_table_count", doc.Find("table.rereadingDatesTable").Length())
		fmt.Println("session_row_count", doc.Find("table.rereadingDatesTable tr.js-readingSessionRow").Length())
		fmt.Println("delete_link_count", doc.Find("table.rereadingDatesTable a.deleteReadingSession").Length())
		for _, bound := range []string{"start", "end"} {
			for _, unit := range []string{"year", "month", "day"} {
				selects := form.Find(fmt.Sprintf("select[name$='[%s][%s]']", bound, unit))
				clearable := selects.FilterFunction(func(_ int, selection *goquery.Selection) bool {
					return selection.Find("option[selected]").FilterFunction(func(_ int, option *goquery.Selection) bool {
						return option.AttrOr("value", "") != ""
					}).Length() == 1
				})
				fmt.Printf("%s_%s_select_count %d\n", bound, unit, selects.Length())
				fmt.Printf("%s_%s_clearable_count %d\n", bound, unit, clearable.Length())
				fmt.Printf("%s_%s_blank_option_count %d\n", bound, unit, clearable.Find("option[value='']").Length())
				selects.Each(func(i int, selection *goquery.Selection) {
					name := selection.AttrOr("name", "")
					current, valueErr := edit.Value(ctx, fmt.Sprintf(`select[name="%s"]`, name))
					blank := selection.Find("option").FilterFunction(func(_ int, option *goquery.Selection) bool {
						return strings.TrimSpace(option.Text()) == ""
					}).First()
					fmt.Printf("%s_%s[%d]_current_pattern=%q value_error=%t blank_text_options=%d blank_value_pattern=%q\n",
						bound, unit, i, valuePattern(current), valueErr != nil,
						selection.Find("option").FilterFunction(func(_ int, option *goquery.Selection) bool {
							return strings.TrimSpace(option.Text()) == ""
						}).Length(), valuePattern(blank.AttrOr("value", "")))
					for _, placeholder := range []string{unit, strings.ToUpper(unit[:1]) + unit[1:]} {
						fmt.Printf("%s_%s[%d]_current_equals_%s=%t option_equals_%s=%d\n",
							bound, unit, i, placeholder, current == placeholder, placeholder,
							selection.Find(fmt.Sprintf(`option[value="%s"]`, placeholder)).Length())
					}
					if unit == "year" {
						ancestor := selection.Parent()
						for depth := 0; depth < 6 && ancestor.Length() == 1; depth++ {
							fmt.Printf("%s_year[%d]_ancestor[%d] tag=%q class=%q delete_links=%d\n",
								bound, i, depth, goquery.NodeName(ancestor), ancestor.AttrOr("class", ""),
								ancestor.Find("a.deleteReadingSession").Length())
							ancestor = ancestor.Parent()
						}
					}
				})
			}
		}
	}
}

func rowMatchesISBN(row *goquery.Selection, target domain.ISBN) bool {
	for _, raw := range []string{
		row.Find("td.field.isbn .value").First().Text(),
		row.Find("td.field.isbn13 .value").First().Text(),
	} {
		parsed, err := domain.NormalizeISBN(strings.TrimSpace(raw))
		if err == nil && parsed.ISBN13 == target.ISBN13 {
			return true
		}
	}
	return false
}

func safeGoodreadsReviewURL(raw string) (string, error) {
	base, _ := url.Parse("https://www.goodreads.com")
	target, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	target = base.ResolveReference(target)
	if target.Scheme != "https" || target.Hostname() != "www.goodreads.com" || target.User != nil ||
		(target.Port() != "" && target.Port() != "443") || !strings.HasPrefix(target.Path, "/review/edit/") {
		return "", fmt.Errorf("unexpected review URL")
	}
	return target.String(), nil
}

var digits = regexp.MustCompile(`[0-9]+`)

func safeToken(value string) string {
	return digits.ReplaceAllString(value, "{id}")
}

func attributeNames(selection *goquery.Selection) string {
	if selection.Length() == 0 {
		return ""
	}
	names := make([]string, 0, len(selection.Get(0).Attr))
	for _, attr := range selection.Get(0).Attr {
		names = append(names, attr.Key)
	}
	return strings.Join(names, ",")
}

func valuePattern(value string) string {
	var result strings.Builder
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
			result.WriteByte('9')
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
			result.WriteByte('A')
		default:
			result.WriteRune(r)
		}
	}
	return result.String()
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
