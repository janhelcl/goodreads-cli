package output

import "github.com/janhelcl/goodreads-cli/internal/domain"

type Mutation struct {
	OK        bool              `json:"ok"`
	Operation string            `json:"operation"`
	ISBN13    string            `json:"isbn13"`
	BookID    string            `json:"book_id"`
	Title     string            `json:"title"`
	Changes   domain.BookUpdate `json:"changes"`
	Verified  bool              `json:"verified"`
}

func NewMutation(result domain.MutationResult, isbn domain.ISBN) Mutation {
	return Mutation{
		OK:        true,
		Operation: result.Operation,
		ISBN13:    isbn.ISBN13,
		BookID:    result.After.BookID,
		Title:     result.After.Title,
		Changes:   result.Changes,
		Verified:  result.Verified,
	}
}
