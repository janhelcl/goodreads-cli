package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/janhelcl/goodreads-cli/internal/browser"
	"github.com/janhelcl/goodreads-cli/internal/domain"
	"github.com/janhelcl/goodreads-cli/internal/goodreads"
)

type exportDestination struct {
	display string
	target  string
	staging string
	force   bool
}

func (s *service) Export(ctx context.Context, destination string, force bool) (result domain.ExportResult, err error) {
	prepared, err := prepareExportDestination(destination, force)
	if err != nil {
		return domain.ExportResult{}, err
	}
	if prepared.staging != "" {
		defer os.Remove(prepared.staging)
	}
	downloadDir, err := os.MkdirTemp("", "goodreads-cli-export-*")
	if err != nil {
		return domain.ExportResult{}, fmt.Errorf("%w: temporary download unavailable", goodreads.ErrExportFailed)
	}
	defer os.RemoveAll(downloadDir)
	if err := os.Chmod(downloadDir, 0700); err != nil {
		return domain.ExportResult{}, fmt.Errorf("%w: temporary download unavailable", goodreads.ErrExportFailed)
	}

	err = s.withBrowser(ctx, requireProfile, browser.LaunchOptions{
		Headless:    !s.headed,
		DownloadDir: downloadDir,
	}, func(b browser.Browser) error {
		var callErr error
		result, callErr = goodreads.DownloadExport(ctx, b)
		return callErr
	})
	if err != nil {
		return domain.ExportResult{}, err
	}
	if prepared.staging == "" {
		return result, nil
	}
	if err := writeExportDestination(prepared, result.Data); err != nil {
		return domain.ExportResult{}, err
	}
	result.Path = prepared.display
	result.Data = nil
	return result, nil
}

func prepareExportDestination(destination string, force bool) (exportDestination, error) {
	if destination == "" {
		return exportDestination{}, nil
	}
	display := filepath.Clean(destination)
	target, err := filepath.Abs(display)
	if err != nil {
		return exportDestination{}, fmt.Errorf("%w: path unavailable", domain.ErrExportDestination)
	}
	parent := filepath.Dir(target)
	parentInfo, err := os.Stat(parent)
	if err != nil || !parentInfo.IsDir() {
		return exportDestination{}, fmt.Errorf("%w: parent directory unavailable", domain.ErrExportDestination)
	}
	info, err := os.Lstat(target)
	switch {
	case err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0):
		return exportDestination{}, fmt.Errorf("%w: destination is not a regular file", domain.ErrExportDestination)
	case err == nil && !force:
		return exportDestination{}, domain.ErrExportExists
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return exportDestination{}, fmt.Errorf("%w: destination unavailable", domain.ErrExportDestination)
	}
	stagingFile, err := os.CreateTemp(parent, ".goodreads-cli-export-*")
	if err != nil {
		return exportDestination{}, fmt.Errorf("%w: destination is not writable", domain.ErrExportDestination)
	}
	staging := stagingFile.Name()
	if err := stagingFile.Chmod(0600); err != nil {
		_ = stagingFile.Close()
		_ = os.Remove(staging)
		return exportDestination{}, fmt.Errorf("%w: destination permissions unavailable", domain.ErrExportDestination)
	}
	if err := stagingFile.Close(); err != nil {
		_ = os.Remove(staging)
		return exportDestination{}, fmt.Errorf("%w: destination is not writable", domain.ErrExportDestination)
	}
	return exportDestination{display: display, target: target, staging: staging, force: force}, nil
}

func writeExportDestination(destination exportDestination, data []byte) error {
	file, err := os.OpenFile(destination.staging, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("%w: destination is not writable", domain.ErrExportDestination)
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("%w: destination write failed", domain.ErrExportDestination)
	}
	if destination.force {
		if info, statErr := os.Lstat(destination.target); statErr == nil &&
			(!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
			return fmt.Errorf("%w: destination changed type", domain.ErrExportDestination)
		} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("%w: destination unavailable", domain.ErrExportDestination)
		}
		if err := replaceFile(destination.staging, destination.target); err != nil {
			return fmt.Errorf("%w: destination replace failed", domain.ErrExportDestination)
		}
		return nil
	}
	if err := installFileNoReplace(destination.staging, destination.target); err != nil {
		if errors.Is(err, os.ErrExist) {
			return domain.ErrExportExists
		}
		return fmt.Errorf("%w: destination install failed", domain.ErrExportDestination)
	}
	return nil
}
