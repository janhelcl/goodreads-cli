//go:build liveprobe

// statusprobe inspects the owner shelf chooser for one exact ISBN. It opens
// the chooser but never selects a shelf, submits a form, or prints book,
// account, shelf, or review values.
package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
	"github.com/janhelcl/goodreads-cli/internal/profile"
)

const libraryURL = "https://www.goodreads.com/review/list"

func main() {
	if len(os.Args) != 3 {
		fail("expected one exact ISBN and target status")
	}
	isbn, err := domain.NormalizeISBN(os.Args[1])
	if err != nil {
		fail("invalid ISBN")
	}
	targetStatus := domain.ReadingStatus(os.Args[2])
	if !targetStatus.Valid() {
		fail("target status must be to-read, currently-reading, or read")
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
	b, err := (browser.RodFactory{}).Launch(ctx, browser.LaunchOptions{
		ProfileDir: paths.Browser,
		Headless:   true,
	})
	if err != nil {
		fail("browser unavailable")
	}
	defer b.Close()

	page, rowID, bookURL := exactOwnerRow(ctx, b, isbn)
	defer page.Close()
	before, err := rowFromPage(ctx, page, rowID)
	if err != nil {
		fail("target row unavailable")
	}
	currentStatus := domain.ReadingStatus("")
	for _, status := range []domain.ReadingStatus{
		domain.StatusToRead,
		domain.StatusCurrentlyReading,
		domain.StatusRead,
	} {
		matches := before.Find("td.field.shelves a[href*='shelf=" + string(status) + "']").Length()
		fmt.Printf("row_status_%s=%t\n", status, matches == 1)
		if matches == 1 {
			if currentStatus != "" {
				fail("target row has multiple core statuses")
			}
			currentStatus = status
		}
	}
	if !currentStatus.Valid() {
		fail("target row status unavailable")
	}
	dateValue := before.Find("td.field.date_read .value").First().Clone()
	dateValue.Find("a,script,style").Remove()
	fmt.Println("date_read_pattern", valuePattern(strings.Join(strings.Fields(dateValue.Text()), " ")))
	before.Find("td.field.date_read .value").First().Find("*").Each(func(i int, node *goquery.Selection) {
		if i >= 20 {
			return
		}
		fmt.Printf("date_node[%d] tag=%q class=%q attrs=%q text_pattern=%q\n",
			i, goquery.NodeName(node), node.AttrOr("class", ""), attributeNames(node),
			valuePattern(strings.Join(strings.Fields(node.Clone().Children().Remove().End().Text()), " ")))
	})
	chooser := before.Find("td.field.shelves a.shelfChooserLink")
	fmt.Println("shelf_chooser_count", chooser.Length())
	if chooser.Length() != 1 {
		fail("exactly one shelf chooser required")
	}
	fmt.Println("shelf_chooser_attributes", attributeNames(chooser))

	selector := "#" + rowID + " td.field.shelves a.shelfChooserLink"
	beforeHTML, err := page.HTML(ctx)
	if err != nil {
		fail("shelf chooser DOM unavailable")
	}
	if err := page.Click(ctx, selector); err != nil {
		fail("shelf chooser could not be opened")
	}
	after, err := waitForChooser(ctx, page, beforeHTML)
	if err != nil {
		fail("shelf chooser did not expose a recognized changed state")
	}
	beforeDoc, err := goquery.NewDocumentFromReader(strings.NewReader(beforeHTML))
	if err != nil {
		fail("initial shelf chooser DOM invalid")
	}
	reportChooser(beforeDoc, after, rowID)
	openedHTML, err := page.HTML(ctx)
	if err != nil {
		fail("opened shelf chooser DOM unavailable")
	}
	inputSelector := "div.shelfChooserWrapper.open input.shelfChooserInput"
	if after.Find(inputSelector).Length() != 1 {
		fail("exactly one opened shelf chooser input required")
	}
	if err := page.Input(ctx, inputSelector, string(targetStatus)); err != nil {
		fail("shelf chooser query failed")
	}
	queried, err := waitForChooser(ctx, page, openedHTML)
	if err != nil {
		fail("shelf chooser query did not expose a changed state")
	}
	reportQueriedChooser(queried, targetStatus)

	pageURL, err := page.URL(ctx)
	if err != nil {
		fail("target shelf URL unavailable")
	}
	datePage, err := b.NewPage(ctx, pageURL)
	if err != nil {
		fail("date probe page unavailable")
	}
	defer datePage.Close()
	probeDateEditor(ctx, datePage, rowID)

	bookPage, err := b.NewPage(ctx, bookURL)
	if err != nil {
		fail("book page unavailable")
	}
	defer bookPage.Close()
	bookCtx, cancelBook := context.WithTimeout(ctx, 20*time.Second)
	bookDoc, err := waitForBookActions(bookCtx, bookPage)
	cancelBook()
	if err != nil {
		fail("book actions did not become available")
	}
	reportBookControls(bookDoc)
	probeModernStatusMenu(ctx, bookPage, bookDoc, currentStatus)
}

func exactOwnerRow(ctx context.Context, b browser.Browser, isbn domain.ISBN) (browser.Page, string, string) {
	target, _ := url.Parse(libraryURL)
	visited := map[string]bool{}
	for pageNumber := 0; pageNumber < 10; pageNumber++ {
		if visited[target.String()] {
			fail("library pagination loop")
		}
		visited[target.String()] = true
		page, err := b.NewPage(ctx, target.String())
		if err != nil {
			fail("library page unavailable")
		}
		raw, err := page.HTML(ctx)
		if err != nil {
			_ = page.Close()
			fail("library DOM unavailable")
		}
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(raw))
		if err != nil {
			_ = page.Close()
			fail("library DOM invalid")
		}
		var rowID string
		matches := 0
		doc.Find("#booksBody > tr").Each(func(_ int, row *goquery.Selection) {
			if rowMatchesISBN(row, isbn) {
				rowID = row.AttrOr("id", "")
				matches++
			}
		})
		if matches > 1 {
			_ = page.Close()
			fail("multiple rendered ISBN matches")
		}
		if matches == 1 {
			if !reviewRowID.MatchString(rowID) {
				_ = page.Close()
				fail("target row identity changed")
			}
			current, err := page.URL(ctx)
			if err != nil {
				_ = page.Close()
				fail("library URL unavailable")
			}
			bookURL, err := exactBookURL(doc.Find("#"+rowID+" td.field.title a[href*='/book/show/']").First(), current)
			if err != nil {
				_ = page.Close()
				fail("book URL unavailable")
			}
			return page, rowID, bookURL
		}
		next := doc.Find("a.next_page").First()
		href, ok := next.Attr("href")
		if !ok || href == "" {
			_ = page.Close()
			fail("exact rendered ISBN match required")
		}
		current, err := page.URL(ctx)
		_ = page.Close()
		if err != nil {
			fail("library URL unavailable")
		}
		currentURL, err := url.Parse(current)
		if err != nil {
			fail("library URL invalid")
		}
		reference, err := url.Parse(href)
		if err != nil {
			fail("pagination URL invalid")
		}
		target = currentURL.ResolveReference(reference)
		if !safeGoodreadsLibraryURL(target) {
			fail("pagination left the library")
		}
	}
	fail("library page limit reached")
	return nil, "", ""
}

