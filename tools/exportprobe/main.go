//go:build liveprobe

// exportprobe inspects Goodreads' export page without starting or downloading
// an export. It prints structural facts only, never CSV data, account details,
// tokens, or raw HTML.
package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/goodreads"
	"github.com/janhelcl/goodreads-cli/internal/profile"
)

func main() {
	if len(os.Args) > 2 || (len(os.Args) == 2 &&
		os.Args[1] != "CONFIRM_EXPORT_GENERATION" &&
		os.Args[1] != "CONFIRM_PRODUCTION_EXPORT") {
		fail("expected no argument, CONFIRM_EXPORT_GENERATION, or CONFIRM_PRODUCTION_EXPORT")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	paths, err := profile.DefaultPaths()
	if err != nil {
		fail("profile unavailable")
	}
	lock, err := paths.Acquire(ctx)
	if err != nil {
		fail("profile busy")
	}
	defer lock.Release()
	downloadDir, err := os.MkdirTemp("", "goodreads-cli-export-probe-*")
	if err != nil {
		fail("download directory unavailable")
	}
	defer os.RemoveAll(downloadDir)
	if err := os.Chmod(downloadDir, 0700); err != nil {
		fail("download directory unavailable")
	}
	b, err := (browser.RodFactory{}).Launch(ctx, browser.LaunchOptions{
		ProfileDir:  paths.Browser,
		Headless:    true,
		DownloadDir: downloadDir,
	})
	if err != nil {
		fail("browser unavailable")
	}
	defer b.Close()
	state, err := goodreads.Status(ctx, b)
	if err != nil || !state.Connected {
		fail("authenticated Goodreads state unavailable")
	}
	if len(os.Args) == 2 && os.Args[1] == "CONFIRM_PRODUCTION_EXPORT" {
		result, err := goodreads.DownloadExport(ctx, b)
		if err != nil {
			_ = b.Close()
			_ = lock.Release()
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("production_export_verified", result.OK)
		fmt.Println("production_export_bytes", result.Bytes)
		return
	}
	page, err := b.NewPage(ctx, "https://www.goodreads.com/review/import")
	if err != nil {
		fail("export page unavailable")
	}
	defer page.Close()
	raw, err := page.HTML(ctx)
	if err != nil {
		fail("export DOM unavailable")
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(raw))
	if err != nil {
		fail("export DOM invalid")
	}
	current, err := page.URL(ctx)
	if err != nil {
		fail("export URL unavailable")
	}
	parsed, _ := url.Parse(current)
	fmt.Println("page_path", parsed.Path)
	fmt.Println("page_heading_matches",
		strings.Join(strings.Fields(doc.Find("h1").First().Text()), " ") == "My Books > Import/Export")
	fmt.Println("sign_out_count", doc.Find("a[href*='/user/sign_out']").Length())
	doc.Find("h1,h2,h3").Each(func(i int, heading *goquery.Selection) {
		if i < 20 {
			fmt.Printf("heading[%d] tag=%q id=%q class=%q text_pattern=%q\n",
				i, goquery.NodeName(heading), safeToken(heading.AttrOr("id", "")),
				safeToken(heading.AttrOr("class", "")), valuePattern(strings.Join(strings.Fields(heading.Text()), " ")))
		}
	})
	doc.Find("form").Each(func(i int, form *goquery.Selection) {
		if i >= 20 {
			return
		}
		action, _ := url.Parse(form.AttrOr("action", ""))
		fmt.Printf("form[%d] method=%q action_path=%q id=%q class=%q\n",
			i, strings.ToLower(form.AttrOr("method", "")), action.Path,
			safeToken(form.AttrOr("id", "")), safeToken(form.AttrOr("class", "")))
		form.Find("button,input[type='submit'],a").Each(func(j int, control *goquery.Selection) {
			if j >= 20 {
				return
			}
			href, _ := url.Parse(control.AttrOr("href", ""))
			text := strings.Join(strings.Fields(control.Text()+" "+control.AttrOr("value", "")), " ")
			fmt.Printf("form[%d]_control[%d] tag=%q type=%q name=%q id=%q class=%q href_path=%q text_pattern=%q attrs=%q\n",
				i, j, goquery.NodeName(control), control.AttrOr("type", ""),
				safeToken(control.AttrOr("name", "")), safeToken(control.AttrOr("id", "")),
				safeToken(control.AttrOr("class", "")), href.Path, valuePattern(text), attributeNames(control))
		})
	})
	doc.Find("a").Each(func(i int, link *goquery.Selection) {
		text := strings.ToLower(strings.Join(strings.Fields(link.Text()), " "))
		href, _ := url.Parse(link.AttrOr("href", ""))
		if !strings.Contains(text, "export") && !strings.Contains(href.Path, "export") {
			return
		}
		fmt.Printf("export_link[%d] path=%q id=%q class=%q text_pattern=%q attrs=%q\n",
			i, href.Path, safeToken(link.AttrOr("id", "")), safeToken(link.AttrOr("class", "")),
			valuePattern(text), attributeNames(link))
	})
	doc.Find("button,input").Each(func(i int, control *goquery.Selection) {
		if i >= 100 {
			return
		}
		text := strings.ToLower(strings.Join(strings.Fields(control.Text()+" "+control.AttrOr("value", "")+" "+control.AttrOr("aria-label", "")), " "))
		if !strings.Contains(text, "export") {
			return
		}
		fmt.Printf("export_control[%d] tag=%q type=%q name=%q id=%q class=%q text_pattern=%q attrs=%q\n",
			i, goquery.NodeName(control), control.AttrOr("type", ""),
			safeToken(control.AttrOr("name", "")), safeToken(control.AttrOr("id", "")),
			safeToken(control.AttrOr("class", "")), valuePattern(text), attributeNames(control))
	})
	doc.Find("body *").FilterFunction(func(_ int, node *goquery.Selection) bool {
		if node.Is("script,style") || node.Children().Length() != 0 {
			return false
		}
		return strings.Contains(strings.ToLower(node.Text()), "export")
	}).Each(func(i int, node *goquery.Selection) {
		if i >= 40 {
			return
		}
		fmt.Printf("export_text_node[%d] tag=%q id=%q class=%q text_pattern=%q parent_tag=%q parent_id=%q parent_class=%q\n",
			i, goquery.NodeName(node), safeToken(node.AttrOr("id", "")), safeToken(node.AttrOr("class", "")),
			valuePattern(strings.Join(strings.Fields(node.Text()), " ")), goquery.NodeName(node.Parent()),
			safeToken(node.Parent().AttrOr("id", "")), safeToken(node.Parent().AttrOr("class", "")))
	})
	button := doc.Find("div.exportBooks button.js-LibraryExport")
	fmt.Println("export_button_count", button.Length())
	if button.Length() == 1 {
		fmt.Println("export_button_text_matches",
			strings.Join(strings.Fields(button.Text()), " ") == "Export Library")
		statusID := button.AttrOr("data-statusid", "")
		fileListID := button.AttrOr("data-filelistid", "")
		fmt.Println("export_status_selector", safeToken(statusID))
		fmt.Println("export_file_list_selector", safeToken(fileListID))
		for _, selector := range []string{statusID, fileListID} {
			if selector == "" {
				continue
			}
			target := doc.Find("#" + selector)
			fmt.Printf("export_target[%q] count=%d tag=%q id=%q class=%q children=%d links=%d text_pattern=%q\n",
				safeToken(selector), target.Length(), goquery.NodeName(target), safeToken(target.AttrOr("id", "")),
				safeToken(target.AttrOr("class", "")), target.Children().Length(), target.Find("a").Length(),
				valuePattern(strings.Join(strings.Fields(target.Text()), " ")))
			target.Find("a").Each(func(i int, link *goquery.Selection) {
				href, _ := url.Parse(link.AttrOr("href", ""))
				fmt.Printf("export_target[%q]_link[%d] route=%q class=%q text_pattern=%q attrs=%q\n",
					safeToken(selector), i, safeToken(href.Path), safeToken(link.AttrOr("class", "")),
					valuePattern(strings.Join(strings.Fields(link.Text()), " ")), attributeNames(link))
			})
		}
	}
	if len(os.Args) == 2 {
		if button.Length() != 1 {
			fail("export button unavailable")
		}
		if err := page.ClickAndWaitForRequest(ctx, "div.exportBooks button.js-LibraryExport"); err != nil {
			fail("export generation request unavailable")
		}
		pollCtx, pollCancel := context.WithTimeout(ctx, 2*time.Minute)
		defer pollCancel()
		for {
			raw, err := page.HTML(pollCtx)
			if err == nil {
				currentDoc, parseErr := goquery.NewDocumentFromReader(strings.NewReader(raw))
				if parseErr == nil {
					status := currentDoc.Find("#exportStatusText")
					files := currentDoc.Find("#exportFile")
					fmt.Printf("generation_poll status_pattern=%q links=%d\n",
						valuePattern(strings.Join(strings.Fields(status.Text()), " ")), files.Find("a").Length())
					if files.Find("a").Length() == 1 {
						link := files.Find("a").First()
						href, _ := url.Parse(link.AttrOr("href", ""))
						fmt.Printf("generated_link route=%q text_pattern=%q attrs=%q\n",
							safeToken(href.Path), valuePattern(strings.Join(strings.Fields(link.Text()), " ")),
							attributeNames(link))
						return
					}
				}
			}
			select {
			case <-pollCtx.Done():
				fail("fresh export link unavailable")
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
}

var digits = regexp.MustCompile(`[0-9]+`)

func safeToken(value string) string { return digits.ReplaceAllString(value, "{id}") }

func attributeNames(selection *goquery.Selection) string {
	if selection.Length() == 0 {
		return ""
	}
	names := make([]string, 0, len(selection.Get(0).Attr))
	for _, attr := range selection.Get(0).Attr {
		names = append(names, attr.Key)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

func valuePattern(value string) string {
	var result strings.Builder
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
			result.WriteByte('9')
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
			result.WriteByte('A')
		default:
			result.WriteRune(r)
		}
	}
	return regexp.MustCompile(`A+`).ReplaceAllString(result.String(), "A")
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
