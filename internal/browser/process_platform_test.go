//go:build linux || darwin || windows

package browser

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestStopProfileProcessesKillsMatchingCommand(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "chromium profile")
	other := profile + "-other"
	_ = startProfileProcess(t, "--user-data-dir="+profile)
	_ = startProfileProcess(t, "--user-data-dir="+other)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := StopProfileProcesses(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if pids, err := processesUsingProfile(ctx, profile); err != nil || len(pids) != 0 {
		t.Fatalf("matching process still using profile: pids=%v err=%v", pids, err)
	}
	if pids, err := processesUsingProfile(ctx, other); err != nil || len(pids) == 0 {
		t.Fatalf("suffix profile process was killed: pids=%v err=%v", pids, err)
	}
}

func startProfileProcess(t *testing.T, extra ...string) *exec.Cmd {
	t.Helper()
	args := append([]string{"-test.run=^TestProfileProcessHelper$", "--"}, extra...)
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), "GOODREADS_CLI_PROFILE_PROCESS_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd
}

func TestProfileProcessHelper(t *testing.T) {
	if os.Getenv("GOODREADS_CLI_PROFILE_PROCESS_HELPER") != "1" {
		return
	}
	for {
		time.Sleep(time.Second)
	}
}
