package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	ansiReset   = "\033[0m"
	ansiRed     = "\033[31m"
	ansiGreen   = "\033[32m"
	ansiMagenta = "\033[35m"
	ansiYellow  = "\033[33m"
	ansiBlue    = "\033[34m"
	ansiCyan    = "\033[36m"
	ansiGray    = "\033[90m"
)
var colorEnabled = true

func setColorEnabled(enabled bool) {
	colorEnabled = enabled
}

func colorize(w io.Writer, code, text string) string {
	if !isColorEnabled(w) {
		return text
	}
	return code + text + ansiReset
}

func isColorEnabled(w io.Writer) bool {
	if !colorEnabled {
		return false
	}

	if strings.TrimSpace(os.Getenv("NO_COLOR")) != "" ||
		strings.TrimSpace(os.Getenv("TERM")) == "dumb" {
		return false
	}

	if force := strings.TrimSpace(os.Getenv("CLICOLOR_FORCE")); force != "" && force != "0" {
		return true
	}
	if strings.TrimSpace(os.Getenv("CLICOLOR")) == "0" {
		return false
	}

	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func warningLine(w io.Writer, msg string) string {
	return fmt.Sprintf("%s %s", colorize(w, ansiYellow, "warning:"), msg)
}

func errorLine(w io.Writer, msg string) string {
	return fmt.Sprintf("%s %s", colorize(w, ansiRed, "error:"), msg)
}

func successLine(w io.Writer, msg string) string {
	return colorize(w, ansiGreen, msg)
}

func runningLabel(w io.Writer, running bool) string {
	if running {
		return colorize(w, ansiGreen, "yes")
	}
	return colorize(w, ansiGray, "no")
}

var sourceLabelMap = map[string]string{"both": ansiGreen, "docker": ansiBlue, "compose": ansiCyan}

func sourceLabel(w io.Writer, source string) string {
	color, ok := sourceLabelMap[source]
	if ok {
		return colorize(w, color, source)
	}
	return source
}

func psIPLabel(w io.Writer, network, ip string) string {
	trimmedIP := strings.TrimSpace(ip)
	trimmedNetwork := strings.TrimSpace(network)
	if strings.EqualFold(trimmedIP, "host") ||
		strings.EqualFold(trimmedIP, "bridge") ||
		strings.EqualFold(trimmedNetwork, "host") ||
		strings.EqualFold(trimmedNetwork, "bridge") {
		return colorize(w, ansiMagenta, ip)
	}
	return colorize(w, ansiYellow, ip)
}

var conflictTypeLabelMap = map[string]string{"duplicate_compose_ip": ansiRed, "running_ip_taken": ansiRed, "out_of_group": ansiYellow}

func conflictTypeLabel(w io.Writer, conflictType string) string {
	color, ok := conflictTypeLabelMap[conflictType]
	if ok {
		return colorize(w, color, conflictType)
	}
	return conflictType
}

func visibleWidth(text string) int {
	return len([]rune(stripANSI(text)))
}

func padRightVisible(text string, width int) string {
	padding := width - visibleWidth(text)
	if padding <= 0 {
		return text
	}
	return text + strings.Repeat(" ", padding)
}

func stripANSI(text string) string {
	var builder strings.Builder
	for idx := 0; idx < len(text); {
		if text[idx] == 0x1b && idx+1 < len(text) && text[idx+1] == '[' {
			idx += 2
			for idx < len(text) && !isANSIFinalByte(text[idx]) {
				idx++
			}
			if idx < len(text) {
				idx++
			}
			continue
		}
		builder.WriteByte(text[idx])
		idx++
	}
	return builder.String()
}

func isANSIFinalByte(b byte) bool {
	return b >= 0x40 && b <= 0x7E
}
