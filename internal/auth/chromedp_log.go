package auth

import (
	"log"
	"strings"
)

// errLogger is the sink chromedpErrf writes to. A variable so tests can
// capture lines without touching the global logger.
var errLogger = log.Printf

// unhandledNodeEvent is the prefix chromedp uses for every CDP event it has no
// handler for (chromedp/target.go). Matching the prefix rather than the event
// type keeps this working as Chrome adds events chromedp has yet to model.
const unhandledNodeEvent = "unhandled node event"

// chromedpErrf is chromedp's error logger, minus one class of noise.
//
// chromedp logs any unmodelled CDP event at ERROR level.
// DOM.topLayerElementsUpdated -- fired whenever a dialog, popover or
// fullscreen element changes the page's top layer -- is one of those, and
// LinkedIn emits it on ordinary job pages. The result was two
//
//	ERROR: unhandled node event *dom.EventTopLayerElementsUpdated
//
// lines on a `jobs get` that had worked perfectly, interleaved with the audit
// output this tool deliberately sends to stderr. A successful command read as
// a failed one, and the daily sweep's .li.log filled with it.
//
// Only that class is dropped. Silencing the logger outright would also hide
// genuine target and transport errors, and a tool whose whole design is
// "never look like it worked when it did not" should not start hiding errors
// to tidy its output.
func chromedpErrf(format string, args ...any) {
	if strings.HasPrefix(format, unhandledNodeEvent) {
		return
	}
	errLogger("ERROR: "+format, args...)
}
