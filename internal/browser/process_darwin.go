//go:build darwin

package browser

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func processesUsingProfile(ctx context.Context, profileDir string) ([]int, error) {
	out, err := exec.CommandContext(ctx, "ps", "-ww", "-axo", "pid=,command=").Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("list browser processes: %w", err)
	}
	return parsePSProcessList(out, profileDir), nil
}

func parsePSProcessList(raw []byte, profileDir string) []int {
	var pids []int
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		fields := bytes.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(string(fields[0]))
		if err != nil || skipProcess(pid) {
			continue
		}
		pidAt := bytes.Index(line, fields[0])
		commandAt := pidAt + len(fields[0])
		command := strings.TrimSpace(string(line[commandAt:]))
		if commandLineTextUsesProfile(command, profileDir) {
			pids = append(pids, pid)
		}
	}
	return pids
}

func commandLineTextUsesProfile(commandLine, profileDir string) bool {
	if profileDir == "" || !filepath.IsAbs(profileDir) {
		return false
	}
	profileDir = filepath.Clean(profileDir)
	for _, candidate := range []string{
		"--user-data-dir=" + profileDir,
		"--user-data-dir=\"" + profileDir + "\"",
		"--user-data-dir='" + profileDir + "'",
		"--user-data-dir " + profileDir,
		"--user-data-dir \"" + profileDir + "\"",
		"--user-data-dir '" + profileDir + "'",
	} {
		for offset := 0; offset < len(commandLine); {
			index := strings.Index(commandLine[offset:], candidate)
			if index < 0 {
				break
			}
			index += offset
			end := index + len(candidate)
			beforeOK := index == 0 || commandLineSpace(commandLine[index-1])
			afterOK := end == len(commandLine) || commandLineSpace(commandLine[end])
			if beforeOK && afterOK {
				return true
			}
			offset = index + 1
		}
	}
	return false
}

func commandLineSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n'
}

func profilePathsEqual(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}
