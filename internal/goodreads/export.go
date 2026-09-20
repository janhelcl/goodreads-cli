package goodreads

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
)

const (
	exportURL            = "https://www.goodreads.com/review/import"
	exportButtonSelector = "div.exportBooks button.js-LibraryExport"
	exportLinkSelector   = "#exportFile a"
	maxExportPageBytes   = 2 << 20
	maxExportBytes       = 100 << 20
)

var ErrExportFailed = errors.New("goodreads export failed")

type exportPage struct {
	Status string
	Link   string
}

func DownloadExport(ctx context.Context, b browser.Browser) (domain.ExportResult, error) {
	state, err := Status(ctx, b)
	if err != nil {
		return domain.ExportResult{}, err
	}
	if !state.Connected {
		return domain.ExportResult{}, ErrSessionExpired
	}
	page, err := b.NewPage(ctx, exportURL)
	if err != nil {
		return domain.ExportResult{}, fmt.Errorf("export.generate: navigation failed: %w", err)
	}
	defer page.Close()
	current, err := page.URL(ctx)
	if err != nil {
		return domain.ExportResult{}, fmt.Errorf("export.generate: URL unavailable: %w", err)
	}
	parsedURL, err := url.Parse(current)
	if err != nil || !isGoodreadsPage(current) || parsedURL.Path != "/review/import" {
		return domain.ExportResult{}, fmt.Errorf("%w at export.generate: unexpected page", ErrCompatibility)
	}
	initial, err := readExportPage(ctx, page)
	if err != nil {
		return domain.ExportResult{}, err
	}
	if err := page.ClickAndWaitForRequest(ctx, exportButtonSelector); err != nil {
		return domain.ExportResult{}, fmt.Errorf("%w at export.generate: request unavailable", ErrExportFailed)
	}
	link, err := waitForFreshExport(ctx, page, initial)
	if err != nil {
		return domain.ExportResult{}, err
	}
	download, err := page.ClickAndWaitForDownload(ctx, exportLinkSelector)
	if err != nil {
		return domain.ExportResult{}, fmt.Errorf("%w at export.download: download unavailable", ErrExportFailed)
	}
	if download.URL != link {
		return domain.ExportResult{}, fmt.Errorf("%w at export.download: unexpected download", ErrCompatibility)
	}
	if filepath.Base(download.SuggestedFilename) != download.SuggestedFilename ||
		!strings.EqualFold(filepath.Ext(download.SuggestedFilename), ".csv") {
		return domain.ExportResult{}, fmt.Errorf("%w at export.download: unexpected filename", ErrExportFailed)
	}
	data, err := readExportFile(download.Path)
	if err != nil {
		return domain.ExportResult{}, err
	}
	return domain.ExportResult{OK: true, Bytes: int64(len(data)), Data: data}, nil
}

