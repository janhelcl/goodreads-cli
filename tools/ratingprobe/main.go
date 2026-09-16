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
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
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
		form.Find("input,select,textarea,button").Each(func(i int, field *goquery.Selection) {
			if i >= 80 {
				return
			}
			fmt.Printf("form_field[%d] tag=%q type=%q name=%q id=%q attrs=%q\n",
				i, goquery.NodeName(field), field.AttrOr("type", ""), safeToken(field.AttrOr("name", "")),
				safeToken(field.AttrOr("id", "")), attributeNames(field))
		})
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

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
