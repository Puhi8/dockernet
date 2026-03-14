package terminalOut

import (
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	ANSIReset   = "\033[0m"
	ANSIRed     = "\033[31m"
	ANSIGreen   = "\033[32m"
	ANSIMagenta = "\033[35m"
	ANSIYellow  = "\033[33m"
	ANSIBlue    = "\033[34m"
	ANSICyan    = "\033[36m"
	ANSIGray    = "\033[90m"
)

var colorEnabled = true

func SetColorEnabled(enabled bool) {
	colorEnabled = enabled
}

func Colorize(w io.Writer, code, text string) string {
	if !isColorEnabled(w) {
		return text
	}
	return code + text + ANSIReset
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

func WarningLine(w io.Writer, msg string) string {
	return fmt.Sprintf("%s %s", Colorize(w, ANSIYellow, "warning:"), msg)
}

func ErrorLine(w io.Writer, msg string) string {
	return fmt.Sprintf("%s %s", Colorize(w, ANSIRed, "error:"), msg)
}

func SuccessLine(w io.Writer, msg string) string {
	return Colorize(w, ANSIGreen, msg)
}

func RunningLabel(w io.Writer, running bool) string {
	if running {
		return Colorize(w, ANSIGreen, "yes")
	}
	return Colorize(w, ANSIGray, "no")
}

var labelsMap = map[string]map[string]string{
	"source":   {"both": ANSIGreen, "docker": ANSIBlue, "compose": ANSICyan},
	"conflict": {"duplicate_compose_ip": ANSIRed, "running_ip_taken": ANSIRed, "out_of_group": ANSIYellow},
}

func ColorizeLabel(w io.Writer, source, label string) string {
	color, ok := labelsMap[label][source]
	if ok {
		return Colorize(w, color, source)
	}
	return source
}

func PSIPLabel(w io.Writer, network, ip string) string {
	trimmedIP, trimmedNetwork := strings.TrimSpace(ip), strings.TrimSpace(network)
	isHostOrBridge := strings.EqualFold(trimmedIP, "host") || strings.EqualFold(trimmedIP, "bridge") || strings.EqualFold(trimmedNetwork, "host") || strings.EqualFold(trimmedNetwork, "bridge")
	if isHostOrBridge {
		return Colorize(w, ANSIMagenta, ip)
	}
	return Colorize(w, ANSIYellow, ip)
}

func PSPortsLabel(w io.Writer, ports []string) string {
	if len(ports) == 0 {
		return Colorize(w, ANSIGray, "-")
	}

	labels := make([]string, 0, len(ports))
	for _, port := range ports {
		if strings.Contains(port, "->") {
			labels = append(labels, Colorize(w, ANSIGreen, port))
			continue
		}
		labels = append(labels, Colorize(w, ANSIGray, port))
	}
	return strings.Join(labels, ", ")
}

func VisibleWidth(text string) int {
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

func PadRightVisible(text string, width int) string {
	padding := width - VisibleWidth(text)
	if padding <= 0 {
		return text
	}
	return text + strings.Repeat(" ", padding)
}
