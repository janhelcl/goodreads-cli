package goodreads

import (
	"errors"
	"fmt"
	"testing"
)

func TestSafeErrorStageReturnsOnlyAllowlistedStage(t *testing.T) {
	err := fmt.Errorf("private ISBN and review at mutation.rating: %w", errors.New("selector #secret"))
	if stage := SafeErrorStage(err); stage != "mutation.rating" {
		t.Fatalf("stage=%q", stage)
	}
	if stage := SafeErrorStage(errors.New("private ISBN /review/list/123 #books")); stage != "" {
		t.Fatalf("arbitrary stage leaked: %q", stage)
	}
}
