package app

import (
	"fmt"
	"io"

	"github.com/Puhi8/dockernet/internal/app/terminal"
)

// Execute runs dockernet CLI and handles top-level error rendering.
func Execute(args []string, stdout, stderr io.Writer) int {
	code, err := run(args, stdout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, terminalOut.ErrorLine(stderr, err.Error()))
	}
	return code
}
