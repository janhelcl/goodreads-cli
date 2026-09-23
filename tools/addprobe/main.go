//go:build liveprobe

// addprobe inspects Goodreads' visible ISBN search and book-page shelf
// controls without clicking or mutating library state. It prints structural
// facts only, never titles, account identifiers, or raw HTML.
package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
	"github.com/janhelcl/goodreads-cli/internal/goodreads"
	"github.com/janhelcl/goodreads-cli/internal/profile"
)

func main() {
	defer func() {
		if recovered := recover(); recovered != nil {
			message, ok := recovered.(probeFailure)
			if !ok {
				panic(recovered)
			}
			fmt.Fprintln(os.Stderr, message)
			os.Exit(1)
		}
	}()
	if len(os.Args) != 2 {
		fail("expected one exact ISBN")
	}
	isbn, err := domain.NormalizeISBN(os.Args[1])
	if err != nil {
		fail("invalid ISBN")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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
	b, err := (browser.RodFactory{}).Launch(ctx, browser.LaunchOptions{
		ProfileDir: paths.Browser,
		Headless:   true,
	})
	if err != nil {
		fail("browser unavailable")
	}
	defer b.Close()
	state, err := goodreads.Status(ctx, b)
	if err != nil || !state.Connected {
		fail("authenticated Goodreads state unavailable")
	}

	searchURL := "https://www.goodreads.com/search?q=" +
		url.QueryEscape(isbn.ISBN13) + "&search_type=books"
	searchPage, err := b.NewPage(ctx, searchURL)
	if err != nil {
		fail("search page unavailable")
	}
	searchDoc, err := waitForSearch(ctx, searchPage)
	if err != nil {
		_ = searchPage.Close()
		fail("search contract unavailable")
	}
	fmt.Println("search_form_count", searchDoc.Find("form[action='/search']").Length())
	fmt.Println("search_results_container_count",
		searchDoc.Find("table.tableList, div.searchResults, div.bookSearchResults, .SearchContentWrapper__results").Length())
	fmt.Println("search_empty_marker_count", searchDoc.Find(".NoBookSearchResults").Length())
	fmt.Println("search_results_count_marker", searchDoc.Find(".SearchResultsCount").Length())
	links := uniqueBookLinks(searchDoc)
	fmt.Println("search_unique_book_routes", len(links))
	fmt.Println("search_exact_isbn10_present", isbn.ISBN10 != "" && strings.Contains(searchDoc.Text(), isbn.ISBN10))
	fmt.Println("search_exact_isbn13_present", strings.Contains(searchDoc.Text(), isbn.ISBN13))
	_ = searchPage.Close()
	if len(links) != 1 {
		fail("ISBN search did not yield one unique book route")
	}

	bookPage, err := b.NewPage(ctx, "https://www.goodreads.com"+links[0])
	if err != nil {
		fail("book page unavailable")
	}
	defer bookPage.Close()
	bookDoc, err := waitForBook(ctx, bookPage)
	if err != nil {
		fail("book page contract unavailable")
	}
	raw, err := bookPage.HTML(ctx)
	if err != nil {
		fail("book DOM unavailable")
	}
	fmt.Println("book_page_isbn10_present", isbn.ISBN10 != "" && strings.Contains(raw, isbn.ISBN10))
	fmt.Println("book_page_isbn13_present", strings.Contains(raw, isbn.ISBN13))
	fmt.Println("book_jsonld_count", bookDoc.Find("script[type='application/ld+json']").Length())
	fmt.Println("book_metadata_count", bookDoc.Find("div.BookPageMetadataSection").Length())
	fmt.Println("book_actions_count", bookDoc.Find("div.BookActions").Length())
	bookDoc.Find("button").Each(func(i int, button *goquery.Selection) {
		text := strings.ToLower(strings.Join(strings.Fields(button.Text()), " "))
		aria := strings.ToLower(button.AttrOr("aria-label", ""))
		if strings.Contains(text+" "+aria, "detail") || strings.Contains(text+" "+aria, "edition") ||
			strings.Contains(text+" "+aria, "isbn") {
			fmt.Printf("book_details_control[%d] class=%q attrs=%q detail=%t edition=%t isbn=%t text_pattern=%q aria_pattern=%q\n",
				i, button.AttrOr("class", ""), attributeNames(button),
				strings.Contains(text+" "+aria, "detail"), strings.Contains(text+" "+aria, "edition"),
				strings.Contains(text+" "+aria, "isbn"), valuePattern(text), valuePattern(aria))
		}
	})
	detailsSelector := "div.BookPageMetadataSection button.Button--inline"
	details := bookDoc.Find(detailsSelector)
	detailsText := strings.ToLower(strings.Join(strings.Fields(details.Text()), " "))
	if details.Length() != 1 || !strings.Contains(detailsText, "detail") {
		fail("book details control contract unavailable")
	}
	if err := bookPage.Click(ctx, detailsSelector); err != nil {
		fail("book details could not be opened")
	}
	detailsCtx, cancelDetails := context.WithTimeout(ctx, 20*time.Second)
	detailsDoc, err := waitForChangedDOM(detailsCtx, bookPage, raw)
	cancelDetails()
	if err != nil {
		fail("book details did not expose a changed DOM")
	}
	dialog := detailsDoc.Find("[role='dialog']")
	detailsRoot := dialog
	if detailsRoot.Length() != 1 {
		detailsRoot = detailsDoc.Selection
	}
	dialogText := visibleText(detailsRoot)
	fmt.Println("book_details_dialog_count", dialog.Length())
	fmt.Println("book_details_isbn10_present", isbn.ISBN10 != "" && strings.Contains(dialogText, isbn.ISBN10))
	fmt.Println("book_details_isbn13_present", strings.Contains(dialogText, isbn.ISBN13))
	detailsRoot.Find("*").Each(func(i int, node *goquery.Selection) {
		if node.Is("script,style") {
			return
		}
		direct := strings.Join(strings.Fields(node.Clone().Children().Remove().End().Text()), " ")
		if !strings.Contains(direct, isbn.ISBN13) &&
			(isbn.ISBN10 == "" || !strings.Contains(direct, isbn.ISBN10)) {
			return
		}
		fmt.Printf("book_details_isbn_node[%d] tag=%q class=%q parent_tag=%q parent_class=%q text_pattern=%q\n",
			i, goquery.NodeName(node), node.AttrOr("class", ""), goquery.NodeName(node.Parent()),
			node.Parent().AttrOr("class", ""), valuePattern(direct))
	})
	bookDoc.Find("div.BookPageMetadataSection").Find("*").Each(func(i int, node *goquery.Selection) {
		direct := strings.Join(strings.Fields(node.Clone().Children().Remove().End().Text()), " ")
		if !strings.Contains(direct, isbn.ISBN13) &&
			(isbn.ISBN10 == "" || !strings.Contains(direct, isbn.ISBN10)) {
			return
		}
		fmt.Printf("book_isbn_node[%d] tag=%q class=%q parent_tag=%q parent_class=%q text_pattern=%q\n",
			i, goquery.NodeName(node), node.AttrOr("class", ""), goquery.NodeName(node.Parent()),
			node.Parent().AttrOr("class", ""), valuePattern(direct))
	})
	bookDoc.Find("div.BookActions button").Each(func(i int, button *goquery.Selection) {
		if i >= 12 {
			return
		}
		fmt.Printf("book_action[%d] class=%q attrs=%q status=%q aria_pattern=%q\n",
			i, button.AttrOr("class", ""), attributeNames(button),
			coreStatus(button.Text()), valuePattern(button.AttrOr("aria-label", "")))
	})
}

func waitForSearch(ctx context.Context, page browser.Page) (*goquery.Document, error) {
	return waitForDOM(ctx, page, func(doc *goquery.Document) bool {
		if doc.Find("form[action='/search']").Length() == 0 {
			return false
		}
		return len(uniqueBookLinks(doc)) > 0 ||
			doc.Find(".NoBookSearchResults").Length() > 0
	})
}

func waitForBook(ctx context.Context, page browser.Page) (*goquery.Document, error) {
	return waitForDOM(ctx, page, func(doc *goquery.Document) bool {
		return doc.Find("div.BookActions button").Length() > 0
	})
}

func waitForDOM(
	ctx context.Context,
	page browser.Page,
	ready func(*goquery.Document) bool,
) (*goquery.Document, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		raw, err := page.HTML(ctx)
		if err == nil {
			doc, parseErr := goquery.NewDocumentFromReader(strings.NewReader(raw))
			if parseErr == nil && ready(doc) {
				return doc, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func waitForChangedDOM(
	ctx context.Context,
	page browser.Page,
	before string,
) (*goquery.Document, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		raw, err := page.HTML(ctx)
		if err == nil && raw != before {
			doc, parseErr := goquery.NewDocumentFromReader(strings.NewReader(raw))
			if parseErr == nil {
				return doc, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func uniqueBookLinks(doc *goquery.Document) []string {
	routes := map[string]bool{}
	doc.Find("a[href*='/book/show/']").Each(func(_ int, link *goquery.Selection) {
		href := link.AttrOr("href", "")
		parsed, err := url.Parse(href)
		if err != nil || !strings.HasPrefix(parsed.Path, "/book/show/") {
			return
		}
		routes[parsed.Path] = true
	})
	result := make([]string, 0, len(routes))
	for route := range routes {
		result = append(result, route)
	}
	sort.Strings(result)
	return result
}

func visibleText(selection *goquery.Selection) string {
	copy := selection.Clone()
	copy.Find("script,style").Remove()
	return strings.Join(strings.Fields(copy.Text()), " ")
}

func coreStatus(value string) string {
	switch strings.ToLower(strings.Join(strings.Fields(value), " ")) {
	case "want to read", "to read":
		return "to-read"
	case "currently reading":
		return "currently-reading"
	case "read":
		return "read"
	default:
		return ""
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
	sort.Strings(names)
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
	return regexp.MustCompile(`A+`).ReplaceAllString(result.String(), "A")
}

type probeFailure string

func fail(message string) { panic(probeFailure(message)) }
