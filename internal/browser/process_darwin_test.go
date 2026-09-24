//go:build darwin

package browser

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestParsePSProcessListMatchesExactProfile(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "chromium profile")
	raw := []byte(
		"  101 /Applications/Google Chrome.app/Contents/MacOS/Google Chrome --user-data-dir=" + profile + " --headless=new\n" +
			"  102 /Applications/Google Chrome.app/Contents/MacOS/Google Chrome --user-data-dir=" + profile + "-other\n" +
			"  103 /usr/bin/helper " + profile + "\n",
	)
	if got := parsePSProcessList(raw, profile); !reflect.DeepEqual(got, []int{101}) {
		t.Fatalf("pids=%v", got)
	}
}

func TestCommandLineTextUsesQuotedProfile(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "chromium profile")
	for _, command := range []string{
		`chrome --user-data-dir="` + profile + `" --headless`,
		`chrome --user-data-dir "` + profile + `" --headless`,
		`chrome --user-data-dir='` + profile + `' --headless`,
	} {
		if !commandLineTextUsesProfile(command, profile) {
			t.Fatalf("profile not matched in %q", command)
		}
	}
	if commandLineTextUsesProfile("chrome --user-data-dir="+profile+"-other", profile) {
		t.Fatal("suffix profile matched")
	}
}
