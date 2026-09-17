//go:build liveprobe

// libraryprobe prints structural shelf information only. It never prints book
// titles, ISBNs, reviews, HTML, cookies, or account identifiers.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
	"github.com/janhelcl/goodreads-cli/internal/goodreads"
	"github.com/janhelcl/goodreads-cli/internal/profile"
)

func main() {
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
	target := "https://www.goodreads.com/review/list"
	if len(os.Args) == 2 {
		if os.Args[1] != "self" && os.Args[1] != "shelves" {
			id, err := strconv.ParseUint(os.Args[1], 10, 64)
			if err != nil || id == 0 {
				fail(`expected "self", "shelves", or a numeric public Goodreads user ID`)
			}
			target += "/" + strconv.FormatUint(id, 10)
		}
	} else if len(os.Args) != 1 {
		fail(`expected no argument, "self", "shelves", or one public Goodreads user ID`)
	}
	p, err := b.NewPage(ctx, target)
	if err != nil {
		fail("library page unavailable")
	}
	defer p.Close()
	html, err := p.HTML(ctx)
	if err != nil {
		fail("library DOM unavailable")
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		fail("library DOM could not be parsed")
	}
	if len(os.Args) == 2 && os.Args[1] == "shelves" {
		probeShelfMarkers(ctx, b)
		return
	}
	fmt.Println("table_count", doc.Find("#books").Length())
	fmt.Println("body_count", doc.Find("#booksBody").Length())
	fmt.Println("body_tag", goquery.NodeName(doc.Find("#booksBody")))
	fmt.Println("body_child_count", doc.Find("#booksBody").Children().Length())
	fmt.Println("row_count", doc.Find("#booksBody > tr").Length())
	fmt.Println("nested_row_count", doc.Find("#booksBody tr").Length())
	fmt.Println("review_row_count", doc.Find("tr[id^='review_']").Length())
	fmt.Println("next_page_link", doc.Find("a.next_page").Length() > 0)
	fmt.Println("book_title_link", doc.Find("#booksBody tr:first-child td.field.title a[href*='/book/show/']").Length() > 0)
	for _, shelf := range []string{"to-read", "currently-reading", "read"} {
		fmt.Printf("navigation_%s=%t\n", shelf, doc.Find("a[href*='shelf="+shelf+"']").Length() > 0)
	}
	for _, shelf := range []string{"to-read", "want-to-read", "currently-reading", "read"} {
		count := 0
		doc.Find("#booksBody tr td.field.shelves a").Each(func(_ int, a *goquery.Selection) {
			label := strings.Join(strings.Fields(strings.ToLower(a.Text())), "-")
			if label == shelf {
				count++
			}
		})
		fmt.Printf("shelf_label_%s_count=%d\n", shelf, count)
	}
	fmt.Println("empty_message", doc.Find("body").Find("p,div,span").FilterFunction(func(_ int, s *goquery.Selection) bool {
		if s.Children().Length() != 0 {
			return false
		}
		text := strings.ToLower(strings.TrimSpace(s.Text()))
		return strings.Contains(text, "no books") || strings.Contains(text, "haven't added any books")
	}).Length() > 0)
	doc.Find("#books").Parent().Children().Each(func(i int, s *goquery.Selection) {
		if i < 12 {
			fmt.Printf("books_sibling[%d] tag=%q id=%q class=%q\n", i, goquery.NodeName(s), s.AttrOr("id", ""), s.AttrOr("class", ""))
		}
	})
	doc.Find("#books thead th").Each(func(i int, th *goquery.Selection) {
		fmt.Printf("header[%d] class=%q label=%q\n", i, th.AttrOr("class", ""), strings.TrimSpace(th.Text()))
	})
	doc.Find("#booksBody tr").First().ChildrenFiltered("td").Each(func(i int, td *goquery.Selection) {
		fmt.Printf("field[%d] class=%q divs=%d anchors=%d spans=%d\n", i, td.AttrOr("class", ""), td.Find("div").Length(), td.Find("a").Length(), td.Find("span").Length())
		if len(os.Args) == 2 && td.HasClass("rating") {
			td.Find("*").Each(func(j int, child *goquery.Selection) {
				if j < 24 {
					fmt.Printf("rating_node[%d] tag=%q class=%q attrs=%q title=%q aria=%q data_rating=%q\n",
						j, goquery.NodeName(child), child.AttrOr("class", ""), attributeNames(child),
						child.AttrOr("title", ""), child.AttrOr("aria-label", ""), child.AttrOr("data-rating", ""))
				}
			})
		}
		if len(os.Args) == 2 && td.HasClass("shelves") {
			for _, shelf := range []string{"to-read", "currently-reading", "read"} {
				fmt.Printf("shelf_%s=%t\n", shelf, td.Find("a").FilterFunction(func(_ int, a *goquery.Selection) bool {
					return strings.TrimSpace(a.Text()) == shelf
				}).Length() > 0)
				fmt.Printf("shelf_text_contains_%s=%t\n", shelf, strings.Contains(strings.ToLower(td.Text()), shelf))
			}
			td.Find("*").Each(func(j int, child *goquery.Selection) {
				if j < 28 {
					fmt.Printf("shelf_node[%d] tag=%q class=%q\n", j, goquery.NodeName(child), child.AttrOr("class", ""))
				}
			})
		}
		if len(os.Args) == 2 && (td.HasClass("date_read") || td.HasClass("date_added")) {
			fmt.Printf("%s_pattern=%q\n", td.AttrOr("class", ""), valuePattern(strings.TrimSpace(td.Find(".value").First().Text())))
			td.Find("*").Each(func(j int, child *goquery.Selection) {
				if j < 16 {
					fmt.Printf("%s_node[%d] tag=%q class=%q attrs=%q\n",
						td.AttrOr("class", ""), j, goquery.NodeName(child), child.AttrOr("class", ""), attributeNames(child))
				}
			})
		}
		if len(os.Args) == 2 && (td.HasClass("review") || td.HasClass("actions")) {
			td.Find("*").Each(func(j int, child *goquery.Selection) {
				if j < 20 {
					fmt.Printf("%s_node[%d] tag=%q class=%q attrs=%q route=%q text_pattern=%q\n",
						td.AttrOr("class", ""), j, goquery.NodeName(child), child.AttrOr("class", ""),
						attributeNames(child), routeKind(child.AttrOr("href", "")),
						valuePattern(strings.Join(strings.Fields(child.Text()), " ")))
					if routeKind(child.AttrOr("href", "")) == "review-destroy" {
						fmt.Printf("%s_node[%d]_method=%q confirm_pattern=%q rel=%q\n",
							td.AttrOr("class", ""), j, child.AttrOr("data-method", ""),
							valuePattern(child.AttrOr("data-confirm", "")), child.AttrOr("rel", ""))
					}
				}
			})
		}
	})
	if len(os.Args) == 2 && os.Args[1] == "self" {
		books, err := goodreads.Library(ctx, b, domain.LibraryFilter{Limit: 20})
		if err != nil {
			fmt.Println("production_parser_error", err)
		} else {
			fmt.Println("production_parser_count", len(books))
		}
		for _, shelf := range []domain.ReadingStatus{domain.StatusToRead, domain.StatusCurrentlyReading, domain.StatusRead} {
			books, err := goodreads.Library(ctx, b, domain.LibraryFilter{Shelf: shelf, Limit: 20})
			if err != nil {
				fmt.Printf("production_shelf_%s_error %v\n", shelf, err)
			} else {
				fmt.Printf("production_shelf_%s_count %d\n", shelf, len(books))
			}
		}
	}
}

