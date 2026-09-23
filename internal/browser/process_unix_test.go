//go:build !windows

package browser

import (
	"context"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestStopProfileProcessesKillsMatchingCommand(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "chromium-profile")
	other := profile + "-other"
	_ = startArgProcess(t, "--user-data-dir="+profile)
	_ = startArgProcess(t, "--user-data-dir="+other)
	neighbor := startArgProcess(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := StopProfileProcesses(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if pids, err := processesUsingProfile(profile); err != nil || len(pids) != 0 {
		t.Fatalf("matching process still using profile: pids=%v err=%v", pids, err)
	}
	if pids, err := processesUsingProfile(other); err != nil || len(pids) == 0 {
		t.Fatalf("suffix profile process was killed: pids=%v err=%v", pids, err)
	}
	if !processAlive(neighbor.Process.Pid) {
		t.Fatal("unrelated process was killed")
	}
}

func startArgProcess(t *testing.T, extra ...string) *exec.Cmd {
	t.Helper()
	args := append([]string{"-c", "while :; do sleep 1; done", "hold"}, extra...)
	cmd := exec.Command("bash", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_, _ = cmd.Process.Wait()
	})
	return cmd
}

func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
