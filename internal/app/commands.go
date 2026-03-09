package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

func runLS(ctx context.Context, opts runtimeOptions, args []string, stdout, stderr io.Writer) (int, error) {
	if hasHelpArg(args) {
		runHelp(stdout, []string{"ls"})
		return exitCodeOK, nil
	}

	flagSet := flag.NewFlagSet("ls", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	if err := parseNoPositionalArgs(flagSet, args, "ls"); err != nil {
		return exitCodeRuntime, err
	}

	state, err := discoverStateAndEmitWarnings(ctx, opts, stderr)
	if err != nil {
		return exitCodeRuntime, err
	}

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
		printTitle := func(text string, value int) {
			fmt.Fprintf(stdout, "%s %s\n",
				colorize(stdout, ansiBlue, text),
				colorize(stdout, ansiGreen, strconv.Itoa(value)),
			)
		}
		fmt.Fprintf(stdout, "%s %s\n",
			colorize(stdout, ansiBlue, "networks:"),
			strings.Join(networkLabels, ", "),
		)
		printTitle("compose_files:", len(state.ComposeFiles))
		for _, file := range state.ComposeFiles {
			fmt.Fprintf(stdout, "  %s\n", colorize(stdout, ansiGray, file))
		}
		printTitle("static_ips:", len(state.ComposeEntries))
		printTitle("running_ips:", runningCount)
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
	var groupName string
	sortBy := "ip"
	groupNumber := -1
	var runningOnly bool
	var composeOnly bool
	var allInOne bool
	addFlag(flagSet, &networkFilter, "n", "network", "", "network filter")
	addFlag(flagSet, &ipPrefix, "i", "ip-prefix", "", "ip prefix filter")
	addGroupSelectionFlags(flagSet, &groupName, &groupNumber, "group filter")
	addFlag(flagSet, &sortBy, "s", "sort", "ip", "sort order: ip|name")
	addFlag(flagSet, &runningOnly, "r", "running", false, "only running")
	addFlag(flagSet, &composeOnly, "c", "compose-only", false, "only compose entries")
	addFlag(flagSet, &allInOne, "a", "all-in-one", false, "print one combined table")

	if err := parseNoPositionalArgs(flagSet, args, "ps"); err != nil {
		return exitCodeRuntime, err
	}
	sortBy = strings.ToLower(strings.TrimSpace(sortBy))
	if sortBy == "" {
		sortBy = "ip"
	}
	if sortBy != "ip" && sortBy != "name" {
		return exitCodeRuntime, fmt.Errorf("invalid sort %q (expected ip|name)", sortBy)
	}
	selectedGroup, err := resolveGroupSelection(groupName, groupNumber, opts.Groups, opts.GroupOrder)
	if err != nil {
		return exitCodeRuntime, err
	}
	if !opts.JSON && selectedGroup.Explicit {
		fmt.Fprintln(stdout, colorize(stdout, ansiMagenta, selectedGroup.Name))
	}
	groupRange := selectedGroupRange(selectedGroup, opts.Groups)

	state, err := discoverStateAndEmitWarnings(ctx, opts, stderr)
	if err != nil {
		return exitCodeRuntime, err
	}

	rows := buildPSRows(state.ComposeEntries, state.DockerEntries)
	rows = resolveHostNetworkIPs(rows)
	trimmedNetworkFilter := strings.TrimSpace(networkFilter)
	trimmedIPPrefix := strings.TrimSpace(ipPrefix)
	filteredRows := make([]IPEntry, 0, len(rows))
	for _, row := range rows {
		if groupRange != nil {
			addr, parseErr := netip.ParseAddr(strings.TrimSpace(row.IP))
			if parseErr != nil || !ipInRange(addr, *groupRange) {
				continue
			}
		}
		if (trimmedNetworkFilter != "" && row.Network != trimmedNetworkFilter) ||
			(trimmedIPPrefix != "" && !strings.HasPrefix(row.IP, trimmedIPPrefix)) ||
			(runningOnly && !row.Running) ||
			(composeOnly && row.Source != "compose" && row.Source != "both") {
			continue
		}
		filteredRows = append(filteredRows, row)
	}
	sortEntries(filteredRows, sortBy)

	if opts.JSON {
		selectedGroupNumber := selectedGroupNumberPointer(selectedGroup)
		payload := struct {
			SchemaVersion       string    `json:"schema_version"`
			Command             string    `json:"command"`
			ComposeOnly         bool      `json:"compose_only"`
			SelectedGroup       string    `json:"selected_group,omitempty"`
			SelectedGroupNumber *int      `json:"selected_group_number,omitempty"`
			Warnings            []string  `json:"warnings,omitempty"`
			Entries             []IPEntry `json:"entries"`
		}{
			SchemaVersion:       schemaVersion,
			Command:             "ps",
			ComposeOnly:         state.Degraded,
			SelectedGroup:       selectedGroup.Name,
			SelectedGroupNumber: selectedGroupNumber,
			Warnings:            state.Warnings,
			Entries:             filteredRows,
		}
		if err := writeJSON(stdout, payload); err != nil {
			return exitCodeRuntime, err
		}
	} else if err := printPSRowsByGroup(stdout, filteredRows, opts.Groups, opts.GroupOrder, allInOne); err != nil {
		return exitCodeRuntime, err
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
	groupNumber := -1
	addFlag(flagSet, &networkFilter, "n", "network", "", "network filter")
	addGroupSelectionFlags(flagSet, &groupName, &groupNumber, "group filter")

	if err := parseNoPositionalArgs(flagSet, args, "check"); err != nil {
		return exitCodeRuntime, err
	}

	selectedGroup, err := resolveGroupSelection(groupName, groupNumber, opts.Groups, opts.GroupOrder)
	if err != nil {
		return exitCodeRuntime, err
	}
	if !opts.JSON && selectedGroup.Explicit {
		printSelectedGroupLine(stdout, selectedGroup)
	}

	state, err := discoverStateAndEmitWarnings(ctx, opts, stderr)
	if err != nil {
		return exitCodeRuntime, err
	}
	scopeNetworks := resolveScopedNetworks(networkFilter, nil, state.Networks, false)
	scopeSet := make(map[string]struct{}, len(scopeNetworks))
	for _, network := range scopeNetworks {
		scopeSet[network] = struct{}{}
	}
	groupRange := selectedGroupRange(selectedGroup, opts.Groups)

	conflicts := collectCheckConflicts(state.ComposeEntries, state.DockerEntries, scopeSet, selectedGroup.Name, groupRange, opts.Groups)

	if opts.JSON {
		selectedGroupNumber := selectedGroupNumberPointer(selectedGroup)
		payload := struct {
			SchemaVersion       string          `json:"schema_version"`
			Command             string          `json:"command"`
			ComposeOnly         bool            `json:"compose_only"`
			SelectedGroup       string          `json:"selected_group,omitempty"`
			SelectedGroupNumber *int            `json:"selected_group_number,omitempty"`
			Warnings            []string        `json:"warnings,omitempty"`
			Conflicts           []checkConflict `json:"conflicts"`
		}{
			SchemaVersion:       schemaVersion,
			Command:             "check",
			ComposeOnly:         state.Degraded,
			SelectedGroup:       selectedGroup.Name,
			SelectedGroupNumber: selectedGroupNumber,
			Warnings:            state.Warnings,
			Conflicts:           conflicts,
		}
		if err := writeJSON(stdout, payload); err != nil {
			return exitCodeRuntime, err
		}
	} else {
		if len(conflicts) == 0 {
			fmt.Fprintln(stdout, successLine(stdout, "no conflicts"))
		} else {
			rows := make([][]string, 0, len(conflicts))
			for _, conflict := range conflicts {
				rows = append(rows, []string{
					conflictTypeLabel(stdout, conflict.Type),
					conflict.Network,
					colorize(stdout, ansiRed, conflict.IP),
					strings.Join(conflict.Details, "; "),
				})
			}
			if err := printAlignedRows(stdout, rows); err != nil {
				return exitCodeRuntime, err
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

func printPSRowsByGroup(w io.Writer, entries []IPEntry, groups map[string]IPRange, configOrder []string, allInOne bool) error {
	orderedGroups := orderedGroupNames(groups, configOrder)
	if allInOne || len(entries) == 0 || len(orderedGroups) == 0 {
		return printPSRowsTable(w, entries)
	}

	entriesByGroup := make(map[string][]IPEntry, len(orderedGroups))
	unassignedEntries := make([]IPEntry, 0, len(entries))
	for _, entry := range entries {
		addr, err := netip.ParseAddr(strings.TrimSpace(entry.IP))
		if err != nil {
			unassignedEntries = append(unassignedEntries, entry)
			continue
		}
		matchedGroup := findMatchingGroupName(addr, groups, orderedGroups)
		if matchedGroup == "" {
			unassignedEntries = append(unassignedEntries, entry)
			continue
		}
		entriesByGroup[matchedGroup] = append(entriesByGroup[matchedGroup], entry)
	}

	rows := make([][]string, 0, len(entries)+len(orderedGroups)+4)
	rows = append(rows, psTableHeaderRow(w))

	printed := false
	for _, groupName := range orderedGroups {
		groupRows := entriesByGroup[groupName]
		if len(groupRows) == 0 {
			continue
		}
		if printed {
			rows = append(rows, []string{"", "", "", "", ""})
		}
		rows = append(rows, psGroupLabelRow(w, groupName))
		for _, row := range groupRows {
			rows = append(rows, psTableEntryRow(w, row))
		}
		printed = true
	}

	if len(unassignedEntries) > 0 {
		if printed {
			rows = append(rows, []string{"", "", "", "", ""})
		}
		rows = append(rows, psGroupLabelRow(w, "unassigned"))
		for _, row := range unassignedEntries {
			rows = append(rows, psTableEntryRow(w, row))
		}
		printed = true
	}

	if !printed {
		return printPSRowsTable(w, entries)
	}
	return printAlignedRows(w, rows)
}

func printPSRowsTable(w io.Writer, entries []IPEntry) error {
	rows := make([][]string, 0, len(entries)+1)
	rows = append(rows, psTableHeaderRow(w))

	for _, row := range entries {
		rows = append(rows, psTableEntryRow(w, row))
	}
	return printAlignedRows(w, rows)
}

func psTableHeaderRow(w io.Writer) []string {
	return makeHeaders(w, "CONTAINER", "NETWORK", "IP", "RUNNING", "SOURCE")
}

func psTableEntryRow(w io.Writer, row IPEntry) []string {
	return []string{
		colorize(w, ansiBlue, psEntryName(row)),
		row.Network,
		psIPLabel(w, row.Network, row.IP),
		runningLabel(w, row.Running),
		sourceLabel(w, row.Source),
	}
}

func psGroupLabelRow(w io.Writer, groupName string) []string {
	return []string{colorize(w, ansiMagenta, groupName), "", "", "", ""}
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
	groupNumber := -1
	count := 2
	addGroupSelectionFlags(flagSet, &groupName, &groupNumber, "group name")
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

	selectedGroup, err := resolveGroupSelection(groupName, groupNumber, opts.Groups, opts.GroupOrder)
	if err != nil {
		return exitCodeRuntime, err
	}
	if !opts.JSON && selectedGroup.Explicit {
		printSelectedGroupLine(stdout, selectedGroup)
	}

	groupNames := selectedOrOrderedGroupNames(selectedGroup, opts.Groups, opts.GroupOrder)
	if len(groupNames) == 0 {
		return exitCodeRuntime, errors.New("no groups configured")
	}

	state, err := discoverStateAndEmitWarnings(ctx, opts, stderr)
	if err != nil {
		return exitCodeRuntime, err
	}

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
		selectedGroupNumber := selectedGroupNumberPointer(selectedGroup)
		payload := struct {
			SchemaVersion       string          `json:"schema_version"`
			Command             string          `json:"command"`
			ComposeOnly         bool            `json:"compose_only"`
			SelectedGroup       string          `json:"selected_group,omitempty"`
			SelectedGroupNumber *int            `json:"selected_group_number,omitempty"`
			Warnings            []string        `json:"warnings,omitempty"`
			Results             []freeResultRow `json:"results"`
		}{
			SchemaVersion:       schemaVersion,
			Command:             "nextFree",
			ComposeOnly:         state.Degraded,
			SelectedGroup:       selectedGroup.Name,
			SelectedGroupNumber: selectedGroupNumber,
			Warnings:            warnings,
			Results:             rows,
		}
		if err := writeJSON(stdout, payload); err != nil {
			return exitCodeRuntime, err
		}
	} else {
		if err := printNextFreeTable(stdout, rows, groupNumber >= 0); err != nil {
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
	if err := parseNoPositionalArgs(flagSet, args, "sections"); err != nil {
		return exitCodeRuntime, err
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

	config := &Config{
		Groups:     opts.Groups,
		GroupOrder: append([]string(nil), opts.GroupOrder...),
	}
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

	groupRows := make([]sectionRow, 0, len(config.Groups))
	groupNames := orderedGroupNames(config.Groups, config.GroupOrder)
	for _, groupName := range groupNames {
		groupRange := config.Groups[groupName]
		groupRows = append(groupRows, sectionRow{
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
		rows := make([][]string, 0, len(groupRows)+1)
		rows = append(rows, makeHeaders(stdout, "SECTION", "START", "END"))
		for _, row := range groupRows {
			rows = append(rows, []string{
				colorize(stdout, ansiBlue, row.Name),
				colorize(stdout, ansiYellow, row.Start),
				colorize(stdout, ansiGreen, row.End),
			})
		}
		if err := printAlignedRows(stdout, rows); err != nil {
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
