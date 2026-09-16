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
		id, err := strconv.ParseUint(os.Args[1], 10, 64)
		if err != nil || id == 0 {
			fail("expected a numeric public Goodreads user ID")
		}
		target += "/" + strconv.FormatUint(id, 10)
	} else if len(os.Args) != 1 {
		fail("expected zero or one public Goodreads user ID")
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
			td.Find("span").Each(func(j int, span *goquery.Selection) {
				if j < 8 {
					fmt.Printf("rating_span[%d] class=%q title=%q aria=%q\n", j, span.AttrOr("class", ""), span.AttrOr("title", ""), span.AttrOr("aria-label", ""))
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
		}
	})
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
