package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/janhelcl/goodreads-cli/internal/app"
	"github.com/janhelcl/goodreads-cli/internal/domain"
	internalmcp "github.com/janhelcl/goodreads-cli/internal/mcp"
)

type authStub struct {
	status      app.ConnectionStatus
	books       []domain.Book
	book        domain.Book
	result      domain.MutationResult
	export      domain.ExportResult
	exportOut   *string
	exportForce *bool
	review      *string
	err         error
	wait        bool
}

func (a authStub) Login(context.Context) (app.ConnectionStatus, error) {
	return a.status, a.err
}
func (a authStub) Status(ctx context.Context) (app.ConnectionStatus, error) {
	if a.wait {
		<-ctx.Done()
		return app.ConnectionStatus{}, ctx.Err()
	}
	return a.status, a.err
}
func (a authStub) Logout(context.Context) (app.LogoutResult, error) {
	return app.LogoutResult{ProfileRemoved: true}, a.err
}
func (a authStub) Library(context.Context, domain.LibraryFilter) ([]domain.Book, error) {
	return a.books, a.err
}
func (a authStub) Get(context.Context, domain.ISBN) (domain.Book, error) {
	return a.book, a.err
}

type getISBNStub struct {
	authStub
	got *domain.ISBN
}

func (s getISBNStub) Get(_ context.Context, isbn domain.ISBN) (domain.Book, error) {
	if s.got != nil {
		*s.got = isbn
	}
	return s.authStub.Get(context.Background(), isbn)
}
func (a authStub) Add(context.Context, domain.ISBN, domain.ReadingStatus) (domain.MutationResult, error) {
	return a.result, a.err
}
func (a authStub) Rate(context.Context, domain.ISBN, int) (domain.MutationResult, error) {
	return a.result, a.err
}
func (a authStub) Start(context.Context, domain.ISBN) (domain.MutationResult, error) {
	return a.result, a.err
}
func (a authStub) Finish(context.Context, domain.ISBN, time.Time, *int) (domain.MutationResult, error) {
	return a.result, a.err
}
func (a authStub) Review(_ context.Context, _ domain.ISBN, review *string) (domain.MutationResult, error) {
	if a.review != nil && review != nil {
		*a.review = *review
	}
	return a.result, a.err
}
func (a authStub) Export(_ context.Context, destination string, force bool) (domain.ExportResult, error) {
	if a.exportOut != nil {
		*a.exportOut = destination
	}
	if a.exportForce != nil {
		*a.exportForce = force
	}
	return a.export, a.err
}

func TestVersionDoesNotStartService(t *testing.T) {
	var out, errOut bytes.Buffer
	called := false
	code := run([]string{"--version"}, &out, &errOut, func(bool) (app.Service, error) {
		called = true
		return authStub{}, nil
	})
	if code != 0 || called || out.String() != "gr dev\n" || errOut.Len() != 0 {
		t.Fatalf("code=%d called=%t stdout=%q stderr=%q", code, called, out.String(), errOut.String())
	}
}

func TestAuthJSONContracts(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"status", "--json"}, "{\"connected\":true,\"session_valid\":true}\n"},
		{[]string{"login", "--json"}, "{\"connected\":true,\"session_valid\":true}\n"},
		{[]string{"logout", "--json"}, "{\"connected\":false,\"profile_removed\":true}\n"},
	} {
		var out, errOut bytes.Buffer
		code := run(tc.args, &out, &errOut, func(bool) (app.Service, error) {
			return authStub{status: app.ConnectionStatus{Connected: true, SessionValid: true}}, nil
		})
		if code != 0 || out.String() != tc.want {
			t.Fatalf("%v: code=%d stdout=%q stderr=%q", tc.args, code, out.String(), errOut.String())
		}
	}
}

