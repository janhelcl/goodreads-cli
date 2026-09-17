package goodreads

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/janhelcl/goodreads-cli/internal/domain"
)

const shelfFixture = `<html><body><h1>My Books</h1><a href="/user/sign_out">Sign out</a>
<table id="books"><thead><tr><th class="field title">title</th><th class="field author">author</th></tr></thead>
<tbody id="booksBody"><tr id="review_7">
<td class="field title"><div class="value"><a href="/book/show/42.Invented_Book">Invented Book</a></div></td>
<td class="field author"><div class="value"><a href="/author/show/8">Ada Example</a></div></td>
<td class="field isbn"><div class="value">0-306-40615-2</div></td>
<td class="field isbn13"><div class="value">9780306406157</div></td>
<td class="field avg_rating"><div class="value">4.25</div></td>
<td class="field rating"><div class="value"><span class="staticStars">
<span class="staticStar p10"></span><span class="staticStar p10"></span><span class="staticStar p0"></span><span class="staticStar p0"></span><span class="staticStar p0"></span>
</span></div></td>
<td class="field shelves"><div class="value"><a href="?shelf=read">read</a>, <a href="?shelf=speculative">speculative</a></div></td>
<td class="field date_read"><div class="value">Sep 12, 2026</div></td>
<td class="field date_added"><div class="value">Aug 1, 2026</div></td>
</tr></tbody></table><a class="next_page" href="?page=2">next</a></body></html>`

func TestParseShelfPage(t *testing.T) {
	page, err := parseShelfPage(shelfFixture)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Books) != 1 || page.Next != "?page=2" {
		t.Fatalf("page=%+v", page)
	}
	b := page.Books[0]
	if b.BookID != "42" || b.Title != "Invented Book" || b.Author != "Ada Example" ||
		b.ISBN10 != "0306406152" || b.ISBN13 != "9780306406157" || b.Rating != 2 ||
		b.Status != domain.StatusRead || b.DateRead == nil || *b.DateRead != "2026-09-12" ||
		len(b.Bookshelves) != 1 || b.Bookshelves[0] != "speculative" {
		t.Fatalf("book=%+v", b)
	}
}

func TestShelfParserFailsClosedOnDrift(t *testing.T) {
	for _, tc := range []struct{ name, html string }{
		{"missing table", "<h1>My Books</h1>"},
		{"missing book link", strings.Replace(shelfFixture, "/book/show/42.Invented_Book", "/search?q=42", 1)},
		{"missing exclusive shelf", strings.Replace(shelfFixture, ">read</a>", ">other</a>", 1)},
		{"ambiguous exclusive shelf", strings.Replace(shelfFixture, "?shelf=speculative\">speculative", "?shelf=to-read\">to-read", 1)},
		{"unknown star", strings.Replace(shelfFixture, "staticStar p10", "staticStar p6", 1)},
		{"conflicting ISBNs", strings.Replace(shelfFixture, "9780306406157", "9781603580557", 1)},
		{"noncontiguous stars", strings.Replace(shelfFixture, `<span class="staticStar p10"></span><span class="staticStar p0"></span>`, `<span class="staticStar p0"></span><span class="staticStar p10"></span>`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseShelfPage(tc.html); !errors.Is(err, ErrCompatibility) {
				t.Fatalf("expected compatibility error, got %v", err)
			}
		})
	}
}

func TestEmptyShelfTable(t *testing.T) {
	empty := `<h1>My Books</h1><a href="/user/sign_out">Sign out</a><table id="books"><thead><tr><th class="field title">title</th><th class="field author">author</th></tr></thead><tbody id="booksBody"></tbody></table>`
	got, err := parseShelfPage(empty)
	if err != nil || len(got.Books) != 0 || got.Next != "" {
		t.Fatalf("empty page=%+v err=%v", got, err)
	}
}

