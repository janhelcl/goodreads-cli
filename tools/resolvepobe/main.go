//go:build liveprobe

// resolvepobe inspects visible owner-library search, per-page options, and
// public ISBN-to-book-ID lookup. It never mutates library state and never
// prints titles, ISBNs, account identifiers, URLs, cookies, or raw HTML.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
	"github.com/janhelcl/goodreads-cli/internal/goodreads"
	"github.com/janhelcl/goodreads-cli/internal/profile"
)

var (
	bookShowPath  = regexp.MustCompile(`^/book/show/([0-9]+)(?:[./-]|$)`)
	defaultAbsent = "9780306406157"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 && len(os.Args) != 3 {
		return errors.New("expected one present exact ISBN and optional absent exact ISBN")
	}
	present, err := domain.NormalizeISBN(os.Args[1])
	if err != nil {
		return errors.New("invalid present ISBN")
	}
	absentISBN := defaultAbsent
	if len(os.Args) == 3 {
		absentISBN = os.Args[2]
	}
	absent, err := domain.NormalizeISBN(absentISBN)
	if err != nil {
		return errors.New("invalid absent ISBN")
	}
	if present.ISBN13 == absent.ISBN13 {
		return errors.New("present and absent ISBNs must differ")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	paths, err := profile.DefaultPaths()
	if err != nil {
		return errors.New("profile unavailable")
	}
	lock, err := paths.Acquire(ctx)
	if err != nil {
		return errors.New("profile busy")
	}
	defer lock.Release()
	b, err := (browser.RodFactory{}).Launch(ctx, browser.LaunchOptions{
		ProfileDir: paths.Browser,
		Headless:   true,
	})
	if err != nil {
		return errors.New("browser unavailable")
	}
	defer b.Close()
	state, err := goodreads.Status(ctx, b)
	if err != nil || !state.Connected {
		return errors.New("authenticated Goodreads state unavailable")
	}

	library, err := open(ctx, b, "https://www.goodreads.com/review/list")
	if err != nil {
		return err
	}
	defer library.page.Close()
	printLibrary("default", library)
	printSearchControls(library.doc)
	originalPerPage := library.parsed.Query().Get("per_page")

	for _, trial := range []struct {
		name string
		isbn domain.ISBN
		form string
	}{
		{"present_isbn13", present, present.ISBN13},
		{"present_isbn10", present, present.ISBN10},
		{"absent_isbn13", absent, absent.ISBN13},
		{"absent_isbn10", absent, absent.ISBN10},
	} {
		if trial.form == "" {
			fmt.Printf("%s skipped empty form\n", trial.name)
			continue
		}
		if err := probeOwnerSearch(ctx, b, library.parsed, trial.name, trial.isbn, trial.form); err != nil {
			fmt.Printf("%s error %s\n", trial.name, publicErr(err))
		}
	}

	for _, size := range []string{"20", "50", "100", "infinite"} {
		if err := probePerPage(ctx, b, library.parsed, size); err != nil {
			fmt.Printf("per_page_%s error %s\n", size, publicErr(err))
		}
	}
	if originalPerPage != "" {
		_ = probePerPage(ctx, b, library.parsed, originalPerPage)
	} else {
		_ = probePerPage(ctx, b, library.parsed, "20")
	}

	if err := probePublicThenOwner(ctx, b, present, "present"); err != nil {
		fmt.Printf("public_present error %s\n", publicErr(err))
	}
	if err := probePublicThenOwner(ctx, b, absent, "absent"); err != nil {
		fmt.Printf("public_absent error %s\n", publicErr(err))
	}

	start := time.Now()
	_, getErr := goodreads.Get(ctx, b, present)
	fmt.Println("production_get_present", publicErr(getErr), "elapsed_ms", time.Since(start).Milliseconds())
	start = time.Now()
	_, getErr = goodreads.Get(ctx, b, absent)
	fmt.Println("production_get_absent", publicErr(getErr), "elapsed_ms", time.Since(start).Milliseconds())
	return nil
}

type opened struct {
	page   browser.Page
	parsed *url.URL
	doc    *goquery.Document
	stats  pageStats
}

type pageStats struct {
	authenticated      bool
	headingOK          bool
	rows               int
	unidentified       int
	exactMatches       int
	exactBookIDs       int
	idMatches          int
	nextPage           bool
	queryKeys          string
	pathKind           string
	perPage            string
	searchQueryPresent bool
}

func open(ctx context.Context, b browser.Browser, target string) (opened, error) {
	page, err := b.NewPage(ctx, target)
	if err != nil {
		return opened{}, errors.New("page unavailable")
	}
	rawURL, err := page.URL(ctx)
	if err != nil {
		_ = page.Close()
		return opened{}, errors.New("page URL unavailable")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		_ = page.Close()
		return opened{}, errors.New("page URL invalid")
	}
	raw, err := page.HTML(ctx)
	if err != nil {
		_ = page.Close()
		return opened{}, errors.New("page DOM unavailable")
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(raw))
	if err != nil {
		_ = page.Close()
		return opened{}, errors.New("page DOM invalid")
	}
	return opened{page: page, parsed: parsed, doc: doc, stats: summarize(parsed, doc, domain.ISBN{}, "")}, nil
}

func printLibrary(label string, page opened) {
	fmt.Printf("%s path=%s query_keys=%s per_page=%s heading=%t authenticated=%t rows=%d unidentified=%d next=%t\n",
		label, page.stats.pathKind, page.stats.queryKeys, dash(page.stats.perPage),
		page.stats.headingOK, page.stats.authenticated, page.stats.rows, page.stats.unidentified, page.stats.nextPage)
}

func printSearchControls(doc *goquery.Document) {
	fmt.Println("header_search_forms", doc.Find(`form[action='/search'], form[action="https://www.goodreads.com/search"]`).Length())
	index := 0
	doc.Find("form").Each(func(_ int, form *goquery.Selection) {
		action := form.AttrOr("action", "")
		parsed, _ := url.Parse(action)
		path := ""
		if parsed != nil {
			path = parsed.Path
		}
		hasQuery := form.Find(`input[name='search[query]'], input#search_query`).Length() > 0
		if !hasQuery && path != "/search" {
			return
		}
		names := []string{}
		form.Find("input,select,button,textarea").Each(func(_ int, field *goquery.Selection) {
			name := field.AttrOr("name", "")
			id := field.AttrOr("id", "")
			if name != "" {
				names = append(names, name)
			} else if id != "" {
				names = append(names, "#"+id)
			}
		})
		fmt.Printf("form[%d] action=%s method=%s query_input=%t fields=%s\n",
			index, pathKind(path), strings.ToLower(form.AttrOr("method", "get")), hasQuery, strings.Join(unique(names), ","))
		form.Find("select").Each(func(_ int, sel *goquery.Selection) {
			options := []string{}
			sel.Find("option").Each(func(_ int, option *goquery.Selection) {
				value := option.AttrOr("value", strings.TrimSpace(option.Text()))
				if value != "" {
					options = append(options, value)
				}
			})
			fmt.Printf("form[%d]_select name=%s options=%s\n",
				index, sel.AttrOr("name", sel.AttrOr("id", "")), strings.Join(unique(options), ","))
		})
		index++
	})
	perPage := []string{}
	doc.Find("select#per_page option, select[name=per_page] option").Each(func(_ int, option *goquery.Selection) {
		value := option.AttrOr("value", strings.TrimSpace(option.Text()))
		if value != "" {
			perPage = append(perPage, value)
		}
	})
	fmt.Println("per_page_options", strings.Join(unique(perPage), ","))
}

func probeOwnerSearch(
	ctx context.Context,
	b browser.Browser,
	library *url.URL,
	name string,
	isbn domain.ISBN,
	query string,
) error {
	target := cloneURL(library)
	values := target.Query()
	values.Del("page")
	values.Set("utf8", "✓")
	values.Set("search[query]", query)
	target.RawQuery = values.Encode()
	page, err := open(ctx, b, target.String())
	if err != nil {
		return err
	}
	_ = page.page.Close()
	stats := summarize(page.parsed, page.doc, isbn, "")
	fmt.Printf("%s path=%s query_keys=%s search_query=%t heading=%t authenticated=%t rows=%d unidentified=%d exact=%d exact_ids=%d next=%t\n",
		name, stats.pathKind, stats.queryKeys, stats.searchQueryPresent, stats.headingOK,
		stats.authenticated, stats.rows, stats.unidentified, stats.exactMatches, stats.exactBookIDs, stats.nextPage)
	return nil
}

func probePerPage(ctx context.Context, b browser.Browser, library *url.URL, size string) error {
	target := cloneURL(library)
	values := target.Query()
	values.Del("page")
	values.Del("search[query]")
	values.Set("per_page", size)
	target.RawQuery = values.Encode()
	page, err := open(ctx, b, target.String())
	if err != nil {
		return err
	}
	_ = page.page.Close()
	fmt.Printf("per_page_%s path=%s applied=%s heading=%t authenticated=%t rows=%d unidentified=%d next=%t query_keys=%s\n",
		size, page.stats.pathKind, dash(page.stats.perPage), page.stats.headingOK,
		page.stats.authenticated, page.stats.rows, page.stats.unidentified, page.stats.nextPage, page.stats.queryKeys)
	return nil
}

func probePublicThenOwner(ctx context.Context, b browser.Browser, isbn domain.ISBN, name string) error {
	searchURL := "https://www.goodreads.com/search?q=" + url.QueryEscape(isbn.ISBN13) + "&search_type=books"
	search, err := open(ctx, b, searchURL)
	if err != nil {
		return err
	}
	routes := uniqueBookIDs(search.doc, search.parsed)
	isbn10Visible := isbn.ISBN10 != "" && strings.Contains(search.doc.Text(), isbn.ISBN10)
	isbn13Visible := strings.Contains(search.doc.Text(), isbn.ISBN13)
	_ = search.page.Close()
	fmt.Printf("public_%s path=%s unique_book_ids=%d isbn10_visible=%t isbn13_visible=%t\n",
		name, search.stats.pathKind, len(routes), isbn10Visible, isbn13Visible)
	if len(routes) != 1 {
		return nil
	}
	var bookID, bookURL string
	for id, target := range routes {
		bookID, bookURL = id, target
	}
	book, err := open(ctx, b, bookURL)
	if err != nil {
		return err
	}
	metadata := book.doc.Find("div.BookPageMetadataSection").Clone()
	metadata.Find("script,style").Remove()
	visible := strings.Contains(metadata.Text(), isbn.ISBN13) ||
		(isbn.ISBN10 != "" && strings.Contains(metadata.Text(), isbn.ISBN10))
	details := book.doc.Find("div.BookPageMetadataSection button.Button--inline")
	detailsText := strings.ToLower(strings.Join(strings.Fields(details.Text()), " "))
	fmt.Printf("public_%s_book metadata=%t details_control=%t isbn_before_details=%t\n",
		name, book.doc.Find("div.BookPageMetadataSection").Length() == 1,
		details.Length() == 1 && strings.Contains(detailsText, "detail"), visible)
	if !visible && details.Length() == 1 {
		if err := book.page.Click(ctx, "div.BookPageMetadataSection button.Button--inline"); err == nil {
			visible = waitForExactISBN(ctx, book.page, isbn)
		}
	}
	_ = book.page.Close()
	fmt.Printf("public_%s_isbn_proven %t\n", name, visible)

	pages, rows, unidentified, idHits, isbnHits, terminated, scanErr := scanOwnerIDs(ctx, b, isbn, bookID)
	fmt.Printf("owner_id_%s pages=%d rows=%d unidentified=%d id_matches=%d isbn_matches=%d terminated=%t error=%s\n",
		name, pages, rows, unidentified, idHits, isbnHits, terminated, publicErr(scanErr))
	return nil
}

func waitForExactISBN(ctx context.Context, page browser.Page, isbn domain.ISBN) bool {
	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		raw, err := page.HTML(waitCtx)
		if err == nil {
			doc, docErr := goquery.NewDocumentFromReader(strings.NewReader(raw))
			if docErr == nil {
				meta := doc.Find("div.BookPageMetadataSection").Clone()
				meta.Find("script,style").Remove()
				if strings.Contains(meta.Text(), isbn.ISBN13) ||
					(isbn.ISBN10 != "" && strings.Contains(meta.Text(), isbn.ISBN10)) {
					return true
				}
			}
		}
		select {
		case <-waitCtx.Done():
			return false
		case <-ticker.C:
		}
	}
}

