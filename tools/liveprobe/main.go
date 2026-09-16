//go:build liveprobe

// This developer-only probe records browser/profile behavior with a dedicated
// Goodreads test account. It is excluded from ordinary builds and tests.
package main

import (
	"bufio"
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"time"

	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/profile"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "probe failed:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	paths, err := profile.DefaultPaths()
	if err != nil {
		return err
	}
	lock, err := paths.Acquire(ctx)
	if err != nil {
		return err
	}
	defer lock.Release()
	if err := paths.EnsureBrowser(); err != nil {
		return err
	}
	factory := browser.RodFactory{}
	options := browser.LaunchOptions{ProfileDir: paths.Browser, InteractiveLogin: true}
	fmt.Fprintln(os.Stderr, "Use a dedicated, non-critical Goodreads test account. Complete sign-in yourself in the visible browser; this probe never reads or enters credentials.")
	b, err := factory.Launch(ctx, options)
	if err != nil {
		return err
	}
	page, err := b.NewPage(ctx, "https://www.goodreads.com/user/sign_in")
	if err != nil {
		_ = b.Close()
		return err
	}
	fmt.Fprintln(os.Stderr, "Press Enter after sign-in is complete in the browser.")
	input := make(chan error, 1)
	go func() {
		_, err := bufio.NewReader(os.Stdin).ReadString('\n')
		input <- err
	}()
	select {
	case err = <-input:
	case <-ctx.Done():
		err = ctx.Err()
	}
	if err != nil {
		_ = page.Close()
		_ = b.Close()
		return err
	}
	if raw, err := page.URL(ctx); err == nil {
		fmt.Fprintln(os.Stderr, "headed final page:", safePage(raw))
	}
	_ = page.Close()
	private, err := b.NewPage(ctx, "https://www.goodreads.com/review/list")
	if err != nil {
		_ = b.Close()
		return err
	}
	if raw, err := private.URL(ctx); err == nil {
		fmt.Fprintln(os.Stderr, "headed private-page navigation:", safePage(raw))
	}
	_ = private.Close()
	if err := b.Close(); err != nil {
		return err
	}
	options.Headless = true
	options.InteractiveLogin = false
	b, err = factory.Launch(ctx, options)
	if err != nil {
		return err
	}
	defer b.Close()
	private, err = b.NewPage(ctx, "https://www.goodreads.com/review/list")
	if err != nil {
		return err
	}
	defer private.Close()
	raw, err := private.URL(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "headless private-page navigation after restart:", safePage(raw))
	fmt.Fprintln(os.Stderr, "These URLs are navigation observations only; use gr status to validate the authenticated private-page markers.")
	return nil
}

var numericPathSegment = regexp.MustCompile(`/[0-9]+`)

func safePage(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "unknown page"
	}
	return u.Scheme + "://" + u.Host + numericPathSegment.ReplaceAllString(u.Path, "/{id}")
}