func TestUsageRejectedBeforeService(t *testing.T) {
	for _, args := range [][]string{{"status", "extra"}, {"status", "--timeout=-1s"}, {"status", "--unknown"}} {
		var out, errOut bytes.Buffer
		called := false
		code := run(args, &out, &errOut, func(bool) (app.Service, error) {
			called = true
			return authStub{}, nil
		})
		if code != 2 || called || out.Len() != 0 {
			t.Fatalf("%v: code=%d factory=%v stdout=%q stderr=%q", args, code, called, out.String(), errOut.String())
		}
	}
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	for _, args := range [][]string{{"foobar"}, {"--debug", "foobar"}, {"--json", "foobar"}} {
		var out, errOut bytes.Buffer
		called := false
		code := run(args, &out, &errOut, func(bool) (app.Service, error) {
			called = true
			return authStub{}, nil
		})
		message := errOut.String()
		if code != 2 || called || out.Len() != 0 {
			t.Fatalf("%v: code=%d called=%t stdout=%q stderr=%q", args, code, called, out.String(), message)
		}
		if !strings.Contains(message, `unknown command "foobar"`) {
			t.Fatalf("%v: missing usage text: %q", args, message)
		}
		if strings.Contains(message, "Goodreads operation failed.") {
			t.Fatalf("%v: treated unknown command as internal: %q", args, message)
		}
		if args[0] == "--debug" &&
			(!strings.Contains(message, "error_kind=invalid_arguments") ||
				!strings.Contains(message, "exit_code=2")) {
			t.Fatalf("%v: debug classification: %q", args, message)
		}
	}
}

func TestRunContextPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errOut bytes.Buffer
	code := runContext(ctx, []string{"status"}, &out, &errOut, func(bool) (app.Service, error) {
		return authStub{wait: true}, nil
	})
	if code != 1 || out.Len() != 0 || !strings.Contains(errOut.String(), "operation was cancelled") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
}

func TestTinyTimeoutIsTimeoutNotUnavailable(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"status", "--timeout", "1ms", "--json", "--debug"}, &out, &errOut, func(bool) (app.Service, error) {
		return authStub{wait: true}, nil
	})
	message := errOut.String()
	if code != 6 || out.Len() != 0 || !strings.Contains(message, "timed out") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), message)
	}
	if strings.Contains(message, "No supported Chrome") {
		t.Fatalf("timeout classified as unavailable: %q", message)
	}
	if !strings.Contains(message, "error_kind=timeout") || !strings.Contains(message, "exit_code=6") {
		t.Fatalf("debug classification: %q", message)
	}
}

func TestBrowserUnavailableKeepsExitNine(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"status", "--json"}, &out, &errOut, func(bool) (app.Service, error) {
		return authStub{err: app.ErrBrowserUnavailable}, nil
	})
	if code != 9 || out.Len() != 0 || !strings.Contains(errOut.String(), "No supported Chrome") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
}

func TestBusyErrorIsSafeAndTyped(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"status", "--json"}, &out, &errOut, func(bool) (app.Service, error) {
		return authStub{err: app.ErrBusy}, nil
	})
	if code != 8 || out.Len() != 0 || !strings.Contains(errOut.String(), "profile is busy") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
}

func TestIncompleteScansUseRemoteFailureExitCode(t *testing.T) {
	if code := exitCode(app.ErrPageLimit); code != 6 {
		t.Fatalf("page limit exit code=%d", code)
	}
	if message := publicError(app.ErrPageLimit); !strings.Contains(message, "safety budget") {
		t.Fatalf("page limit message=%q", message)
	}
	if code := exitCode(app.ErrScanIncomplete); code != 6 {
		t.Fatalf("scan incomplete exit code=%d", code)
	}
	if message := publicError(app.ErrScanIncomplete); !strings.Contains(message, "no mutation") {
		t.Fatalf("scan incomplete message=%q", message)
	}
	if code := exitCode(app.ErrNetwork); code != 6 {
		t.Fatalf("network exit code=%d", code)
	}
}

