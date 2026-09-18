package app

import (
	"context"
	"time"

	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
	"github.com/janhelcl/goodreads-cli/internal/goodreads"
)

func connectionStatus(status goodreads.ConnectionStatus) ConnectionStatus {
	return ConnectionStatus{
		Connected:    status.Connected,
		SessionValid: status.SessionValid,
	}
}

func (s *service) Login(ctx context.Context) (result ConnectionStatus, err error) {
	err = s.withBrowser(ctx, "login", createProfile, browser.LaunchOptions{InteractiveLogin: true}, func(b browser.Browser) error {
		status, err := goodreads.Login(ctx, b)
		result = connectionStatus(status)
		return err
	})
	return result, err
}

func (s *service) Status(ctx context.Context) (result ConnectionStatus, err error) {
	err = s.withBrowser(ctx, "status", allowMissingProfile, browser.LaunchOptions{Headless: !s.headed}, func(b browser.Browser) error {
		status, err := goodreads.Status(ctx, b)
		result = connectionStatus(status)
		return err
	})
	return result, err
}

func (s *service) Logout(ctx context.Context) (LogoutResult, error) {
	err := s.withProfileLock(ctx, s.paths.RemoveBrowser)
	if err != nil {
		return LogoutResult{}, err
	}
	return LogoutResult{ProfileRemoved: true}, nil
}

func (s *service) Library(ctx context.Context, filter domain.LibraryFilter) (books []domain.Book, err error) {
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	err = s.withBrowser(ctx, "library", requireProfile, browser.LaunchOptions{Headless: !s.headed}, func(b browser.Browser) error {
		var callErr error
		books, callErr = goodreads.Library(ctx, b, filter)
		return callErr
	})
	return books, err
}

func (s *service) Get(ctx context.Context, isbn domain.ISBN) (book domain.Book, err error) {
	err = s.withBrowser(ctx, "get", requireProfile, browser.LaunchOptions{Headless: !s.headed}, func(b browser.Browser) error {
		var callErr error
		book, callErr = goodreads.Get(ctx, b, isbn)
		return callErr
	})
	return book, err
}

func (s *service) mutate(
	ctx context.Context,
	operation string,
	call func(browser.Browser) (domain.MutationResult, error),
) (result domain.MutationResult, err error) {
	err = s.withBrowser(ctx, operation, requireProfile, browser.LaunchOptions{Headless: !s.headed}, func(b browser.Browser) error {
		var callErr error
		result, callErr = call(b)
		return callErr
	})
	if err != nil {
		return domain.MutationResult{}, err
	}
	if !result.Verified {
		return domain.MutationResult{}, ErrVerificationFailed
	}
	return result, nil
}

func (s *service) Add(
	ctx context.Context,
	isbn domain.ISBN,
	status domain.ReadingStatus,
) (domain.MutationResult, error) {
	if !status.Valid() {
		return domain.MutationResult{}, domain.ErrInvalidStatus
	}
	return s.mutate(ctx, "add", func(b browser.Browser) (domain.MutationResult, error) {
		return goodreads.Add(ctx, b, isbn, status)
	})
}

func (s *service) Rate(ctx context.Context, isbn domain.ISBN, rating int) (domain.MutationResult, error) {
	if err := domain.ValidateRating(rating); err != nil {
		return domain.MutationResult{}, err
	}
	return s.mutate(ctx, "rate", func(b browser.Browser) (domain.MutationResult, error) {
		return goodreads.Rate(ctx, b, isbn, rating)
	})
}

func (s *service) Start(ctx context.Context, isbn domain.ISBN) (domain.MutationResult, error) {
	return s.mutate(ctx, "start", func(b browser.Browser) (domain.MutationResult, error) {
		return goodreads.Start(ctx, b, isbn)
	})
}

func (s *service) Finish(
	ctx context.Context,
	isbn domain.ISBN,
	date time.Time,
	rating *int,
) (domain.MutationResult, error) {
	if err := domain.ValidateDate(date); err != nil {
		return domain.MutationResult{}, err
	}
	if rating != nil {
		if err := domain.ValidateRating(*rating); err != nil {
			return domain.MutationResult{}, err
		}
	}
	return s.mutate(ctx, "finish", func(b browser.Browser) (domain.MutationResult, error) {
		return goodreads.Finish(ctx, b, isbn, date, rating)
	})
}

func (s *service) Review(
	ctx context.Context,
	isbn domain.ISBN,
	review *string,
) (domain.MutationResult, error) {
	if review == nil {
		return domain.MutationResult{}, domain.ErrInvalidReview
	}
	return s.mutate(ctx, "review", func(b browser.Browser) (domain.MutationResult, error) {
		return goodreads.Review(ctx, b, isbn, review)
	})
}
