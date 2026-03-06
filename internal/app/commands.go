package app

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
		networkLabels := make([]string, 0, len(state.Networks))
		for _, network := range state.Networks {
			networkLabels = append(networkLabels, colorize(stdout, ansiCyan, network))
		}

		fmt.Fprintf(stdout, "%s %s\n",
			colorize(stdout, ansiBlue, "networks:"),
			strings.Join(networkLabels, ", "),
		)
		fmt.Fprintf(stdout, "%s %s\n",
			colorize(stdout, ansiBlue, "compose_files:"),
			colorize(stdout, ansiGreen, strconv.Itoa(len(state.ComposeFiles))),
		)
		for _, file := range state.ComposeFiles {
			fmt.Fprintf(stdout, "  %s\n", colorize(stdout, ansiGray, file))
		}
		fmt.Fprintf(stdout, "%s %s\n",
			colorize(stdout, ansiBlue, "static_ips:"),
			colorize(stdout, ansiGreen, strconv.Itoa(len(state.ComposeEntries))),
		)
		fmt.Fprintf(stdout, "%s %s\n",
			colorize(stdout, ansiBlue, "running_ips:"),
			colorize(stdout, ansiGreen, strconv.Itoa(runningCount)),
		)
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
	rows = resolveHostNetworkIPs(rows)
	trimmedNetworkFilter := strings.TrimSpace(networkFilter)
	trimmedIPPrefix := strings.TrimSpace(ipPrefix)
	filteredRows := make([]IPEntry, 0, len(rows))
	for _, row := range rows {
		if (trimmedNetworkFilter != "" && row.Network != trimmedNetworkFilter) ||
			(trimmedIPPrefix != "" && !strings.HasPrefix(row.IP, trimmedIPPrefix)) ||
			(runningOnly && !row.Running) ||
			(composeOnly && row.Source != "compose" && row.Source != "both") {
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
			colorize(stdout, ansiCyan, "CONTAINER"),
			colorize(stdout, ansiCyan, "NETWORK"),
			colorize(stdout, ansiCyan, "IP"),
			colorize(stdout, ansiCyan, "RUNNING"),
			colorize(stdout, ansiCyan, "SOURCE"),
		})

		for _, row := range filteredRows {
			rows = append(rows, []string{
				colorize(stdout, ansiBlue, psEntryName(row)),
				row.Network,
				psIPLabel(stdout, row.Network, row.IP),
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
		count = parsedCount
	default:
		return exitCodeRuntime, fmt.Errorf("unexpected args for nextFree: %v", positionals)
	}
	if count <= 0 {
		return exitCodeRuntime, errors.New("nextFree count must be > 0")
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

			rows = append(rows, freeResultRow{
				Group:   currentGroup,
				Network: network,
				IPs:     addrStrings(candidates),
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
	if len(notEnoughGroupList) > 0 && len(groupNames) > 1 {
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
			colorize(stdout, ansiCyan, "GROUP"),
			colorize(stdout, ansiCyan, "NETWORK"),
			colorize(stdout, ansiCyan, "NEXT_FREE"),
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