func TestMutationErrorsAreSafeAndTyped(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{app.ErrMutationAmbiguous, "do not retry automatically"},
		{fmt.Errorf("%w: review changed", app.ErrVerificationFailed), "did not match"},
	} {
		var out, errOut bytes.Buffer
		code := run([]string{"status", "--json"}, &out, &errOut, func(bool) (app.Service, error) {
			return authStub{err: tc.err}, nil
		})
		if code != 5 || out.Len() != 0 || !strings.Contains(errOut.String(), tc.want) ||
			strings.Contains(errOut.String(), "review") {
			t.Fatalf("err=%v code=%d stdout=%q stderr=%q", tc.err, code, out.String(), errOut.String())
		}
	}
}

func TestPartialMutationErrorUsesExitFiveAndSafeDetails(t *testing.T) {
	rating := 3
	date := "2026-09-18"
	partial := &domain.PartialMutationError{
		Operation: "finish",
		Completed: []string{"status", "date"},
		Failed:    "rating",
		Observed: domain.ObservedMutationState{
			Status:   domain.StatusRead,
			Rating:   &rating,
			DateRead: &date,
		},
	}
	var out, errOut bytes.Buffer
	code := run([]string{"status"}, &out, &errOut, func(bool) (app.Service, error) {
		return authStub{err: partial}, nil
	})
	message := errOut.String()
	if code != 5 || out.Len() != 0 ||
		!strings.Contains(message, "completed: status, date") ||
		!strings.Contains(message, "failed: rating") ||
		!strings.Contains(message, "do not retry automatically") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), message)
	}
	for _, forbidden := range []string{"9780306406157", "private review", "/review/", "#books"} {
		if strings.Contains(message, forbidden) {
			t.Fatalf("partial error leaked %q: %q", forbidden, message)
		}
	}
}

func TestLibraryJSONAndValidation(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"library", "--shelf", "read", "--limit", "2", "--json"}, &out, &errOut,
		func(bool) (app.Service, error) { return authStub{books: []domain.Book{}}, nil })
	if code != 0 || out.String() != "[]\n" || errOut.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	for _, args := range [][]string{{"library", "--shelf", "unknown"}, {"library", "--rating", "0"}, {"library", "--limit", "201"}} {
		called := false
		out.Reset()
		errOut.Reset()
		code = run(args, &out, &errOut, func(bool) (app.Service, error) {
			called = true
			return authStub{}, nil
		})
		if code != 2 || called || out.Len() != 0 {
			t.Fatalf("%v: code=%d called=%t stdout=%q stderr=%q", args, code, called, out.String(), errOut.String())
		}
	}
}

