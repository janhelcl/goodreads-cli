package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/janhelcl/goodreads-cli/internal/domain"
)

func TestPrepareExportDestinationRejectsExistingBeforeBrowserWork(t *testing.T) {
	target := filepath.Join(t.TempDir(), "library.csv")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareExportDestination(target, false); !errors.Is(err, domain.ErrExportExists) {
		t.Fatalf("existing destination err=%v", err)
	}
}

func TestWriteExportDestinationInstallsAndReplacesAtomically(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "library.csv")
	prepared, err := prepareExportDestination(target, false)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(prepared.staging)
	if err := writeExportDestination(prepared, []byte("fresh")); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "fresh" {
		t.Fatalf("content=%q err=%v", content, err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatalf("mode=%v err=%v", info.Mode(), err)
	}

	prepared, err = prepareExportDestination(target, true)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(prepared.staging)
	if err := writeExportDestination(prepared, []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	content, err = os.ReadFile(target)
	if err != nil || string(content) != "replacement" {
		t.Fatalf("replacement=%q err=%v", content, err)
	}
}

func TestWriteExportDestinationDoesNotOverwriteFileCreatedAfterValidation(t *testing.T) {
	target := filepath.Join(t.TempDir(), "library.csv")
	prepared, err := prepareExportDestination(target, false)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(prepared.staging)
	if err := os.WriteFile(target, []byte("racer"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeExportDestination(prepared, []byte("export")); !errors.Is(err, domain.ErrExportExists) {
		t.Fatalf("race err=%v", err)
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "racer" {
		t.Fatalf("race content=%q err=%v", content, err)
	}
}

func TestPrepareExportDestinationRejectsNonRegularTarget(t *testing.T) {
	if _, err := prepareExportDestination(t.TempDir(), true); !errors.Is(err, domain.ErrExportDestination) {
		t.Fatalf("directory destination err=%v", err)
	}
}
