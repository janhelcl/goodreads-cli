package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/janhelcl/goodreads-cli/internal/app"
	"github.com/janhelcl/goodreads-cli/internal/domain"
)

type serviceStub struct {
	mu sync.Mutex

	err      error
	books    []domain.Book
	book     domain.Book
	filter   domain.LibraryFilter
	isbn     domain.ISBN
	status   domain.ReadingStatus
	date     time.Time
	rating   int
	ratingAt *int
	review   *string
	calls    []string

	blockLibrary bool
	entered      chan struct{}
	release      chan struct{}
	active       int
	maxActive    int
}

func (s *serviceStub) Login(context.Context) (app.ConnectionStatus, error) {
	return app.ConnectionStatus{}, s.err
}

func (s *serviceStub) Status(context.Context) (app.ConnectionStatus, error) {
	return app.ConnectionStatus{}, s.err
}

func (s *serviceStub) Logout(context.Context) (app.LogoutResult, error) {
	return app.LogoutResult{}, s.err
}

func (s *serviceStub) Library(ctx context.Context, filter domain.LibraryFilter) ([]domain.Book, error) {
	s.mu.Lock()
	s.calls = append(s.calls, "library")
	s.filter = filter
	if s.blockLibrary {
		s.active++
		if s.active > s.maxActive {
			s.maxActive = s.active
		}
	}
	s.mu.Unlock()
	if s.blockLibrary {
		s.entered <- struct{}{}
		select {
		case <-ctx.Done():
		case <-s.release:
		}
		s.mu.Lock()
		s.active--
		s.mu.Unlock()
	}
	return s.books, s.err
}

func (s *serviceStub) Get(_ context.Context, isbn domain.ISBN) (domain.Book, error) {
	s.record("get", isbn)
	return s.book, s.err
}

func (s *serviceStub) Add(
	_ context.Context,
	isbn domain.ISBN,
	status domain.ReadingStatus,
) (domain.MutationResult, error) {
	s.record("add", isbn)
	s.mu.Lock()
	s.status = status
	s.mu.Unlock()
	return mutationResult("add"), s.err
}

func (s *serviceStub) Rate(_ context.Context, isbn domain.ISBN, rating int) (domain.MutationResult, error) {
	s.record("rate", isbn)
	s.mu.Lock()
	s.rating = rating
	s.mu.Unlock()
	result := mutationResult("rate")
	result.Changes.Rating = &rating
	return result, s.err
}

func (s *serviceStub) Start(_ context.Context, isbn domain.ISBN) (domain.MutationResult, error) {
	s.record("start", isbn)
	return mutationResult("start"), s.err
}

func (s *serviceStub) Finish(
	_ context.Context,
	isbn domain.ISBN,
	date time.Time,
	rating *int,
) (domain.MutationResult, error) {
	s.record("finish", isbn)
	s.mu.Lock()
	s.date = date
	s.ratingAt = rating
	s.mu.Unlock()
	result := mutationResult("finish")
	value := date.Format("2006-01-02")
	result.Changes.DateRead = &value
	result.Changes.Rating = rating
	return result, s.err
}

func (s *serviceStub) Review(
	_ context.Context,
	isbn domain.ISBN,
	review *string,
) (domain.MutationResult, error) {
	s.record("review", isbn)
	s.mu.Lock()
	s.review = review
	s.mu.Unlock()
	result := mutationResult("review")
	result.Changes.Review = review
	return result, s.err
}

func (s *serviceStub) Export(context.Context, string, bool) (domain.ExportResult, error) {
	return domain.ExportResult{}, s.err
}

func (s *serviceStub) record(operation string, isbn domain.ISBN) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, operation)
	s.isbn = isbn
}

func mutationResult(operation string) domain.MutationResult {
	return domain.MutationResult{
		Operation: operation,
		After: domain.Book{
			BookID: "42",
			Title:  "Invented Book",
		},
		Verified: true,
	}
}

