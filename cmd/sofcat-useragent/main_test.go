package main

import (
	"flag"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// runMainEnv makes the test binary run the agent's main with the arguments
// that follow "--", so a test can see what the real command line does.
const runMainEnv = "SOFCAT_USERAGENT_TEST_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(runMainEnv) == "1" {
		for i, arg := range os.Args {
			if arg == "--" {
				os.Args = append([]string{"sofcat-useragent"}, os.Args[i+1:]...)
				break
			}
		}
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// The agent reaches pkg/config through pkg/service; sofcat's own flags must
// not come with it, or -h, -c and the rest mean something here.
func TestSofcatFlagsNotRegistered(t *testing.T) {
	for _, name := range []string{"h", "help", "c", "config", "service"} {
		if flag.CommandLine.Lookup(name) != nil {
			t.Errorf("flag -%s is registered in the user agent", name)
		}
	}
}

// -h must print the agent's usage and exit 0 without syncing.
func TestHelpPrintsUsage(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$", "--", "-h")
	cmd.Env = append(os.Environ(), runMainEnv+"=1", "SOFCAT_DEBUG=", "SOFCAT_UI_DEBUG=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("-h: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "-no-retry") {
		t.Fatalf("-h did not print the agent's usage:\n%s", out)
	}
}
