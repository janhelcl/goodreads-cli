package browser

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/janhelcl/goodreads-cli/internal/profile"
)

func TestOriginPolicy(t *testing.T) {
	allowed := []string{"https://www.goodreads.com"}
	for _, raw := range []string{"https://www.goodreads.com/book/show/1", "https://www.goodreads.com:443/review/list"} {
		if !originAllowed(raw, allowed) {
			t.Fatalf("rejected %s", raw)
		}
	}
	for _, raw := range []string{"http://www.goodreads.com", "https://www.goodreads.com.evil.example", "file:///etc/passwd", "https://user@www.goodreads.com"} {
		if originAllowed(raw, allowed) {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, raw := range []string{"ws://127.0.0.1:1234/devtools/browser/x", "ws://[::1]:1234/devtools/browser/x"} {
		if !loopbackControlURL(raw) {
			t.Fatalf("rejected local CDP URL %s", raw)
		}
	}
	if loopbackControlURL("ws://0.0.0.0:1234/devtools/browser/x") || loopbackControlURL("ws://example.com:1234/devtools/browser/x") {
		t.Fatal("accepted non-local CDP URL")
	}
}

func TestExplicitBrowserValidation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-only")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "browser")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'Google Chrome 123.0'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveExecutable(context.Background(), path)
	if err != nil || got.Product != "Chrome" || got.Path != path {
		t.Fatalf("browser: %+v err=%v", got, err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'Other Browser 1.0'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveExecutable(context.Background(), path); err == nil {
		t.Fatal("accepted unsupported browser")
	}
}

func TestRuntimeErrorClassification(t *testing.T) {
	if got := numericVersion("Google Chrome 143.0.7499.40"); got != "143.0.7499.40" {
		t.Fatalf("version=%q", got)
	}
	if err := classifyRuntimeError(errors.New("net::ERR_CONNECTION_RESET")); !errors.Is(err, ErrNetwork) {
		t.Fatalf("network error=%v", err)
	}
	if err := classifyRuntimeError(context.DeadlineExceeded); !errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, ErrNetwork) {
		t.Fatalf("deadline was reclassified: %v", err)
	}
	if err := classifyRuntimeError(errors.New("element not found")); errors.Is(err, ErrNetwork) {
		t.Fatalf("DOM error was reclassified: %v", err)
	}
}

func TestBrowserProductPinnedToProfile(t *testing.T) {
	paths := profile.PathsForRoot(filepath.Join(t.TempDir(), "app"))
	if err := paths.EnsureBrowser(); err != nil {
		t.Fatal(err)
	}
	if err := recordBrowserProduct(paths.Browser, "Chrome"); err != nil {
		t.Fatal(err)
	}
	if err := recordBrowserProduct(paths.Browser, "Chrome"); err != nil {
		t.Fatalf("same product after upgrade: %v", err)
	}
	if err := checkBrowserProduct(paths.Browser, "Chromium"); !errors.Is(err, ErrLaunch) {
		t.Fatalf("different product was accepted: %v", err)
	}
	if err := paths.RemoveBrowser(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(productMarkerPath(paths.Browser)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("logout left browser marker: %v", err)
	}
}

// This component test is opt-in for machines/CI jobs with a usable Chromium.
// It exercises the real Rod wrapper against a local page, without credentials.
func TestRodProfilePersistsAndRejectsRedirect(t *testing.T) {
	if os.Getenv("GOODREADS_BROWSER_TESTS") != "1" {
		t.Skip("set GOODREADS_BROWSER_TESTS=1 for local Chromium component test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	paths := profile.PathsForRoot(filepath.Join(t.TempDir(), "app"))
	lock, err := paths.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if err := paths.EnsureBrowser(); err != nil {
		t.Fatal(err)
	}
	escape := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html><body>unexpected origin</body></html>")
	}))
	defer escape.Close()
	var mutationRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/select" {
			fmt.Fprint(w, `<html><body><select id="year"><option>Year</option><option>2026</option></select></body></html>`)
			return
		}
		if r.URL.Path == "/input" {
			fmt.Fprint(w, `<html><body><textarea id="review">existing text</textarea></body></html>`)
			return
		}
		if r.URL.Path == "/download" {
			fmt.Fprint(w, `<html><body><a id="export" href="/library.csv">download</a></body></html>`)
			return
		}
		if r.URL.Path == "/library.csv" {
			w.Header().Set("Content-Disposition", `attachment; filename="library.csv"`)
			w.Header().Set("Content-Type", "text/csv")
			fmt.Fprint(w, "Book Id,Title\n1,Fixture\n")
			return
		}
		if r.URL.Path == "/request" {
			fmt.Fprint(w, `<html><body>
				<button id="mutate" onclick="fetch('/mutation', {method: 'POST'})">mutate</button>
				<button id="confirm" onclick="if (confirm('continue?')) fetch('/mutation', {method: 'POST'})">confirm</button>
			</body></html>`)
			return
		}
		if r.URL.Path == "/mutation" {
			mutationRequests.Add(1)
			fmt.Fprint(w, "ok")
			return
		}
		if r.URL.Path == "/escape" {
			http.Redirect(w, r, escape.URL, http.StatusFound)
			return
		}
		if r.URL.Path == "/set" {
			http.SetCookie(w, &http.Cookie{Name: "fixture", Value: "persisted", Path: "/", MaxAge: 3600})
		}
		fmt.Fprint(w, "<html><body>fixture</body></html>")
	}))
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	downloadDir := t.TempDir()
	if err := os.Chmod(downloadDir, 0700); err != nil {
		t.Fatal(err)
	}
	opts := LaunchOptions{
		ProfileDir: paths.Browser, Headless: true, DownloadDir: downloadDir,
		AllowedOrigins: []string{u.Scheme + "://" + u.Host},
	}
	factory := RodFactory{}
	first, err := factory.Launch(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	page, err := first.NewPage(ctx, server.URL+"/set")
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	immediate, err := page.(*rodPage).rod.Eval("() => document.cookie")
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	if immediate.Value.String() != "fixture=persisted" {
		_ = first.Close()
		t.Fatalf("initial cookie missing: %q", immediate.Value.String())
	}
	_ = page.Close()
	requestPage, err := first.NewPage(ctx, server.URL+"/request")
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	if err := requestPage.ClickAndWaitForRequest(ctx, "#mutate"); err != nil {
		_ = requestPage.Close()
		_ = first.Close()
		t.Fatal(err)
	}
	_ = requestPage.Close()
	if mutationRequests.Load() != 1 {
		_ = first.Close()
		t.Fatalf("mutation request count=%d", mutationRequests.Load())
	}
	confirmPage, err := first.NewPage(ctx, server.URL+"/request")
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	if err := confirmPage.ClickAndAcceptConfirmAndWaitForRequest(ctx, "#confirm"); err != nil {
		_ = confirmPage.Close()
		_ = first.Close()
		t.Fatal(err)
	}
	_ = confirmPage.Close()
	if mutationRequests.Load() != 2 {
		_ = first.Close()
		t.Fatalf("confirmed mutation request count=%d", mutationRequests.Load())
	}
	selectPage, err := first.NewPage(ctx, server.URL+"/select")
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	if err := selectPage.SelectValue(ctx, "#year", "2026"); err != nil {
		_ = selectPage.Close()
		_ = first.Close()
		t.Fatal(err)
	}
	if selected, err := selectPage.Value(ctx, "#year"); err != nil || selected != "2026" {
		_ = selectPage.Close()
		_ = first.Close()
		t.Fatalf("selected value=%q err=%v", selected, err)
	}
	_ = selectPage.Close()
	inputPage, err := first.NewPage(ctx, server.URL+"/input")
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	if err := inputPage.Input(ctx, "#review", "replacement ✓"); err != nil {
		_ = inputPage.Close()
		_ = first.Close()
		t.Fatal(err)
	}
	if entered, err := inputPage.Value(ctx, "#review"); err != nil || entered != "replacement ✓" {
		_ = inputPage.Close()
		_ = first.Close()
		t.Fatalf("replacement value=%q err=%v", entered, err)
	}
	if err := inputPage.Input(ctx, "#review", ""); err != nil {
		_ = inputPage.Close()
		_ = first.Close()
		t.Fatal(err)
	}
	if entered, err := inputPage.Value(ctx, "#review"); err != nil || entered != "" {
		_ = inputPage.Close()
		_ = first.Close()
		t.Fatalf("cleared value=%q err=%v", entered, err)
	}
	_ = inputPage.Close()
	downloadPage, err := first.NewPage(ctx, server.URL+"/download")
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	download, err := downloadPage.ClickAndWaitForDownload(ctx, "#export")
	_ = downloadPage.Close()
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	content, err := os.ReadFile(download.Path)
	if err != nil || download.SuggestedFilename != "library.csv" ||
		string(content) != "Book Id,Title\n1,Fixture\n" {
		_ = first.Close()
		t.Fatalf("download=%+v content=%q err=%v", download, string(content), err)
	}
	if _, err := first.NewPage(ctx, server.URL+"/escape"); !errors.Is(err, ErrOrigin) {
		_ = first.Close()
		t.Fatalf("redirect error: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := factory.Launch(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	page, err = second.NewPage(ctx, server.URL+"/check")
	if err != nil {
		t.Fatal(err)
	}
	defer page.Close()
	value, err := page.(*rodPage).rod.Eval("() => document.cookie")
	if err != nil {
		t.Fatal(err)
	}
	if value.Value.String() != "fixture=persisted" {
		t.Fatalf("profile cookie did not persist: %q", value.Value.String())
	}
}

func TestRodCancellationStopsBrowser(t *testing.T) {
	if os.Getenv("GOODREADS_BROWSER_TESTS") != "1" {
		t.Skip("set GOODREADS_BROWSER_TESTS=1 for local Chromium component test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	paths := profile.PathsForRoot(filepath.Join(t.TempDir(), "app"))
	if err := paths.EnsureBrowser(); err != nil {
		t.Fatal(err)
	}
	b, err := (RodFactory{}).Launch(ctx, LaunchOptions{ProfileDir: paths.Browser, Headless: true})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	pid := b.(*rodBrowser).launcher.PID()
	cancel()
	checkCtx, checkCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer checkCancel()
	if err := waitProcessExit(checkCtx, pid); err != nil {
		t.Fatalf("browser survived cancellation: %v", err)
	}
}
