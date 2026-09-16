//go:build liveprobe

// authprobe compares safe structural markers in authenticated and fresh browser
// profiles. It never prints page text, HTML, cookies, or account identifiers.
package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/profile"
)

type marker struct {
	name     string
	selector string
	text     string
}

var markers = []marker{
	{"heading-my-books", "h1", "My Books"},
	{"secondary-heading-my-books", "h2", "My Books"},
	{"books-table", "#books", ""},
	{"books-body", "#booksBody", ""},
	{"paginated-list", "#paginatedReviewList", ""},
	{"shelves", "#shelves", ""},
	{"shelf-list", "#shelfList", ""},
	{"my-books", "#myBooks", ""},
	{"private-library-link", "a[href^='/review/list/']", ""},
	{"sign-out-link", "a[href*='/user/sign_out']", ""},
	{"sign-in-link", "a[href*='/user/sign_in']", ""},
	{"shelf-form", "form[action*='/review/list']", ""},
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	paths, err := profile.DefaultPaths()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot locate CLI profile")
		os.Exit(1)
	}
	if _, err := os.Stat(paths.Browser); err != nil {
		fmt.Fprintln(os.Stderr, "CLI profile missing; run the headed liveprobe first")
		os.Exit(1)
	}
	lock, err := paths.Acquire(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "CLI profile busy")
		os.Exit(1)
	}
	defer lock.Release()

	factory := browser.RodFactory{}
	compare(ctx, factory, "saved", paths.Browser)

	tmp, err := os.MkdirTemp("", "goodreads-auth-probe-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot create temporary browser profile")
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)
	fresh := filepath.Join(tmp, "chromium-profile")
	if err := os.Mkdir(fresh, 0700); err != nil {
		fmt.Fprintln(os.Stderr, "cannot create temporary browser profile")
		os.Exit(1)
	}
	compare(ctx, factory, "fresh", fresh)
}

func compare(ctx context.Context, factory browser.RodFactory, label, dir string) {
	b, err := factory.Launch(ctx, browser.LaunchOptions{ProfileDir: dir, Headless: true})
	if err != nil {
		fmt.Fprintln(os.Stderr, label, "browser launch failed")
		return
	}
	defer b.Close()
	p, err := b.NewPage(ctx, "https://www.goodreads.com/review/list")
	if err != nil {
		fmt.Fprintln(os.Stderr, label, "library navigation failed")
		return
	}
	defer p.Close()
	raw, err := p.URL(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, label, "page URL unavailable")
		return
	}
	fmt.Println(label, "page", safePath(raw))
	for _, m := range markers {
		var found bool
		if m.text == "" {
			found, err = p.Has(ctx, m.selector)
		} else {
			found, err = p.HasText(ctx, m.selector, m.text)
		}
		if err != nil {
			fmt.Println(label, m.name, "query-error")
		} else {
			fmt.Println(label, m.name, found)
		}
	}
}

var numericPathSegment = regexp.MustCompile(`/[0-9]+`)

func safePath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "unknown"
	}
	path := numericPathSegment.ReplaceAllString(u.Path, "/{id}")
	if strings.HasPrefix(path, "/review/list/") {
		path = "/review/list/{account}"
	}
	return u.Scheme + "://" + u.Host + path
}
