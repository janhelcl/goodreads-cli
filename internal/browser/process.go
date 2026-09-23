package browser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StopProfileProcesses signals only processes whose command line uses the
// exact dedicated --user-data-dir. It never targets a user's ordinary
// browser profile. Errors omit the profile path.
func StopProfileProcesses(ctx context.Context, profileDir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if profileDir == "" || !filepath.IsAbs(profileDir) {
		return fmt.Errorf("browser profile path must be absolute")
	}
	profileDir = filepath.Clean(profileDir)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		pids, err := processesUsingProfile(profileDir)
		if err != nil {
			return err
		}
		if len(pids) == 0 {
			return nil
		}
		for _, pid := range pids {
			_ = killProcess(pid)
		}
		select {
		case <-ctx.Done():
			remaining, err := processesUsingProfile(profileDir)
			if err != nil {
				return err
			}
			if len(remaining) == 0 {
				return nil
			}
			return fmt.Errorf("leftover browser still using the dedicated profile")
		case <-ticker.C:
		}
	}
}

func stopProfileProcessesNow(profileDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return StopProfileProcesses(ctx, profileDir)
}

func commandLineUsesProfile(args []string, profileDir string) bool {
	if profileDir == "" || !filepath.IsAbs(profileDir) {
		return false
	}
	profileDir = filepath.Clean(profileDir)
	for i, arg := range args {
		if arg == "--user-data-dir" {
			if i+1 < len(args) && filepath.Clean(args[i+1]) == profileDir {
				return true
			}
			continue
		}
		path, ok := strings.CutPrefix(arg, "--user-data-dir=")
		if ok && filepath.Clean(path) == profileDir {
			return true
		}
	}
	return false
}

func parseCommandLine(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	return strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
}

func skipProcess(pid int) bool {
	return pid <= 0 || pid == os.Getpid()
}
