package auth

import (
	"strings"
	"testing"
)

// chromedp logs every CDP event it has no handler for at ERROR level
// (chromedp/target.go: `t.errf("unhandled node event %T", ev)`).
// DOM.topLayerElementsUpdated is one it does not model, and LinkedIn fires it
// on ordinary page loads -- so a SUCCESSFUL `jobs get` printed:
//
//	ERROR: unhandled node event *dom.EventTopLayerElementsUpdated
//
// twice, onto the same stderr that carries our own audit lines. A command that
// worked looked like one that failed, and a sweep's .li.log filled with it.
//
// Filter that one class and nothing else: a blanket silencer would also
// swallow genuine target and transport errors, which is the opposite of what
// this tool is for.
func TestChromedpErrfDropsUnhandledNodeEvents(t *testing.T) {
	var got []string
	restore := errLogger
	errLogger = func(format string, args ...any) { got = append(got, format) }
	defer func() { errLogger = restore }()

	chromedpErrf("unhandled node event %T", struct{}{})

	if len(got) != 0 {
		t.Fatalf("unhandled-node-event line was logged, want it dropped: %q", got)
	}
}

func TestChromedpErrfKeepsEverythingElse(t *testing.T) {
	var got []string
	restore := errLogger
	errLogger = func(format string, args ...any) { got = append(got, format) }
	defer func() { errLogger = restore }()

	chromedpErrf("could not unmarshal event: %v", "boom")
	chromedpErrf("websocket: close 1006")

	if len(got) != 2 {
		t.Fatalf("got %d logged lines, want 2: %q", len(got), got)
	}
	for _, line := range got {
		if !strings.HasPrefix(line, "ERROR: ") {
			t.Errorf("line lost its ERROR prefix: %q", line)
		}
	}
}
