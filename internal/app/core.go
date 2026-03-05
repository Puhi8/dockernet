package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	schemaVersion    = "1"
	exitCodeOK       = 0
	exitCodeRuntime  = 1
	exitCodeConflict = 2
	exitCodeDegraded = 3
)

type parsedGlobalFlags struct {
	Command     string
	CommandArgs []string
	Help        bool

	ConfigPath string
	RootsCSV   string
	IPv6       bool
	JSON       bool
	Quiet      bool

	ConfigPathSet bool
	RootsSet      bool
	IPv6Set       bool
	JSONSet       bool
	QuietSet      bool
}

type runtimeOptions struct {
	ConfigPath   string
	ComposeRoots []string
	IncludeIPv6  bool
	EnableColor  bool
	JSON         bool
	Quiet        bool
	Networks     []string
	IgnorePaths  []string
	Groups       map[string]IPRange
}

type IPEntry struct {
	Network       string `json:"network"`
	IP            string `json:"ip"`
	IPVersion     int    `json:"ip_version"`
	Service       string `json:"service,omitempty"`
	ContainerName string `json:"container_name,omitempty"`
	Project       string `json:"project,omitempty"`
	ComposeFile   string `json:"compose_file,omitempty"`
	Running       bool   `json:"running"`
	Source        string `json:"source"`
}

type discoveryResult struct {
	ComposeFiles   []string  `json:"compose_files"`
	ComposeEntries []IPEntry `json:"compose_entries"`
	DockerEntries  []IPEntry `json:"docker_entries"`
	Networks       []string  `json:"networks"`
	Warnings       []string  `json:"warnings"`
	Degraded       bool      `json:"compose_only"`
}

type composeParseResult struct {
	Entries     []IPEntry
	Networks    []string
	VolumePaths []string
	IsCompose   bool
}

type dockerDiscovery struct {
	Entries   []IPEntry
	Networks  []string
	Warnings  []string
	Available bool
}

type checkConflict struct {
	Type    string   `json:"type"`
	Network string   `json:"network"`
	IP      string   `json:"ip"`
	Details []string `json:"details"`
}

type freeResultRow struct {
	Group   string   `json:"group"`
	Network string   `json:"network"`
	IPs     []string `json:"ips"`
}

func run(args []string, stdout, stderr io.Writer) (int, error) {
	globals, err := parseGlobalFlags(args)
	if err != nil {
		printMainUsage(stdout)
		return exitCodeRuntime, err
	}

	if globals.Command == "help" {
		runHelp(stdout, globals.CommandArgs)
		return exitCodeOK, nil
	}

	configPath, err := resolveConfigPath(globals)
	if err != nil {
		return exitCodeRuntime, err
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		return exitCodeRuntime, fmt.Errorf("load config %q: %w", configPath, err)
	}

	opts, err := buildRuntimeOptions(globals, cfg, configPath)
	if err != nil {
		return exitCodeRuntime, err
	}
	setColorEnabled(opts.EnableColor)

	switch globals.Command {
	case "ls":
		return runLS(context.Background(), opts, globals.CommandArgs, stdout, stderr)
	case "ps":
		return runPS(context.Background(), opts, globals.CommandArgs, stdout, stderr)
	case "check":
		return runCheck(context.Background(), opts, globals.CommandArgs, stdout, stderr)
	case "free":
		return runFree(context.Background(), opts, globals.CommandArgs, stdout, stderr)
	case "nextFree", "nextfree":
		return runNextFree(context.Background(), opts, globals.CommandArgs, stdout, stderr)
	case "sections":
		return runSections(opts, globals.CommandArgs, stdout, stderr)
	default:
		printMainUsage(stdout)
		return exitCodeRuntime, fmt.Errorf("unknown command %q", globals.Command)
	}
}

func printMainUsage(w io.Writer) {
	writeHelpMenu(w, "main")
}

