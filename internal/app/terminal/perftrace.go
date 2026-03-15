//go:build perftrace

package terminalOut

import (
	"fmt"
	"strings"
	"time"
)

var perfOrigin = time.Now()

func PerfStart(name string) func() {
	start := time.Now()
	return func() {
		name = strings.TrimSpace(name)
		if name == "" {
			name = "perf"
		}

		totalMS := time.Since(perfOrigin).Milliseconds()
		writeLine(fmt.Sprintf("[PERF +%dms] %s took %dms\n", totalMS, name, time.Since(start).Milliseconds()))
	}
}
