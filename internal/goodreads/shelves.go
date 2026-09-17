package goodreads

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/janhelcl/goodreads-cli/internal/domain"
)

var bookPath = regexp.MustCompile(`^/book/show/([0-9]+)(?:[./-]|$)`)
var filteredLibraryHeading = regexp.MustCompile(`^My Books: (Want to Read|Currently Reading|Read) \([0-9]+\)$`)

type shelfPage struct {
	Books []domain.Book
	Next  string
}

func parseShelfPage(raw string) (shelfPage, error) {
	if len(raw) > 10<<20 {
		return shelfPage{}, fmt.Errorf("%w at library.page: page too large", ErrCompatibility)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(raw))
	if err != nil {
		return shelfPage{}, fmt.Errorf("%w at library.page: invalid HTML", ErrCompatibility)
	}
	if doc.Find("#books").Length() != 1 || doc.Find("#booksBody").Length() != 1 ||
		doc.Find("#books th.field.title").Length() != 1 || doc.Find("#books th.field.author").Length() != 1 ||
		!recognizedLibraryHeading(doc.Find("h1").First().Text()) || doc.Find(signOutCSS).Length() == 0 {
		return shelfPage{}, fmt.Errorf("%w at library.page: required table markers missing", ErrCompatibility)
	}
	result := shelfPage{Books: []domain.Book{}}
	var rowErr error
	doc.Find("#booksBody > tr").EachWithBreak(func(_ int, row *goquery.Selection) bool {
		book, err := parseShelfRow(row)
		if err != nil {
			rowErr = err
			return false
		}
		result.Books = append(result.Books, book)
		return true
	})
	if rowErr != nil {
		return shelfPage{}, rowErr
	}
	if next := doc.Find("a.next_page"); next.Length() > 0 {
		var ok bool
		result.Next, ok = next.First().Attr("href")
		if !ok || result.Next == "" {
			return shelfPage{}, fmt.Errorf("%w at library.page: pagination link missing target", ErrCompatibility)
		}
	}
	return result, nil
}

func recognizedLibraryHeading(raw string) bool {
	normalized := strings.Join(strings.Fields(strings.ReplaceAll(raw, "\u200e", "")), " ")
	return normalized == "My Books" || filteredLibraryHeading.MatchString(normalized)
}

func parseShelfRow(row *goquery.Selection) (domain.Book, error) {
	if id := row.AttrOr("id", ""); !strings.HasPrefix(id, "review_") {
		return domain.Book{}, fmt.Errorf("%w at library.row: unknown row identity", ErrCompatibility)
	}
	titleLink := row.Find("td.field.title .value a[href*='/book/show/']").First()
	if titleLink.Length() != 1 {
		return domain.Book{}, fmt.Errorf("%w at library.row: book link missing", ErrCompatibility)
	}
	href, _ := titleLink.Attr("href")
	u, err := url.Parse(href)
	if err != nil || !isGoodreadsPage((&url.URL{Scheme: "https", Host: "www.goodreads.com"}).ResolveReference(u).String()) {
		return domain.Book{}, fmt.Errorf("%w at library.row: invalid book link", ErrCompatibility)
	}
	match := bookPath.FindStringSubmatch(u.Path)
	if len(match) != 2 {
		return domain.Book{}, fmt.Errorf("%w at library.row: book ID missing", ErrCompatibility)
	}
	title := strings.TrimSpace(titleLink.Text())
	author := strings.TrimSpace(row.Find("td.field.author .value a").First().Text())
	if title == "" || author == "" {
		return domain.Book{}, fmt.Errorf("%w at library.row: title or author missing", ErrCompatibility)
	}
	book := domain.Book{BookID: match[1], Title: title, Author: author}
	book.ISBN10 = normalizedISBNField(row.Find("td.field.isbn .value").First().Text(), true)
	book.ISBN13 = normalizedISBNField(row.Find("td.field.isbn13 .value").First().Text(), false)
	if book.ISBN10 != "" && book.ISBN13 != "" {
		fromTen, _ := domain.NormalizeISBN(book.ISBN10)
		if fromTen.ISBN13 != book.ISBN13 {
			return domain.Book{}, fmt.Errorf("%w at library.row: conflicting edition ISBNs", ErrCompatibility)
		}
	}
	book.Rating, err = parsePersonalRating(row.Find("td.field.rating"))
	if err != nil {
		return domain.Book{}, err
	}
	if avg := strings.TrimSpace(row.Find("td.field.avg_rating .value").First().Text()); avg != "" {
		if parsed, err := strconv.ParseFloat(avg, 64); err == nil {
			book.AverageRating = &parsed
		}
	}
	book.Status, book.Bookshelves, err = parseShelves(row.Find("td.field.shelves .value"))
	if err != nil {
		return domain.Book{}, err
	}
	book.DateRead, err = parseShelfDateCell(row.Find("td.field.date_read"))
	if err != nil {
		return domain.Book{}, err
	}
	book.DateAdded, err = parseShelfDate(shelfDateText(row.Find("td.field.date_added")))
	if err != nil {
		return domain.Book{}, err
	}
	return book, nil
}

