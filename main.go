package main

import (
	"os"

	"github.com/Puhi8/dockernet/internal/app"
)

func main() {
	os.Exit(app.Execute(os.Args, os.Stdout, os.Stderr))
}
