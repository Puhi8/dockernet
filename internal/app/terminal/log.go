//go:build perftrace || debug

package terminalOut

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

var (
	perfMu sync.Mutex
)

func Log(message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	writeLine("[LOG] " + message + "\n")
}

func Logf(format string, args ...any) {
	format = strings.TrimSpace(format)
	if len(args) == 0 {
		Log(format)
		return
	}

	// Allow calls like Logf("Workers", 4) without fmt verbs.
	if strings.Contains(format, "%") {
		Log(fmt.Sprintf(format, args...))
		return
	}

	extra := strings.TrimSpace(fmt.Sprintln(args...))
	if format == "" {
		Log(extra)
		return
	}
	Log(format + " " + extra)
}

func writeLine(line string) {
	perfMu.Lock()
	_, _ = os.Stderr.WriteString(line)
	perfMu.Unlock()
}
