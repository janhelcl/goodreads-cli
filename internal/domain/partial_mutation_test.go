package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestPartialMutationErrorIsTypedAndSafe(t *testing.T) {
	rating := 3
	date := "2026-09-18"
	err := &PartialMutationError{
		Operation: "finish",
		Completed: []string{"status", "date"},
		Failed:    "rating",
		Observed: ObservedMutationState{
			Status:   StatusRead,
			Rating:   &rating,
			DateRead: &date,
		},
	}
	if !errors.Is(err, ErrPartialMutation) ||
		!strings.Contains(err.Error(), "do not retry automatically") ||
		strings.Contains(err.Error(), "isbn") || strings.Contains(err.Error(), "title") {
		t.Fatalf("unsafe partial error: %v", err)
	}
}
