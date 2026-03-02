package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
)

func runLS(ctx context.Context, opts runtimeOptions, args []string, stdout, stderr io.Writer) (int, error) {
	if hasHelpArg(args) {
		runHelp(stdout, []string{"ls"})
		return exitCodeOK, nil
	}

	flagSet := flag.NewFlagSet("ls", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	if err := flagSet.Parse(args); err != nil {
		return exitCodeRuntime, err
	}
	if len(flagSet.Args()) > 0 {
		return exitCodeRuntime, fmt.Errorf("unexpected args for ls: %v", flagSet.Args())
	}

	state, err := discoverState(ctx, opts)
	if err != nil {
		return exitCodeRuntime, err
	}
	emitDiscoveryWarnings(stderr, state, opts.Quiet, opts.JSON)

	runningCount := 0
	for _, entry := range state.DockerEntries {
		if entry.Running && entry.IP != "" && entry.IP != "host" {
			runningCount++
		}
	}

	if opts.JSON {
		payload := struct {
			SchemaVersion string   `json:"schema_version"`
			Command       string   `json:"command"`
			ComposeOnly   bool     `json:"compose_only"`
			Warnings      []string `json:"warnings,omitempty"`
			Networks      []string `json:"networks"`
			ComposeFiles  []string `json:"compose_files"`
			Counts        struct {
				StaticIPs  int `json:"static_ips"`
				RunningIPs int `json:"running_ips"`
			} `json:"counts"`
		}{
			SchemaVersion: schemaVersion,
			Command:       "ls",
			ComposeOnly:   state.Degraded,
			Warnings:      state.Warnings,
			Networks:      state.Networks,
			ComposeFiles:  state.ComposeFiles,
		}
		payload.Counts.StaticIPs = len(state.ComposeEntries)
		payload.Counts.RunningIPs = runningCount
		if err := writeJSON(stdout, payload); err != nil {
			return exitCodeRuntime, err
		}
	} else {
		fmt.Fprintf(stdout, "networks: %s\n", strings.Join(state.Networks, ","))
		fmt.Fprintf(stdout, "compose_files: %d\n", len(state.ComposeFiles))
		for _, file := range state.ComposeFiles {
			fmt.Fprintf(stdout, "  %s\n", file)
		}
		fmt.Fprintf(stdout, "static_ips: %d\n", len(state.ComposeEntries))
		fmt.Fprintf(stdout, "running_ips: %d\n", runningCount)
	}

	if state.Degraded {
		return exitCodeDegraded, nil
	}
	return exitCodeOK, nil
}

func runPS(ctx context.Context, opts runtimeOptions, args []string, stdout, stderr io.Writer) (int, error) {
	if hasHelpArg(args) {
		runHelp(stdout, []string{"ps"})
		return exitCodeOK, nil
	}

	flagSet := flag.NewFlagSet("ps", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)

	var networkFilter string
	var ipPrefix string
	var runningOnly bool
	var composeOnly bool
	addFlag(flagSet, &networkFilter, "n", "network", "", "network filter")
	addFlag(flagSet, &ipPrefix, "i", "ip-prefix", "", "ip prefix filter")
	addFlag(flagSet, &runningOnly, "r", "running", false, "only running")
	addFlag(flagSet, &composeOnly, "c", "compose-only", false, "only compose entries")

	if err := flagSet.Parse(args); err != nil {
		return exitCodeRuntime, err
	}
	if len(flagSet.Args()) > 0 {
		return exitCodeRuntime, fmt.Errorf("unexpected args for ps: %v", flagSet.Args())
	}

	state, err := discoverState(ctx, opts)
	if err != nil {
		return exitCodeRuntime, err
	}
	emitDiscoveryWarnings(stderr, state, opts.Quiet, opts.JSON)

	rows := buildPSRows(state.ComposeEntries, state.DockerEntries)
	filteredRows := make([]IPEntry, 0, len(rows))
	for _, row := range rows {
		if strings.TrimSpace(networkFilter) != "" && row.Network != networkFilter {
			continue
		}
		if strings.TrimSpace(ipPrefix) != "" && !strings.HasPrefix(row.IP, ipPrefix) {
			continue
		}
		if runningOnly && !row.Running {
			continue
		}
		if composeOnly && row.Source != "compose" {
			continue
		}
		filteredRows = append(filteredRows, row)
	}
	sortPSRowsByIP(filteredRows)

	if opts.JSON {
		payload := struct {
			SchemaVersion string    `json:"schema_version"`
			Command       string    `json:"command"`
			ComposeOnly   bool      `json:"compose_only"`
			Warnings      []string  `json:"warnings,omitempty"`
			Entries       []IPEntry `json:"entries"`
		}{
			SchemaVersion: schemaVersion,
			Command:       "ps",
			ComposeOnly:   state.Degraded,
			Warnings:      state.Warnings,
			Entries:       filteredRows,
		}
		if err := writeJSON(stdout, payload); err != nil {
			return exitCodeRuntime, err
		}
	} else {
		rows := make([][]string, 0, len(filteredRows)+1)
		rows = append(rows, []string{
			colorize(stdout, ansiCyan, "container/service"),
			colorize(stdout, ansiCyan, "network"),
			colorize(stdout, ansiCyan, "ip"),
			colorize(stdout, ansiCyan, "running"),
			colorize(stdout, ansiCyan, "source"),
		})

		for _, row := range filteredRows {
			name := strings.TrimSpace(row.ContainerName)
			if name == "" {
				name = strings.TrimSpace(row.Service)
			}
			if name == "" {
				name = "-"
			}
			rows = append(rows, []string{
				colorize(stdout, ansiBlue, name),
				row.Network,
				colorize(stdout, ansiYellow, row.IP),
				runningLabel(stdout, row.Running),
				sourceLabel(stdout, row.Source),
			})
		}
		if err := printAlignedRows(stdout, rows); err != nil {
			return exitCodeRuntime, err
		}
	}

	if state.Degraded {
		return exitCodeDegraded, nil
	}
	return exitCodeOK, nil
}

func runCheck(ctx context.Context, opts runtimeOptions, args []string, stdout, stderr io.Writer) (int, error) {
	if hasHelpArg(args) {
		runHelp(stdout, []string{"check"})
		return exitCodeOK, nil
	}

	flagSet := flag.NewFlagSet("check", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)

	var networkFilter string
	var groupName string
	addFlag(flagSet, &networkFilter, "n", "network", "", "network filter")
	addFlag(flagSet, &groupName, "g", "group", "", "group filter")

	if err := flagSet.Parse(args); err != nil {
		return exitCodeRuntime, err
	}
	if len(flagSet.Args()) > 0 {
		return exitCodeRuntime, fmt.Errorf("unexpected args for check: %v", flagSet.Args())
	}

	state, err := discoverState(ctx, opts)
	if err != nil {
		return exitCodeRuntime, err
	}
	emitDiscoveryWarnings(stderr, state, opts.Quiet, opts.JSON)

	// For conflict detection, default scope should cover all discovered networks.
	// Configured network filters can hide real collisions and make "check" misleading.
	scopeNetworks := resolveScopedNetworks(networkFilter, nil, state.Networks, false)
	scopeSet := make(map[string]struct{}, len(scopeNetworks))
	for _, network := range scopeNetworks {
		scopeSet[network] = struct{}{}
	}

	var groupRange *IPRange
	if strings.TrimSpace(groupName) != "" {
		foundRange, ok := opts.Groups[groupName]
		if !ok {
			return exitCodeRuntime, fmt.Errorf("group %q not found", groupName)
		}
		groupRange = &foundRange
	}

	conflicts := collectCheckConflicts(state.ComposeEntries, state.DockerEntries, scopeSet, groupName, groupRange)

	if opts.JSON {
		payload := struct {
			SchemaVersion string          `json:"schema_version"`
			Command       string          `json:"command"`
			ComposeOnly   bool            `json:"compose_only"`
			Warnings      []string        `json:"warnings,omitempty"`
			Conflicts     []checkConflict `json:"conflicts"`
		}{
			SchemaVersion: schemaVersion,
			Command:       "check",
			ComposeOnly:   state.Degraded,
			Warnings:      state.Warnings,
			Conflicts:     conflicts,
		}
		if err := writeJSON(stdout, payload); err != nil {
			return exitCodeRuntime, err
		}
	} else {
		if len(conflicts) == 0 {
			fmt.Fprintln(stdout, successLine(stdout, "no conflicts"))
		} else {
			for _, conflict := range conflicts {
				fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n",
					conflictTypeLabel(stdout, conflict.Type),
					conflict.Network,
					colorize(stdout, ansiRed, conflict.IP),
					strings.Join(conflict.Details, "; "),
				)
			}
		}
	}

	if state.Degraded {
		return exitCodeDegraded, nil
	}
	if len(conflicts) > 0 {
		return exitCodeConflict, nil
	}
	return exitCodeOK, nil
}

