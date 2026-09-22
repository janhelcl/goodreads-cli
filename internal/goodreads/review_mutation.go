package goodreads

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
)

const reviewMutationStage = "mutation.review"

// Review replaces or explicitly clears the full review for one exact edition,
// then freshly reads the review and all preservation fields before succeeding.
// A nil review is invalid; callers represent an explicit clear with "".
func Review(
	ctx context.Context,
	b browser.Browser,
	isbn domain.ISBN,
	review *string,
) (domain.MutationResult, error) {
	if review == nil {
		return domain.MutationResult{}, domain.ErrInvalidReview
	}
	candidate, err := findMutationCandidate(ctx, b, isbn, reviewMutationStage, false)
	if err != nil {
		return domain.MutationResult{}, err
	}
	before := candidate.Book
	before.Review, err = loadFullReview(ctx, b, candidate.ReviewURL, reviewMutationStage)
	if err != nil {
		return domain.MutationResult{}, err
	}
	if *before.Review == *review {
		return VerifyReviewMutation(before, before, *review)
	}

	page, err := b.NewPage(ctx, candidate.ReviewURL)
	if err != nil {
		return domain.MutationResult{}, fmt.Errorf("%s: review page unavailable: %w", reviewMutationStage, err)
	}
	raw, err := page.HTML(ctx)
	if err != nil {
		_ = page.Close()
		return domain.MutationResult{}, fmt.Errorf("%s: review DOM unavailable: %w", reviewMutationStage, err)
	}
	current, err := page.URL(ctx)
	currentURL, parseErr := url.Parse(current)
	if err != nil || parseErr != nil || !isGoodreadsPage(current) || !reviewEditPath.MatchString(currentURL.Path) {
		_ = page.Close()
		return domain.MutationResult{}, fmt.Errorf("%w at %s: unexpected review page", ErrCompatibility, reviewMutationStage)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(raw))
	if err != nil {
		_ = page.Close()
		return domain.MutationResult{}, fmt.Errorf("%w at %s: invalid review DOM", ErrCompatibility, reviewMutationStage)
	}
	textareaSelector, submitSelector, err := reviewEditorContract(doc)
	if err != nil {
		_ = page.Close()
		return domain.MutationResult{}, err
	}
	currentReview, err := page.Value(ctx, textareaSelector)
	if err != nil || currentReview != *before.Review {
		_ = page.Close()
		return domain.MutationResult{}, fmt.Errorf("%w at %s: current review changed before editing", ErrCompatibility, reviewMutationStage)
	}
	if err := page.Input(ctx, textareaSelector, *review); err != nil {
		_ = page.Close()
		return domain.MutationResult{}, fmt.Errorf("%w at %s: review field could not be changed", ErrCompatibility, reviewMutationStage)
	}
	entered, err := page.Value(ctx, textareaSelector)
	if err != nil || entered != *review {
		_ = page.Close()
		return domain.MutationResult{}, fmt.Errorf("%w at %s: review input did not match", ErrCompatibility, reviewMutationStage)
	}

	beforeSubmitURL, _ := page.URL(ctx)
	clickErr := page.Click(ctx, submitSelector)
	var completionErr error
	if clickErr == nil {
		completionCtx, cancelCompletion := context.WithTimeout(ctx, 10*time.Second)
		completionErr = waitForReviewSubmission(completionCtx, page, beforeSubmitURL)
		cancelCompletion()
	}
	_ = page.Close()

	afterCandidate, readbackErr := readbackMutationCandidate(ctx, b, isbn, candidate, reviewMutationStage, false)
	if readbackErr != nil {
		return domain.MutationResult{}, fmt.Errorf("%w at mutation.verify: readback unavailable", ErrMutationAmbiguous)
	}
	after := afterCandidate.Book
	after.Review, readbackErr = loadFullReview(ctx, b, afterCandidate.ReviewURL, reviewMutationStage)
	if readbackErr != nil {
		return domain.MutationResult{}, fmt.Errorf("%w at mutation.verify: review readback unavailable", ErrMutationAmbiguous)
	}
	result, verifyErr := VerifyReviewMutation(before, after, *review)
	if verifyErr == nil {
		return result, nil
	}
	if clickErr != nil || completionErr != nil {
		return domain.MutationResult{}, fmt.Errorf("%w at %s: completion unknown", ErrMutationAmbiguous, reviewMutationStage)
	}
	return domain.MutationResult{}, verifyErr
}

func reviewEditorContract(doc *goquery.Document) (string, string, error) {
	textarea := doc.Find("textarea[name='review[review]'], textarea#review_review_usertext")
	if textarea.Length() != 1 {
		return "", "", fmt.Errorf("%w at %s: full review field missing", ErrCompatibility, reviewMutationStage)
	}
	selector := "textarea#review_review_usertext"
	if textarea.AttrOr("name", "") == "review[review]" {
		selector = "textarea[name='review[review]']"
	}
	form := textarea.Closest("form")
	if form.Length() != 1 || form.Find("input[type='submit'][name='next']").Length() != 1 {
		return "", "", fmt.Errorf("%w at %s: review submit control changed", ErrCompatibility, reviewMutationStage)
	}
	return "form:has(" + selector + ") " + selector,
		"form:has(" + selector + ") input[type='submit'][name='next']", nil
}

// VerifyReviewMutation never includes either review value in an error.
func VerifyReviewMutation(before, after domain.Book, review string) (domain.MutationResult, error) {
	if before.BookID == "" || after.BookID != before.BookID {
		return domain.MutationResult{}, verificationError("book_id", "changed")
	}
	if before.ISBN10 != "" && after.ISBN10 != before.ISBN10 {
		return domain.MutationResult{}, verificationError("isbn10", "changed")
	}
	if before.ISBN13 != "" && after.ISBN13 != before.ISBN13 {
		return domain.MutationResult{}, verificationError("isbn13", "changed")
	}
	if before.Review == nil || after.Review == nil {
		return domain.MutationResult{}, verificationError("review", "was unavailable")
	}
	if *after.Review != review {
		return domain.MutationResult{}, verificationError("review", "did not match")
	}
	if after.Status != before.Status {
		return domain.MutationResult{}, verificationError("status", "changed")
	}
	if after.Rating != before.Rating {
		return domain.MutationResult{}, verificationError("rating", "changed")
	}
	if !equalOptionalString(after.DateRead, before.DateRead) {
		return domain.MutationResult{}, verificationError("date_read", "changed")
	}
	if !equalStringSet(after.Bookshelves, before.Bookshelves) {
		return domain.MutationResult{}, verificationError("bookshelves", "changed")
	}
	return domain.MutationResult{
		Operation: "review",
		Before:    before,
		After:     after,
		Changes:   domain.BookUpdate{Review: &review},
		Verified:  true,
	}, nil
}
