package app

import (
	"context"
	"time"

	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
	"github.com/janhelcl/goodreads-cli/internal/goodreads"
	"github.com/janhelcl/goodreads-cli/internal/profile"
)

type AuthService interface {
	Login(context.Context) (goodreads.ConnectionStatus, error)
	Status(context.Context) (goodreads.ConnectionStatus, error)
	Logout(context.Context) (LogoutResult, error)
}

type Service interface {
	AuthService
	Library(context.Context, domain.LibraryFilter) ([]domain.Book, error)
	Get(context.Context, domain.ISBN) (domain.Book, error)
	Add(context.Context, domain.ISBN, domain.ReadingStatus) (domain.MutationResult, error)
	Rate(context.Context, domain.ISBN, int) (domain.MutationResult, error)
	Start(context.Context, domain.ISBN) (domain.MutationResult, error)
	Finish(context.Context, domain.ISBN, time.Time, *int) (domain.MutationResult, error)
	Review(context.Context, domain.ISBN, *string) (domain.MutationResult, error)
}

type LogoutResult struct {
	Connected      bool `json:"connected"`
	ProfileRemoved bool `json:"profile_removed"`
}

type Auth struct {
	Factory     browser.Factory
	Paths       profile.Paths
	BrowserPath string
	Headed      bool
}

func (a Auth) acquire(ctx context.Context) (*profile.Lock, error) {
	lockCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return a.Paths.Acquire(lockCtx)
}

func (a Auth) Login(ctx context.Context) (goodreads.ConnectionStatus, error) {
	lock, err := a.acquire(ctx)
	if err != nil {
		return goodreads.ConnectionStatus{}, err
	}
	defer lock.Release()
	if err := a.Paths.EnsureBrowser(); err != nil {
		return goodreads.ConnectionStatus{}, err
	}
	b, err := a.Factory.Launch(ctx, browser.LaunchOptions{
		ProfileDir: a.Paths.Browser, BrowserPath: a.BrowserPath, InteractiveLogin: true,
	})
	if err != nil {
		return goodreads.ConnectionStatus{}, err
	}
	result, err := goodreads.Login(ctx, b)
	closeErr := b.Close()
	if err != nil {
		return goodreads.ConnectionStatus{}, err
	}
	if closeErr != nil {
		return goodreads.ConnectionStatus{}, closeErr
	}
	return result, nil
}

func (a Auth) Status(ctx context.Context) (goodreads.ConnectionStatus, error) {
	lock, err := a.acquire(ctx)
	if err != nil {
		return goodreads.ConnectionStatus{}, err
	}
	defer lock.Release()
	exists, err := a.Paths.HasBrowser()
	if err != nil || !exists {
		return goodreads.ConnectionStatus{}, err
	}
	b, err := a.Factory.Launch(ctx, browser.LaunchOptions{
		ProfileDir: a.Paths.Browser, BrowserPath: a.BrowserPath, Headless: !a.Headed,
	})
	if err != nil {
		return goodreads.ConnectionStatus{}, err
	}
	result, err := goodreads.Status(ctx, b)
	closeErr := b.Close()
	if err != nil {
		return goodreads.ConnectionStatus{}, err
	}
	if closeErr != nil {
		return goodreads.ConnectionStatus{}, closeErr
	}
	return result, nil
}

func (a Auth) Logout(ctx context.Context) (LogoutResult, error) {
	lock, err := a.acquire(ctx)
	if err != nil {
		return LogoutResult{}, err
	}
	defer lock.Release()
	if err := a.Paths.RemoveBrowser(); err != nil {
		return LogoutResult{}, err
	}
	return LogoutResult{Connected: false, ProfileRemoved: true}, nil
}

func (a Auth) Library(ctx context.Context, filter domain.LibraryFilter) ([]domain.Book, error) {
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	lock, err := a.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	exists, err := a.Paths.HasBrowser()
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, goodreads.ErrSessionExpired
	}
	b, err := a.Factory.Launch(ctx, browser.LaunchOptions{
		ProfileDir: a.Paths.Browser, BrowserPath: a.BrowserPath, Headless: !a.Headed,
	})
	if err != nil {
		return nil, err
	}
	books, err := goodreads.Library(ctx, b, filter)
	closeErr := b.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return books, nil
}

func (a Auth) Get(ctx context.Context, isbn domain.ISBN) (domain.Book, error) {
	lock, err := a.acquire(ctx)
	if err != nil {
		return domain.Book{}, err
	}
	defer lock.Release()
	exists, err := a.Paths.HasBrowser()
	if err != nil {
		return domain.Book{}, err
	}
	if !exists {
		return domain.Book{}, goodreads.ErrSessionExpired
	}
	b, err := a.Factory.Launch(ctx, browser.LaunchOptions{
		ProfileDir: a.Paths.Browser, BrowserPath: a.BrowserPath, Headless: !a.Headed,
	})
	if err != nil {
		return domain.Book{}, err
	}
	book, err := goodreads.Get(ctx, b, isbn)
	closeErr := b.Close()
	if err != nil {
		return domain.Book{}, err
	}
	if closeErr != nil {
		return domain.Book{}, closeErr
	}
	return book, nil
}

