package terminalOut

import (
	"embed"
	"fmt"
	"io"
	"strings"
)

//go:embed help.txt/*.txt
var helpMenus embed.FS

func RunHelp(w io.Writer, args []string) {
	if len(args) == 0 {
		WriteHelpMenu(w, "main")
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
	case "main", "check", "ls", "ps", "nextfree", "sections":
		WriteHelpMenu(w, name)
	default:
		fmt.Fprintln(w, WarningLine(w, fmt.Sprintf("unknown help topic %q", args[0])))
		fmt.Fprintln(w)
		WriteHelpMenu(w, "main")
	}
}

func WriteHelpMenu(w io.Writer, name string) {
	path := "help.txt/" + name + ".txt"
	data, err := helpMenus.ReadFile(path)
	if err != nil {
		fmt.Fprintln(w, WarningLine(w, fmt.Sprintf("help topic %q not found", name)))
		return
	}
	menu := strings.TrimSpace(string(data))
	if menu == "" {
		fmt.Fprintln(w, WarningLine(w, fmt.Sprintf("empty help menu %q", name)))
		return
	}
	fmt.Fprintln(w, menu)
}

func HasHelpArg(args []string) bool {
	for _, arg := range args {
		switch strings.TrimSpace(strings.ToLower(arg)) {
		case "-h", "--help", "help":
			return true
		}
	}
	return false
}