func probeShelfMarkers(ctx context.Context, b browser.Browser) {
	for _, shelf := range []domain.ReadingStatus{domain.StatusToRead, domain.StatusCurrentlyReading, domain.StatusRead} {
		page, pageErr := b.NewPage(ctx, "https://www.goodreads.com/review/list?shelf="+string(shelf))
		if pageErr != nil {
			fmt.Printf("shelf_%s_probe_error navigation\n", shelf)
			continue
		}
		raw, htmlErr := page.HTML(ctx)
		_ = page.Close()
		if htmlErr != nil {
			fmt.Printf("shelf_%s_probe_error DOM\n", shelf)
			continue
		}
		shelfDoc, parseErr := goquery.NewDocumentFromReader(strings.NewReader(raw))
		if parseErr != nil {
			fmt.Printf("shelf_%s_probe_error parse\n", shelf)
			continue
		}
		fmt.Printf("shelf_%s_markers heading=%q books=%d body=%d title_header=%d author_header=%d rows=%d\n",
			shelf, strings.TrimSpace(shelfDoc.Find("h1").First().Text()), shelfDoc.Find("#books").Length(),
			shelfDoc.Find("#booksBody").Length(), shelfDoc.Find("#books th.field.title").Length(),
			shelfDoc.Find("#books th.field.author").Length(), shelfDoc.Find("#booksBody > tr").Length())
	}
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

func routeKind(href string) string {
	switch {
	case strings.Contains(href, "/review/edit"):
		return "review-edit"
	case strings.Contains(href, "/review/show"):
		return "review-show"
	case strings.Contains(href, "/review/destroy"):
		return "review-destroy"
	case strings.Contains(href, "/book/show"):
		return "book-show"
	case strings.Contains(href, "/shelf/"):
		return "shelf"
	case href != "":
		return "other"
	default:
		return ""
	}
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

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
