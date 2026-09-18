package app

import (
	"context"
	"time"

	"github.com/janhelcl/goodreads-cli/internal/goodreads"
)

type DiagnosticEvent struct {
	Operation  string
	Stage      string
	Elapsed    time.Duration
	Browser    string
	Version    string
	ErrorKind  ErrorKind
	RetryCount int
}

type DiagnosticSink func(DiagnosticEvent)

type diagnosticSinkKey struct{}

func WithDiagnosticSink(ctx context.Context, sink DiagnosticSink) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, diagnosticSinkKey{}, sink)
}

func emitDiagnostic(ctx context.Context, event DiagnosticEvent) {
	sink, _ := ctx.Value(diagnosticSinkKey{}).(DiagnosticSink)
	if sink != nil {
		sink(event)
	}
}

func SafeErrorStage(err error) string {
	return goodreads.SafeErrorStage(err)
}