func waitForFreshExport(ctx context.Context, page browser.Page, initial exportPage) (string, error) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	started := false
	for {
		current, err := readExportPage(ctx, page)
		if err != nil {
			return "", err
		}
		if current.Status != "" || (initial.Link != "" && current.Link == "") {
			started = true
		}
		if current.Link != "" && current.Link != initial.Link {
			started = true
		}
		if started && current.Status == "" && current.Link != "" {
			return current.Link, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

func readExportPage(ctx context.Context, page browser.Page) (exportPage, error) {
	raw, err := page.HTML(ctx)
	if err != nil {
		return exportPage{}, fmt.Errorf("export.generate: DOM unavailable: %w", err)
	}
	return parseExportPage(raw)
}

func parseExportPage(raw string) (exportPage, error) {
	if len(raw) > maxExportPageBytes {
		return exportPage{}, fmt.Errorf("%w at export.generate: page too large", ErrCompatibility)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(raw))
	if err != nil {
		return exportPage{}, fmt.Errorf("%w at export.generate: invalid DOM", ErrCompatibility)
	}
	heading := strings.Join(strings.Fields(doc.Find("h1").First().Text()), " ")
	button := doc.Find(exportButtonSelector)
	status := doc.Find("#exportStatusText")
	files := doc.Find("#exportFile")
	if heading != "My Books > Import/Export" || doc.Find(signOutCSS).Length() < 1 ||
		button.Length() != 1 || status.Length() != 1 || files.Length() != 1 {
		return exportPage{}, fmt.Errorf("%w at export.generate: required markers missing", ErrCompatibility)
	}
	if strings.Join(strings.Fields(button.Text()), " ") != "Export Library" ||
		button.AttrOr("data-statusid", "") != "exportStatusText" ||
		button.AttrOr("data-filelistid", "") != "exportFile" {
		return exportPage{}, fmt.Errorf("%w at export.generate: export control changed", ErrCompatibility)
	}
	links := files.Find("a[href]")
	if links.Length() > 1 {
		return exportPage{}, fmt.Errorf("%w at export.generate: export link is ambiguous", ErrCompatibility)
	}
	result := exportPage{Status: strings.Join(strings.Fields(status.Text()), " ")}
	if links.Length() == 1 {
		link := links.First()
		if !strings.HasPrefix(strings.ToLower(strings.Join(strings.Fields(link.Text()), " ")), "your export from ") {
			return exportPage{}, fmt.Errorf("%w at export.generate: export link label changed", ErrCompatibility)
		}
		target, err := safeExportURL(link.AttrOr("href", ""))
		if err != nil {
			return exportPage{}, err
		}
		result.Link = target
	}
	return result, nil
}

func safeExportURL(raw string) (string, error) {
	base, _ := url.Parse(exportURL)
	target, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w at export.generate: invalid export link", ErrCompatibility)
	}
	target = base.ResolveReference(target)
	const prefix = "/review_porter/export/"
	parts := strings.Split(strings.TrimPrefix(target.Path, prefix), "/")
	exportIDValid := false
	if len(parts) == 2 {
		id, parseErr := strconv.ParseUint(parts[0], 10, 64)
		exportIDValid = parseErr == nil && id > 0
	}
	if !isGoodreadsPage(target.String()) || !strings.HasPrefix(target.Path, prefix) ||
		!exportIDValid || path.Base(target.Path) != "goodreads_export.csv" ||
		parts[1] != "goodreads_export.csv" || target.RawQuery != "" || target.Fragment != "" {
		return "", fmt.Errorf("%w at export.generate: unsafe export link", ErrCompatibility)
	}
	return target.String(), nil
}

func readExportFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() <= 0 || info.Size() > maxExportBytes {
		return nil, fmt.Errorf("%w at export.download: invalid file", ErrExportFailed)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w at export.download: file unavailable", ErrExportFailed)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxExportBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(data) > maxExportBytes {
		return nil, fmt.Errorf("%w at export.download: file unreadable", ErrExportFailed)
	}
	if err := validateExportCSV(data); err != nil {
		return nil, err
	}
	return data, nil
}

func validateExportCSV(data []byte) error {
	check := bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if !utf8.Valid(check) || bytes.IndexByte(check, 0) >= 0 {
		return fmt.Errorf("%w at export.download: invalid CSV encoding", ErrExportFailed)
	}
	reader := csv.NewReader(bytes.NewReader(check))
	header, err := reader.Read()
	if err != nil {
		return fmt.Errorf("%w at export.download: CSV header unavailable", ErrExportFailed)
	}
	columns := make(map[string]bool, len(header))
	for _, field := range header {
		columns[strings.TrimSpace(field)] = true
	}
	for _, required := range []string{"Book Id", "Title", "Author", "ISBN", "ISBN13", "My Rating", "Exclusive Shelf"} {
		if !columns[required] {
			return fmt.Errorf("%w at export.download: CSV header changed", ErrCompatibility)
		}
	}
	for {
		if _, err := reader.Read(); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return fmt.Errorf("%w at export.download: malformed CSV", ErrExportFailed)
		}
	}
	return nil
}
