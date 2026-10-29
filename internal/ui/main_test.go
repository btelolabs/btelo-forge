package ui

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/btelolabs/btelo-forge/internal/speech/speechtest"
)

// TestMain runs the tests with the services on ports of the system's choosing, on this computer only: an app
// running beside the tests keeps its own ports, and an AI connected to it never reaches a test's editor.
func TestMain(m *testing.M) {
	if speechtest.Enabled() { // started as the speech engine by a voice-over test
		speechtest.Serve(os.Args[1:])
		return
	}
	mcpPort, phonePort, phoneHost = 0, 0, "127.0.0.1"
	os.Exit(m.Run())
}

// A test's editor serves MCP on a port of its own, which answers, and never on the port a running app takes.
func TestTestsKeepOffTheAppsPorts(t *testing.T) {
	x := newHarness(t)
	url := x.a.services.mcpURL
	if url == "" || strings.Contains(url, fmt.Sprintf(":%d/", 38471)) {
		t.Fatalf("the test's MCP server is at %q", url)
	}
	connectMCP(t, x) // it answers
	l, port, err := listen("127.0.0.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	if port == 0 {
		t.Fatal("listen on port 0 did not say which port it got")
	}
}
