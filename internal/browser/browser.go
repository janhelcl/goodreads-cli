package browser

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/launcher/flags"
	"github.com/go-rod/rod/lib/proto"
)

var (
	ErrUnavailable = errors.New("supported Chrome, Chromium, or Edge browser unavailable")
	ErrLaunch      = errors.New("could not launch dedicated browser")
	ErrOrigin      = errors.New("unexpected browser navigation origin")
)

type Executable struct {
	Path    string
	Product string
	Version string
}

// ResolveExecutable does not auto-download a browser. The managed Chromium
// fallback remains gated on the compatibility and first-run download spike.
func ResolveExecutable(ctx context.Context, explicit string) (Executable, error) {
	if explicit == "" {
		explicit = os.Getenv("GOODREADS_CLI_BROWSER")
	}
	if explicit != "" {
		found, err := validateExecutable(ctx, explicit)
		if err != nil {
			return Executable{}, fmt.Errorf("%w: configured browser is unsupported (%v)", ErrUnavailable, err)
		}
		return found, nil
	}
	candidates := []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge", "microsoft-edge-stable"}
	if runtime.GOOS == "windows" {
		candidates = append(candidates,
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		)
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			candidates = append(candidates, filepath.Join(local, "Google", "Chrome", "Application", "chrome.exe"))
		}
	}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates,
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		)
	}
	for _, name := range candidates {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if found, err := validateExecutable(ctx, path); err == nil {
			return found, nil
		}
	}
	return Executable{}, ErrUnavailable
}

func validateExecutable(ctx context.Context, path string) (Executable, error) {
	resolved, err := exec.LookPath(path)
	if err != nil {
		return Executable{}, err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return Executable{}, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, resolved, "--version").Output()
	if err != nil {
		return Executable{}, err
	}
	version := strings.TrimSpace(string(out))
	product := ""
	switch {
	case strings.Contains(version, "Google Chrome"):
		product = "Chrome"
	case strings.Contains(version, "Chromium"):
		product = "Chromium"
	case strings.Contains(version, "Microsoft Edge"):
		product = "Edge"
	}
	if product == "" {
		return Executable{}, ErrUnavailable
	}
	return Executable{Path: resolved, Product: product, Version: version}, nil
}

type LaunchOptions struct {
	ProfileDir       string
	Headless         bool
	BrowserPath      string
	AllowedOrigins   []string // tests may add a local origin; production defaults to Goodreads
	InteractiveLogin bool     // provider navigation is user-controlled in headed login
}

type Factory interface {
	Launch(ctx context.Context, opts LaunchOptions) (Browser, error)
}

type Browser interface {
	NewPage(ctx context.Context, targetURL string) (Page, error)
	Close() error
}

type Page interface {
	URL(ctx context.Context) (string, error)
	Has(ctx context.Context, selector string) (bool, error)
	HasText(ctx context.Context, selector, jsRegex string) (bool, error)
	HTML(ctx context.Context) (string, error)
	Click(ctx context.Context, selector string) error
	ClickAndWaitForRequest(ctx context.Context, selector string) error
	Input(ctx context.Context, selector, value string) error
	SelectValue(ctx context.Context, selector, value string) error
	Value(ctx context.Context, selector string) (string, error)
	Close() error
}

type RodFactory struct{}