func scanOwnerIDs(
	ctx context.Context,
	b browser.Browser,
	isbn domain.ISBN,
	bookID string,
) (pages, rows, unidentified, idHits, isbnHits int, terminated bool, err error) {
	target, _ := url.Parse("https://www.goodreads.com/review/list")
	visited := map[string]bool{}
	for pages < 100 {
		if err = ctx.Err(); err != nil {
			return
		}
		if visited[target.String()] {
			err = errors.New("pagination loop")
			return
		}
		visited[target.String()] = true
		page, openErr := open(ctx, b, target.String())
		if openErr != nil {
			err = openErr
			return
		}
		pages++
		stats := summarize(page.parsed, page.doc, isbn, bookID)
		rows += stats.rows
		unidentified += stats.unidentified
		idHits += stats.idMatches
		isbnHits += stats.exactMatches
		next := page.doc.Find("a.next_page").First().AttrOr("href", "")
		current := page.parsed
		_ = page.page.Close()
		if next == "" {
			terminated = true
			return
		}
		ref, parseErr := url.Parse(next)
		if parseErr != nil {
			err = errors.New("invalid next link")
			return
		}
		target = current.ResolveReference(ref)
	}
	return
}

func summarize(page *url.URL, doc *goquery.Document, isbn domain.ISBN, bookID string) pageStats {
	heading := strings.Join(strings.Fields(strings.ReplaceAll(doc.Find("h1").First().Text(), "\u200e", "")), " ")
	stats := pageStats{
		authenticated:      doc.Find("a[href*='/user/sign_out']").Length() > 0,
		headingOK:          heading == "My Books" || strings.HasPrefix(heading, "My Books:"),
		nextPage:           doc.Find("a.next_page").Length() > 0,
		queryKeys:          queryKeys(page),
		pathKind:           pathKind(page.Path),
		perPage:            page.Query().Get("per_page"),
		searchQueryPresent: strings.TrimSpace(page.Query().Get("search[query]")) != "",
	}
	ids := map[string]bool{}
	doc.Find("#booksBody > tr").Each(func(_ int, row *goquery.Selection) {
		stats.rows++
		isbn10 := strings.TrimSpace(row.Find("td.field.isbn .value").First().Text())
		isbn13 := strings.TrimSpace(row.Find("td.field.isbn13 .value").First().Text())
		if isbn10 == "" && isbn13 == "" {
			stats.unidentified++
		}
		href := row.Find("td.field.title .value a[href*='/book/show/']").First().AttrOr("href", "")
		parsed, _ := url.Parse(href)
		rowID := ""
		if parsed != nil {
			if match := bookShowPath.FindStringSubmatch(parsed.Path); len(match) == 2 {
				rowID = match[1]
			}
		}
		if bookID != "" && rowID == bookID {
			stats.idMatches++
		}
		if isbn.ISBN13 != "" && rowMatchesISBN(isbn10, isbn13, isbn) {
			stats.exactMatches++
			if rowID != "" {
				ids[rowID] = true
			}
		}
	})
	stats.exactBookIDs = len(ids)
	return stats
}

