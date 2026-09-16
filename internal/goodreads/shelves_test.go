package goodreads

import (
	"errors"
	"strings"
	"testing"

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
