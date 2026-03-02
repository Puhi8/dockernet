package main

import (
	"fmt"
	"os"
)

func main() {
	code, err := run(os.Args, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, errorLine(os.Stderr, err.Error()))
	}
	os.Exit(code)
}
