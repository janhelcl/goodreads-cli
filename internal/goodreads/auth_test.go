package goodreads

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/janhelcl/goodreads-cli/internal/browser"
)

type fakePage struct {
	url       string
	html      string
	selectors map[string]bool
	text      map[string]bool
	click     func(string) error
	download  func(string) (browser.Download, error)
	htmlFunc  func() string
	input     func(string, string) error
	selectVal func(string, string) error
	value     func(string) (string, error)
}

func (p *fakePage) URL(context.Context) (string, error) { return p.url, nil }
func (p *fakePage) Has(_ context.Context, selector string) (bool, error) {
	return p.selectors[selector], nil
}
func (p *fakePage) HasText(_ context.Context, selector, regex string) (bool, error) {
	return p.text[selector+"|"+regex], nil
}
func (p *fakePage) Close() error { return nil }
func (p *fakePage) HTML(context.Context) (string, error) {
	if p.htmlFunc != nil {
		return p.htmlFunc(), nil
	}
	return p.html, nil
}
func (p *fakePage) Click(_ context.Context, selector string) error {
	if p.click != nil {
		return p.click(selector)
	}
	return nil
}
func (p *fakePage) ClickDOM(ctx context.Context, selector string) error {
	return p.Click(ctx, selector)
}
func (p *fakePage) ClickAndWaitForRequest(ctx context.Context, selector string) error {
	return p.Click(ctx, selector)
}
func (p *fakePage) ClickAndAcceptConfirmAndWaitForRequest(ctx context.Context, selector string) error {
	return p.Click(ctx, selector)
}
func (p *fakePage) ClickAndWaitForDownload(_ context.Context, selector string) (browser.Download, error) {
	if p.download != nil {
		return p.download(selector)
	}
	return browser.Download{}, nil
}
func (p *fakePage) Input(_ context.Context, selector, value string) error {
	if p.input != nil {
		return p.input(selector, value)
	}
	return nil
}
func (p *fakePage) SelectValue(_ context.Context, selector, value string) error {
	if p.selectVal != nil {
		return p.selectVal(selector, value)
	}
	return nil
}
func (p *fakePage) Value(_ context.Context, selector string) (string, error) {
	if p.value != nil {
		return p.value(selector)
	}
	return "", nil
}

type fakeBrowser struct {
	pages      map[string]browser.Page
	pagesQueue map[string][]browser.Page
	calls      []string
}

func (b *fakeBrowser) NewPage(_ context.Context, target string) (browser.Page, error) {
	b.calls = append(b.calls, target)
	if queued := b.pagesQueue[target]; len(queued) > 0 {
		b.pagesQueue[target] = queued[1:]
		return queued[0], nil
	}
	if page := b.pages[target]; page != nil {
		return page, nil
	}
	if target == shelfTarget("", exactShelfPageSize).String() {
		if queued := b.pagesQueue[libraryURL]; len(queued) > 0 {
			b.pagesQueue[libraryURL] = queued[1:]
			return queued[0], nil
		}
		return b.pages[libraryURL], nil
	}
	return nil, nil
}
func (b *fakeBrowser) Close() error { return nil }

func privatePage() *fakePage {
	return &fakePage{
		url:       "https://www.goodreads.com/review/list/123",
		selectors: map[string]bool{signOutCSS: true, "#books": true, "#booksBody": true},
		text:      map[string]bool{"h1|^My Books$": true},
	}
}

