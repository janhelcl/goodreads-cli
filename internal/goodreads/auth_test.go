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
func (p *fakePage) Close() error                         { return nil }
func (p *fakePage) HTML(context.Context) (string, error) { return p.html, nil }
func (p *fakePage) Click(_ context.Context, selector string) error {
	if p.click != nil {
		return p.click(selector)
	}
	return nil
}
func (p *fakePage) ClickAndWaitForRequest(ctx context.Context, selector string) error {
	return p.Click(ctx, selector)
}
func (p *fakePage) ClickAndAcceptConfirmAndWaitForRequest(ctx context.Context, selector string) error {
	return p.Click(ctx, selector)
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
	return b.pages[target], nil
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
	for _, tc := range []struct {
		name    string
		page    *fakePage
		want    bool
		wantErr error
	}{
		{"authenticated", privatePage(), true, nil},
		{"signed-out redirect", &fakePage{url: signInURL}, false, nil},
		{"missing marker", &fakePage{url: privatePage().url, text: map[string]bool{"h1|^My Books$": true}}, false, ErrCompatibility},
		{"service page", &fakePage{url: "https://www.goodreads.com/error"}, false, ErrCompatibility},
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