func TestRecognizedLibraryHeading(t *testing.T) {
	for _, heading := range []string{
		"My Books",
		"My Books:\n Want to Read\u200e\n (1)",
		"My Books: Currently Reading (0)",
		"My Books: Read (20)",
	} {
		if !recognizedLibraryHeading(heading) {
			t.Fatalf("rejected heading %q", heading)
		}
	}
	for _, heading := range []string{"Books", "My Books: Favorites (1)", "My Books: Read", "My Books: Read (many)"} {
		if recognizedLibraryHeading(heading) {
			t.Fatalf("accepted heading %q", heading)
		}
	}
}

func TestParseOwnerRatingControl(t *testing.T) {
	cell := func(rating string, stars int) *goquery.Selection {
		links := strings.Repeat(`<a class="star off" href="#"></a>`, stars)
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(fmt.Sprintf(
			`<table><tr><td class="field rating"><div class="value"><div class="stars" data-rating="%s">%s</div></div></td></tr></table>`,
			rating, links,
		)))
		if err != nil {
			t.Fatal(err)
		}
		return doc.Find("td.field.rating")
	}
	for _, tc := range []struct {
		rating string
		stars  int
		want   int
		ok     bool
	}{
		{"0.0", 5, 0, true},
		{"4.0", 5, 4, true},
		{"4.5", 5, 0, false},
		{"6.0", 5, 0, false},
		{"4.0", 4, 0, false},
	} {
		got, err := parsePersonalRating(cell(tc.rating, tc.stars))
		if (err == nil) != tc.ok || got != tc.want {
			t.Fatalf("rating=%q stars=%d: got=%d err=%v", tc.rating, tc.stars, got, err)
		}
	}
}

func TestShelfDateTextExcludesEditControl(t *testing.T) {
	for _, tc := range []struct {
		html string
		want *string
	}{
		{`<td><div class="value"><div class="editable_date"><span class="greyText">Not set</span><a href="#">[edit]</a></div></div></td>`, nil},
		{`<td><div class="value"><div class="editable_date">Sep 12, 2026 <a href="#">edit</a></div></div></td>`, stringPointer("2026-09-12")},
	} {
		doc, err := goquery.NewDocumentFromReader(strings.NewReader("<table><tr>" + tc.html + "</tr></table>"))
		if err != nil {
			t.Fatal(err)
		}
		got, err := parseShelfDate(shelfDateText(doc.Find("td")))
		if err != nil || !equalOptionalString(got, tc.want) {
			t.Fatalf("date=%v err=%v want=%v", got, err, tc.want)
		}
	}
}

func TestParseShelfDateCellHandlesReadingSessions(t *testing.T) {
	cell := func(rows string) *goquery.Selection {
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(
			`<table><tr><td class="field date_read"><div class="value">` + rows + `</div></td></tr></table>`,
		))
		if err != nil {
			t.Fatal(err)
		}
		return doc.Find("td")
	}
	unset := `<div class="date_row"><div class="editable_date"><span class="greyText">Not set</span><a href="#">[edit]</a></div></div>`
	got, err := parseShelfDateCell(cell(unset + unset))
	if err != nil || got != nil {
		t.Fatalf("repeated unset date=%v err=%v", got, err)
	}
	got, err = parseShelfDateCell(cell(unset + `<div class="date_row"><div class="editable_date">Sep 12, 2026 <a href="#">[edit]</a></div></div>`))
	if err != nil || got == nil || *got != "2026-09-12" {
		t.Fatalf("one completed session date=%v err=%v", got, err)
	}
	_, err = parseShelfDateCell(cell(
		`<div class="date_row"><div class="editable_date">Sep 12, 2026</div></div>` +
			`<div class="date_row"><div class="editable_date">Sep 13, 2026</div></div>`,
	))
	if !errors.Is(err, ErrCompatibility) {
		t.Fatalf("expected ambiguous reread dates, got %v", err)
	}
}

func stringPointer(value string) *string {
	return &value
}