func TestGetExactISBNAndErrors(t *testing.T) {
	var out, errOut bytes.Buffer
	called := false
	code := run([]string{"get", "bad-isbn", "--json"}, &out, &errOut, func(bool) (app.Service, error) {
		called = true
		return authStub{}, nil
	})
	if code != 2 || called || out.Len() != 0 {
		t.Fatalf("invalid ISBN code=%d called=%t stdout=%q", code, called, out.String())
	}
	out.Reset()
	errOut.Reset()
	code = run([]string{"get", "9780306406157", "--json"}, &out, &errOut, func(bool) (app.Service, error) {
		return authStub{book: domain.Book{BookID: "42", Title: "Invented Book", ISBN13: "9780306406157"}}, nil
	})
	if code != 0 || !strings.Contains(out.String(), `"book_id":"42"`) || errOut.Len() != 0 {
		t.Fatalf("get code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	var gotISBN domain.ISBN
	code = run([]string{"get", "978 0 306 40615 7", "--json"}, &out, &errOut, func(bool) (app.Service, error) {
		return getISBNStub{
			authStub: authStub{book: domain.Book{BookID: "42", Title: "Invented Book", ISBN13: "9780306406157"}},
			got:      &gotISBN,
		}, nil
	})
	if code != 0 || gotISBN.ISBN13 != "9780306406157" || gotISBN.ISBN10 != "0306406152" {
		t.Fatalf("spaced ISBN code=%d isbn=%+v stdout=%q", code, gotISBN, out.String())
	}
	out.Reset()
	errOut.Reset()
	code = run([]string{"get", "9780306406157", "--json"}, &out, &errOut, func(bool) (app.Service, error) {
		return authStub{err: app.ErrBookNotFound}, nil
	})
	if code != 4 || out.Len() != 0 || !strings.Contains(errOut.String(), "exact ISBN") {
		t.Fatalf("not found code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
}

func TestAddJSONAndValidation(t *testing.T) {
	for _, args := range [][]string{
		{"add", "bad-isbn"},
		{"add"},
		{"add", "9781603580557", "--shelf", "paused"},
		{"add", "9781603580557", "extra"},
	} {
		var out, errOut bytes.Buffer
		called := false
		code := run(args, &out, &errOut, func(bool) (app.Service, error) {
			called = true
			return authStub{}, nil
		})
		if code != 2 || called || out.Len() != 0 {
			t.Fatalf("%v: code=%d called=%t stdout=%q", args, code, called, out.String())
		}
	}

	status := domain.StatusCurrentlyReading
	result := domain.MutationResult{
		Operation: "add",
		After: domain.Book{
			BookID: "42", Title: "Invented Book", ISBN13: "9781603580557", Status: status,
		},
		Changes:  domain.BookUpdate{Status: &status},
		Verified: true,
	}
	var out, errOut bytes.Buffer
	code := run([]string{"add", "9781603580557", "--shelf", "currently-reading", "--json"},
		&out, &errOut, func(bool) (app.Service, error) {
			return authStub{result: result}, nil
		})
	want := `{"ok":true,"operation":"add","isbn13":"9781603580557","book_id":"42","title":"Invented Book","changes":{"status":"currently-reading"},"verified":true}` + "\n"
	if code != 0 || out.String() != want || errOut.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
}

func TestRateJSONAndValidation(t *testing.T) {
	for _, args := range [][]string{
		{"rate", "bad-isbn", "4"},
		{"rate", "9780142437247", "0"},
		{"rate", "9780142437247", "six"},
		{"rate", "9780142437247"},
	} {
		var out, errOut bytes.Buffer
		called := false
		code := run(args, &out, &errOut, func(bool) (app.Service, error) {
			called = true
			return authStub{}, nil
		})
		if code != 2 || called || out.Len() != 0 {
			t.Fatalf("%v: code=%d called=%t stdout=%q", args, code, called, out.String())
		}
	}

	rating := 4
	result := domain.MutationResult{
		Operation: "rate",
		After:     domain.Book{BookID: "42", Title: "Invented Book"},
		Changes:   domain.BookUpdate{Rating: &rating},
		Verified:  true,
	}
	var out, errOut bytes.Buffer
	code := run([]string{"rate", "9780142437247", "4", "--json"}, &out, &errOut, func(bool) (app.Service, error) {
		return authStub{result: result}, nil
	})
	want := `{"ok":true,"operation":"rate","isbn13":"9780142437247","book_id":"42","title":"Invented Book","changes":{"rating":4},"verified":true}` + "\n"
	if code != 0 || out.String() != want || errOut.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
}

func TestStartJSONAndValidation(t *testing.T) {
	for _, args := range [][]string{
		{"start", "bad-isbn"},
		{"start"},
		{"start", "9780142437247", "extra"},
	} {
		var out, errOut bytes.Buffer
		called := false
		code := run(args, &out, &errOut, func(bool) (app.Service, error) {
			called = true
			return authStub{}, nil
		})
		if code != 2 || called || out.Len() != 0 {
			t.Fatalf("%v: code=%d called=%t stdout=%q", args, code, called, out.String())
		}
	}

	status := domain.StatusCurrentlyReading
	result := domain.MutationResult{
		Operation: "start",
		After:     domain.Book{BookID: "42", Title: "Invented Book", Status: status},
		Changes:   domain.BookUpdate{Status: &status},
		Verified:  true,
	}
	var out, errOut bytes.Buffer
	code := run([]string{"start", "9780142437247", "--json"}, &out, &errOut, func(bool) (app.Service, error) {
		return authStub{result: result}, nil
	})
	want := `{"ok":true,"operation":"start","isbn13":"9780142437247","book_id":"42","title":"Invented Book","changes":{"status":"currently-reading"},"verified":true}` + "\n"
	if code != 0 || out.String() != want || errOut.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
}

func TestFinishJSONAndValidation(t *testing.T) {
	for _, args := range [][]string{
		{"finish", "bad-isbn"},
		{"finish"},
		{"finish", "9780142437247", "--date", "2026-9-17"},
		{"finish", "9780142437247", "--date", "not-a-date"},
		{"finish", "9780142437247", "--rating", "0"},
		{"finish", "9780142437247", "--rating", "6"},
	} {
		var out, errOut bytes.Buffer
		called := false
		code := run(args, &out, &errOut, func(bool) (app.Service, error) {
			called = true
			return authStub{}, nil
		})
		if code != 2 || called || out.Len() != 0 {
			t.Fatalf("%v: code=%d called=%t stdout=%q", args, code, called, out.String())
		}
	}

	status := domain.StatusRead
	date := "2026-09-17"
	rating := 4
	result := domain.MutationResult{
		Operation: "finish",
		After:     domain.Book{BookID: "42", Title: "Invented Book", Status: status, DateRead: &date, Rating: rating},
		Changes:   domain.BookUpdate{Status: &status, DateRead: &date, Rating: &rating},
		Verified:  true,
	}
	var out, errOut bytes.Buffer
	code := run([]string{"finish", "9780142437247", "--date", date, "--rating", "4", "--json"}, &out, &errOut, func(bool) (app.Service, error) {
		return authStub{result: result}, nil
	})
	want := `{"ok":true,"operation":"finish","isbn13":"9780142437247","book_id":"42","title":"Invented Book","changes":{"status":"read","rating":4,"date_read":"2026-09-17"},"verified":true}` + "\n"
	if code != 0 || out.String() != want || errOut.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
}

func TestReviewJSONFileClearAndValidation(t *testing.T) {
	for _, args := range [][]string{
		{"review", "bad-isbn", "--text", "good"},
		{"review", "9780142437247"},
		{"review", "9780142437247", "--text", "one", "--clear"},
		{"review", "9780142437247", "--text", ""},
		{"review", "9780142437247", "--file", "missing-review.txt"},
	} {
		var out, errOut bytes.Buffer
		called := false
		code := run(args, &out, &errOut, func(bool) (app.Service, error) {
			called = true
			return authStub{}, nil
		})
		if code != 2 || called || out.Len() != 0 {
			t.Fatalf("%v: code=%d called=%t stdout=%q stderr=%q", args, code, called, out.String(), errOut.String())
		}
	}

	wanted := "A precise review."
	result := domain.MutationResult{
		Operation: "review",
		After:     domain.Book{BookID: "42", Title: "Invented Book"},
		Changes:   domain.BookUpdate{Review: &wanted},
		Verified:  true,
	}
	var out, errOut bytes.Buffer
	code := run([]string{"review", "9780142437247", "--text", wanted, "--json"}, &out, &errOut, func(bool) (app.Service, error) {
		return authStub{result: result}, nil
	})
	want := `{"ok":true,"operation":"review","isbn13":"9780142437247","book_id":"42","title":"Invented Book","changes":{"review":"A precise review."},"verified":true}` + "\n"
	if code != 0 || out.String() != want || errOut.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}

	path := t.TempDir() + "/review.md"
	if err := os.WriteFile(path, []byte(wanted), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	code = run([]string{"review", "9780142437247", "--file", path}, &out, &errOut, func(bool) (app.Service, error) {
		return authStub{result: result}, nil
	})
	if code != 0 || !strings.Contains(out.String(), "review saved") || errOut.Len() != 0 {
		t.Fatalf("file review code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}

	empty := ""
	result.Changes.Review = &empty
	out.Reset()
	errOut.Reset()
	code = run([]string{"review", "9780142437247", "--clear", "--json"}, &out, &errOut, func(bool) (app.Service, error) {
		return authStub{result: result}, nil
	})
	if code != 0 || !strings.Contains(out.String(), `"review":""`) || errOut.Len() != 0 {
		t.Fatalf("clear review code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
}

func TestReviewFileContentsStripsOneTerminator(t *testing.T) {
	wanted := "A precise review."
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"unix", wanted + "\n", wanted},
		{"windows", wanted + "\r\n", wanted},
		{"no terminator", wanted, wanted},
		{"keeps extra blank line", wanted + "\n\n", wanted + "\n"},
		{"multiline unix", "line one\nline two\n", "line one\nline two"},
		{"empty", "", ""},
		{"only newline", "\n", ""},
	} {
		if got := reviewFileContents([]byte(tc.in)); got != tc.want {
			t.Fatalf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestReviewFileStripsTerminatingNewlineBeforeService(t *testing.T) {
	wanted := "A precise review."
	result := domain.MutationResult{
		Operation: "review",
		After:     domain.Book{BookID: "42", Title: "Invented Book"},
		Changes:   domain.BookUpdate{Review: &wanted},
		Verified:  true,
	}
	for _, tc := range []struct {
		name    string
		content string
		want    string
		code    int
	}{
		{"unix", wanted + "\n", wanted, 0},
		{"windows", wanted + "\r\n", wanted, 0},
		{"extra blank line", wanted + "\n\n", wanted + "\n", 0},
		{"only newline", "\n", "", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := t.TempDir() + "/review.md"
			if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			var got string
			var out, errOut bytes.Buffer
			called := false
			code := run([]string{"review", "9780142437247", "--file", path}, &out, &errOut, func(bool) (app.Service, error) {
				called = true
				return authStub{result: result, review: &got}, nil
			})
			if code != tc.code {
				t.Fatalf("code=%d want=%d stdout=%q stderr=%q", code, tc.code, out.String(), errOut.String())
			}
			if tc.code == 2 {
				if called {
					t.Fatal("empty terminator-only file started the service")
				}
				return
			}
			if !called || got != tc.want || errOut.Len() != 0 {
				t.Fatalf("called=%t got=%q want=%q stderr=%q", called, got, tc.want, errOut.String())
			}
		})
	}

	var got string
	var out, errOut bytes.Buffer
	code := run([]string{"review", "9780142437247", "--text", wanted + "\n"}, &out, &errOut, func(bool) (app.Service, error) {
		return authStub{result: result, review: &got}, nil
	})
	if code != 0 || got != wanted+"\n" || errOut.Len() != 0 {
		t.Fatalf("text keeps newline code=%d got=%q stderr=%q", code, got, errOut.String())
	}
}

func TestExportOutputJSONAndValidation(t *testing.T) {
	for _, args := range [][]string{
		{"export", "--json"},
		{"export", "--force"},
		{"export", "extra"},
	} {
		var out, errOut bytes.Buffer
		called := false
		code := run(args, &out, &errOut, func(bool) (app.Service, error) {
			called = true
			return authStub{}, nil
		})
		if code != 2 || called || out.Len() != 0 {
			t.Fatalf("%v: code=%d called=%t stdout=%q stderr=%q", args, code, called, out.String(), errOut.String())
		}
	}

	csv := []byte("Book Id,Title\n1,Fixture\n")
	var out, errOut bytes.Buffer
	code := run([]string{"export"}, &out, &errOut, func(bool) (app.Service, error) {
		return authStub{export: domain.ExportResult{OK: true, Bytes: int64(len(csv)), Data: csv}}, nil
	})
	if code != 0 || out.String() != string(csv) || errOut.Len() != 0 {
		t.Fatalf("stdout export code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}

	out.Reset()
	errOut.Reset()
	var destination string
	var force bool
	code = run([]string{"export", "--out", "library.csv", "--force", "--json"}, &out, &errOut,
		func(bool) (app.Service, error) {
			return authStub{
				export:    domain.ExportResult{OK: true, Path: "library.csv", Bytes: 123},
				exportOut: &destination, exportForce: &force,
			}, nil
		})
	want := "{\"ok\":true,\"path\":\"library.csv\",\"bytes\":123}\n"
	if code != 0 || out.String() != want || errOut.Len() != 0 ||
		destination != "library.csv" || !force {
		t.Fatalf("JSON export code=%d stdout=%q stderr=%q destination=%q force=%t",
			code, out.String(), errOut.String(), destination, force)
	}

	out.Reset()
	errOut.Reset()
	code = run([]string{"export", "--out", "library.csv"}, &out, &errOut, func(bool) (app.Service, error) {
		return authStub{err: domain.ErrExportExists}, nil
	})
	if code != 2 || out.Len() != 0 || !strings.Contains(errOut.String(), "use --force") {
		t.Fatalf("existing export code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
}

func TestMCPCommandWiresServiceAndGlobalOptions(t *testing.T) {
	var out, errOut bytes.Buffer
	var gotService app.Service
	var gotOptions internalmcp.Options
	headed := false
	code := runContextWithMCP(
		context.Background(),
		[]string{"mcp", "--headed", "--timeout", "3s"},
		&out,
		&errOut,
		func(value bool) (app.Service, error) {
			headed = value
			return authStub{}, nil
		},
		func(_ context.Context, service app.Service, options internalmcp.Options) error {
			gotService = service
			gotOptions = options
			return nil
		},
	)
	if code != 0 || out.Len() != 0 || errOut.Len() != 0 || !headed ||
		gotService == nil || gotOptions.Timeout != 3*time.Second {
		t.Fatalf("code=%d stdout=%q stderr=%q headed=%t service=%T timeout=%s",
			code, out.String(), errOut.String(), headed, gotService, gotOptions.Timeout)
	}

	called := false
	code = runContextWithMCP(
		context.Background(),
		[]string{"mcp", "extra"},
		&out,
		&errOut,
		func(bool) (app.Service, error) {
			called = true
			return authStub{}, nil
		},
		func(context.Context, app.Service, internalmcp.Options) error {
			called = true
			return nil
		},
	)
	if code != 2 || called {
		t.Fatalf("invalid mcp usage code=%d called=%t", code, called)
	}
}

func TestDebugIsRedactedAndNoColorAccepted(t *testing.T) {
	forbidden := []string{
		"9780306406157",
		"private title",
		"private author",
		"private review",
		"#books",
		"https://www.goodreads.com/review/list/123",
		"<html>",
		"session_cookie=secret",
		"localStorage-secret",
		"password-field-value",
		"Book Id,Title",
		"/home/private/profile",
	}
	var out, errOut bytes.Buffer
	code := run([]string{"status", "--json", "--debug", "--no-color"}, &out, &errOut, func(bool) (app.Service, error) {
		return authStub{err: fmt.Errorf("%w at mutation.rating: %s", app.ErrCompatibility, strings.Join(forbidden, " "))}, nil
	})
	diagnostics := errOut.String()
	if code != 7 || out.Len() != 0 ||
		!strings.Contains(diagnostics, "debug: operation=status") ||
		!strings.Contains(diagnostics, "stage=mutation.rating") ||
		!strings.Contains(diagnostics, "elapsed_ms=") ||
		!strings.Contains(diagnostics, "error_kind=compatibility") ||
		!strings.Contains(diagnostics, "retry_count=0") ||
		!strings.Contains(diagnostics, "exit_code=7") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	for _, value := range forbidden {
		if strings.Contains(diagnostics, value) {
			t.Fatalf("debug output leaked %q: %q", value, diagnostics)
		}
	}
}