func rowFromPage(ctx context.Context, page browser.Page, rowID string) (*goquery.Selection, error) {
	raw, err := page.HTML(ctx)
	if err != nil {
		return nil, err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(raw))
	if err != nil {
		return nil, err
	}
	row := doc.Find("#" + rowID)
	if row.Length() != 1 {
		return nil, fmt.Errorf("target row count changed")
	}
	return row, nil
}

func waitForChooser(ctx context.Context, page browser.Page, before string) (*goquery.Document, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		raw, err := page.HTML(ctx)
		if err == nil && raw != before {
			doc, parseErr := goquery.NewDocumentFromReader(strings.NewReader(raw))
			if parseErr == nil {
				return doc, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func reportChooser(before, after *goquery.Document, rowID string) {
	fmt.Println("chooser_form_delta", after.Find("form").Length()-before.Find("form").Length())
	fmt.Println("chooser_input_delta", after.Find("input").Length()-before.Find("input").Length())
	fmt.Println("chooser_select_delta", after.Find("select").Length()-before.Find("select").Length())
	fmt.Println("chooser_button_delta", after.Find("button").Length()-before.Find("button").Length())
	for _, status := range []string{"to-read", "want-to-read", "currently-reading", "read"} {
		fmt.Printf("chooser_mentions_%s_delta=%d\n", status,
			statusMentions(after.Selection, status)-statusMentions(before.Selection, status))
	}
	open := after.Find("div.shelfChooserWrapper.open")
	fmt.Println("open_chooser_count", open.Length())
	fmt.Println("open_exclusive_option_count", open.Find("li.visible.exclusive").Length())
	fmt.Println("open_chosen_option_count", open.Find("li.visible.exclusive.exclusive_chosen").Length())
	rowOpen := after.Find("#" + rowID + " td.field.shelves div.shelfChooserWrapper.open")
	fmt.Println("row_open_chooser_count", rowOpen.Length())
	fmt.Println("row_exclusive_option_count", rowOpen.Find("li.visible.exclusive").Length())
	fmt.Println("row_chosen_option_count", rowOpen.Find("li.visible.exclusive.exclusive_chosen").Length())
	for _, status := range []string{"to-read", "want-to-read", "currently-reading", "read"} {
		fmt.Printf("open_chooser_mentions_%s=%d\n", status, statusMentions(open, status))
	}
	reportStatusNodes(open)
	open.Find("form,input,select,option,button,a,label,div,span").Each(func(i int, node *goquery.Selection) {
		if i >= 40 {
			return
		}
		fmt.Printf("open_node[%d] tag=%q class=%q type=%q attrs=%q route=%q\n",
			i, goquery.NodeName(node), node.AttrOr("class", ""), node.AttrOr("type", ""),
			attributeNames(node), routeKind(node.AttrOr("href", ""), node.AttrOr("action", "")))
	})
	beforeNodes := nodeCounts(before.Selection)
	printed := 0
	after.Find("form,input,select,option,button,a,label,div,span").EachWithBreak(func(_ int, node *goquery.Selection) bool {
		signature := nodeSignature(node)
		if beforeNodes[signature] > 0 {
			beforeNodes[signature]--
			return true
		}
		fmt.Printf("new_node[%d] tag=%q class=%q type=%q attrs=%q route=%q\n",
			printed, goquery.NodeName(node), node.AttrOr("class", ""), node.AttrOr("type", ""),
			attributeNames(node), routeKind(node.AttrOr("href", ""), node.AttrOr("action", "")))
		printed++
		return printed < 80
	})
}

func reportBookControls(doc *goquery.Document) {
	fmt.Println("book_form_count", doc.Find("form").Length())
	fmt.Println("book_button_count", doc.Find("button").Length())
	fmt.Println("book_input_count", doc.Find("input").Length())
	fmt.Println("book_select_count", doc.Find("select").Length())
	for _, status := range []string{"to-read", "want-to-read", "currently-reading", "read"} {
		fmt.Printf("book_mentions_%s=%d\n", status, statusMentions(doc.Selection, status))
	}
	actions := doc.Find("div.BookActions")
	fmt.Println("book_actions_count", actions.Length())
	actions.Each(func(groupIndex int, group *goquery.Selection) {
		fmt.Printf("book_actions[%d]_attrs=%q button_count=%d\n",
			groupIndex, attributeNames(group), group.Find("button").Length())
		group.Find("button").Each(func(buttonIndex int, button *goquery.Selection) {
			textStatus := coreStatusText(button)
			labelStatus := normalizedCoreStatus(button.AttrOr("aria-label", ""))
			fmt.Printf("book_actions[%d]_button[%d] class=%q attrs=%q text_status=%q aria_status=%q aria_pattern=%q expanded=%q popup=%q parent_class=%q\n",
				groupIndex, buttonIndex, button.AttrOr("class", ""), attributeNames(button),
				textStatus, labelStatus, valuePattern(button.AttrOr("aria-label", "")),
				button.AttrOr("aria-expanded", ""), button.AttrOr("aria-haspopup", ""),
				button.Parent().AttrOr("class", ""))
		})
	})
	printed := 0
	doc.Find("button,a,input,select,option,label,div,span").EachWithBreak(func(_ int, node *goquery.Selection) bool {
		if !isCoreStatusControl(node) && !hasStatusToken(node) &&
			!(goquery.NodeName(node) == "button" || goquery.NodeName(node) == "select") {
			return true
		}
		parent := node.Parent()
		fmt.Printf("book_control[%d] tag=%q class=%q type=%q attrs=%q route=%q parent_tag=%q parent_class=%q\n",
			printed, goquery.NodeName(node), node.AttrOr("class", ""), node.AttrOr("type", ""),
			attributeNames(node), routeKind(node.AttrOr("href", ""), node.AttrOr("action", "")),
			goquery.NodeName(parent), parent.AttrOr("class", ""))
		printed++
		return printed < 80
	})
}

func waitForBookActions(ctx context.Context, page browser.Page) (*goquery.Document, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		raw, err := page.HTML(ctx)
		if err == nil {
			doc, parseErr := goquery.NewDocumentFromReader(strings.NewReader(raw))
			if parseErr == nil && doc.Find("div.BookActions button").Length() > 0 {
				return doc, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func probeModernStatusMenu(
	ctx context.Context,
	page browser.Page,
	doc *goquery.Document,
	current domain.ReadingStatus,
) {
	selector := "div.Sticky div.BookActions div.BookActions__button button.Button--secondary"
	button := doc.Find(selector)
	fmt.Println("modern_status_button_count", button.Length())
	aria := strings.ToLower(button.AttrOr("aria-label", ""))
	fmt.Println("modern_status_aria_shelved", strings.Contains(aria, "shelved"))
	fmt.Println("modern_status_aria_shelf", strings.Contains(aria, "shelf"))
	if button.Length() != 1 || coreStatusText(button) != string(current) ||
		!strings.Contains(aria, "shelved") || !strings.Contains(aria, "shelf") ||
		!strings.Contains(aria, "tap to") {
		fmt.Println("modern_status_menu_contract", false)
		return
	}
	fmt.Println("modern_status_menu_contract", true)
	before, err := page.HTML(ctx)
	if err != nil {
		fail("modern status page DOM unavailable")
	}
	if err := page.Click(ctx, selector); err != nil {
		fail("modern status menu could not be opened")
	}
	menuCtx, cancelMenu := context.WithTimeout(ctx, 10*time.Second)
	after, err := waitForModernDialog(menuCtx, page, before)
	cancelMenu()
	if err != nil {
		fail("modern status menu did not expose a changed state")
	}
	fmt.Println("modern_dialog_count", after.Find("[role='dialog']").Length())
	fmt.Println("modern_menu_count", after.Find("[role='menu']").Length())
	fmt.Println("modern_listbox_count", after.Find("[role='listbox']").Length())
	for _, status := range []string{"to-read", "want-to-read", "currently-reading", "read"} {
		fmt.Printf("modern_menu_mentions_%s_delta=%d\n", status,
			statusMentions(after.Selection, status)-statusMentions(doc.Selection, status))
	}
	dialog := after.Find("[role='dialog']")
	fmt.Println("modern_dialog_input_count", dialog.Find("input").Length())
	fmt.Println("modern_dialog_button_count", dialog.Find("button").Length())
	dialogText := normalizedText(dialog.Text())
	for _, status := range []string{"to read", "want to read", "currently reading", "read"} {
		fmt.Printf("modern_dialog_contains_%s=%t\n", strings.ReplaceAll(status, " ", "_"),
			strings.Contains(dialogText, status))
	}
	dialog.Find("input,button,label,select,option,a").Each(func(i int, node *goquery.Selection) {
		if i >= 80 {
			return
		}
		text := strings.ToLower(strings.Join(strings.Fields(node.Text()), " "))
		aria := strings.ToLower(node.AttrOr("aria-label", ""))
		fmt.Printf("modern_dialog_control[%d] tag=%q class=%q type=%q attrs=%q text_status=%q value_status=%q text_pattern=%q aria_pattern=%q checked=%t disabled=%t\n",
			i, goquery.NodeName(node), node.AttrOr("class", ""), node.AttrOr("type", ""),
			attributeNames(node), coreStatusText(node), normalizedCoreStatus(node.AttrOr("value", "")),
			valuePattern(text), valuePattern(aria), node.Is("[checked]"), node.Is("[disabled]"))
		fmt.Printf("modern_dialog_control[%d]_remove=%t shelf=%t library=%t book=%t\n",
			i, strings.Contains(text+" "+aria, "remove"), strings.Contains(text+" "+aria, "shelf"),
			strings.Contains(text+" "+aria, "library"), strings.Contains(text+" "+aria, "book"))
	})
	reportStatusNodes(after.Selection)
	beforeNodes := nodeCounts(doc.Selection)
	printed := 0
	after.Find("form,input,select,option,button,a,label,div,span,li").EachWithBreak(func(_ int, node *goquery.Selection) bool {
		signature := nodeSignature(node)
		if beforeNodes[signature] > 0 {
			beforeNodes[signature]--
			return true
		}
		if !hasStatusToken(node) && coreStatusText(node) == "" &&
			node.AttrOr("role", "") == "" && goquery.NodeName(node) != "button" {
			return true
		}
		fmt.Printf("modern_new_node[%d] tag=%q class=%q type=%q role=%q attrs=%q text_status=%q aria_pattern=%q\n",
			printed, goquery.NodeName(node), node.AttrOr("class", ""), node.AttrOr("type", ""),
			node.AttrOr("role", ""), attributeNames(node), coreStatusText(node),
			valuePattern(node.AttrOr("aria-label", "")))
		printed++
		return printed < 80
	})
}

func waitForModernDialog(ctx context.Context, page browser.Page, before string) (*goquery.Document, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		raw, err := page.HTML(ctx)
		if err == nil && raw != before {
			doc, parseErr := goquery.NewDocumentFromReader(strings.NewReader(raw))
			if parseErr == nil {
				dialog := doc.Find("[role='dialog']")
				if dialog.Length() == 1 &&
					!dialog.HasClass("Overlay__window--willOpen") &&
					!dialog.HasClass("Overlay__window--willClose") {
					return doc, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func reportQueriedChooser(doc *goquery.Document, target domain.ReadingStatus) {
	open := doc.Find("div.shelfChooserWrapper.open")
	fmt.Println("queried_open_chooser_count", open.Length())
	fmt.Println("queried_target_mentions", statusMentions(open, string(target)))
	reportStatusNodes(open)
	open.Find("form,input,select,option,button,a,label,div,span").Each(func(i int, node *goquery.Selection) {
		if i >= 60 {
			return
		}
		fmt.Printf("queried_node[%d] tag=%q class=%q type=%q attrs=%q route=%q\n",
			i, goquery.NodeName(node), node.AttrOr("class", ""), node.AttrOr("type", ""),
			attributeNames(node), routeKind(node.AttrOr("href", ""), node.AttrOr("action", "")))
	})
}

func probeDateEditor(ctx context.Context, page browser.Page, rowID string) {
	before, err := page.HTML(ctx)
	if err != nil {
		fail("date editor DOM unavailable")
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(before))
	if err != nil {
		fail("date editor DOM invalid")
	}
	row := doc.Find("#" + rowID)
	dated := row.Find("td.field.date_read .date_row").FilterFunction(func(_ int, selection *goquery.Selection) bool {
		return selection.Find("span.date_read_value").Length() == 1
	})
	fmt.Println("dated_session_count", dated.Length())
	if dated.Length() != 1 || dated.Find("a.floatingBoxLink").Length() != 1 {
		return
	}
	selector := "#" + rowID + " td.field.date_read .date_row:has(span.date_read_value) a.floatingBoxLink"
	if err := page.Click(ctx, selector); err != nil {
		fail("date editor could not be opened")
	}
	after, err := waitForChooser(ctx, page, before)
	if err != nil {
		fail("date editor did not expose a changed state")
	}
	dateCell := after.Find("#" + rowID + " td.field.date_read")
	fmt.Println("date_editor_form_count", dateCell.Find("form").Length())
	fmt.Println("date_editor_select_count", dateCell.Find("select").Length())
	fmt.Println("date_editor_input_count", dateCell.Find("input").Length())
	fmt.Println("date_global_form_delta", after.Find("form").Length()-doc.Find("form").Length())
	fmt.Println("date_global_select_delta", after.Find("select").Length()-doc.Find("select").Length())
	fmt.Println("date_global_input_delta", after.Find("input").Length()-doc.Find("input").Length())
	for _, action := range []string{"save", "cancel", "clear", "remove", "delete"} {
		fmt.Printf("date_editor_action_%s=%d\n", action, exactTextCount(dateCell, action))
		fmt.Printf("date_global_action_%s_delta=%d\n", action,
			exactTextCount(after.Selection, action)-exactTextCount(doc.Selection, action))
	}
	dateCell.Find("form,input,select,option,button,a,label,div,span").Each(func(i int, node *goquery.Selection) {
		if i >= 80 {
			return
		}
		fmt.Printf("date_editor_node[%d] tag=%q class=%q type=%q attrs=%q text_pattern=%q\n",
			i, goquery.NodeName(node), node.AttrOr("class", ""), node.AttrOr("type", ""),
			attributeNames(node), valuePattern(strings.Join(strings.Fields(node.Text()), " ")))
	})
	beforeNodes := nodeCounts(doc.Selection)
	printed := 0
	after.Find("form,input,select,option,button,a,label,div,span").EachWithBreak(func(_ int, node *goquery.Selection) bool {
		signature := nodeSignature(node)
		if beforeNodes[signature] > 0 {
			beforeNodes[signature]--
			return true
		}
		fmt.Printf("date_new_node[%d] tag=%q class=%q type=%q attrs=%q text_pattern=%q\n",
			printed, goquery.NodeName(node), node.AttrOr("class", ""), node.AttrOr("type", ""),
			attributeNames(node), valuePattern(strings.Join(strings.Fields(node.Text()), " ")))
		printed++
		return printed < 80
	})
}

func exactTextCount(root *goquery.Selection, target string) int {
	count := 0
	root.Find("button,a,input,option,label,span").Each(func(_ int, node *goquery.Selection) {
		text := normalizedText(node.Text())
		value := normalizedText(node.AttrOr("value", ""))
		if text == target || value == target {
			count++
		}
	})
	return count
}

func reportStatusNodes(root *goquery.Selection) {
	index := 0
	root.Find("label,option,a,button,div,span").Each(func(_ int, node *goquery.Selection) {
		status := coreStatusText(node)
		if status == "" {
			return
		}
		parent := node.Parent()
		fmt.Printf("status_node[%d] status=%q tag=%q class=%q attrs=%q parent_tag=%q parent_class=%q parent_attrs=%q alt_matches=%t\n",
			index, status, goquery.NodeName(node), node.AttrOr("class", ""), attributeNames(node),
			goquery.NodeName(parent), parent.AttrOr("class", ""), attributeNames(parent),
			parent.AttrOr("alt", "") == status)
		index++
	})
}

func hasStatusToken(node *goquery.Selection) bool {
	value := strings.ToLower(strings.Join([]string{
		node.AttrOr("class", ""),
		node.AttrOr("id", ""),
		node.AttrOr("name", ""),
		node.AttrOr("data-testid", ""),
		node.AttrOr("aria-label", ""),
	}, " "))
	for _, token := range []string{"shelf", "status", "reading", "wtr", "bookaction"} {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
}

func isCoreStatusControl(node *goquery.Selection) bool {
	text := normalizedText(node.Text())
	value := strings.ToLower(strings.Join(strings.Fields(node.AttrOr("value", "")), " "))
	title := strings.ToLower(strings.Join(strings.Fields(node.AttrOr("title", "")), " "))
	for _, candidate := range []string{"to read", "want to read", "currently reading", "read"} {
		if text == candidate || value == candidate || title == candidate {
			return true
		}
	}
	return false
}

func coreStatusText(node *goquery.Selection) string {
	return normalizedCoreStatus(node.Text())
}

func normalizedCoreStatus(value string) string {
	switch normalizedText(value) {
	case "to read", "want to read":
		return "to-read"
	case "currently reading":
		return "currently-reading"
	case "read":
		return "read"
	default:
		return ""
	}
}

func normalizedText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.ReplaceAll(value, "-", " ")), " "))
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
	return result.String()
}

func statusMentions(root *goquery.Selection, status string) int {
	needle := strings.ReplaceAll(status, "-", " ")
	found := 0
	root.Find("label,option,a,button,div,span").Each(func(_ int, node *goquery.Selection) {
		text := strings.ToLower(strings.Join(strings.Fields(node.Text()), " "))
		value := strings.ToLower(node.AttrOr("value", ""))
		if text == needle || text == status || value == status {
			found++
		}
	})
	return found
}

func nodeCounts(root *goquery.Selection) map[string]int {
	counts := map[string]int{}
	root.Find("form,input,select,option,button,a,label,div,span").Each(func(_ int, node *goquery.Selection) {
		counts[nodeSignature(node)]++
	})
	return counts
}

func nodeSignature(node *goquery.Selection) string {
	return strings.Join([]string{
		goquery.NodeName(node),
		node.AttrOr("class", ""),
		node.AttrOr("type", ""),
		attributeNames(node),
		routeKind(node.AttrOr("href", ""), node.AttrOr("action", "")),
	}, "|")
}

func rowMatchesISBN(row *goquery.Selection, target domain.ISBN) bool {
	for _, raw := range []string{
		row.Find("td.field.isbn .value").First().Text(),
		row.Find("td.field.isbn13 .value").First().Text(),
	} {
		parsed, err := domain.NormalizeISBN(strings.TrimSpace(raw))
		if err == nil && parsed.ISBN13 == target.ISBN13 {
			return true
		}
	}
	return false
}

func safeGoodreadsLibraryURL(target *url.URL) bool {
	return target.Scheme == "https" && target.Hostname() == "www.goodreads.com" &&
		target.User == nil && (target.Port() == "" || target.Port() == "443") &&
		(target.Path == "/review/list" || strings.HasPrefix(target.Path, "/review/list/"))
}

func exactBookURL(link *goquery.Selection, current string) (string, error) {
	href, ok := link.Attr("href")
	if !ok {
		return "", fmt.Errorf("book link missing")
	}
	base, err := url.Parse(current)
	if err != nil {
		return "", err
	}
	reference, err := url.Parse(href)
	if err != nil {
		return "", err
	}
	target := base.ResolveReference(reference)
	if target.Scheme != "https" || target.Hostname() != "www.goodreads.com" || target.User != nil ||
		(target.Port() != "" && target.Port() != "443") || !bookPath.MatchString(target.Path) {
		return "", fmt.Errorf("unsafe book link")
	}
	target.RawQuery = ""
	target.Fragment = ""
	return target.String(), nil
}

func attributeNames(selection *goquery.Selection) string {
	if selection.Length() == 0 {
		return ""
	}
	names := make([]string, 0, len(selection.Get(0).Attr))
	for _, attr := range selection.Get(0).Attr {
		names = append(names, attr.Key)
	}
	return strings.Join(names, ",")
}

func routeKind(href, action string) string {
	raw := href
	if raw == "" {
		raw = action
	}
	switch {
	case strings.Contains(raw, "/review/edit"):
		return "review-edit"
	case strings.Contains(raw, "/review/list"):
		return "review-list"
	case raw == "#":
		return "local"
	case raw != "":
		return "other"
	default:
		return ""
	}
}

var reviewRowID = regexp.MustCompile(`^review_[0-9]+$`)
var bookPath = regexp.MustCompile(`^/book/show/[0-9]+(?:[./-]|$)`)

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