func connect(t *testing.T, server *sdk.Server) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "1"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = clientSession.Close()
		_ = serverSession.Wait()
	})
	return clientSession
}

func callTool(t *testing.T, session *sdk.ClientSession, name string, arguments map[string]any) *sdk.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestToolSurfaceAndSchemas(t *testing.T) {
	session := connect(t, NewServer(&serviceStub{}, Options{}))
	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
		if tool.Name == "login" || tool.Name == "export" {
			t.Fatalf("out-of-scope tool exposed: %s", tool.Name)
		}
		if strings.Contains(tool.Name, "book") || strings.Contains(tool.Name, "reading") {
			if tool.Name != "get_book" &&
				(!strings.Contains(tool.Description, "MUTATES Goodreads") ||
					!strings.Contains(tool.Description, "read back before success")) {
				t.Fatalf("%s mutation safety description=%q", tool.Name, tool.Description)
			}
		}
	}
	sort.Strings(names)
	want := []string{
		"add_book", "finish_reading", "get_book", "get_library",
		"rate_book", "review_book", "start_reading",
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("tools=%v want=%v", names, want)
	}
}

func TestReadToolsUseNormalizedInputsAndStructuredResults(t *testing.T) {
	book := domain.Book{BookID: "42", Title: "Invented Book", ISBN13: "9780306406157"}
	service := &serviceStub{books: []domain.Book{book}, book: book}
	session := connect(t, NewServer(service, Options{}))

	result := callTool(t, session, "get_library", map[string]any{"shelf": "read", "rating": 5})
	if result.IsError {
		t.Fatalf("get_library error: %v", result.Content)
	}
	service.mu.Lock()
	filter := service.filter
	service.mu.Unlock()
	if filter.Shelf != domain.StatusRead || filter.Rating != 5 || filter.Limit != 20 {
		t.Fatalf("filter=%+v", filter)
	}
	books, ok := result.StructuredContent.([]any)
	if !ok || len(books) != 1 {
		t.Fatalf("structured library=%T %#v", result.StructuredContent, result.StructuredContent)
	}

	result = callTool(t, session, "get_book", map[string]any{"isbn": "0-306-40615-2"})
	if result.IsError {
		t.Fatalf("get_book error: %v", result.Content)
	}
	service.mu.Lock()
	isbn := service.isbn
	service.mu.Unlock()
	if isbn.ISBN13 != "9780306406157" {
		t.Fatalf("normalized ISBN=%+v", isbn)
	}
	output, ok := result.StructuredContent.(map[string]any)
	if !ok || output["book_id"] != "42" {
		t.Fatalf("structured book=%T %#v", result.StructuredContent, result.StructuredContent)
	}
}

func TestMutationToolsMapArgumentsAndReturnVerifiedShape(t *testing.T) {
	now := time.Date(2026, 9, 17, 23, 30, 0, 0, time.FixedZone("test", 2*60*60))
	service := &serviceStub{}
	session := connect(t, NewServer(service, Options{Now: func() time.Time { return now }}))
	isbn := "9780306406157"

	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"add_book", map[string]any{"isbn": isbn}},
		{"start_reading", map[string]any{"isbn": isbn}},
		{"finish_reading", map[string]any{"isbn": isbn, "rating": 4}},
		{"rate_book", map[string]any{"isbn": isbn, "rating": 5}},
		{"review_book", map[string]any{"isbn": isbn, "clear": true}},
	} {
		result := callTool(t, session, tc.name, tc.args)
		if result.IsError {
			t.Fatalf("%s error: %v", tc.name, result.Content)
		}
		output, ok := result.StructuredContent.(map[string]any)
		if !ok || output["ok"] != true || output["verified"] != true ||
			output["isbn13"] != isbn || output["book_id"] != "42" {
			t.Fatalf("%s structured=%T %#v", tc.name, result.StructuredContent, result.StructuredContent)
		}
	}

	service.mu.Lock()
	defer service.mu.Unlock()
	if service.status != domain.StatusToRead {
		t.Fatalf("default add status=%q", service.status)
	}
	if service.date.Format("2006-01-02") != "2026-09-17" ||
		service.ratingAt == nil || *service.ratingAt != 4 {
		t.Fatalf("finish date=%v rating=%v", service.date, service.ratingAt)
	}
	if service.rating != 5 {
		t.Fatalf("rate=%d", service.rating)
	}
	if service.review == nil || *service.review != "" {
		t.Fatalf("clear review=%v", service.review)
	}
}