func TestStatusRequiresPrivatePageAndMarkers(t *testing.T) {
	original := pageReadyTimeout
	pageReadyTimeout = 80 * time.Millisecond
	t.Cleanup(func() { pageReadyTimeout = original })
	for _, tc := range []struct {
		name    string
		page    *fakePage
		want    bool
		wantErr error
	}{
		{"authenticated", privatePage(), true, nil},
		{"signed-out redirect", &fakePage{url: signInURL}, false, nil},
		{"missing marker", &fakePage{url: privatePage().url, text: map[string]bool{"h1|^My Books$": true}}, false, ErrCompatibility},
		{"service path", &fakePage{url: "https://www.goodreads.com/error"}, false, browser.ErrNetwork},
		{"service response", &fakePage{url: privatePage().url, html: "<title>503 Service Unavailable</title>"}, false, browser.ErrNetwork},
		{"unexpected port", &fakePage{url: "https://www.goodreads.com:8443/review/list/123"}, false, ErrCompatibility},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &fakeBrowser{pages: map[string]browser.Page{libraryURL: tc.page}}
			got, err := Status(context.Background(), b)
			if got.Connected != tc.want || !errors.Is(err, tc.wantErr) {
				t.Fatalf("status=%+v err=%v; want connected=%v err=%v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestStatusWaitsForPrivateLibraryMarkers(t *testing.T) {
	page := privatePage()
	page.selectors = map[string]bool{}
	page.text = map[string]bool{}
	reads := 0
	page.htmlFunc = func() string {
		reads++
		if reads >= 3 {
			page.selectors = privatePage().selectors
			page.text = privatePage().text
		}
		return "<html><body></body></html>"
	}
	b := &fakeBrowser{pages: map[string]browser.Page{libraryURL: page}}
	got, err := Status(context.Background(), b)
	if err != nil || !got.Connected || !got.SessionValid || reads < 3 {
		t.Fatalf("status=%+v err=%v reads=%d", got, err, reads)
	}
}

func TestLoginCompletesFreshInteractivePath(t *testing.T) {
	b := &fakeBrowser{
		pages: map[string]browser.Page{
			signInURL: &fakePage{url: "https://www.goodreads.com/", selectors: map[string]bool{signOutCSS: true}},
		},
		pagesQueue: map[string][]browser.Page{
			libraryURL: {&fakePage{url: signInURL}, privatePage()},
		},
	}
	got, err := Login(context.Background(), b)
	if err != nil || !got.Connected || len(b.calls) != 3 {
		t.Fatalf("fresh login=%+v err=%v calls=%v", got, err, b.calls)
	}
}

func TestLoginDoesNotOpenLibraryTabUntilSignInTabLooksAuthenticated(t *testing.T) {
	for _, raw := range []string{
		"https://www.goodreads.com/user/sign_in",
		"https://www.goodreads.com/ap/signin",
		"https://www.goodreads.com/",
	} {
		t.Run(raw, func(t *testing.T) {
			b := &fakeBrowser{pages: map[string]browser.Page{
				signInURL:  &fakePage{url: raw},
				libraryURL: &fakePage{url: signInURL},
			}}
			ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
			defer cancel()
			if _, err := Login(ctx, b); !errors.Is(err, ErrLoginCancelled) {
				t.Fatalf("login err=%v", err)
			}
			if len(b.calls) != 2 || b.calls[0] != libraryURL || b.calls[1] != signInURL {
				t.Fatalf("calls=%v", b.calls)
			}
		})
	}
}

func TestLoginOpensLibraryTabOnceAfterProviderSignInAuthenticates(t *testing.T) {
	page := &loginWatchPage{
		step: -1,
		urls: []string{
			"https://www.goodreads.com/user/sign_in",
			"https://www.goodreads.com/ap/signin",
			"https://www.goodreads.com/",
		},
		signOut: []bool{false, false, true},
	}
	b := &fakeBrowser{
		pages: map[string]browser.Page{signInURL: page},
		pagesQueue: map[string][]browser.Page{
			libraryURL: {&fakePage{url: signInURL}, privatePage()},
		},
	}
	got, err := Login(context.Background(), b)
	if err != nil || !got.Connected || !got.SessionValid {
		t.Fatalf("login=%+v err=%v", got, err)
	}
	if len(b.calls) != 3 || b.calls[0] != libraryURL || b.calls[1] != signInURL || b.calls[2] != libraryURL {
		t.Fatalf("calls=%v", b.calls)
	}
}

type loginWatchPage struct {
	fakePage
	step    int
	urls    []string
	signOut []bool
}

func (p *loginWatchPage) URL(context.Context) (string, error) {
	if p.step < len(p.urls)-1 {
		p.step++
	}
	return p.urls[p.step], nil
}

func (p *loginWatchPage) Has(_ context.Context, selector string) (bool, error) {
	if selector == signOutCSS && p.step >= 0 && p.step < len(p.signOut) {
		return p.signOut[p.step], nil
	}
	return p.selectors[selector], nil
}

func TestLoginVerifiesPrivatePage(t *testing.T) {
	b := &fakeBrowser{pages: map[string]browser.Page{
		signInURL:  &fakePage{url: "https://www.goodreads.com/", selectors: map[string]bool{signOutCSS: true}},
		libraryURL: privatePage(),
	}}
	got, err := Login(context.Background(), b)
	if err != nil || !got.Connected || !got.SessionValid || len(b.calls) != 1 {
		t.Fatalf("login=%+v err=%v calls=%v", got, err, b.calls)
	}
	b.pages[libraryURL] = &fakePage{url: signInURL}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := Login(ctx, b); !errors.Is(err, ErrLoginCancelled) {
		t.Fatalf("login accepted failed private-page validation: %v", err)
	}
}
