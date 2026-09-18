package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/janhelcl/goodreads-cli/internal/app"
	"github.com/janhelcl/goodreads-cli/internal/buildinfo"
	"github.com/janhelcl/goodreads-cli/internal/domain"
	"github.com/janhelcl/goodreads-cli/internal/output"
)

const serverName = "goodreads-cli"

type Options struct {
	Timeout time.Duration
	Now     func() time.Time
}

type adapter struct {
	service app.Service
	timeout time.Duration
	now     func() time.Time
	serial  chan struct{}
}

type GetLibraryInput struct {
	Shelf  *string `json:"shelf,omitempty"`
	Rating *int    `json:"rating,omitempty"`
	Limit  *int    `json:"limit,omitempty"`
}

type ISBNInput struct {
	ISBN string `json:"isbn"`
}

type AddBookInput struct {
	ISBN   string  `json:"isbn"`
	Status *string `json:"status,omitempty"`
}

type FinishReadingInput struct {
	ISBN   string  `json:"isbn"`
	Date   *string `json:"date,omitempty"`
	Rating *int    `json:"rating,omitempty"`
}

type RateBookInput struct {
	ISBN   string `json:"isbn"`
	Rating int    `json:"rating"`
}

type ReviewBookInput struct {
	ISBN   string  `json:"isbn"`
	Review *string `json:"review,omitempty"`
	Clear  bool    `json:"clear,omitempty"`
}

type ToolError struct {
	Code               string                        `json:"code"`
	Message            string                        `json:"message"`
	Operation          string                        `json:"operation,omitempty"`
	Completed          []string                      `json:"completed,omitempty"`
	Failed             string                        `json:"failed,omitempty"`
	Observed           *domain.ObservedMutationState `json:"observed,omitempty"`
	RetryAutomatically *bool                         `json:"retry_automatically,omitempty"`
}

func (e ToolError) Error() string {
	data, _ := json.Marshal(e)
	return string(data)
}

func Run(ctx context.Context, service app.Service, options Options) error {
	return NewServer(service, options).Run(ctx, &sdk.StdioTransport{})
}

func NewServer(service app.Service, options Options) *sdk.Server {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	a := &adapter{
		service: service,
		timeout: options.Timeout,
		now:     now,
		serial:  make(chan struct{}, 1),
	}
	a.serial <- struct{}{}

	server := sdk.NewServer(&sdk.Implementation{Name: serverName, Version: buildinfo.Version}, nil)
	server.AddReceivingMiddleware(normalizeToolErrors)
	registerTools(server, a)
	return server
}