func (RodFactory) Launch(ctx context.Context, opts LaunchOptions) (Browser, error) {
	if !filepath.IsAbs(opts.ProfileDir) || opts.ProfileDir == "" {
		return nil, fmt.Errorf("browser profile path must be absolute")
	}
	info, err := os.Lstat(opts.ProfileDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("dedicated browser profile is missing or unsafe")
	}
	if opts.InteractiveLogin && opts.Headless {
		return nil, fmt.Errorf("interactive login requires a headed browser")
	}
	exe, err := ResolveExecutable(ctx, opts.BrowserPath)
	if err != nil {
		return nil, err
	}
	if err := checkBrowserProduct(opts.ProfileDir, exe.Product); err != nil {
		return nil, err
	}
	l := launcher.New().Context(ctx)
	// Rod's default launcher includes several performance/security-changing
	// Chromium flags. This product needs only a private profile, local CDP, and
	// optional headless mode.
	l.Flags = map[flags.Flag][]string{}
	l.Bin(exe.Path).UserDataDir(opts.ProfileDir).Headless(opts.Headless).
		RemoteDebuggingPort(0).Set("remote-debugging-address", "127.0.0.1").
		Set("no-first-run").Set("no-startup-window")
	controlURL, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLaunch, err)
	}
	if !loopbackControlURL(controlURL) {
		l.Kill()
		return nil, fmt.Errorf("%w: debugging endpoint was not local", ErrLaunch)
	}
	r := rod.New().ControlURL(controlURL).Context(ctx)
	if err := r.Connect(); err != nil {
		l.Kill()
		return nil, fmt.Errorf("%w: %v", ErrLaunch, err)
	}
	allowed := opts.AllowedOrigins
	if len(allowed) == 0 {
		allowed = []string{"https://www.goodreads.com", "https://goodreads.com"}
	}
	b := &rodBrowser{rod: r, launcher: l, allowed: allowed, login: opts.InteractiveLogin}
	b.stopCancellation = context.AfterFunc(ctx, func() { _ = b.Close() })
	if err := recordBrowserProduct(opts.ProfileDir, exe.Product); err != nil {
		_ = b.Close()
		return nil, err
	}
	return b, nil
}

func productMarkerPath(profileDir string) string {
	return filepath.Join(filepath.Dir(profileDir), "browser-product")
}

func checkBrowserProduct(profileDir, product string) error {
	path := productMarkerPath(profileDir)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: browser choice metadata is unsafe", ErrLaunch)
	}
	previous, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(previous)) != product {
		return fmt.Errorf("%w: profile was created with a different Chromium product", ErrLaunch)
	}
	return nil
}

func recordBrowserProduct(profileDir, product string) error {
	path := productMarkerPath(profileDir)
	if err := checkBrowserProduct(profileDir, product); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return checkBrowserProduct(profileDir, product)
	}
	if err != nil {
		return err
	}
	_, err = file.WriteString(product + "\n")
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}

func loopbackControlURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") {
		return false
	}
	host := u.Hostname()
	return host == "localhost" || (net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())
}

type rodBrowser struct {
	rod              *rod.Browser
	launcher         *launcher.Launcher
	allowed          []string
	login            bool
	stopCancellation func() bool
	closeOnce        sync.Once
	closeErr         error
}

func (b *rodBrowser) NewPage(ctx context.Context, targetURL string) (Page, error) {
	if !originAllowed(targetURL, b.allowed) {
		return nil, ErrOrigin
	}
	p, err := b.rod.Context(ctx).Page(proto.TargetCreateTarget{URL: targetURL})
	if err != nil {
		return nil, err
	}
	page := &rodPage{rod: p, allowed: b.allowed, login: b.login}
	if err := p.Context(ctx).WaitLoad(); err != nil {
		_ = p.Close()
		return nil, err
	}
	if _, err := page.URL(ctx); err != nil {
		_ = p.Close()
		return nil, err
	}
	return page, nil
}

func (b *rodBrowser) Close() error {
	b.closeOnce.Do(func() {
		if b.stopCancellation != nil {
			b.stopCancellation()
		}
		b.closeErr = b.rod.Close()
		if b.closeErr != nil {
			b.launcher.Kill()
			return
		}
		waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := waitProcessExit(waitCtx, b.launcher.PID()); err != nil {
			b.launcher.Kill()
			b.closeErr = fmt.Errorf("browser did not exit after close: %w", err)
		}
	})
	return b.closeErr
}

type rodPage struct {
	rod     *rod.Page
	allowed []string
	login   bool
}

func (p *rodPage) URL(ctx context.Context) (string, error) {
	info, err := p.rod.Context(ctx).Info()
	if err != nil {
		return "", err
	}
	if !p.login && !originAllowed(info.URL, p.allowed) {
		return "", ErrOrigin
	}
	return info.URL, nil
}

func (p *rodPage) Close() error { return p.rod.Close() }

