package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/janhelcl/goodreads-cli/internal/app"
	"github.com/janhelcl/goodreads-cli/internal/buildinfo"
	"github.com/janhelcl/goodreads-cli/internal/domain"
	internalmcp "github.com/janhelcl/goodreads-cli/internal/mcp"
	"github.com/janhelcl/goodreads-cli/internal/output"
)

var errUsage = errors.New("invalid command usage")

type serviceFactory func(headed bool) (app.Service, error)
type mcpRunner func(context.Context, app.Service, internalmcp.Options) error

func defaultServiceFactory(headed bool) (app.Service, error) {
	return app.NewService(app.Config{Headed: headed})
}

func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(runContext(ctx, os.Args[1:], os.Stdout, os.Stderr, defaultServiceFactory))
}

func run(args []string, out, errOut io.Writer, factory serviceFactory) int {
	return runContext(context.Background(), args, out, errOut, factory)
}

func runContext(ctx context.Context, args []string, out, errOut io.Writer, factory serviceFactory) int {
	return runContextWithMCP(ctx, args, out, errOut, factory, internalmcp.Run)
}

func runContextWithMCP(
	ctx context.Context,
	args []string,
	out, errOut io.Writer,
	factory serviceFactory,
	runMCP mcpRunner,
) int {
	root := newRootWithMCP(out, errOut, factory, runMCP)
	root.SetContext(ctx)
	root.SetArgs(args)
	started := time.Now()
	if err := root.Execute(); err != nil {
		if debug, _ := root.PersistentFlags().GetBool("debug"); debug {
			stage := app.SafeErrorStage(err)
			if stage == "" {
				stage = "unknown"
			}
			fmt.Fprintf(
				errOut,
				"debug: stage=%s elapsed_ms=%d error_kind=%s retry_count=0 exit_code=%d\n",
				stage,
				time.Since(started).Milliseconds(),
				app.DescribeError(err).Kind,
				exitCode(err),
			)
		}
		fmt.Fprintln(errOut, publicError(err))
		return exitCode(err)
	}
	return 0
}

