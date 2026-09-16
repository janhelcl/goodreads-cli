//go:build !windows

package browser

import (
	"context"
	"errors"
	"syscall"
	"time"
)

func waitProcessExit(ctx context.Context, pid int) error {
	if pid <= 0 {
		return nil
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
