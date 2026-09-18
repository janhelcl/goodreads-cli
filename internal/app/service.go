package app

import (
	"context"
	"errors"
	"time"

	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
	"github.com/janhelcl/goodreads-cli/internal/goodreads"
	"github.com/janhelcl/goodreads-cli/internal/profile"
)

type ConnectionStatus struct {
	Connected    bool `json:"connected"`
	SessionValid bool `json:"session_valid"`
}

type LogoutResult struct {
	Connected      bool `json:"connected"`
	ProfileRemoved bool `json:"profile_removed"`
}

type AuthService interface {
	Login(context.Context) (ConnectionStatus, error)
	Status(context.Context) (ConnectionStatus, error)
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
	Export(context.Context, string, bool) (domain.ExportResult, error)
}

type Config struct {
	BrowserPath string
	Headed      bool
}

func NewService(config Config) (Service, error) {
	paths, err := profile.DefaultPaths()
	if err != nil {
		return nil, err
	}
	return &service{
		factory:     browser.RodFactory{},
		paths:       paths,
		browserPath: config.BrowserPath,
		headed:      config.Headed,
	}, nil
}

type service struct {
	factory     browser.Factory
	paths       profile.Paths
	browserPath string
	headed      bool
}

type profilePolicy int

const (
	requireProfile profilePolicy = iota
	createProfile
	allowMissingProfile
)

func (s *service) withBrowser(
	ctx context.Context,
	operation string,
	policy profilePolicy,
	options browser.LaunchOptions,
	use func(browser.Browser) error,
) error {
	started := time.Now()
	var runtimeInfo browser.RuntimeInfo
	err := s.withProfileLock(ctx, func() (err error) {
		switch policy {
		case createProfile:
			if err := s.paths.EnsureBrowser(); err != nil {
				return err
			}
		default:
			exists, err := s.paths.HasBrowser()
			if err != nil {
				return err
			}
			if !exists {
				if policy == allowMissingProfile {
					return nil
				}
				return ErrSessionExpired
			}
		}

		options.ProfileDir = s.paths.Browser
		options.BrowserPath = s.browserPath
		b, err := s.factory.Launch(ctx, options)
		if err != nil {
			return err
		}
		if provider, ok := b.(browser.RuntimeInfoProvider); ok {
			runtimeInfo = provider.RuntimeInfo()
		}
		defer func() {
			err = errors.Join(err, b.Close())
		}()
		return use(b)
	})
	event := DiagnosticEvent{
		Operation:  operation,
		Elapsed:    time.Since(started),
		Browser:    runtimeInfo.Product,
		Version:    runtimeInfo.Version,
		RetryCount: 0,
	}
	if err != nil {
		event.Stage = goodreads.SafeErrorStage(err)
		event.ErrorKind = DescribeError(err).Kind
	} else {
		event.Stage = operation + ".complete"
	}
	emitDiagnostic(ctx, event)
	return err
}

func (s *service) withProfileLock(ctx context.Context, use func() error) (err error) {
	lockCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	lock, err := s.paths.Acquire(lockCtx)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, lock.Release())
	}()
	return use()
}
