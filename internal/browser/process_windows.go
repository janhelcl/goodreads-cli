//go:build windows

package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

func waitProcessExit(ctx context.Context, pid int) error {
	if pid <= 0 {
		return nil
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return nil
	}
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := windows.WaitForSingleObject(handle, 0)
		if err != nil {
			return err
		}
		if result == windows.WAIT_OBJECT_0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

type windowsProcess struct {
	ProcessID   int    `json:"ProcessId"`
	CommandLine string `json:"CommandLine"`
}

const listWindowsProcesses = `$ErrorActionPreference = 'Stop'; ` +
	`[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; ` +
	`ConvertTo-Json -Compress -InputObject @(Get-CimInstance Win32_Process | ` +
	`Select-Object ProcessId, CommandLine)`

func processesUsingProfile(ctx context.Context, profileDir string) ([]int, error) {
	out, err := exec.CommandContext(
		ctx,
		"powershell.exe",
		"-NoLogo",
		"-NoProfile",
		"-NonInteractive",
		"-Command",
		listWindowsProcesses,
	).Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("list browser processes: %w", err)
	}
	var processes []windowsProcess
	out = bytes.TrimPrefix(out, []byte{0xef, 0xbb, 0xbf})
	if err := json.Unmarshal(out, &processes); err != nil {
		return nil, fmt.Errorf("decode browser process list: %w", err)
	}
	var pids []int
	for _, process := range processes {
		if skipProcess(process.ProcessID) || process.CommandLine == "" {
			continue
		}
		args, err := windows.DecomposeCommandLine(process.CommandLine)
		if err != nil {
			continue
		}
		if commandLineUsesProfile(args, profileDir) {
			pids = append(pids, process.ProcessID)
		}
	}
	return pids, nil
}

func profilePathsEqual(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func killProcess(pid int) error {
	if skipProcess(pid) {
		return nil
	}
	handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return nil
	}
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	return windows.TerminateProcess(handle, 1)
}
