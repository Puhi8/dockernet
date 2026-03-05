package main

import (
	"embed"
	"fmt"
	"io"
	"strings"
)

//go:embed help.txt/*.txt
var helpMenus embed.FS

func runHelp(w io.Writer, args []string) {
	if len(args) == 0 {
		writeHelpMenu(w, "main")
		return
	}

	var name string
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "":
		name = "main"
	case "nextfree", "next_free", "next-free":
		name = "nextfree"
	default:
		name = strings.ToLower(strings.TrimSpace(args[0]))
	}

	switch name {
	case "main", "check", "ls", "ps", "free", "nextfree", "sections":
		writeHelpMenu(w, name)
	default:
		fmt.Fprintln(w, warningLine(w, fmt.Sprintf("unknown help topic %q", args[0])))
		fmt.Fprintln(w)
		writeHelpMenu(w, "main")
	}
}

func writeHelpMenu(w io.Writer, name string) {
	path := "help.txt/" + name + ".txt"
	data, err := helpMenus.ReadFile(path)
	if err != nil {
		fmt.Fprintln(w, warningLine(w, fmt.Sprintf("help topic %q not found", name)))
		return
	}
	menu := strings.TrimSpace(string(data))
	if menu == "" {
		fmt.Fprintln(w, warningLine(w, fmt.Sprintf("empty help menu %q", name)))
		return
	}
	fmt.Fprintln(w, menu)
}

func hasHelpArg(args []string) bool {
	for _, arg := range args {
		switch strings.TrimSpace(strings.ToLower(arg)) {
		case "-h", "--help", "help":
			return true
		}
	}
	return false
}
