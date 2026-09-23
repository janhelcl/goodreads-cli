package browser

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestCommandLineUsesProfile(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "chromium-profile")
	other := profile + "-other"
	nested := filepath.Join(profile, "Default")
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"equals", []string{"chrome", "--user-data-dir=" + profile}, true},
		{"separate", []string{"chrome", "--user-data-dir", profile}, true},
		{"cleaned", []string{"chrome", "--user-data-dir=" + profile + "/"}, true},
		{"suffix", []string{"chrome", "--user-data-dir=" + other}, false},
		{"nested", []string{"chrome", "--user-data-dir=" + nested}, false},
		{"bare path", []string{"chrome", profile}, false},
		{"empty", nil, false},
	} {
		if got := commandLineUsesProfile(tc.args, profile); got != tc.want {
			t.Fatalf("%s got=%t want=%t", tc.name, got, tc.want)
		}
	}
	if commandLineUsesProfile([]string{"chrome", "--user-data-dir=" + profile}, "chromium-profile") {
		t.Fatal("accepted a relative profile path")
	}
}

func TestParseCommandLine(t *testing.T) {
	args := parseCommandLine([]byte("chrome\x00--user-data-dir=/tmp/chromium-profile\x00"))
	if len(args) != 2 || args[0] != "chrome" || args[1] != "--user-data-dir=/tmp/chromium-profile" {
		t.Fatalf("args=%q", args)
	}
	if parseCommandLine(nil) != nil {
		t.Fatal("empty cmdline")
	}
}

func TestStopProfileProcessesRequiresAbsolutePath(t *testing.T) {
	err := StopProfileProcesses(context.Background(), "chromium-profile")
	if err == nil {
		t.Fatal("accepted a relative profile path")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := StopProfileProcesses(ctx, filepath.Join(t.TempDir(), "chromium-profile")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled stop remapped: %v", err)
	}
}

func TestStopProfileProcessesEmptyIsNoop(t *testing.T) {
	if err := StopProfileProcesses(context.Background(), filepath.Join(t.TempDir(), "chromium-profile")); err != nil {
		t.Fatal(err)
	}
}
