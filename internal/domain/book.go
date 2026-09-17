package domain

import (
	"errors"
	"time"
)

type ReadingStatus string

const (
	StatusToRead           ReadingStatus = "to-read"
	StatusCurrentlyReading ReadingStatus = "currently-reading"
	StatusRead             ReadingStatus = "read"
)

var (
	ErrInvalidStatus = errors.New("invalid reading status")
	ErrInvalidRating = errors.New("invalid rating")
	ErrInvalidDate   = errors.New("invalid date")
	ErrInvalidLimit  = errors.New("invalid library limit")
)

func (s ReadingStatus) Valid() bool {
	return s == StatusToRead || s == StatusCurrentlyReading || s == StatusRead
}

type Book struct {
	BookID        string        `json:"book_id"`
	Title         string        `json:"title"`
	Author        string        `json:"author"`
	ISBN10        string        `json:"isbn10"`
	ISBN13        string        `json:"isbn13"`
	Rating        int           `json:"rating"`
	AverageRating *float64      `json:"average_rating,omitempty"`
	DateRead      *string       `json:"date_read"`
	DateAdded     *string       `json:"date_added"`
	Status        ReadingStatus `json:"status"`
	Bookshelves   []string      `json:"bookshelves,omitempty"`
	// Review is absent on shelf reads because the table can truncate it.
	Review *string `json:"review,omitempty"`
}

type BookUpdate struct {
	Status   *ReadingStatus `json:"status,omitempty"`
	Rating   *int           `json:"rating,omitempty"`
	DateRead *string        `json:"date_read,omitempty"`
	Review   *string        `json:"review,omitempty"`
}

type MutationResult struct {
	Operation string     `json:"operation"`
	Before    Book       `json:"before"`
	After     Book       `json:"after"`
	Changes   BookUpdate `json:"changes"`
	Verified  bool       `json:"verified"`
}

type LibraryFilter struct {
	Shelf  ReadingStatus
	Rating int
	Limit  int
}

func (f LibraryFilter) Validate() error {
	if f.Shelf != "" && !f.Shelf.Valid() {
		return ErrInvalidStatus
	}
	if f.Rating < 0 || f.Rating > 5 {
		return ErrInvalidRating
	}
	if f.Limit < 1 || f.Limit > 200 {
		return ErrInvalidLimit
	}
	return nil
}

func ValidateRating(rating int) error {
	if rating < 1 || rating > 5 {
		return ErrInvalidRating
	}
	return nil
}

func ValidateDate(date time.Time) error {
	if date.IsZero() {
		return ErrInvalidDate
	}
	return nil
}
