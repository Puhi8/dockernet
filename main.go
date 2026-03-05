package main

import (
	"os"
	"dockernet/internal/app"
)

func main() {
	os.Exit(app.Execute(os.Args, os.Stdout, os.Stderr))
}