func registerTools(server *sdk.Server, a *adapter) {
	readOnly := true
	mutating := false
	destructive := true
	idempotent := true
	openWorld := true

	sdk.AddTool(server, &sdk.Tool{
		Name:        "get_library",
		Description: "Read the current user's live Goodreads library. Optional shelf values are to-read, currently-reading, or read; rating is 1 through 5; limit defaults to 20.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: readOnly, OpenWorldHint: &openWorld},
		InputSchema: objectSchema(map[string]any{
			"shelf":  stringEnumSchema("Only return this core Goodreads shelf.", "to-read", "currently-reading", "read"),
			"rating": integerSchema("Only return books with this user rating.", 1, 5),
			"limit":  integerSchema("Maximum books to return. Defaults to 20.", 1, 200),
		}),
	}, a.getLibrary)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "get_book",
		Description: "Read one live private-library entry by exact ISBN-10 or ISBN-13. This does not discover books by title.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: readOnly, OpenWorldHint: &openWorld},
		InputSchema: isbnSchema(),
	}, a.getBook)

	mutationAnnotations := &sdk.ToolAnnotations{
		ReadOnlyHint:    mutating,
		DestructiveHint: &destructive,
		IdempotentHint:  idempotent,
		OpenWorldHint:   &openWorld,
	}
	sdk.AddTool(server, &sdk.Tool{
		Name: "add_book",
		Description: "MUTATES Goodreads: add an exact ISBN edition and set its core status, defaulting to to-read. " +
			"Rating, review, dates, and custom shelves are preserved. Goodreads is read back before success; do not blindly retry an ambiguous result.",
		Annotations: mutationAnnotations,
		InputSchema: objectSchema(map[string]any{
			"isbn":   isbnProperty(),
			"status": stringEnumSchema("Desired core reading status. Defaults to to-read.", "to-read", "currently-reading", "read"),
		}, "isbn"),
	}, a.addBook)

	sdk.AddTool(server, &sdk.Tool{
		Name: "start_reading",
		Description: "MUTATES Goodreads: set an exact ISBN edition to currently-reading while preserving rating, review, dates, and custom shelves. " +
			"Goodreads is read back before success; do not blindly retry an ambiguous result.",
		Annotations: mutationAnnotations,
		InputSchema: isbnSchema(),
	}, a.startReading)

	sdk.AddTool(server, &sdk.Tool{
		Name: "finish_reading",
		Description: "MUTATES Goodreads: set an exact ISBN edition to read, set its finish date, and optionally set rating. " +
			"Date defaults to the server's local calendar date; clients should send YYYY-MM-DD explicitly. If rating is omitted, the existing rating is preserved. " +
			"Review and custom shelves are preserved. " +
			"Goodreads is read back before success; do not blindly retry an ambiguous result.",
		Annotations: mutationAnnotations,
		InputSchema: objectSchema(map[string]any{
			"isbn":   isbnProperty(),
			"date":   map[string]any{"type": "string", "format": "date", "description": "Finish date in YYYY-MM-DD; server-local today if omitted."},
			"rating": integerSchema("Optional user rating to set.", 1, 5),
		}, "isbn"),
	}, a.finishReading)

	sdk.AddTool(server, &sdk.Tool{
		Name: "rate_book",
		Description: "MUTATES Goodreads: set only the user rating for an exact ISBN edition. Other library fields are preserved. " +
			"Goodreads is read back before success; do not blindly retry an ambiguous result.",
		Annotations: mutationAnnotations,
		InputSchema: objectSchema(map[string]any{
			"isbn":   isbnProperty(),
			"rating": integerSchema("Desired user rating.", 1, 5),
		}, "isbn", "rating"),
	}, a.rateBook)

	sdk.AddTool(server, &sdk.Tool{
		Name: "review_book",
		Description: "MUTATES Goodreads: replace only the review for an exact ISBN edition, or explicitly clear it with clear=true. " +
			"Provide exactly one of review or clear=true. Other library fields are preserved. Goodreads is read back before success; " +
			"do not blindly retry an ambiguous result.",
		Annotations: mutationAnnotations,
		InputSchema: objectSchema(map[string]any{
			"isbn":   isbnProperty(),
			"review": map[string]any{"type": "string", "description": "Complete replacement review text. Empty text is not a clear request."},
			"clear":  map[string]any{"type": "boolean", "description": "Set true to explicitly clear the review."},
		}, "isbn"),
	}, a.reviewBook)
}

func (a *adapter) getLibrary(
	ctx context.Context,
	_ *sdk.CallToolRequest,
	input GetLibraryInput,
) (*sdk.CallToolResult, []domain.Book, error) {
	var filter domain.LibraryFilter
	if input.Shelf != nil {
		filter.Shelf = domain.ReadingStatus(*input.Shelf)
	}
	if input.Rating != nil {
		filter.Rating = *input.Rating
	}
	filter.Limit = 20
	if input.Limit != nil {
		filter.Limit = *input.Limit
	}
	if err := filter.Validate(); err != nil {
		return nil, nil, safeError(err)
	}
	return withServiceCall(ctx, a, time.Minute, func(ctx context.Context) ([]domain.Book, error) {
		books, err := a.service.Library(ctx, filter)
		if err == nil && books == nil {
			books = []domain.Book{}
		}
		return books, err
	})
}

