package goodreads

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/janhelcl/goodreads-cli/internal/browser"
)

const exportFixtureURL = "https://www.goodreads.com/review_porter/export/123/goodreads_export.csv"

func exportHTML(status, link string) string {
	linkHTML := ""
	if link != "" {
		linkHTML = `<a href="` + link + `">Your export from 09/17/2026 - 21:00</a>`
	}
	return `<html><body>
		<a href="/user/sign_out">Sign out</a>
		<h1>My Books &gt; Import/Export</h1>
		<div class="exportBooks">
			<button class="gr-form--compact__submitButton js-LibraryExport"
				data-statusid="exportStatusText" data-filelistid="exportFile" data-userid="123">Export Library</button>
		</div>
		<div id="exportStatusText" class="exportStatus">` + status + `</div>
		<div id="exportFile" class="fileList">` + linkHTML + `</div>
	</body></html>`
}

func validExportCSV() []byte {
	return []byte("Book Id,Title,Author,ISBN,ISBN13,My Rating,Exclusive Shelf\n" +
		"1,Fixture,Example,123456789X,9781234567897,4,read\n")
}

func TestDownloadExportRequiresFreshGenerationAndValidatesCSV(t *testing.T) {
	path := filepath.Join(t.TempDir(), "download")
	data := validExportCSV()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	var state atomic.Int32
	var clicks atomic.Int32
	var downloads atomic.Int32
	exportPage := &fakePage{
		url: exportURL,
		htmlFunc: func() string {
			switch state.Load() {
			case 0:
				return exportHTML("", "")
			case 1:
				state.Store(2)
				return exportHTML("Your library is exporting.", "")
			default:
				return exportHTML("", exportFixtureURL)
			}
		},
		click: func(selector string) error {
			if selector != exportButtonSelector {
				t.Fatalf("generation selector=%q", selector)
			}
			clicks.Add(1)
			state.Store(1)
			return nil
		},
		download: func(selector string) (browser.Download, error) {
			if selector != exportLinkSelector {
				t.Fatalf("download selector=%q", selector)
			}
			downloads.Add(1)
			return browser.Download{
				URL: exportFixtureURL, SuggestedFilename: "goodreads_export.csv", Path: path,
			}, nil
		},
	}
	b := &fakeBrowser{pages: map[string]browser.Page{
		libraryURL: privatePage(),
		exportURL:  exportPage,
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := DownloadExport(ctx, b)
	if err != nil || !result.OK || result.Bytes != int64(len(data)) ||
		string(result.Data) != string(data) || clicks.Load() != 1 || downloads.Load() != 1 {
		t.Fatalf("result=%+v err=%v clicks=%d downloads=%d", result, err, clicks.Load(), downloads.Load())
	}
}

func TestDownloadExportFailsClosedWithoutGenerationMarker(t *testing.T) {
	page := &fakePage{
		url:      exportURL,
		htmlFunc: func() string { return exportHTML("", exportFixtureURL) },
	}
	b := &fakeBrowser{pages: map[string]browser.Page{
		libraryURL: privatePage(),
		exportURL:  page,
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := DownloadExport(ctx, b); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("accepted an unproven old export: %v", err)
	}
}

func TestParseExportPageRejectsDriftAndUnsafeLinks(t *testing.T) {
	valid, err := parseExportPage(exportHTML("", exportFixtureURL))
	if err != nil || valid.Link != exportFixtureURL {
		t.Fatalf("valid page=%+v err=%v", valid, err)
	}
	for _, raw := range []string{
		strings.Replace(exportHTML("", ""), "Export Library", "Download Library", 1),
		exportHTML("", "https://example.com/goodreads_library_export.csv"),
		exportHTML("", "/assets/sample_export.csv"),
		exportHTML("", "/review_porter/export/not-an-id/goodreads_export.csv"),
		exportHTML("", "/review_porter/export/123/extra/goodreads_export.csv"),
		exportHTML("", exportFixtureURL+"?download=1"),
		exportHTML("", exportFixtureURL) + `<div id="exportFile"></div>`,
	} {
		if _, err := parseExportPage(raw); !errors.Is(err, ErrCompatibility) {
			t.Fatalf("accepted drifted export page: %v", err)
		}
	}
}

func TestValidateExportCSV(t *testing.T) {
	if err := validateExportCSV(validExportCSV()); err != nil {
		t.Fatalf("valid CSV: %v", err)
	}
	if err := validateExportCSV(append([]byte{0xef, 0xbb, 0xbf}, validExportCSV()...)); err != nil {
		t.Fatalf("valid BOM CSV: %v", err)
	}
	for _, data := range [][]byte{
		[]byte("Book Id,Title\n1,Fixture\n"),
		[]byte("Book Id,Title,Author,ISBN,ISBN13,My Rating,Exclusive Shelf\n1,\"unterminated\n"),
		[]byte{0xff, 0xfe},
	} {
		if err := validateExportCSV(data); err == nil {
			t.Fatalf("accepted invalid CSV %q", data)
		}
	}
}
