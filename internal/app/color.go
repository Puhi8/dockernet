package app

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

var labelsMap = map[string]map[string]string{
	"source":   {"both": ansiGreen, "docker": ansiBlue, "compose": ansiCyan},
	"conflict": {"duplicate_compose_ip": ansiRed, "running_ip_taken": ansiRed, "out_of_group": ansiYellow},
}

func colorizeLabel(w io.Writer, source, label string) string {
	color, ok := labelsMap[label][source]
	if ok {
		return colorize(w, color, source)
	}
	return source
}

func psIPLabel(w io.Writer, network, ip string) string {
	trimmedIP, trimmedNetwork := strings.TrimSpace(ip), strings.TrimSpace(network)
	isHostOrBridge := strings.EqualFold(trimmedIP, "host") || strings.EqualFold(trimmedIP, "bridge") || strings.EqualFold(trimmedNetwork, "host") || strings.EqualFold(trimmedNetwork, "bridge")
	if isHostOrBridge {
		return colorize(w, ansiMagenta, ip)
	}
	return colorize(w, ansiYellow, ip)
}

func visibleWidth(text string) int {
	isANSIFinalByte := func(b byte) bool {
		return b >= 0x40 && b <= 0x7E
	}
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
	return len([]rune(builder.String()))
}

func padRightVisible(text string, width int) string {
	padding := width - visibleWidth(text)
	if padding <= 0 {
		return text
	}
	return text + strings.Repeat(" ", padding)
}
