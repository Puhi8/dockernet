package app

import (
	"bufio"
	"fmt"
	"net/netip"
	"os"
	"strings"
)

type IPRange struct {
	Start netip.Addr
	End   netip.Addr
}

type Config struct {
	Networks     []string
	ComposeRoots []string
	IgnorePaths  []string
	EnableIPv6   bool
	EnableColor  bool
	Groups       map[string]IPRange
}

func LoadConfig(path string) (*Config, error) {
	cfg := &Config{
		EnableColor: true,
		Groups:      make(map[string]IPRange),
	}

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	defer file.Close()

	var legacyNetwork string

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)

		switch {
		case key == "NETWORK":
			legacyNetwork = val
		case key == "NETWORKS":
			cfg.Networks = parseCSVList(val)
		case key == "COMPOSE_ROOTS":
			cfg.ComposeRoots = parseCSVList(val)
		case key == "IGNORE_PATHS":
			cfg.IgnorePaths = parseCSVList(val)
		case key == "ENABLE_IPV6":
			enabled, ok := parseBoolValue(val)
			if !ok {
				return nil, fmt.Errorf("invalid ENABLE_IPV6 value: %q", val)
			}
			cfg.EnableIPv6 = enabled
		case key == "ENABLE_COLOR":
			enabled, ok := parseBoolValue(val)
			if !ok {
				return nil, fmt.Errorf("invalid ENABLE_COLOR value: %q", val)
			}
			cfg.EnableColor = enabled
		case strings.HasPrefix(key, "GROUP_"):
			groupName := strings.TrimPrefix(key, "GROUP_")
			if groupName == "" {
				continue
			}

			ipRange, ok := parseIPRange(val)
			if !ok {
				return nil, fmt.Errorf("invalid range for %s: %q", key, val)
			}
			cfg.Groups[groupName] = ipRange
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	if len(cfg.Networks) == 0 && strings.TrimSpace(legacyNetwork) != "" {
		cfg.Networks = []string{strings.TrimSpace(legacyNetwork)}
	}

	return cfg, nil
}

func parseBoolValue(raw string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}

func parseCSVList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		result = append(result, item)
	}
	return result
}

func parseIPRange(value string) (IPRange, bool) {
	startRaw, endRaw, ok := strings.Cut(value, "-")
	if !ok {
		return IPRange{}, false
	}

	start, err := netip.ParseAddr(strings.TrimSpace(startRaw))
	end, err := netip.ParseAddr(strings.TrimSpace(endRaw))
	if err != nil {
		return IPRange{}, false
	}

	if !start.IsValid() || !end.IsValid() || start.Is4() != end.Is4() || start.Compare(end) > 0 {
		return IPRange{}, false
	}

	// Legacy short ranges like "1-10" are intentionally unsupported.
	if strings.Count(strings.TrimSpace(startRaw), ".") == 0 || strings.Count(strings.TrimSpace(endRaw), ".") == 0 {
		return IPRange{}, false
	}

	return IPRange{Start: start, End: end}, true
}
