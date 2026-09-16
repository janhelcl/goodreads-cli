package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/janhelcl/goodreads-cli/internal/app"
	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
	"github.com/janhelcl/goodreads-cli/internal/goodreads"
	"github.com/janhelcl/goodreads-cli/internal/profile"
)

var errUsage = errors.New("invalid command usage")

type authFactory func(headed bool) (app.Service, error)

type mutationOutput struct {
	OK        bool              `json:"ok"`
	Operation string            `json:"operation"`
	ISBN13    string            `json:"isbn13"`
	BookID    string            `json:"book_id"`
	Title     string            `json:"title"`
	Changes   domain.BookUpdate `json:"changes"`
	Verified  bool              `json:"verified"`
}

func defaultAuthFactory(headed bool) (app.Service, error) {
	paths, err := profile.DefaultPaths()
	if err != nil {
		return nil, err
	}
	return app.Auth{Factory: browser.RodFactory{}, Paths: paths, Headed: headed}, nil
}

func Execute() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, defaultAuthFactory))
}

func run(args []string, out, errOut io.Writer, factory authFactory) int {
	root := newRoot(out, errOut, factory)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		if debug, _ := root.PersistentFlags().GetBool("debug"); debug {
			fmt.Fprintf(errOut, "debug: exit_code=%d\n", exitCode(err))
		}
		fmt.Fprintln(errOut, publicError(err))
		return exitCode(err)
	}
	return 0
}

func newRoot(out, errOut io.Writer, factory authFactory) *cobra.Command {
	var jsonOutput bool
	var headed bool
	var debug bool
	var noColor bool
	var timeout time.Duration
	root := &cobra.Command{
		Use:           "gr",
		Short:         "Work with your Goodreads library",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return fmt.Errorf("%w: %v", errUsage, err)
	})
	root.PersistentFlags().BoolVar(&jsonOutput, "json", false, "emit one JSON result")
	root.PersistentFlags().BoolVar(&headed, "headed", false, "show the browser for troubleshooting")
	root.PersistentFlags().BoolVar(&debug, "debug", false, "emit redacted diagnostics to stderr")
	root.PersistentFlags().BoolVar(&noColor, "no-color", false, "disable terminal colors")
	root.PersistentFlags().DurationVar(&timeout, "timeout", 0, "total operation timeout")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if timeout < 0 {
			return fmt.Errorf("%w: --timeout must not be negative", errUsage)
		}
		if debug {
			fmt.Fprintf(errOut, "debug: operation=%s\n", cmd.Name())
		}
		_ = noColor // Human output currently has no color sequences.
		return nil
	}

	noArgs := func(_ *cobra.Command, args []string) error {
		if len(args) != 0 {
			return fmt.Errorf("%w: this command takes no arguments", errUsage)
		}
		return nil
	}
	newContext := func(cmd *cobra.Command, defaultTimeout time.Duration) (context.Context, context.CancelFunc) {
		d := defaultTimeout
		if timeout > 0 {
			d = timeout
		}
		return context.WithTimeout(cmd.Context(), d)
	}
	write := func(cmd *cobra.Command, value any, human string) error {
		if jsonOutput {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(value)
		}
		_, err := fmt.Fprintln(cmd.OutOrStdout(), human)
		return err
	}

	root.AddCommand(&cobra.Command{
		Use:   "login",
		Short: "Sign in through a dedicated browser window",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service, err := factory(true)
			if err != nil {
				return err
			}
			ctx, cancel := newContext(cmd, 10*time.Minute)
			defer cancel()
			fmt.Fprintln(cmd.ErrOrStderr(), "Opening Goodreads in a dedicated browser. Complete sign-in there.")
			result, err := service.Login(ctx)
			if err != nil {
				return err
			}
			return write(cmd, result, "Goodreads connected")
		},
	})
	root.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Check the saved Goodreads session",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service, err := factory(headed)
			if err != nil {
				return err
			}
			ctx, cancel := newContext(cmd, time.Minute)
			defer cancel()
			result, err := service.Status(ctx)
			if err != nil {
				return err
			}
			human := "Goodreads: disconnected"
			if result.Connected && result.SessionValid {
				human = "Goodreads: connected"
			}
			return write(cmd, result, human)
		},
	})
	root.AddCommand(&cobra.Command{
		Use:   "logout",
		Short: "Delete the dedicated Goodreads browser profile",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service, err := factory(headed)
			if err != nil {
				return err
			}
			ctx, cancel := newContext(cmd, 15*time.Second)
			defer cancel()
			result, err := service.Logout(ctx)
			if err != nil {
				return err
			}
			return write(cmd, result, "Goodreads profile removed")
		},
	})
	var shelf string
	var rating int
	var limit int
	library := &cobra.Command{
		Use:   "library",
		Short: "Read live Goodreads shelf pages",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			filter := domain.LibraryFilter{Shelf: domain.ReadingStatus(shelf), Rating: rating, Limit: limit}
			if cmd.Flags().Changed("rating") && rating == 0 {
				return fmt.Errorf("%w: --rating must be 1 through 5", errUsage)
			}
			if err := filter.Validate(); err != nil {
				return fmt.Errorf("%w: %v", errUsage, err)
			}
			service, err := factory(headed)
			if err != nil {
				return err
			}
			ctx, cancel := newContext(cmd, time.Minute)
			defer cancel()
			books, err := service.Library(ctx, filter)
			if err != nil {
				return err
			}
			if jsonOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(books)
			}
			if len(books) == 0 {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "No books found")
				return err
			}
			for _, book := range books {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s — %s (%s)\n", book.Title, book.Author, book.Status); err != nil {
					return err
				}
			}
			return nil
		},
	}
	library.Flags().StringVar(&shelf, "shelf", "", "to-read, currently-reading, or read")
	library.Flags().IntVar(&rating, "rating", 0, "show only books rated 1 through 5")
	library.Flags().IntVar(&limit, "limit", 20, "maximum books to return (1–200)")
	root.AddCommand(library)
	root.AddCommand(&cobra.Command{
		Use:   "get <isbn>",
		Short: "Find one exact ISBN in your live Goodreads library",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("%w: get requires one ISBN", errUsage)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			isbn, err := domain.NormalizeISBN(args[0])
			if err != nil {
				return fmt.Errorf("%w: %v", errUsage, err)
			}
			service, err := factory(headed)
			if err != nil {
				return err
			}
			ctx, cancel := newContext(cmd, time.Minute)
			defer cancel()
			book, err := service.Get(ctx, isbn)
			if err != nil {
				return err
			}
			return write(cmd, book, fmt.Sprintf("%s — %s (%s)", book.Title, book.Author, book.Status))
		},
	})
	root.AddCommand(&cobra.Command{
		Use:   "rate <isbn> <rating>",
		Short: "Set and verify a book rating",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 2 {
				return fmt.Errorf("%w: rate requires one ISBN and a rating from 1 through 5", errUsage)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			isbn, err := domain.NormalizeISBN(args[0])
			if err != nil {
				return fmt.Errorf("%w: %v", errUsage, err)
			}
			rating, err := strconv.Atoi(args[1])
			if err != nil || domain.ValidateRating(rating) != nil {
				return fmt.Errorf("%w: rating must be 1 through 5", errUsage)
			}
			service, err := factory(headed)
			if err != nil {
				return err
			}
			ctx, cancel := newContext(cmd, time.Minute)
			defer cancel()
			result, err := service.Rate(ctx, isbn, rating)
			if err != nil {
				return err
			}
			output := mutationOutput{
				OK:        true,
				Operation: result.Operation,
				ISBN13:    isbn.ISBN13,
				BookID:    result.After.BookID,
				Title:     result.After.Title,
				Changes:   result.Changes,
				Verified:  result.Verified,
			}
			return write(cmd, output, fmt.Sprintf("Updated %s — %d/5", result.After.Title, rating))
		},
	})
	return root
}