func TestToolErrorsAreTypedActionableAndRedacted(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"busy", app.ErrBusy, "busy"},
		{"auth", app.ErrSessionExpired, "not_authenticated"},
		{"network", app.ErrNetwork, "network"},
		{"scan incomplete", app.ErrScanIncomplete, "scan_incomplete"},
		{"verification", errors.Join(app.ErrVerificationFailed, errors.New("review contained private text")), "verification_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &serviceStub{err: tc.err}
			session := connect(t, NewServer(service, Options{}))
			result := callTool(t, session, "get_book", map[string]any{"isbn": "9780306406157"})
			if !result.IsError || len(result.Content) != 1 {
				t.Fatalf("result=%+v", result)
			}
			text, ok := result.Content[0].(*sdk.TextContent)
			if !ok {
				t.Fatalf("error content=%T", result.Content[0])
			}
			var safe ToolError
			if err := json.Unmarshal([]byte(text.Text), &safe); err != nil {
				t.Fatalf("error is not structured: %q: %v", text.Text, err)
			}
			if safe.Code != tc.code || strings.Contains(text.Text, "private text") ||
				strings.Contains(text.Text, `"review"`) {
				t.Fatalf("safe error=%+v raw=%q", safe, text.Text)
			}
			if tc.code == "not_authenticated" && !strings.Contains(safe.Message, "gr login") {
				t.Fatalf("auth error is not actionable: %+v", safe)
			}
		})
	}
}

func TestPartialMutationToolErrorIncludesOnlySafeState(t *testing.T) {
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
	service := &serviceStub{err: fmt.Errorf(
		"private review ISBN title author selector URL HTML cookie storage form export profile: %w",
		partial,
	)}
	session := connect(t, NewServer(service, Options{}))
	result := callTool(t, session, "finish_reading", map[string]any{
		"isbn": "9780306406157", "date": date, "rating": 4,
	})
	if !result.IsError || len(result.Content) != 1 {
		t.Fatalf("result=%+v", result)
	}
	text := result.Content[0].(*sdk.TextContent).Text
	var safe ToolError
	if err := json.Unmarshal([]byte(text), &safe); err != nil ||
		safe.Code != "partial_mutation" ||
		safe.Operation != "finish" ||
		!slices.Equal(safe.Completed, []string{"status", "date"}) ||
		safe.Failed != "rating" ||
		safe.Observed == nil ||
		safe.Observed.Status != domain.StatusRead ||
		safe.RetryAutomatically == nil || *safe.RetryAutomatically {
		t.Fatalf("safe=%+v raw=%q err=%v", safe, text, err)
	}
	for _, forbidden := range []string{
		"private review", "ISBN", "title", "author", "selector", "URL", "HTML",
		"cookie", "storage", "form", "export", "profile", `"isbn"`, `"book_id"`, `"title"`, `"url"`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("partial error leaked %q: %q", forbidden, text)
		}
	}
}

