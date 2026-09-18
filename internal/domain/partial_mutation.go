package domain

import (
	"errors"
	"strconv"
	"strings"
)

var ErrPartialMutation = errors.New("Goodreads mutation partially completed")

type ObservedMutationState struct {
	Status   ReadingStatus `json:"status,omitempty"`
	Rating   *int          `json:"rating,omitempty"`
	DateRead *string       `json:"date_read,omitempty"`
}

type PartialMutationError struct {
	Operation          string                `json:"operation"`
	Completed          []string              `json:"completed"`
	Failed             string                `json:"failed"`
	Observed           ObservedMutationState `json:"observed"`
	RetryAutomatically bool                  `json:"retry_automatically"`
}

func (e *PartialMutationError) Error() string {
	completed := strings.Join(e.Completed, ", ")
	if completed == "" {
		completed = "none"
	}
	observed := make([]string, 0, 3)
	if e.Observed.Status.Valid() {
		observed = append(observed, "status="+string(e.Observed.Status))
	}
	if e.Observed.Rating != nil {
		observed = append(observed, "rating="+strconv.Itoa(*e.Observed.Rating))
	}
	if e.Observed.DateRead != nil {
		observed = append(observed, "date_read="+*e.Observed.DateRead)
	}
	observedText := strings.Join(observed, ", ")
	if observedText == "" {
		observedText = "unavailable"
	}
	return "Goodreads " + e.Operation + " partially completed; completed: " +
		completed + "; failed: " + e.Failed + "; observed: " + observedText +
		"; do not retry automatically"
}

func (e *PartialMutationError) Unwrap() error {
	return ErrPartialMutation
}