func runFree(ctx context.Context, opts runtimeOptions, args []string, stdout, stderr io.Writer) (int, error) {
	if hasHelpArg(args) {
		runHelp(stdout, []string{"free"})
		return exitCodeOK, nil
	}

	flagSet := flag.NewFlagSet("free", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)

	var groupName string
	var networkFilter string
	var limit int
	addFlag(flagSet, &groupName, "g", "group", "", "group name")
	addFlag(flagSet, &networkFilter, "n", "network", "", "network filter")
	addFlag(flagSet, &limit, "l", "limit", 1, "number of free addresses to return")

	if err := flagSet.Parse(args); err != nil {
		return exitCodeRuntime, err
	}
	if len(flagSet.Args()) > 0 {
		return exitCodeRuntime, fmt.Errorf("unexpected args for free: %v", flagSet.Args())
	}
	if strings.TrimSpace(groupName) == "" {
		return exitCodeRuntime, errors.New("free requires --group")
	}
	if limit <= 0 {
		return exitCodeRuntime, errors.New("--limit must be > 0")
	}

	groupRange, ok := opts.Groups[groupName]
	if !ok {
		return exitCodeRuntime, fmt.Errorf("group %q not found", groupName)
	}

	state, err := discoverState(ctx, opts)
	if err != nil {
		return exitCodeRuntime, err
	}
	emitDiscoveryWarnings(stderr, state, opts.Quiet, opts.JSON)

	scopeNetworks := resolveScopedNetworks(networkFilter, opts.Networks, state.Networks, false)
	if len(scopeNetworks) == 0 {
		return exitCodeRuntime, errors.New("no networks available for allocation")
	}

	usedByNetwork := buildUsedIPv4ByNetwork(state.ComposeEntries, state.DockerEntries)
	rows := make([]freeResultRow, 0, len(scopeNetworks))
	notEnough := false
	for _, network := range scopeNetworks {
		candidates := collectFreeAddresses(groupRange, usedByNetwork[network], limit)
		if len(candidates) < limit {
			notEnough = true
		}

		ipStrings := make([]string, 0, len(candidates))
		for _, addr := range candidates {
			ipStrings = append(ipStrings, addr.String())
		}

		rows = append(rows, freeResultRow{
			Group:   groupName,
			Network: network,
			IPs:     compressIPv4RangeStrings(ipStrings),
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Group != rows[j].Group {
			return rows[i].Group < rows[j].Group
		}
		return rows[i].Network < rows[j].Network
	})

	if opts.JSON {
		warnings := append([]string(nil), state.Warnings...)
		if notEnough {
			warnings = append(warnings, "not enough space")
		}
		payload := struct {
			SchemaVersion string          `json:"schema_version"`
			Command       string          `json:"command"`
			ComposeOnly   bool            `json:"compose_only"`
			Warnings      []string        `json:"warnings,omitempty"`
			Results       []freeResultRow `json:"results"`
		}{
			SchemaVersion: schemaVersion,
			Command:       "free",
			ComposeOnly:   state.Degraded,
			Warnings:      warnings,
			Results:       rows,
		}
		if err := writeJSON(stdout, payload); err != nil {
			return exitCodeRuntime, err
		}
	} else {
		table := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(table, "group\tnetwork\tfree")
		for _, row := range rows {
			freeValue := "-"
			if len(row.IPs) > 0 {
				freeValue = strings.Join(row.IPs, ", ")
			}
			fmt.Fprintf(table, "%s\t%s\t%s\n", row.Group, row.Network, freeValue)
		}
		if err := table.Flush(); err != nil {
			return exitCodeRuntime, err
		}
		if notEnough {
			fmt.Fprintln(stderr, warningLine(stderr, "not enough space"))
		}
	}

	if state.Degraded {
		return exitCodeDegraded, nil
	}
	return exitCodeOK, nil
}

func runNextFree(ctx context.Context, opts runtimeOptions, args []string, stdout, stderr io.Writer) (int, error) {
	if hasHelpArg(args) {
		runHelp(stdout, []string{"nextFree"})
		return exitCodeOK, nil
	}

	flagSet := flag.NewFlagSet("nextFree", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)

	var groupName string
	var networkFilter string
	count := 2
	addFlag(flagSet, &groupName, "g", "group", "", "group name")
	addFlag(flagSet, &networkFilter, "n", "network", "", "network filter")

	if err := flagSet.Parse(args); err != nil {
		return exitCodeRuntime, err
	}
	positionals := flagSet.Args()
	switch len(positionals) {
	case 0:
	case 1:
		parsedCount, err := strconv.Atoi(strings.TrimSpace(positionals[0]))
		if err != nil {
			return exitCodeRuntime, fmt.Errorf("invalid nextFree count %q", positionals[0])
		}
		if parsedCount <= 0 {
			return exitCodeRuntime, errors.New("nextFree count must be > 0")
		}
		count = parsedCount
	default:
		return exitCodeRuntime, fmt.Errorf("unexpected args for nextFree: %v", positionals)
	}

	groupNames := make([]string, 0)
	if strings.TrimSpace(groupName) != "" {
		if _, ok := opts.Groups[groupName]; !ok {
			return exitCodeRuntime, fmt.Errorf("group %q not found", groupName)
		}
		groupNames = append(groupNames, groupName)
	} else {
		for name := range opts.Groups {
			groupNames = append(groupNames, name)
		}
		sort.Strings(groupNames)
	}
	if len(groupNames) == 0 {
		return exitCodeRuntime, errors.New("no groups configured")
	}

	state, err := discoverState(ctx, opts)
	if err != nil {
		return exitCodeRuntime, err
	}
	emitDiscoveryWarnings(stderr, state, opts.Quiet, opts.JSON)

	scopeNetworks := resolveScopedNetworks(networkFilter, opts.Networks, state.Networks, false)
	if len(scopeNetworks) == 0 {
		return exitCodeRuntime, errors.New("no networks available for allocation")
	}
	usedByNetwork := buildUsedIPv4ByNetwork(state.ComposeEntries, state.DockerEntries)

	rows := make([]freeResultRow, 0, len(groupNames)*len(scopeNetworks))
	notEnough := false
	notEnoughGroups := make(map[string]struct{})
	for _, currentGroup := range groupNames {
		groupRange := opts.Groups[currentGroup]
		for _, network := range scopeNetworks {
			candidates := collectFreeAddresses(groupRange, usedByNetwork[network], count)
			if len(candidates) < count {
				notEnough = true
				notEnoughGroups[currentGroup] = struct{}{}
			}

			ipStrings := make([]string, 0, len(candidates))
			for _, addr := range candidates {
				ipStrings = append(ipStrings, addr.String())
			}

			rows = append(rows, freeResultRow{
				Group:   currentGroup,
				Network: network,
				IPs:     ipStrings,
			})
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Group != rows[j].Group {
			return rows[i].Group < rows[j].Group
		}
		return rows[i].Network < rows[j].Network
	})

	notEnoughGroupList := make([]string, 0, len(notEnoughGroups))
	for group := range notEnoughGroups {
		notEnoughGroupList = append(notEnoughGroupList, group)
	}
	sort.Strings(notEnoughGroupList)

	notEnoughWarning := "not enough space"
	if len(notEnoughGroupList) > 0 {
		notEnoughWarning = fmt.Sprintf("not enough space: %s", strings.Join(notEnoughGroupList, ", "))
	}

	if opts.JSON {
		warnings := append([]string(nil), state.Warnings...)
		if notEnough {
			warnings = append(warnings, notEnoughWarning)
		}
		payload := struct {
			SchemaVersion string          `json:"schema_version"`
			Command       string          `json:"command"`
			ComposeOnly   bool            `json:"compose_only"`
			Warnings      []string        `json:"warnings,omitempty"`
			Results       []freeResultRow `json:"results"`
		}{
			SchemaVersion: schemaVersion,
			Command:       "nextFree",
			ComposeOnly:   state.Degraded,
			Warnings:      warnings,
			Results:       rows,
		}
		if err := writeJSON(stdout, payload); err != nil {
			return exitCodeRuntime, err
		}
	} else {
		table := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintf(table, "%s\t%s\t%s\n",
			colorize(stdout, ansiCyan, "group"),
			colorize(stdout, ansiCyan, "network"),
			colorize(stdout, ansiCyan, "next_free"),
		)
		for _, row := range rows {
			nextValue := "-"
			if len(row.IPs) > 0 {
				nextValue = strings.Join(row.IPs, ", ")
			}
			nextValueOut := colorize(stdout, ansiGray, nextValue)
			if nextValue != "-" {
				nextValueOut = colorize(stdout, ansiGreen, nextValue)
			}
			fmt.Fprintf(table, "%s\t%s\t%s\n",
				colorize(stdout, ansiBlue, row.Group),
				colorize(stdout, ansiCyan, row.Network),
				nextValueOut,
			)
		}
		if err := table.Flush(); err != nil {
			return exitCodeRuntime, err
		}
		if notEnough {
			fmt.Fprintln(stderr, warningLine(stderr, notEnoughWarning))
		}
	}

	if state.Degraded {
		return exitCodeDegraded, nil
	}
	return exitCodeOK, nil
}

func runSections(opts runtimeOptions, args []string, stdout, stderr io.Writer) (int, error) {
	if hasHelpArg(args) {
		runHelp(stdout, []string{"sections"})
		return exitCodeOK, nil
	}

	flagSet := flag.NewFlagSet("sections", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)

	var edit bool
	var validate bool
	var showPath bool
	addFlag(flagSet, &edit, "e", "edit", false, "open config in $EDITOR")
	addFlag(flagSet, &validate, "v", "validate", false, "validate group overlaps")
	addFlag(flagSet, &showPath, "p", "path", false, "print config file path")
	if err := flagSet.Parse(args); err != nil {
		return exitCodeRuntime, err
	}
	if len(flagSet.Args()) > 0 {
		return exitCodeRuntime, fmt.Errorf("unexpected args for sections: %v", flagSet.Args())
	}
	if showPath {
		if opts.JSON {
			payload := struct {
				SchemaVersion string `json:"schema_version"`
				Command       string `json:"command"`
				ConfigPath    string `json:"config_path"`
			}{
				SchemaVersion: schemaVersion,
				Command:       "sections",
				ConfigPath:    opts.ConfigPath,
			}
			if err := writeJSON(stdout, payload); err != nil {
				return exitCodeRuntime, err
			}
		} else {
			fmt.Fprintln(stdout, opts.ConfigPath)
		}
		return exitCodeOK, nil
	}

	config := &Config{Groups: opts.Groups}
	if edit {
		editor := strings.TrimSpace(os.Getenv("EDITOR"))
		if editor == "" {
			return exitCodeRuntime, errors.New("EDITOR is not set")
		}
		cmd := exec.Command(editor, opts.ConfigPath)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return exitCodeRuntime, fmt.Errorf("run editor %q: %w", editor, err)
		}

		loaded, err := LoadConfig(opts.ConfigPath)
		if err != nil {
			return exitCodeRuntime, fmt.Errorf("reload config after edit: %w", err)
		}
		config = loaded
	}

	groupRows := make([]struct {
		Name  string `json:"name"`
		Start string `json:"start"`
		End   string `json:"end"`
	}, 0, len(config.Groups))
	groupNames := make([]string, 0, len(config.Groups))
	for name := range config.Groups {
		groupNames = append(groupNames, name)
	}
	sort.Strings(groupNames)
	for _, groupName := range groupNames {
		groupRange := config.Groups[groupName]
		groupRows = append(groupRows, struct {
			Name  string `json:"name"`
			Start string `json:"start"`
			End   string `json:"end"`
		}{
			Name:  groupName,
			Start: groupRange.Start.String(),
			End:   groupRange.End.String(),
		})
	}

	validationErrors := []string{}
	if validate {
		validationErrors = validateGroupOverlaps(config.Groups)
	}

	if opts.JSON {
		payload := struct {
			SchemaVersion    string   `json:"schema_version"`
			Command          string   `json:"command"`
			Sections         any      `json:"sections"`
			ValidationErrors []string `json:"validation_errors,omitempty"`
		}{
			SchemaVersion:    schemaVersion,
			Command:          "sections",
			Sections:         groupRows,
			ValidationErrors: validationErrors,
		}
		if err := writeJSON(stdout, payload); err != nil {
			return exitCodeRuntime, err
		}
	} else {
		table := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(table, "section\tstart\tend")
		for _, row := range groupRows {
			fmt.Fprintf(table, "%s\t%s\t%s\n", row.Name, row.Start, row.End)
		}
		if err := table.Flush(); err != nil {
			return exitCodeRuntime, err
		}
		for _, validationError := range validationErrors {
			fmt.Fprintln(stderr, warningLine(stderr, validationError))
		}
	}

	if len(validationErrors) > 0 {
		return exitCodeConflict, nil
	}
	return exitCodeOK, nil
}

func printAlignedRows(w io.Writer, rows [][]string) error {
	if len(rows) == 0 {
		return nil
	}

	maxCols := 0
	for _, row := range rows {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}

	widths := make([]int, maxCols)
	for _, row := range rows {
		for idx, cell := range row {
			if cellWidth := visibleWidth(cell); cellWidth > widths[idx] {
				widths[idx] = cellWidth
			}
		}
	}

	for _, row := range rows {
		for idx := 0; idx < maxCols; idx++ {
			if idx > 0 {
				if _, err := io.WriteString(w, "  "); err != nil {
					return err
				}
			}

			cell := ""
			if idx < len(row) {
				cell = row[idx]
			}
			if _, err := io.WriteString(w, padRightVisible(cell, widths[idx])); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
	}
	return nil
}