func rowMatchesISBN(isbn10, isbn13 string, isbn domain.ISBN) bool {
	for _, raw := range []string{isbn10, isbn13} {
		parsed, err := domain.NormalizeISBN(strings.TrimSpace(raw))
		if err == nil && parsed.ISBN13 == isbn.ISBN13 {
			return true
		}
	}
	return false
}

func uniqueBookIDs(doc *goquery.Document, base *url.URL) map[string]string {
	routes := map[string]string{}
	doc.Find("a[href*='/book/show/']").Each(func(_ int, link *goquery.Selection) {
		href := link.AttrOr("href", "")
		ref, err := url.Parse(href)
		if err != nil {
			return
		}
		target := base.ResolveReference(ref)
		match := bookShowPath.FindStringSubmatch(target.Path)
		if len(match) != 2 || target.Host != "www.goodreads.com" {
			return
		}
		target.RawQuery = ""
		target.Fragment = ""
		routes[match[1]] = target.String()
	})
	return routes
}

func queryKeys(page *url.URL) string {
	if page == nil {
		return ""
	}
	keys := make([]string, 0, len(page.Query()))
	for key := range page.Query() {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func pathKind(path string) string {
	switch {
	case path == "/search":
		return "search"
	case strings.HasPrefix(path, "/review/list"):
		return "library"
	case bookShowPath.MatchString(path):
		return "book"
	case path == "/user/sign_in":
		return "sign-in"
	default:
		return "other"
	}
}

func publicErr(err error) string {
	if err == nil {
		return "ok"
	}
	switch {
	case errors.Is(err, goodreads.ErrBookNotFound):
		return "not_found"
	case errors.Is(err, goodreads.ErrBookAmbiguous):
		return "ambiguous"
	case errors.Is(err, goodreads.ErrScanIncomplete):
		return "scan_incomplete"
	case errors.Is(err, goodreads.ErrCompatibility):
		return "compatibility"
	case errors.Is(err, goodreads.ErrSessionExpired):
		return "session_expired"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "other"
	}
}

func cloneURL(in *url.URL) *url.URL {
	copied := *in
	return &copied
}

func unique(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func dash(value string) string {
	if value == "" {
		return "-"
	}
	if value == "infinite" {
		return "infinite"
	}
	if _, err := strconv.Atoi(value); err == nil {
		return value
	}
	return "non-numeric"
}
