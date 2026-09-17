package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
	"github.com/janhelcl/goodreads-cli/internal/profile"
)

type pageStub struct{ url string }

func (p pageStub) URL(context.Context) (string, error)                   { return p.url, nil }
func (p pageStub) Has(context.Context, string) (bool, error)             { return true, nil }
func (p pageStub) HasText(context.Context, string, string) (bool, error) { return true, nil }
func (p pageStub) Close() error                                          { return nil }
func (p pageStub) HTML(context.Context) (string, error)                  { return "", nil }
func (p pageStub) Click(context.Context, string) error                   { return nil }
func (p pageStub) ClickAndWaitForRequest(context.Context, string) error  { return nil }
func (p pageStub) ClickAndAcceptConfirmAndWaitForRequest(context.Context, string) error {
	return nil
}
func (p pageStub) ClickAndWaitForDownload(context.Context, string) (browser.Download, error) {
	return browser.Download{}, nil
}
func (p pageStub) Input(context.Context, string, string) error       { return nil }
func (p pageStub) SelectValue(context.Context, string, string) error { return nil }
func (p pageStub) Value(context.Context, string) (string, error)     { return "", nil }

type browserStub struct {
	closeErr   error
	closeCalls int
}

func (*browserStub) NewPage(_ context.Context, target string) (browser.Page, error) {
	if target == "https://www.goodreads.com/user/sign_in" {
		return pageStub{url: "https://www.goodreads.com/"}, nil
	}
	return pageStub{url: "https://www.goodreads.com/review/list/123"}, nil
}
func (b *browserStub) Close() error {
	b.closeCalls++
	return b.closeErr
}

type factoryStub struct {
	browser *browserStub
	calls   []browser.LaunchOptions
}

func (f *factoryStub) Launch(_ context.Context, opts browser.LaunchOptions) (browser.Browser, error) {
	f.calls = append(f.calls, opts)
	if f.browser == nil {
		f.browser = &browserStub{}
	}
	return f.browser, nil
}

func TestAuthProfileLifecycle(t *testing.T) {
	paths := profile.PathsForRoot(filepath.Join(t.TempDir(), "app"))
	factory := &factoryStub{}
	auth := service{factory: factory, paths: paths}
	disconnected, err := auth.Status(context.Background())
	if err != nil || disconnected.Connected || len(factory.calls) != 0 {
		t.Fatalf("missing profile status=%+v err=%v launches=%d", disconnected, err, len(factory.calls))
	}
	connected, err := auth.Login(context.Background())
	if err != nil || !connected.Connected || len(factory.calls) != 1 || factory.calls[0].Headless {
		t.Fatalf("login=%+v err=%v launch=%+v", connected, err, factory.calls)
	}
	connected, err = auth.Status(context.Background())
	if err != nil || !connected.Connected || len(factory.calls) != 2 || !factory.calls[1].Headless {
		t.Fatalf("status=%+v err=%v launch=%+v", connected, err, factory.calls)
	}
	sibling := filepath.Join(paths.Root, "keep")
	if err := os.WriteFile(sibling, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		result, err := auth.Logout(context.Background())
		if err != nil || !result.ProfileRemoved {
			t.Fatalf("logout %d: %+v err=%v", i, result, err)
		}
	}
	if _, err := os.Stat(sibling); err != nil {
		t.Fatalf("logout removed sibling file: %v", err)
	}
	exists, err := paths.HasBrowser()
	if err != nil || exists {
		t.Fatalf("profile still present: %v %v", exists, err)
	}
}

func TestAddRejectsInvalidStatusBeforeProfileOrBrowserWork(t *testing.T) {
	paths := profile.PathsForRoot(filepath.Join(t.TempDir(), "app"))
	factory := &factoryStub{}
	auth := service{factory: factory, paths: paths}
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Add(context.Background(), isbn, "paused"); !errors.Is(err, domain.ErrInvalidStatus) {
		t.Fatalf("err=%v", err)
	}
	if len(factory.calls) != 0 {
		t.Fatalf("browser launched for invalid status: %+v", factory.calls)
	}
	if _, err := os.Stat(paths.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("profile path created for invalid status: %v", err)
	}
}

func TestReviewRejectsOmittedValueBeforeProfileOrBrowserWork(t *testing.T) {
	paths := profile.PathsForRoot(filepath.Join(t.TempDir(), "app"))
	factory := &factoryStub{}
	auth := service{factory: factory, paths: paths}
	isbn, err := domain.NormalizeISBN("9780306406157")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Review(context.Background(), isbn, nil); !errors.Is(err, domain.ErrInvalidReview) {
		t.Fatalf("err=%v", err)
	}
	if len(factory.calls) != 0 {
		t.Fatalf("browser launched for omitted review: %+v", factory.calls)
	}
	if _, err := os.Stat(paths.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("profile path created for omitted review: %v", err)
	}
}

func TestServiceReturnsBrowserCloseError(t *testing.T) {
	paths := profile.PathsForRoot(filepath.Join(t.TempDir(), "app"))
	if err := paths.EnsureBrowser(); err != nil {
		t.Fatal(err)
	}
	closeErr := errors.New("close failed")
	b := &browserStub{closeErr: closeErr}
	auth := service{factory: &factoryStub{browser: b}, paths: paths}

	if _, err := auth.Status(context.Background()); !errors.Is(err, closeErr) {
		t.Fatalf("status close err=%v", err)
	}
	if b.closeCalls != 1 {
		t.Fatalf("browser close calls=%d", b.closeCalls)
	}
	lock, err := paths.Acquire(context.Background())
	if err != nil {
		t.Fatalf("profile lock was not released: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
}