func TestSemanticValidationHappensBeforeServiceCalls(t *testing.T) {
	for _, tc := range []struct {
		name string
		tool string
		args map[string]any
	}{
		{"missing isbn", "get_book", map[string]any{}},
		{"wrong isbn type", "get_book", map[string]any{"isbn": 42}},
		{"isbn", "get_book", map[string]any{"isbn": "bad"}},
		{"status", "add_book", map[string]any{"isbn": "9780306406157", "status": "paused"}},
		{"rating", "rate_book", map[string]any{"isbn": "9780306406157", "rating": 0}},
		{"review missing", "review_book", map[string]any{"isbn": "9780306406157"}},
		{"review ambiguous", "review_book", map[string]any{"isbn": "9780306406157", "review": "text", "clear": true}},
		{"review empty", "review_book", map[string]any{"isbn": "9780306406157", "review": "  "}},
		{"date", "finish_reading", map[string]any{"isbn": "9780306406157", "date": "2026-9-17"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &serviceStub{}
			session := connect(t, NewServer(service, Options{}))
			result := callTool(t, session, tc.tool, tc.args)
			if !result.IsError {
				t.Fatalf("expected tool error: %+v", result)
			}
			text, ok := result.Content[0].(*sdk.TextContent)
			if !ok {
				t.Fatalf("error content=%T", result.Content[0])
			}
			var safe ToolError
			if err := json.Unmarshal([]byte(text.Text), &safe); err != nil ||
				safe.Code != "invalid_arguments" {
				t.Fatalf("validation error=%q parsed=%+v err=%v", text.Text, safe, err)
			}
			service.mu.Lock()
			calls := len(service.calls)
			service.mu.Unlock()
			if calls != 0 {
				t.Fatalf("service called %d times", calls)
			}
		})
	}
}

func TestUnverifiedMutationCannotReportSuccess(t *testing.T) {
	service := &serviceStub{}
	session := connect(t, NewServer(service, Options{}))
	service.err = errors.New("fixture")
	result := callTool(t, session, "rate_book", map[string]any{"isbn": "9780306406157", "rating": 4})
	if !result.IsError {
		t.Fatalf("unexpected success: %+v", result)
	}

	service.err = nil
	// A local fake can bypass the application service's verification guard, so
	// exercise the MCP adapter's independent success invariant directly.
	a := &adapter{service: service, now: time.Now, serial: make(chan struct{}, 1)}
	a.serial <- struct{}{}
	_, _, err := a.mutate(context.Background(), time.Second, domain.ISBN{ISBN13: "9780306406157"},
		func(context.Context) (domain.MutationResult, error) {
			return domain.MutationResult{Verified: false}, nil
		})
	var safe ToolError
	if err == nil || json.Unmarshal([]byte(err.Error()), &safe) != nil || safe.Code != "verification_failed" {
		t.Fatalf("unverified error=%v safe=%+v", err, safe)
	}
}

func TestToolCallsAreSerialized(t *testing.T) {
	service := &serviceStub{
		blockLibrary: true,
		entered:      make(chan struct{}, 2),
		release:      make(chan struct{}, 2),
	}
	session := connect(t, NewServer(service, Options{Timeout: time.Second}))
	call := func(done chan<- *sdk.CallToolResult) {
		result, err := session.CallTool(context.Background(), &sdk.CallToolParams{
			Name: "get_library", Arguments: map[string]any{},
		})
		if err != nil {
			done <- &sdk.CallToolResult{IsError: true}
			return
		}
		done <- result
	}
	done := make(chan *sdk.CallToolResult, 2)
	go call(done)
	<-service.entered
	go call(done)

	select {
	case <-service.entered:
		t.Fatal("second tool entered the application service concurrently")
	case <-time.After(50 * time.Millisecond):
	}
	service.release <- struct{}{}
	select {
	case <-service.entered:
	case <-time.After(time.Second):
		t.Fatal("second tool did not enter after first completed")
	}
	service.release <- struct{}{}
	for range 2 {
		if result := <-done; result.IsError {
			t.Fatalf("serialized call failed: %+v", result)
		}
	}
	service.mu.Lock()
	maxActive := service.maxActive
	service.mu.Unlock()
	if maxActive != 1 {
		t.Fatalf("maximum concurrent calls=%d", maxActive)
	}
}
