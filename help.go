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

	name := normalizeHelpName(args[0])
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
	menu, err := readHelpMenu(name)
	if err != nil {
		fmt.Fprintln(w, warningLine(w, fmt.Sprintf("help topic %q not found", name)))
		return
	}
	fmt.Fprintln(w, menu)
}

func readHelpMenu(name string) (string, error) {
	path := "help.txt/" + name + ".txt"
	data, err := helpMenus.ReadFile(path)
	if err != nil {
		return "", err
	}

	menu := strings.TrimSpace(string(data))
	if menu == "" {
		return "", fmt.Errorf("empty help menu %q", name)
	}
	return menu, nil
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

func normalizeHelpName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "":
		return "main"
	case "nextfree", "next_free", "next-free":
		return "nextfree"
	default:
		return strings.ToLower(strings.TrimSpace(name))
	}
}