func normalizedISBNField(raw string, isbn10 bool) string {
	parsed, err := domain.NormalizeISBN(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	if isbn10 {
		return parsed.ISBN10
	}
	return parsed.ISBN13
}

func parsePersonalRating(cell *goquery.Selection) (int, error) {
	if cell.Length() != 1 {
		return 0, fmt.Errorf("%w at library.row: rating field missing", ErrCompatibility)
	}
	interactive := cell.Find("div.stars[data-rating]")
	if interactive.Length() > 0 {
		if interactive.Length() != 1 || interactive.Find("a.star").Length() != 5 {
			return 0, fmt.Errorf("%w at library.row: owner rating control changed", ErrCompatibility)
		}
		raw, ok := interactive.Attr("data-rating")
		rating, err := strconv.ParseFloat(raw, 64)
		if !ok || err != nil || rating < 0 || rating > 5 || rating != float64(int(rating)) {
			return 0, fmt.Errorf("%w at library.row: invalid owner rating", ErrCompatibility)
		}
		return int(rating), nil
	}
	stars := cell.Find("span.staticStars > span.staticStar")
	if stars.Length() != 5 {
		return 0, fmt.Errorf("%w at library.row: rating control changed", ErrCompatibility)
	}
	filled := 0
	valid := true
	emptySeen := false
	stars.Each(func(_ int, star *goquery.Selection) {
		if star.HasClass("p10") {
			filled++
			if emptySeen {
				valid = false
			}
		} else if star.HasClass("p0") {
			emptySeen = true
		} else {
			valid = false
		}
	})
	if !valid {
		return 0, fmt.Errorf("%w at library.row: unknown rating star", ErrCompatibility)
	}
	return filled, nil
}

func parseShelves(cell *goquery.Selection) (domain.ReadingStatus, []string, error) {
	if cell.Length() != 1 {
		return "", nil, fmt.Errorf("%w at library.row: shelves field missing", ErrCompatibility)
	}
	seen := map[string]bool{}
	cell.Find("a[href*='shelf=']").Each(func(_ int, link *goquery.Selection) {
		label := strings.Join(strings.Fields(strings.ToLower(link.Text())), "-")
		if label != "" {
			seen[label] = true
		}
	})
	// The owner view may render plain labels for the exclusive shelf.
	for _, part := range strings.Split(strings.ToLower(cell.Text()), ",") {
		label := strings.Join(strings.Fields(strings.TrimSpace(part)), "-")
		if label == "to-read" || label == "want-to-read" || label == "currently-reading" || label == "read" {
			seen[label] = true
		}
	}
	if seen["want-to-read"] {
		seen["to-read"] = true
		delete(seen, "want-to-read")
	}
	statuses := 0
	var status domain.ReadingStatus
	for _, core := range []domain.ReadingStatus{domain.StatusToRead, domain.StatusCurrentlyReading, domain.StatusRead} {
		if seen[string(core)] {
			status = core
			statuses++
			delete(seen, string(core))
		}
	}
	if statuses != 1 {
		return "", nil, fmt.Errorf("%w at library.row: exclusive shelf missing or ambiguous", ErrCompatibility)
	}
	custom := make([]string, 0, len(seen))
	for shelf := range seen {
		custom = append(custom, shelf)
	}
	sort.Strings(custom)
	return status, custom, nil
}

func parseShelfDate(raw string) (*string, error) {
	value := strings.TrimSpace(raw)
	if value == "" || strings.EqualFold(value, "not set") {
		return nil, nil
	}
	for _, layout := range []string{"Jan 2, 2006", "January 2, 2006", "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			date := parsed.Format("2006-01-02")
			return &date, nil
		}
	}
	return nil, fmt.Errorf("%w at library.row: unrecognized date", ErrCompatibility)
}

func shelfDateText(cell *goquery.Selection) string {
	value := cell.Find(".value").First()
	if value.Length() == 0 {
		return ""
	}
	return visibleDateText(value)
}

func parseShelfDateCell(cell *goquery.Selection) (*string, error) {
	rows := cell.Find(".value .date_row")
	if rows.Length() == 0 {
		return parseShelfDate(shelfDateText(cell))
	}
	var result *string
	var rowErr error
	rows.EachWithBreak(func(_ int, row *goquery.Selection) bool {
		parsed, err := parseShelfDate(visibleDateText(row))
		if err != nil {
			rowErr = err
			return false
		}
		if parsed == nil {
			return true
		}
		if result != nil && *result != *parsed {
			rowErr = fmt.Errorf("%w at library.row: multiple finish dates are ambiguous", ErrCompatibility)
			return false
		}
		result = parsed
		return true
	})
	return result, rowErr
}

func visibleDateText(selection *goquery.Selection) string {
	copy := selection.Clone()
	copy.Find("a,script,style").Remove()
	return strings.Join(strings.Fields(copy.Text()), " ")
}