func parseGlobalFlags(args []string) (parsedGlobalFlags, error) {
	if len(args) < 2 {
		return parsedGlobalFlags{}, errors.New("missing command")
	}

	flagSet := flag.NewFlagSet("dockernet", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)

	var globals parsedGlobalFlags
	addFlag(flagSet, &globals.ConfigPath, "c", "config", "", "config path")
	addFlag(flagSet, &globals.RootsCSV, "r", "root", "", "compose roots")
	addFlag(flagSet, &globals.IPv6, "6", "ipv6", false, "include ipv6")
	addFlag(flagSet, &globals.JSON, "j", "json", false, "json output")
	addFlag(flagSet, &globals.Quiet, "q", "quiet", false, "quiet mode")
	addFlag(flagSet, &globals.Help, "h", "help", false, "show help")

	if err := flagSet.Parse(args[1:]); err != nil {
		return parsedGlobalFlags{}, err
	}

	seen := make(map[string]bool)
	flagSet.Visit(func(f *flag.Flag) {
		seen[f.Name] = true
	})

	globals.ConfigPathSet = seen["config"] || seen["c"]
	globals.RootsSet = seen["root"] || seen["r"]
	globals.IPv6Set = seen["ipv6"] || seen["6"]
	globals.JSONSet = seen["json"] || seen["j"]
	globals.QuietSet = seen["quiet"] || seen["q"]

	remaining := flagSet.Args()
	if globals.Help {
		globals.Command = "help"
		globals.CommandArgs = remaining
		return globals, nil
	}

	if len(remaining) == 0 {
		return parsedGlobalFlags{}, errors.New("missing command")
	}

	globals.Command = remaining[0]
	globals.CommandArgs = remaining[1:]
	return globals, nil
}

func resolveConfigPath(globals parsedGlobalFlags) (string, error) {
	if globals.ConfigPathSet && strings.TrimSpace(globals.ConfigPath) != "" {
		return strings.TrimSpace(globals.ConfigPath), nil
	}
	if envPath := strings.TrimSpace(os.Getenv("DOCKERNET_CONFIG")); envPath != "" {
		return envPath, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".dockernet.conf"), nil
}

func buildRuntimeOptions(globals parsedGlobalFlags, cfg *Config, configPath string) (runtimeOptions, error) {
	opts := runtimeOptions{
		ConfigPath:  configPath,
		Networks:    dedupeStrings(cfg.Networks),
		IgnorePaths: append([]string(nil), cfg.IgnorePaths...),
		Groups:      cfg.Groups,
	}

	composeRoots, err := resolveComposeRoots(globals, cfg)
	if err != nil {
		return runtimeOptions{}, err
	}
	opts.ComposeRoots = composeRoots

	ipv6, err := resolveBoolOption(globals.IPv6Set, globals.IPv6, "DOCKERNET_IPV6", cfg.EnableIPv6)
	if err != nil {
		return runtimeOptions{}, fmt.Errorf("parse DOCKERNET_IPV6: %w", err)
	}
	opts.IncludeIPv6 = ipv6

	enableColor, err := resolveBoolOption(false, false, "DOCKERNET_COLOR", cfg.EnableColor)
	if err != nil {
		return runtimeOptions{}, fmt.Errorf("parse DOCKERNET_COLOR: %w", err)
	}
	opts.EnableColor = enableColor

	jsonOutput, err := resolveBoolOption(globals.JSONSet, globals.JSON, "DOCKERNET_JSON", false)
	if err != nil {
		return runtimeOptions{}, fmt.Errorf("parse DOCKERNET_JSON: %w", err)
	}
	opts.JSON = jsonOutput

	quiet, err := resolveBoolOption(globals.QuietSet, globals.Quiet, "DOCKERNET_QUIET", false)
	if err != nil {
		return runtimeOptions{}, fmt.Errorf("parse DOCKERNET_QUIET: %w", err)
	}
	opts.Quiet = quiet

	return opts, nil
}

func resolveComposeRoots(globals parsedGlobalFlags, cfg *Config) ([]string, error) {
	switch {
	case globals.RootsSet:
		roots := dedupeStrings(parseCSVList(globals.RootsCSV))
		if len(roots) > 0 {
			return roots, nil
		}
	case strings.TrimSpace(os.Getenv("DOCKERNET_ROOTS")) != "":
		roots := dedupeStrings(parseCSVList(os.Getenv("DOCKERNET_ROOTS")))
		if len(roots) > 0 {
			return roots, nil
		}
	case len(cfg.ComposeRoots) > 0:
		return dedupeStrings(cfg.ComposeRoots), nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve current directory: %w", err)
	}
	return []string{cwd}, nil
}

func resolveBoolOption(flagWasSet bool, flagValue bool, envName string, configValue bool) (bool, error) {
	if flagWasSet {
		return flagValue, nil
	}

	if raw, ok := os.LookupEnv(envName); ok && strings.TrimSpace(raw) != "" {
		value, parsed := parseBoolValue(raw)
		if !parsed {
			return false, fmt.Errorf("invalid boolean value %q", raw)
		}
		return value, nil
	}

	return configValue, nil
}
