package goodreads

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
)

const statusMutationStage = "mutation.status"

var (
	chooserOpenTimeout = 30 * time.Second
	chooserRetryWait   = 2 * time.Second
	errChooserClick    = errors.New("shelf chooser unavailable")
)

func Start(ctx context.Context, b browser.Browser, isbn domain.ISBN) (domain.MutationResult, error) {
	result, err := SetStatus(ctx, b, isbn, domain.StatusCurrentlyReading)
	if err != nil {
		return domain.MutationResult{}, err
	}
	result.Operation = "start"
	return result, nil
}

// SetStatus changes one exact rendered edition through the owner shelf
// chooser, then freshly reads every preservation field before reporting
// success.
func SetStatus(
	ctx context.Context,
	b browser.Browser,
	isbn domain.ISBN,
	status domain.ReadingStatus,
) (domain.MutationResult, error) {
	return setStatus(ctx, b, isbn, status, false)
}

func setStatus(
	ctx context.Context,
	b browser.Browser,
	isbn domain.ISBN,
	status domain.ReadingStatus,
	allowFinishDateChange bool,
) (domain.MutationResult, error) {
	if !status.Valid() {
		return domain.MutationResult{}, domain.ErrInvalidStatus
	}
	// Session state comes from the owner-library scan; a separate Status page
	// load must not consume the command deadline before the chooser click.
	candidate, err := findMutationCandidate(ctx, b, isbn, statusMutationStage, true)
	if err != nil {
		return domain.MutationResult{}, err
	}
	page, err := b.NewPage(ctx, candidate.PageURL)
	if err != nil {
		return domain.MutationResult{}, fmt.Errorf("%s: target page unavailable: %w", statusMutationStage, err)
	}
	before, err := mutationBookOnPage(ctx, page, candidate, isbn, statusMutationStage)
	if err != nil {
		_ = page.Close()
		return domain.MutationResult{}, err
	}
	before.Review, err = loadFullReview(ctx, b, candidate.ReviewURL, statusMutationStage)
	if err != nil {
		_ = page.Close()
		return domain.MutationResult{}, err
	}
	if before.Status == status {
		_ = page.Close()
		return verifyStatusMutation(before, before, status, allowFinishDateChange)
	}

	if err := openStatusChooser(ctx, page, candidate, isbn, before.Status); err != nil {
		_ = page.Close()
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(err, errChooserClick) {
			return domain.MutationResult{}, err
		}
		return domain.MutationResult{}, fmt.Errorf("%w at %s: shelf chooser contract changed (%v)", ErrCompatibility, statusMutationStage, err)
	}

	statusSelector := fmt.Sprintf(
		"div.shelfChooserWrapper.open li.visible.exclusive[alt='%s'] > span",
		status,
	)
	requestCtx, cancelRequest := context.WithTimeout(ctx, 15*time.Second)
	clickErr := page.ClickAndWaitForRequest(requestCtx, statusSelector)
	cancelRequest()
	var completionErr error
	if clickErr == nil {
		completionCtx, cancelCompletion := context.WithTimeout(ctx, 10*time.Second)
		completionErr = waitForStatus(completionCtx, page, candidate, isbn, status)
		cancelCompletion()
	}
	_ = page.Close()

	afterCandidate, readbackErr := findMutationCandidate(ctx, b, isbn, statusMutationStage, true)
	if readbackErr != nil {
		return domain.MutationResult{}, fmt.Errorf("%w at mutation.verify: readback unavailable", ErrMutationAmbiguous)
	}
	after := afterCandidate.Book
	after.Review, readbackErr = loadFullReview(ctx, b, afterCandidate.ReviewURL, statusMutationStage)
	if readbackErr != nil {
		return domain.MutationResult{}, fmt.Errorf("%w at mutation.verify: preservation readback unavailable", ErrMutationAmbiguous)
	}
	result, verifyErr := verifyStatusMutation(before, after, status, allowFinishDateChange)
	if verifyErr == nil {
		return result, nil
	}
	if clickErr != nil || completionErr != nil {
		return domain.MutationResult{}, fmt.Errorf("%w at %s: completion unknown", ErrMutationAmbiguous, statusMutationStage)
	}
	return domain.MutationResult{}, verifyErr
}

func openStatusChooser(
	ctx context.Context,
	page browser.Page,
	candidate ratingCandidate,
	isbn domain.ISBN,
	current domain.ReadingStatus,
) error {
	openSelector := fmt.Sprintf("#%s td.field.shelves a.shelfChooserLink", candidate.RowID)
	chooserCtx, cancel := context.WithTimeout(ctx, chooserOpenTimeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	var lastClick time.Time
	for {
		contractErr := statusChooserContract(chooserCtx, page, candidate, isbn, current)
		if contractErr == nil {
			return nil
		}
		lastErr = contractErr
		if lastClick.IsZero() || time.Since(lastClick) >= chooserRetryWait {
			if err := page.Click(chooserCtx, openSelector); err != nil {
				if chooserCtx.Err() != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					if lastErr != nil {
						return lastErr
					}
					return chooserCtx.Err()
				}
				return fmt.Errorf("%s: %w: %v", statusMutationStage, errChooserClick, err)
			}
			lastClick = time.Now()
		}
		select {
		case <-chooserCtx.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if lastErr != nil {
				return lastErr
			}
			return chooserCtx.Err()
		case <-ticker.C:
		}
	}
}

