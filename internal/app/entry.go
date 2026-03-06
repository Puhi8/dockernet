package app

import (
	"fmt"
	"io"
)

// Execute runs dockernet CLI and handles top-level error rendering.
func Execute(args []string, stdout, stderr io.Writer) int {
	code, err := run(args, stdout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, errorLine(stderr, err.Error()))
	}
	return code
}
