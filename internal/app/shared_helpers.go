package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/Puhi8/dockernet/internal/app/terminal"
)

const groupNumberFlagUsage = "group number (0-based, config order)"

func addGroupSelectionFlags(flagSet *flag.FlagSet, groupName *string, groupNumber *int, groupUsage string) {
	terminalOut.AddFlag(flagSet, groupName, "g", "group", "", groupUsage)
	terminalOut.AddFlag(flagSet, groupNumber, "gn", "group-number", -1, groupNumberFlagUsage)
}

func parseNoPositionalArgs(flagSet *flag.FlagSet, args []string, commandName string) error {
	if err := flagSet.Parse(args); err != nil {
		return err
	}
	if len(flagSet.Args()) > 0 {
		return fmt.Errorf("unexpected args for %s: %v", commandName, flagSet.Args())
	}
	return nil
}

func discoverStateAndEmitWarnings(ctx context.Context, opts runtimeOptions, stderr io.Writer) (*discoveryResult, error) {
	state, err := discoverState(ctx, opts)
	if err != nil {
		return nil, err
	}
	emitDiscoveryWarnings(stderr, state, opts.Quiet, opts.JSON)
	return state, nil
}

func nextFreeValueLabel(w io.Writer, ips []string) string {
	if len(ips) == 0 {
		return terminalOut.Colorize(w, terminalOut.ANSIGray, "-")
	}
	return terminalOut.Colorize(w, terminalOut.ANSIGreen, strings.Join(ips, ", "))
}

func makeHeaders(w io.Writer, titles ...string) []string {
	headers := make([]string, 0, len(titles))
	for _, title := range titles {
		headers = append(headers, terminalOut.Colorize(w, terminalOut.ANSICyan, title))
	}
	return headers
}

func printNextFreeTable(w io.Writer, rows []freeResultRow, singleGroupView bool) error {
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if !singleGroupView {
		headers := makeHeaders(w, "GROUP", "NETWORK", "NEXT_FREE")
		fmt.Fprintf(table, "%s\t%s\t%s\n", headers[0], headers[1], headers[2])
	}
	for _, row := range rows {
		if singleGroupView {
			fmt.Fprintf(table, "%s\t%s\n",
				terminalOut.Colorize(w, terminalOut.ANSICyan, row.Network),
				nextFreeValueLabel(w, row.IPs),
			)
			continue
		}
		fmt.Fprintf(table, "%s\t%s\t%s\n",
			terminalOut.Colorize(w, terminalOut.ANSIBlue, row.Group),
			terminalOut.Colorize(w, terminalOut.ANSICyan, row.Network),
			nextFreeValueLabel(w, row.IPs),
		)
	}
	return table.Flush()
}

type groupSelection struct {
	Name     string
	Number   int
	Explicit bool
}

type sectionRow struct {
	Name  string `json:"name"`
	Start string `json:"start"`
	End   string `json:"end"`
}

func selectedGroupNumberPointer(selected groupSelection) *int {
	if !selected.Explicit || selected.Number < 0 {
		return nil
	}
	return &selected.Number
}

func selectedGroupRange(selected groupSelection, groups map[string]IPRange) *IPRange {
	if !selected.Explicit {
		return nil
	}
	groupRange, ok := groups[selected.Name]
	if !ok {
		return nil
	}
	return &groupRange
}

func selectedOrOrderedGroupNames(selected groupSelection, groups map[string]IPRange, configOrder []string) []string {
	if selected.Explicit {
		return []string{selected.Name}
	}
	return orderedGroupNames(groups, configOrder)
}

func resolveGroupSelection(groupName string, groupNumber int, groups map[string]IPRange, configOrder []string) (groupSelection, error) {
	selected := groupSelection{Number: -1}
	groupName = strings.TrimSpace(groupName)

	if groupName != "" && groupNumber != -1 {
		return selected, errors.New("use either --group or --group-number, not both")
	}
	if groupName == "" && groupNumber == -1 {
		return selected, nil
	}
	ordered := orderedGroupNames(groups, configOrder)
	if groupName != "" {
		if _, ok := groups[groupName]; !ok {
			return selected, fmt.Errorf("group %q not found", groupName)
		}
		selected.Name = groupName
		selected.Number = indexOfString(ordered, groupName)
		selected.Explicit = true
		return selected, nil
	}
	if groupNumber < 0 {
		return selected, errors.New("group number must be >= 0")
	}
	if len(ordered) == 0 {
		return selected, errors.New("no groups configured")
	}
	if groupNumber >= len(ordered) {
		return selected, fmt.Errorf("group number %d out of range (valid: 0-%d)", groupNumber, len(ordered)-1)
	}

	selected.Name = ordered[groupNumber]
	selected.Number = groupNumber
	selected.Explicit = true
	return selected, nil
}

func getEntryName(entry IPEntry) string {
	name := strings.TrimSpace(entry.ContainerName)
	if name == "" {
		name = strings.TrimSpace(entry.Service)
	}
	return name
}

func printSelectedGroupLine(w io.Writer, selected groupSelection) {
	if !selected.Explicit {
		return
	}
	label := selected.Name
	if selected.Number >= 0 {
		label = fmt.Sprintf("%s (#%d)", selected.Name, selected.Number)
	}
	fmt.Fprintf(w, "%s %s\n", terminalOut.Colorize(w, terminalOut.ANSIBlue, "group:"), terminalOut.Colorize(w, terminalOut.ANSIMagenta, label))
}

