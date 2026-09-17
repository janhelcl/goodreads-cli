package domain

import "errors"

var (
	ErrExportDestination = errors.New("invalid export destination")
	ErrExportExists      = errors.New("export destination already exists")
)

type ExportResult struct {
	OK    bool   `json:"ok"`
	Path  string `json:"path,omitempty"`
	Bytes int64  `json:"bytes"`
	Data  []byte `json:"-"`
}
