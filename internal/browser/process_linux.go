//go:build linux

package browser

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
)

func processesUsingProfile(ctx context.Context, profileDir string) ([]int, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || skipProcess(pid) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		if commandLineUsesProfile(parseCommandLine(raw), profileDir) {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

func profilePathsEqual(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}