func exitCode(err error) int {
	switch {
	case errors.Is(err, errUsage):
		return 2
	case errors.Is(err, domain.ErrInvalidStatus), errors.Is(err, domain.ErrInvalidRating), errors.Is(err, domain.ErrInvalidLimit):
		return 2
	case errors.Is(err, profile.ErrBusy):
		return 8
	case errors.Is(err, browser.ErrUnavailable):
		return 9
	case errors.Is(err, goodreads.ErrCompatibility):
		return 7
	case errors.Is(err, goodreads.ErrBookNotFound), errors.Is(err, goodreads.ErrBookAmbiguous):
		return 4
	case errors.Is(err, goodreads.ErrMutationAmbiguous), errors.Is(err, goodreads.ErrVerificationFailed):
		return 5
	case errors.Is(err, goodreads.ErrSessionExpired), errors.Is(err, goodreads.ErrLoginCancelled), errors.Is(err, browser.ErrLaunch):
		return 3
	case errors.Is(err, context.DeadlineExceeded):
		return 6
	default:
		return 1
	}
}

func publicError(err error) string {
	switch {
	case errors.Is(err, errUsage):
		return err.Error()
	case errors.Is(err, profile.ErrBusy):
		return "Goodreads browser profile is busy; retry after the other command finishes."
	case errors.Is(err, browser.ErrUnavailable):
		return "No supported Chrome, Chromium, or Edge browser found. Set GOODREADS_CLI_BROWSER to its executable."
	case errors.Is(err, browser.ErrLaunch):
		return "Could not launch the dedicated browser."
	case errors.Is(err, goodreads.ErrCompatibility):
		return "Goodreads UI changed; retry with --headed for diagnosis."
	case errors.Is(err, goodreads.ErrPageLimit):
		return "Library scan reached its page limit before the result could be confirmed."
	case errors.Is(err, goodreads.ErrBookNotFound):
		return "No library entry has that exact ISBN."
	case errors.Is(err, goodreads.ErrBookAmbiguous):
		return "Multiple library entries have that ISBN; exact edition is ambiguous."
	case errors.Is(err, goodreads.ErrMutationAmbiguous):
		return "Goodreads may have changed the book, but the result could not be confirmed; do not retry automatically."
	case errors.Is(err, goodreads.ErrVerificationFailed):
		return "Goodreads did not match the requested change after readback."
	case errors.Is(err, goodreads.ErrSessionExpired):
		return "Goodreads session expired; run gr login."
	case errors.Is(err, goodreads.ErrLoginCancelled):
		return "Goodreads login was cancelled or timed out."
	case errors.Is(err, context.DeadlineExceeded):
		return "Goodreads operation timed out."
	default:
		return "Goodreads operation failed."
	}
}