func (a Auth) Add(
	ctx context.Context,
	isbn domain.ISBN,
	status domain.ReadingStatus,
) (domain.MutationResult, error) {
	if !status.Valid() {
		return domain.MutationResult{}, domain.ErrInvalidStatus
	}
	lock, err := a.acquire(ctx)
	if err != nil {
		return domain.MutationResult{}, err
	}
	defer lock.Release()
	exists, err := a.Paths.HasBrowser()
	if err != nil {
		return domain.MutationResult{}, err
	}
	if !exists {
		return domain.MutationResult{}, goodreads.ErrSessionExpired
	}
	b, err := a.Factory.Launch(ctx, browser.LaunchOptions{
		ProfileDir: a.Paths.Browser, BrowserPath: a.BrowserPath, Headless: !a.Headed,
	})
	if err != nil {
		return domain.MutationResult{}, err
	}
	result, err := goodreads.Add(ctx, b, isbn, status)
	closeErr := b.Close()
	if err != nil {
		return domain.MutationResult{}, err
	}
	if closeErr != nil {
		return domain.MutationResult{}, closeErr
	}
	if !result.Verified {
		return domain.MutationResult{}, goodreads.ErrVerificationFailed
	}
	return result, nil
}

func (a Auth) Rate(ctx context.Context, isbn domain.ISBN, rating int) (domain.MutationResult, error) {
	if err := domain.ValidateRating(rating); err != nil {
		return domain.MutationResult{}, err
	}
	lock, err := a.acquire(ctx)
	if err != nil {
		return domain.MutationResult{}, err
	}
	defer lock.Release()
	exists, err := a.Paths.HasBrowser()
	if err != nil {
		return domain.MutationResult{}, err
	}
	if !exists {
		return domain.MutationResult{}, goodreads.ErrSessionExpired
	}
	b, err := a.Factory.Launch(ctx, browser.LaunchOptions{
		ProfileDir: a.Paths.Browser, BrowserPath: a.BrowserPath, Headless: !a.Headed,
	})
	if err != nil {
		return domain.MutationResult{}, err
	}
	result, err := goodreads.Rate(ctx, b, isbn, rating)
	closeErr := b.Close()
	if err != nil {
		return domain.MutationResult{}, err
	}
	if closeErr != nil {
		return domain.MutationResult{}, closeErr
	}
	if !result.Verified {
		return domain.MutationResult{}, goodreads.ErrVerificationFailed
	}
	return result, nil
}

func (a Auth) Start(ctx context.Context, isbn domain.ISBN) (domain.MutationResult, error) {
	lock, err := a.acquire(ctx)
	if err != nil {
		return domain.MutationResult{}, err
	}
	defer lock.Release()
	exists, err := a.Paths.HasBrowser()
	if err != nil {
		return domain.MutationResult{}, err
	}
	if !exists {
		return domain.MutationResult{}, goodreads.ErrSessionExpired
	}
	b, err := a.Factory.Launch(ctx, browser.LaunchOptions{
		ProfileDir: a.Paths.Browser, BrowserPath: a.BrowserPath, Headless: !a.Headed,
	})
	if err != nil {
		return domain.MutationResult{}, err
	}
	result, err := goodreads.Start(ctx, b, isbn)
	closeErr := b.Close()
	if err != nil {
		return domain.MutationResult{}, err
	}
	if closeErr != nil {
		return domain.MutationResult{}, closeErr
	}
	if !result.Verified {
		return domain.MutationResult{}, goodreads.ErrVerificationFailed
	}
	return result, nil
}

func (a Auth) Finish(
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
	lock, err := a.acquire(ctx)
	if err != nil {
		return domain.MutationResult{}, err
	}
	defer lock.Release()
	exists, err := a.Paths.HasBrowser()
	if err != nil {
		return domain.MutationResult{}, err
	}
	if !exists {
		return domain.MutationResult{}, goodreads.ErrSessionExpired
	}
	b, err := a.Factory.Launch(ctx, browser.LaunchOptions{
		ProfileDir: a.Paths.Browser, BrowserPath: a.BrowserPath, Headless: !a.Headed,
	})
	if err != nil {
		return domain.MutationResult{}, err
	}
	result, err := goodreads.Finish(ctx, b, isbn, date, rating)
	closeErr := b.Close()
	if err != nil {
		return domain.MutationResult{}, err
	}
	if closeErr != nil {
		return domain.MutationResult{}, closeErr
	}
	if !result.Verified {
		return domain.MutationResult{}, goodreads.ErrVerificationFailed
	}
	return result, nil
}

func (a Auth) Review(
	ctx context.Context,
	isbn domain.ISBN,
	review *string,
) (domain.MutationResult, error) {
	if review == nil {
		return domain.MutationResult{}, domain.ErrInvalidReview
	}
	lock, err := a.acquire(ctx)
	if err != nil {
		return domain.MutationResult{}, err
	}
	defer lock.Release()
	exists, err := a.Paths.HasBrowser()
	if err != nil {
		return domain.MutationResult{}, err
	}
	if !exists {
		return domain.MutationResult{}, goodreads.ErrSessionExpired
	}
	b, err := a.Factory.Launch(ctx, browser.LaunchOptions{
		ProfileDir: a.Paths.Browser, BrowserPath: a.BrowserPath, Headless: !a.Headed,
	})
	if err != nil {
		return domain.MutationResult{}, err
	}
	result, err := goodreads.Review(ctx, b, isbn, review)
	closeErr := b.Close()
	if err != nil {
		return domain.MutationResult{}, err
	}
	if closeErr != nil {
		return domain.MutationResult{}, closeErr
	}
	if !result.Verified {
		return domain.MutationResult{}, goodreads.ErrVerificationFailed
	}
	return result, nil
}