func (a *adapter) getBook(
	ctx context.Context,
	_ *sdk.CallToolRequest,
	input ISBNInput,
) (*sdk.CallToolResult, domain.Book, error) {
	isbn, err := normalizeISBN(input.ISBN)
	if err != nil {
		return nil, domain.Book{}, err
	}
	return withServiceCall(ctx, a, time.Minute, func(ctx context.Context) (domain.Book, error) {
		return a.service.Get(ctx, isbn)
	})
}

func (a *adapter) addBook(
	ctx context.Context,
	_ *sdk.CallToolRequest,
	input AddBookInput,
) (*sdk.CallToolResult, output.Mutation, error) {
	isbn, err := normalizeISBN(input.ISBN)
	if err != nil {
		return nil, output.Mutation{}, err
	}
	status := domain.StatusToRead
	if input.Status != nil {
		status = domain.ReadingStatus(*input.Status)
	}
	if !status.Valid() {
		return nil, output.Mutation{}, safeError(domain.ErrInvalidStatus)
	}
	return a.mutate(ctx, 2*time.Minute, isbn, func(ctx context.Context) (domain.MutationResult, error) {
		return a.service.Add(ctx, isbn, status)
	})
}

func (a *adapter) startReading(
	ctx context.Context,
	_ *sdk.CallToolRequest,
	input ISBNInput,
) (*sdk.CallToolResult, output.Mutation, error) {
	isbn, err := normalizeISBN(input.ISBN)
	if err != nil {
		return nil, output.Mutation{}, err
	}
	return a.mutate(ctx, time.Minute, isbn, func(ctx context.Context) (domain.MutationResult, error) {
		return a.service.Start(ctx, isbn)
	})
}

func (a *adapter) finishReading(
	ctx context.Context,
	_ *sdk.CallToolRequest,
	input FinishReadingInput,
) (*sdk.CallToolResult, output.Mutation, error) {
	isbn, err := normalizeISBN(input.ISBN)
	if err != nil {
		return nil, output.Mutation{}, err
	}
	date := a.now().In(time.Local)
	if input.Date != nil {
		date, err = time.ParseInLocation("2006-01-02", *input.Date, time.Local)
		if err != nil || date.Format("2006-01-02") != *input.Date {
			return nil, output.Mutation{}, safeError(domain.ErrInvalidDate)
		}
	}
	if input.Rating != nil {
		if err := domain.ValidateRating(*input.Rating); err != nil {
			return nil, output.Mutation{}, safeError(err)
		}
	}
	return a.mutate(ctx, 10*time.Minute, isbn, func(ctx context.Context) (domain.MutationResult, error) {
		return a.service.Finish(ctx, isbn, date, input.Rating)
	})
}

func (a *adapter) rateBook(
	ctx context.Context,
	_ *sdk.CallToolRequest,
	input RateBookInput,
) (*sdk.CallToolResult, output.Mutation, error) {
	isbn, err := normalizeISBN(input.ISBN)
	if err != nil {
		return nil, output.Mutation{}, err
	}
	if err := domain.ValidateRating(input.Rating); err != nil {
		return nil, output.Mutation{}, safeError(err)
	}
	return a.mutate(ctx, time.Minute, isbn, func(ctx context.Context) (domain.MutationResult, error) {
		return a.service.Rate(ctx, isbn, input.Rating)
	})
}

func (a *adapter) reviewBook(
	ctx context.Context,
	_ *sdk.CallToolRequest,
	input ReviewBookInput,
) (*sdk.CallToolResult, output.Mutation, error) {
	isbn, err := normalizeISBN(input.ISBN)
	if err != nil {
		return nil, output.Mutation{}, err
	}
	if input.Review == nil && !input.Clear ||
		input.Review != nil && input.Clear ||
		input.Review != nil && strings.TrimSpace(*input.Review) == "" {
		return nil, output.Mutation{}, safeError(domain.ErrInvalidReview)
	}
	review := input.Review
	if input.Clear {
		empty := ""
		review = &empty
	}
	return a.mutate(ctx, 2*time.Minute, isbn, func(ctx context.Context) (domain.MutationResult, error) {
		return a.service.Review(ctx, isbn, review)
	})
}