func newRootWithMCP(out, errOut io.Writer, factory serviceFactory, runMCP mcpRunner) *cobra.Command {
	var jsonOutput bool
	var headed bool
	var debug bool
	var noColor bool
	var timeout time.Duration
	root := &cobra.Command{
		Use:           "gr",
		Short:         "Work with your Goodreads library",
		Version:       buildinfo.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate("gr {{.Version}}\n")
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
			cmd.SetContext(app.WithDiagnosticSink(cmd.Context(), func(event app.DiagnosticEvent) {
				browserProduct := event.Browser
				if browserProduct == "" {
					browserProduct = "unknown"
				}
				browserVersion := event.Version
				if browserVersion == "" {
					browserVersion = "unknown"
				}
				stage := event.Stage
				if stage == "" {
					stage = "unknown"
				}
				errorKind := event.ErrorKind
				if errorKind == "" {
					errorKind = "none"
				}
				fmt.Fprintf(
					errOut,
					"debug: operation=%s stage=%s elapsed_ms=%d browser_product=%s browser_version=%s error_kind=%s retry_count=%d\n",
					event.Operation,
					stage,
					event.Elapsed.Milliseconds(),
					browserProduct,
					browserVersion,
					errorKind,
					event.RetryCount,
				)
			}))
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
			ctx, cancel := newContext(cmd, 2*time.Minute)
			defer cancel()
			book, err := service.Get(ctx, isbn)
			if err != nil {
				return err
			}
			return write(cmd, book, fmt.Sprintf("%s — %s (%s)", book.Title, book.Author, book.Status))
		},
	})
	var addShelf string
	add := &cobra.Command{
		Use:   "add <isbn>",
		Short: "Add an exact edition and verify its shelf",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("%w: add requires one ISBN", errUsage)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			isbn, err := domain.NormalizeISBN(args[0])
			if err != nil {
				return fmt.Errorf("%w: %v", errUsage, err)
			}
			status := domain.ReadingStatus(addShelf)
			if !status.Valid() {
				return fmt.Errorf("%w: --shelf must be to-read, currently-reading, or read", errUsage)
			}
			service, err := factory(headed)
			if err != nil {
				return err
			}
			ctx, cancel := newContext(cmd, 2*time.Minute)
			defer cancel()
			result, err := service.Add(ctx, isbn, status)
			if err != nil {
				return err
			}
			return write(cmd, output.NewMutation(result, isbn), fmt.Sprintf("Added %s — %s", result.After.Title, status))
		},
	}
	add.Flags().StringVar(&addShelf, "shelf", string(domain.StatusToRead), "to-read, currently-reading, or read")
	root.AddCommand(add)
	root.AddCommand(&cobra.Command{
		Use:   "start <isbn>",
		Short: "Set and verify currently-reading status",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("%w: start requires one ISBN", errUsage)
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
			result, err := service.Start(ctx, isbn)
			if err != nil {
				return err
			}
			return write(cmd, output.NewMutation(result, isbn), fmt.Sprintf("Started %s — currently-reading", result.After.Title))
		},
	})
	var finishDate string
	var finishRating int
	finish := &cobra.Command{
		Use:   "finish <isbn>",
		Short: "Set and verify read status and finish date",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("%w: finish requires one ISBN", errUsage)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			isbn, err := domain.NormalizeISBN(args[0])
			if err != nil {
				return fmt.Errorf("%w: %v", errUsage, err)
			}
			date := time.Now().In(time.Local)
			if cmd.Flags().Changed("date") {
				date, err = time.ParseInLocation("2006-01-02", finishDate, time.Local)
				if err != nil || date.Format("2006-01-02") != finishDate {
					return fmt.Errorf("%w: --date must be YYYY-MM-DD", errUsage)
				}
			}
			var rating *int
			if cmd.Flags().Changed("rating") {
				if err := domain.ValidateRating(finishRating); err != nil {
					return fmt.Errorf("%w: --rating must be 1 through 5", errUsage)
				}
				rating = &finishRating
			}
			service, err := factory(headed)
			if err != nil {
				return err
			}
			ctx, cancel := newContext(cmd, 10*time.Minute)
			defer cancel()
			result, err := service.Finish(ctx, isbn, date, rating)
			if err != nil {
				return err
			}
			human := fmt.Sprintf("Updated %s — read, finished %s", result.After.Title, date.Format("2006-01-02"))
			if rating != nil {
				human = fmt.Sprintf("Updated %s — read, %d/5, finished %s", result.After.Title, *rating, date.Format("2006-01-02"))
			}
			return write(cmd, output.NewMutation(result, isbn), human)
		},
	}
	finish.Flags().StringVar(&finishDate, "date", "", "finish date in YYYY-MM-DD (default: today)")
	finish.Flags().IntVar(&finishRating, "rating", 0, "optional rating from 1 through 5")
	root.AddCommand(finish)
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
			return write(cmd, output.NewMutation(result, isbn), fmt.Sprintf("Updated %s — %d/5", result.After.Title, rating))
		},
	})
	var reviewText string
	var reviewFile string
	var clearReview bool
	review := &cobra.Command{
		Use:   "review <isbn>",
		Short: "Set, replace, or explicitly clear a book review",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("%w: review requires one ISBN", errUsage)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			isbn, err := domain.NormalizeISBN(args[0])
			if err != nil {
				return fmt.Errorf("%w: %v", errUsage, err)
			}
			choices := 0
			if cmd.Flags().Changed("text") {
				choices++
			}
			if cmd.Flags().Changed("file") {
				choices++
			}
			if clearReview {
				choices++
			}
			if choices != 1 {
				return fmt.Errorf("%w: exactly one of --text, --file, or --clear is required", errUsage)
			}
			desired := reviewText
			if reviewFile != "" {
				content, err := os.ReadFile(reviewFile)
				if err != nil {
					return fmt.Errorf("%w: could not read --file", errUsage)
				}
				desired = reviewFileContents(content)
			}
			if clearReview {
				desired = ""
			} else if strings.TrimSpace(desired) == "" {
				return fmt.Errorf("%w: an empty review requires --clear", errUsage)
			}
			service, err := factory(headed)
			if err != nil {
				return err
			}
			ctx, cancel := newContext(cmd, 2*time.Minute)
			defer cancel()
			result, err := service.Review(ctx, isbn, &desired)
			if err != nil {
				return err
			}
			human := fmt.Sprintf("Updated %s — review saved", result.After.Title)
			if clearReview {
				human = fmt.Sprintf("Updated %s — review cleared", result.After.Title)
			}
			return write(cmd, output.NewMutation(result, isbn), human)
		},
	}
	review.Flags().StringVar(&reviewText, "text", "", "review text")
	review.Flags().StringVar(&reviewFile, "file", "", "read review text from a file")
	review.Flags().BoolVar(&clearReview, "clear", false, "explicitly clear the review")
	root.AddCommand(review)
	var exportOut string
	var exportForce bool
	export := &cobra.Command{
		Use:   "export",
		Short: "Generate and download a fresh Goodreads CSV export",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if jsonOutput && exportOut == "" {
				return fmt.Errorf("%w: --json requires --out for export", errUsage)
			}
			if exportForce && exportOut == "" {
				return fmt.Errorf("%w: --force requires --out", errUsage)
			}
			service, err := factory(headed)
			if err != nil {
				return err
			}
			ctx, cancel := newContext(cmd, 10*time.Minute)
			defer cancel()
			result, err := service.Export(ctx, exportOut, exportForce)
			if err != nil {
				return err
			}
			if exportOut == "" {
				_, err = cmd.OutOrStdout().Write(result.Data)
				return err
			}
			return write(cmd, result, fmt.Sprintf("Exported Goodreads library to %s", result.Path))
		},
	}
	export.Flags().StringVar(&exportOut, "out", "", "write the CSV to this path")
	export.Flags().BoolVar(&exportForce, "force", false, "replace an existing regular file")
	root.AddCommand(export)
	root.AddCommand(&cobra.Command{
		Use:   "mcp",
		Short: "Run the local Goodreads MCP server over stdio",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service, err := factory(headed)
			if err != nil {
				return err
			}
			return runMCP(cmd.Context(), service, internalmcp.Options{Timeout: timeout})
		},
	})
	return root
}

