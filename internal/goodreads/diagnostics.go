package goodreads

import "strings"

var safeDiagnosticStages = []string{
	"auth.private-library",
	"auth.page",
	"library.page",
	"library.row",
	"book.resolve",
	"mutation.finish-date",
	"mutation.rating",
	"mutation.review",
	"mutation.status",
	"mutation.add",
	"mutation.verify",
	"export.generate",
	"export.download",
}

// SafeErrorStage returns only a fixed public stage identifier. It never
// returns arbitrary text from an underlying browser or Goodreads error.
func SafeErrorStage(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	for _, stage := range safeDiagnosticStages {
		if strings.Contains(message, stage) {
			return stage
		}
	}
	return ""
}