func (a *adapter) mutate(
	ctx context.Context,
	defaultTimeout time.Duration,
	isbn domain.ISBN,
	call func(context.Context) (domain.MutationResult, error),
) (*sdk.CallToolResult, output.Mutation, error) {
	return withServiceCall(ctx, a, defaultTimeout, func(ctx context.Context) (output.Mutation, error) {
		result, err := call(ctx)
		if err != nil {
			return output.Mutation{}, err
		}
		if !result.Verified {
			return output.Mutation{}, app.ErrVerificationFailed
		}
		return output.NewMutation(result, isbn), nil
	})
}

func withServiceCall[T any](
	ctx context.Context,
	a *adapter,
	defaultTimeout time.Duration,
	call func(context.Context) (T, error),
) (*sdk.CallToolResult, T, error) {
	var zero T
	timeout := defaultTimeout
	if a.timeout > 0 {
		timeout = a.timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case <-ctx.Done():
		return nil, zero, safeError(ctx.Err())
	case <-a.serial:
	}
	defer func() { a.serial <- struct{}{} }()

	result, err := call(ctx)
	if err != nil {
		return nil, zero, safeError(err)
	}
	return nil, result, nil
}

func normalizeISBN(value string) (domain.ISBN, error) {
	isbn, err := domain.NormalizeISBN(value)
	if err != nil {
		return domain.ISBN{}, safeError(err)
	}
	return isbn, nil
}

func safeError(err error) error {
	description := app.DescribeError(err)
	result := ToolError{Code: string(description.Kind), Message: description.Message}
	var partial *domain.PartialMutationError
	if errors.As(err, &partial) {
		retry := false
		observed := partial.Observed
		result.Operation = partial.Operation
		result.Completed = append([]string(nil), partial.Completed...)
		result.Failed = partial.Failed
		result.Observed = &observed
		result.RetryAutomatically = &retry
	}
	return result
}

func normalizeToolErrors(next sdk.MethodHandler) sdk.MethodHandler {
	return func(ctx context.Context, method string, request sdk.Request) (sdk.Result, error) {
		result, err := next(ctx, method, request)
		if err != nil || method != "tools/call" {
			return result, err
		}
		call, ok := result.(*sdk.CallToolResult)
		if !ok || !call.IsError || len(call.Content) != 1 {
			return result, nil
		}
		text, ok := call.Content[0].(*sdk.TextContent)
		if !ok {
			return result, nil
		}
		var structured ToolError
		if json.Unmarshal([]byte(text.Text), &structured) == nil &&
			structured.Code != "" && structured.Message != "" {
			return result, nil
		}
		normalized := ToolError{
			Code:    "invalid_arguments",
			Message: "Tool arguments did not match the declared schema.",
		}
		call.Content = []sdk.Content{&sdk.TextContent{Text: normalized.Error()}}
		return call, nil
	}
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func isbnSchema() map[string]any {
	return objectSchema(map[string]any{"isbn": isbnProperty()}, "isbn")
}

func isbnProperty() map[string]any {
	return map[string]any{
		"type":        "string",
		"minLength":   1,
		"description": "Exact ISBN-10 or ISBN-13; common spaces and dashes are accepted.",
	}
}

func stringEnumSchema(description string, values ...string) map[string]any {
	enum := make([]any, len(values))
	for i, value := range values {
		enum[i] = value
	}
	return map[string]any{"type": "string", "description": description, "enum": enum}
}

func integerSchema(description string, minimum, maximum int) map[string]any {
	return map[string]any{
		"type":        "integer",
		"description": description,
		"minimum":     minimum,
		"maximum":     maximum,
	}
}