func indexOfString(values []string, needle string) int {
	for idx, value := range values {
		if value == needle {
			return idx
		}
	}
	return -1
}

func orderedGroupNames(groups map[string]IPRange, configOrder []string) []string {
	if len(groups) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(groups))
	ordered := make([]string, 0, len(groups))
	for _, name := range configOrder {
		_, ok := groups[name]
		_, exists := seen[name]
		if !ok || exists {
			continue
		}
		seen[name] = struct{}{}
		ordered = append(ordered, name)
	}

	extras := make([]string, 0, len(groups)-len(ordered))
	for name := range groups {
		if _, exists := seen[name]; !exists {
			extras = append(extras, name)
		}
	}
	sort.Strings(extras)
	return append(ordered, extras...)
}

func findMatchingGroupName(addr netip.Addr, groups map[string]IPRange, orderedGroups []string) string {
	if len(orderedGroups) > 0 {
		for _, name := range orderedGroups {
			group, ok := groups[name]
			if ok && ipInRange(addr, group) {
				return name
			}
		}
	} else {
		for name, group := range groups {
			if ipInRange(addr, group) {
				return name
			}
		}
	}
	return ""
}

func ipInRange(addr netip.Addr, groupRange IPRange) bool {
	return addr.Is4() == groupRange.Start.Is4() && addr.Compare(groupRange.Start) >= 0 && addr.Compare(groupRange.End) <= 0
}

type SortType uint8

const (
	SortName SortType = iota
	SortIP
	SortIPEntries
	SortPort
)

var sortByStringMap = map[string]SortType{"name": SortName, "ip": SortIP, "ip_entries": SortIPEntries, "port": SortPort}

func sortEntries(entries []IPEntry, style SortType) {
	compareByString := func(left, right string) (bool, bool) {
		if left != right {
			return left < right, true
		}
		return false, false
	}

	sort.Slice(entries, func(i, j int) bool {
		left, right := entries[i], entries[j]
		cmpName := func() (bool, bool) {
			return compareByString(getEntryName(left), getEntryName(right))
		}
		cmpService := func() (bool, bool) {
			return compareByString(strings.TrimSpace(left.Service), strings.TrimSpace(right.Service))
		}
		cmpNetwork := func() (bool, bool) {
			return compareByString(left.Network, right.Network)
		}
		cmpIP := func() (bool, bool) {
			if cmp := compareIPStrings(left.IP, right.IP); cmp != 0 {
				return cmp < 0, true
			}
			return false, false
		}
		cmpIPVersion := func() (bool, bool) {
			if left.IPVersion != right.IPVersion {
				return left.IPVersion < right.IPVersion, true
			}
			return false, false
		}
		cmpRunning := func() (bool, bool) {
			if left.Running != right.Running {
				return left.Running && !right.Running, true
			}
			return false, false
		}
		cmpSource := func() (bool, bool) {
			return compareByString(left.Source, right.Source)
		}
		cmpProject := func() (bool, bool) {
			return compareByString(left.Project, right.Project)
		}
		cmpComposeFile := func() (bool, bool) {
			return compareByString(left.ComposeFile, right.ComposeFile)
		}
		cmpContainerPort := func() (bool, bool) {
			if left.ContainerPort != right.ContainerPort {
				return left.ContainerPort < right.ContainerPort, true
			}
			return false, false
		}
		cmpProtocol := func() (bool, bool) {
			return compareByString(normalizePortProtocol(left.Protocol), normalizePortProtocol(right.Protocol))
		}
		cmpPublished := func() (bool, bool) {
			if left.Published != right.Published {
				return left.Published && !right.Published, true
			}
			return false, false
		}
		cmpHostIP := func() (bool, bool) {
			return compareByString(strings.TrimSpace(left.HostIP), strings.TrimSpace(right.HostIP))
		}
		cmpHostPort := func() (bool, bool) {
			if left.HostPort != right.HostPort {
				return left.HostPort < right.HostPort, true
			}
			return false, false
		}
		cmpOrigin := func() (bool, bool) {
			return compareByString(strings.TrimSpace(left.Origin), strings.TrimSpace(right.Origin))
		}

		var compares []func() (bool, bool)
		switch style {
		case SortName:
			compares = []func() (bool, bool){cmpName, cmpNetwork, cmpIP, cmpRunning, cmpSource, cmpProject, cmpComposeFile}
		case SortIP:
			compares = []func() (bool, bool){cmpIP, cmpNetwork, cmpName, cmpRunning, cmpSource, cmpProject, cmpComposeFile}
		case SortIPEntries:
			compares = []func() (bool, bool){cmpNetwork, cmpIPVersion, cmpIP, cmpSource, cmpName, cmpRunning, cmpProject, cmpComposeFile}
		case SortPort:
			compares = []func() (bool, bool){cmpProject, cmpService, cmpContainerPort, cmpProtocol, cmpPublished, cmpHostIP, cmpHostPort, cmpOrigin, cmpSource, cmpName, cmpComposeFile}
		default:
			compares = []func() (bool, bool){cmpIP, cmpNetwork, cmpName, cmpRunning, cmpSource, cmpProject, cmpComposeFile}
		}
		for _, cmp := range compares {
			if result, ok := cmp(); ok {
				return result
			}
		}
		return false
	})
}

func normalizePortProtocol(raw string) string {
	protocol := strings.ToLower(strings.TrimSpace(raw))
	if protocol == "" {
		return "tcp"
	}
	return protocol
}