func statusChooserContract(
	ctx context.Context,
	page browser.Page,
	candidate ratingCandidate,
	isbn domain.ISBN,
	current domain.ReadingStatus,
) error {
	raw, err := page.HTML(ctx)
	if err != nil {
		return fmt.Errorf("DOM unavailable: %v", err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(raw))
	if err != nil {
		return fmt.Errorf("invalid DOM")
	}
	row := doc.Find("#" + candidate.RowID)
	if row.Length() != 1 {
		return fmt.Errorf("target row count changed")
	}
	book, err := parseShelfRow(row)
	if err != nil {
		return fmt.Errorf("target row could not be parsed")
	}
	if !exactISBN(book, isbn) || book.BookID != candidate.Book.BookID {
		return fmt.Errorf("target identity changed")
	}
	if book.Status != current {
		return fmt.Errorf("current status changed before selection")
	}
	// Goodreads moves the opened chooser to a floating box outside the row.
	// The page starts with no open chooser, and the exact target-row link above
	// is the only control clicked, so require exactly one global open box.
	chooser := doc.Find("div.shelfChooserWrapper.open")
	if chooser.Length() != 1 {
		return fmt.Errorf("open chooser count was not one")
	}
	options := chooser.Find("li.visible.exclusive")
	if options.Length() < 3 {
		return fmt.Errorf("fewer than three exclusive options were present")
	}
	for _, status := range []domain.ReadingStatus{
		domain.StatusToRead,
		domain.StatusCurrentlyReading,
		domain.StatusRead,
	} {
		option := options.Filter("[alt='" + string(status) + "']")
		if option.Length() != 1 || option.Find("span").Length() != 1 {
			return fmt.Errorf("%s option contract changed", status)
		}
	}
	chosen := options.Filter(".exclusive_chosen")
	if chosen.Length() != 1 || chosen.AttrOr("alt", "") != string(current) {
		return fmt.Errorf("chosen option did not match current status")
	}
	return nil
}

func waitForStatus(
	ctx context.Context,
	page browser.Page,
	candidate ratingCandidate,
	isbn domain.ISBN,
	status domain.ReadingStatus,
) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		book, err := mutationBookOnPage(ctx, page, candidate, isbn, statusMutationStage)
		if err == nil && book.Status == status {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// VerifyStatusMutation checks the requested exclusive shelf plus all state
// that the shelf chooser could unintentionally alter.
func VerifyStatusMutation(
	before domain.Book,
	after domain.Book,
	status domain.ReadingStatus,
) (domain.MutationResult, error) {
	return verifyStatusMutation(before, after, status, false)
}

func verifyStatusMutation(
	before domain.Book,
	after domain.Book,
	status domain.ReadingStatus,
	allowFinishDateChange bool,
) (domain.MutationResult, error) {
	if !status.Valid() {
		return domain.MutationResult{}, domain.ErrInvalidStatus
	}
	if before.BookID == "" || after.BookID != before.BookID {
		return domain.MutationResult{}, verificationError("book_id", "changed")
	}
	if before.ISBN10 != "" && after.ISBN10 != before.ISBN10 {
		return domain.MutationResult{}, verificationError("isbn10", "changed")
	}
	if before.ISBN13 != "" && after.ISBN13 != before.ISBN13 {
		return domain.MutationResult{}, verificationError("isbn13", "changed")
	}
	if after.Status != status {
		return domain.MutationResult{}, verificationError("status", "did not match")
	}
	if after.Rating != before.Rating {
		return domain.MutationResult{}, verificationError("rating", "changed")
	}
	if !allowFinishDateChange && !equalOptionalString(after.DateRead, before.DateRead) {
		return domain.MutationResult{}, verificationError("date_read", "changed")
	}
	if !equalStringSet(after.Bookshelves, before.Bookshelves) {
		return domain.MutationResult{}, verificationError("bookshelves", "changed")
	}
	if before.Review == nil || after.Review == nil {
		return domain.MutationResult{}, verificationError("review", "was unavailable")
	}
	if *after.Review != *before.Review {
		return domain.MutationResult{}, verificationError("review", "changed")
	}
	return domain.MutationResult{
		Operation: "status",
		Before:    before,
		After:     after,
		Changes:   domain.BookUpdate{Status: &status},
		Verified:  true,
	}, nil
}
