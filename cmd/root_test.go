package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/janhelcl/goodreads-cli/internal/app"
	"github.com/janhelcl/goodreads-cli/internal/domain"
	"github.com/janhelcl/goodreads-cli/internal/goodreads"
	"github.com/janhelcl/goodreads-cli/internal/profile"
)

type authStub struct {
	status goodreads.ConnectionStatus
	books  []domain.Book
	book   domain.Book
	result domain.MutationResult
	err    error
}

func (a authStub) Login(context.Context) (goodreads.ConnectionStatus, error) {
	return a.status, a.err
}
func (a authStub) Status(context.Context) (goodreads.ConnectionStatus, error) {
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
func (a authStub) Rate(context.Context, domain.ISBN, int) (domain.MutationResult, error) {
	return a.result, a.err
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
			return authStub{status: goodreads.ConnectionStatus{Connected: true, SessionValid: true}}, nil
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

func TestBusyErrorIsSafeAndTyped(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"status", "--json"}, &out, &errOut, func(bool) (app.Service, error) {
		return authStub{err: profile.ErrBusy}, nil
	})
	if code != 8 || out.Len() != 0 || !strings.Contains(errOut.String(), "profile is busy") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
}

func TestMutationErrorsAreSafeAndTyped(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{goodreads.ErrMutationAmbiguous, "do not retry automatically"},
		{&goodreads.VerificationError{Field: "review", Reason: "changed"}, "did not match"},
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
	code = run([]string{"get", "9780306406157", "--json"}, &out, &errOut, func(bool) (app.Service, error) {
		return authStub{err: goodreads.ErrBookNotFound}, nil
	})
	if code != 4 || out.Len() != 0 || !strings.Contains(errOut.String(), "exact ISBN") {
		t.Fatalf("not found code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
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

func TestDebugIsRedactedAndNoColorAccepted(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"status", "--json", "--debug", "--no-color"}, &out, &errOut, func(bool) (app.Service, error) {
		return authStub{err: goodreads.ErrCompatibility}, nil
	})
	if code != 7 || out.Len() != 0 || !strings.Contains(errOut.String(), "debug: operation=status") ||
		!strings.Contains(errOut.String(), "debug: exit_code=7") || strings.Contains(errOut.String(), "review/list") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
}