func originAllowed(raw string, allowed []string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host == "" {
		return false
	}
	for _, origin := range allowed {
		v, err := url.Parse(origin)
		if err == nil && u.Scheme == v.Scheme && strings.EqualFold(u.Hostname(), v.Hostname()) && effectivePort(u) == effectivePort(v) {
			return true
		}
	}
	return false
}

func effectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if u.Scheme == "https" {
		return "443"
	}
	if u.Scheme == "http" {
		return "80"
	}
	return ""
}

func (p *rodPage) Has(ctx context.Context, selector string) (bool, error) {
	found, _, err := p.rod.Context(ctx).Has(selector)
	return found, err
}

func (p *rodPage) HasText(ctx context.Context, selector, jsRegex string) (bool, error) {
	found, _, err := p.rod.Context(ctx).HasR(selector, jsRegex)
	return found, err
}

func (p *rodPage) HTML(ctx context.Context) (string, error) {
	return p.rod.Context(ctx).HTML()
}

func (p *rodPage) Click(ctx context.Context, selector string) error {
	element, err := p.rod.Context(ctx).Element(selector)
	if err != nil {
		return err
	}
	if err := element.ScrollIntoView(); err != nil {
		return err
	}
	return element.Click(proto.InputMouseButtonLeft, 1)
}

// ClickAndWaitForRequest clicks one control and waits for the first
// browser-initiated, allowed-origin XHR/fetch that starts afterward to finish.
// The caller must still verify resulting application state independently.
func (p *rodPage) ClickAndWaitForRequest(ctx context.Context, selector string) error {
	eventCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var requestID proto.NetworkRequestID
	var requestFailed bool
	wait := p.rod.Context(eventCtx).EachEvent(
		func(event *proto.NetworkRequestWillBeSent) {
			if requestID != "" ||
				(event.Type != proto.NetworkResourceTypeXHR && event.Type != proto.NetworkResourceTypeFetch) ||
				!originAllowed(event.Request.URL, p.allowed) {
				return
			}
			requestID = event.RequestID
		},
		func(event *proto.NetworkLoadingFinished) bool {
			return requestID != "" && event.RequestID == requestID
		},
		func(event *proto.NetworkLoadingFailed) bool {
			if requestID == "" || event.RequestID != requestID {
				return false
			}
			requestFailed = true
			return true
		},
	)
	element, err := p.rod.Context(ctx).Element(selector)
	if err == nil {
		err = element.ScrollIntoView()
	}
	if err == nil {
		err = element.Click(proto.InputMouseButtonLeft, 1)
	}
	if err != nil {
		cancel()
		wait()
		return err
	}
	wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if requestID == "" {
		return fmt.Errorf("no matching browser request observed")
	}
	if requestFailed {
		return fmt.Errorf("browser request failed")
	}
	return nil
}

func (p *rodPage) Input(ctx context.Context, selector, value string) error {
	element, err := p.rod.Context(ctx).Element(selector)
	if err != nil {
		return err
	}
	if err := element.ScrollIntoView(); err != nil {
		return err
	}
	return element.Input(value)
}

func (p *rodPage) SelectValue(ctx context.Context, selector, value string) error {
	element, err := p.rod.Context(ctx).Element(selector)
	if err != nil {
		return err
	}
	err = element.Select([]string{fmt.Sprintf(`[value="%s"]`, value)}, true, rod.SelectorTypeCSSSector)
	if err == nil {
		return nil
	}
	var notFound *rod.ElementNotFoundError
	if !errors.As(err, &notFound) {
		return err
	}
	// Goodreads year/day options currently omit value attributes, in which
	// case the DOM value is their rendered text.
	return element.Select([]string{"^" + regexp.QuoteMeta(value) + "$"}, true, rod.SelectorTypeRegex)
}

func (p *rodPage) Value(ctx context.Context, selector string) (string, error) {
	element, err := p.rod.Context(ctx).Element(selector)
	if err != nil {
		return "", err
	}
	value, err := element.Property("value")
	if err != nil {
		return "", err
	}
	return value.Str(), nil
}