func exitCode(err error) int {
	if errors.Is(err, errUsage) {
		return 2
	}
	switch app.DescribeError(err).Kind {
	case app.ErrorInvalidArguments:
		return 2
	case app.ErrorBusy:
		return 8
	case app.ErrorBrowserUnavailable:
		return 9
	case app.ErrorCompatibility:
		return 7
	case app.ErrorBookNotFound, app.ErrorBookAmbiguous:
		return 4
	case app.ErrorMutationAmbiguous, app.ErrorPartialMutation, app.ErrorVerificationFailed:
		return 5
	case app.ErrorNotAuthenticated, app.ErrorLoginCancelled, app.ErrorBrowserLaunch:
		return 3
	case app.ErrorTimeout, app.ErrorNetwork, app.ErrorExportFailed, app.ErrorScanIncomplete:
		return 6
	default:
		return 1
	}
}

func publicError(err error) string {
	if errors.Is(err, errUsage) {
		return err.Error()
	}
	return app.DescribeError(err).Message
}

// reviewFileContents treats a single trailing newline as a POSIX/Windows file
// terminator, not review text. Ordinary editor-saved files end that way, and
// Goodreads does not keep that extra newline on readback.
func reviewFileContents(content []byte) string {
	text := string(content)
	if strings.HasSuffix(text, "\r\n") {
		return strings.TrimSuffix(text, "\r\n")
	}
	return strings.TrimSuffix(text, "\n")
}
