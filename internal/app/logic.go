package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/Puhi8/dockernet/internal/app/terminal"
)

func buildPSRows(composeEntries, dockerEntries []IPEntry, key keyKind) []IPEntry {
	defer terminalOut.PerfStart("Build PS rows")()

	rows := make([]IPEntry, 0, len(composeEntries)+len(dockerEntries))
	dockerIndex := buildPSMatchIndex(
		dockerEntries,
		func(entry IPEntry) string { return makeKey(entry, key) },
		func(entry IPEntry) string { return entry.ContainerName },
	)
	matchedDocker := make([]bool, len(dockerEntries))

	var sortType SortType
	var dedupe func([]IPEntry) []IPEntry
	var preliminaryFactory, fallbackFactory func(IPEntry) func(int) bool
	matchesCompose := func(entry IPEntry) func(int) bool {
		return func(idx int) bool {
			return ownerMatchesCompose(entry.ContainerName, entry.Project, entry.Service, dockerEntries[idx].ContainerName)
		}
	}
	portMatchesConflict := func(entry IPEntry) func(int) bool {
		return func(idx int) bool { return dockerPortConfigMatchesCompose(entry, dockerEntries[idx]) }
	}

	switch key {
	case keyPortBase:
		preliminaryFactory = portMatchesConflict
		fallbackFactory = func(entry IPEntry) func(int) bool {
			return func(idx int) bool { return portMatchesConflict(entry)(idx) && matchesCompose(entry)(idx) }
		}
		sortType = SortPort
		dedupe = dedupePortEntries
	case keyEntry:
		preliminaryFactory = nil
		fallbackFactory = matchesCompose
		sortType = SortIPEntries
		dedupe = dedupePSRows
	default:
		panic("Unsupported key when building ps rows!")
	}
	matchComposeDone := terminalOut.PerfStart("Build PS rows: match compose entries")
	for _, composeEntry := range composeEntries {
		var preliminaryPredicate func(int) bool
		if preliminaryFactory != nil {
			preliminaryPredicate = preliminaryFactory(composeEntry)
		}
		fallbackPredicate := fallbackFactory(composeEntry)
		matched := findPSMatchedDockerIndex(
			dockerIndex,
			matchedDocker,
			makeKey(composeEntry, key),
			composeEntry.ContainerName,
			composeEntry.Project,
			composeEntry.Service,
			preliminaryPredicate,
			fallbackPredicate,
		)

		if matched == -1 {
			rows = append(rows, composeEntry)
			continue
		}
		dockerEntry := dockerEntries[matched]
		matchedDocker[matched] = true
		row := composeEntry
		row.Source = "both"
		row.Running = dockerEntry.Running
		if strings.TrimSpace(row.ContainerName) == "" {
			row.ContainerName = dockerEntry.ContainerName
		}
		if key == keyPortBase {
			if strings.TrimSpace(row.HostIP) == "" {
				row.HostIP = dockerEntry.HostIP
			}
			if row.HostPort == 0 {
				row.HostPort = dockerEntry.HostPort
			}
			if row.Origin == "" {
				row.Origin = dockerEntry.Origin
			} else if row.Origin != dockerEntry.Origin &&
				(row.Origin == "published" || dockerEntry.Origin == "published") {
				row.Origin = "published"
			}
		}
		rows = append(rows, row)
	}
	matchComposeDone()
	finalizeDone := terminalOut.PerfStart("Build PS rows: finalize")
	rows = appendUnmatchedEntries(rows, dockerEntries, matchedDocker)
	rows = dedupe(rows)
	sortEntries(rows, sortType)
	finalizeDone()
	return rows
}

type psMatchIndex struct {
	byKey                map[string][]int
	byKeyByContainerName map[string]map[string][]int
	byKeyByPrefix        map[string]map[string][]int
}

func buildPSMatchIndex[T any](entries []T, keyFn func(T) string, containerNameFn func(T) string) psMatchIndex {
	index := psMatchIndex{
		byKey:                make(map[string][]int),
		byKeyByContainerName: make(map[string]map[string][]int),
		byKeyByPrefix:        make(map[string]map[string][]int),
	}

	for idx, entry := range entries {
		key := keyFn(entry)
		index.byKey[key] = append(index.byKey[key], idx)

		containerName := strings.TrimSpace(containerNameFn(entry))
		if containerName == "" {
			continue
		}

		if _, ok := index.byKeyByContainerName[key]; !ok {
			index.byKeyByContainerName[key] = make(map[string][]int)
		}
		index.byKeyByContainerName[key][containerName] = append(index.byKeyByContainerName[key][containerName], idx)

		for _, prefix := range dockerContainerManagedPrefixes(containerName) {
			if _, ok := index.byKeyByPrefix[key]; !ok {
				index.byKeyByPrefix[key] = make(map[string][]int)
			}
			index.byKeyByPrefix[key][prefix] = append(index.byKeyByPrefix[key][prefix], idx)
		}
	}

	return index
}

func dockerContainerManagedPrefixes(containerName string) []string {
	prefixes := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)
	for _, separator := range []byte{'-', '_'} {
		idx := strings.LastIndexByte(containerName, separator)
		if idx <= 0 || idx >= len(containerName)-1 {
			continue
		}
		suffix := containerName[idx+1:]
		if _, err := strconv.Atoi(suffix); err != nil {
			continue
		}

		prefix := containerName[:idx+1]
		if _, ok := seen[prefix]; !ok {
			seen[prefix] = struct{}{}
			prefixes = append(prefixes, prefix)
		}
	}
	return prefixes
}

func composeManagedPrefixes(project, service string) []string {
	project = strings.TrimSpace(project)
	service = strings.TrimSpace(service)
	if project == "" || service == "" {
		return nil
	}
	return []string{project + "-" + service + "-", project + "_" + service + "_"}
}

func firstUnmatchedIndex(candidates []int, matched []bool, predicate func(int) bool) int {
	for _, idx := range candidates {
		if !matched[idx] && (predicate == nil || predicate(idx)) {
			return idx
		}
	}
	return -1
}

func findPSMatchedDockerIndex(
	index psMatchIndex,
	matched []bool,
	key, composeContainerName, composeProject, composeService string,
	preliminaryPredicate func(int) bool,
	fallbackPredicate func(int) bool,
) int {
	match := -1
	composeContainerName = strings.TrimSpace(composeContainerName)
	if composeContainerName != "" {
		match = firstUnmatchedIndex(index.byKeyByContainerName[key][composeContainerName], matched, preliminaryPredicate)
	} else {
		for _, prefix := range composeManagedPrefixes(composeProject, composeService) {
			match = firstUnmatchedIndex(index.byKeyByPrefix[key][prefix], matched, preliminaryPredicate)
			if match != -1 {
				break
			}
		}
	}
	if match != -1 {
		return match
	}
	if fallbackPredicate == nil {
		fallbackPredicate = preliminaryPredicate
	}
	return firstUnmatchedIndex(index.byKey[key], matched, fallbackPredicate)
}

func appendUnmatchedEntries[T any](rows, entries []T, matched []bool) []T {
	for idx, entry := range entries {
		if !matched[idx] {
			rows = append(rows, entry)
		}
	}
	return rows
}

func dedupePSRows(rows []IPEntry) []IPEntry {
	if len(rows) == 0 {
		return nil
	}

	mergedKeys := make(map[string]struct{})
	for _, row := range rows {
		if row.Source == "both" {
			mergedKeys[makeKey(row, keyPSCompose)] = struct{}{}
		}
	}

	seen := make(map[string]struct{}, len(rows))
	deduped := make([]IPEntry, 0, len(rows))
	for _, row := range rows {
		if row.Source == "compose" {
			if _, exists := mergedKeys[makeKey(row, keyPSCompose)]; exists {
				continue
			}
		}

		key := makeKey(row, keyPSDisplay)
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			deduped = append(deduped, row)
		}
	}
	return deduped
}

func psComposeIdentity(entry IPEntry) string {
	service := strings.TrimSpace(entry.Service)
	project := strings.TrimSpace(entry.Project)
	composeFile := strings.TrimSpace(entry.ComposeFile)
	switch {
	case project != "" && service != "":
		return "project:" + project + "|service:" + service
	case service != "" && composeFile != "":
		return "service:" + service + "|file:" + composeFile
	case service != "":
		return "service:" + service
	case composeFile != "":
		return "file:" + composeFile
	}
	if containerName := strings.TrimSpace(entry.ContainerName); containerName != "" {
		return "container:" + containerName
	}
	return "-"
}

func enrichPSRowsWithPorts(rows []IPEntry, composePorts, dockerPorts []IPEntry, includeProtocol, includeSummaries bool) []IPEntry {
	defer terminalOut.PerfStart("Enrich PS rows with ports")()

	if len(rows) == 0 {
		return rows
	}

	mergedPorts := buildPSRows(composePorts, dockerPorts, keyPortBase)
	if len(mergedPorts) == 0 {
		return rows
	}

	enriched := make([]IPEntry, len(rows))
	copy(enriched, rows)
	attachSummariesDone := terminalOut.PerfStart("Enrich PS rows with ports: attach summaries")
	for idx, row := range enriched {
		summaries := collectPortSummariesForPSRow(row, mergedPorts, includeProtocol)
		if len(summaries) > 0 {
			enriched[idx].HasPorts = true
			if includeSummaries {
				enriched[idx].Ports = summaries
			}
		}
	}
	attachSummariesDone()
	return enriched
}

func collectPortSummariesForPSRow(row IPEntry, ports []IPEntry, includeProtocol bool) []string {
	if len(ports) == 0 {
		return nil
	}

	seen := make(map[string]struct{})
	summaries := make([]string, 0)
	for _, port := range ports {
		if !psRowMatchesPort(row, port) {
			continue
		}
		summary := psPortSummary(port, includeProtocol)
		if _, exists := seen[summary]; exists {
			continue
		}
		seen[summary] = struct{}{}
		summaries = append(summaries, summary)
	}
	sort.Strings(summaries)
	return summaries
}

func psRowMatchesPort(row IPEntry, port IPEntry) bool {
	rowContainer := getEntryName(row)
	portContainer := getEntryName(port)
	if rowContainer != "" && portContainer != "" {
		return rowContainer == portContainer
	}
	if rowContainer != "" {
		return ownerMatchesCompose(port.ContainerName, port.Project, port.Service, rowContainer)
	}

	rowProject := strings.TrimSpace(row.Project)
	rowService := strings.TrimSpace(row.Service)
	if rowProject != "" && rowService != "" {
		sameComposeServiceIdentity := strings.TrimSpace(rowProject) != "" &&
			strings.TrimSpace(rowService) != "" &&
			strings.TrimSpace(rowProject) == strings.TrimSpace(port.Project) &&
			strings.TrimSpace(rowService) == strings.TrimSpace(port.Service)
		if sameComposeServiceIdentity {
			return true
		}
		if portContainer != "" {
			return ownerMatchesCompose("", rowProject, rowService, portContainer)
		}
	}
	return false
}

func psPortSummary(entry IPEntry, includeProtocol bool) string {
	target := strconv.Itoa(entry.ContainerPort)
	if includeProtocol {
		target = fmt.Sprintf("%s/%s", target, normalizePortProtocol(entry.Protocol))
	}
	if !entry.Published {
		return target
	}

	hostPort := "random"
	if entry.HostPort > 0 {
		hostPort = strconv.Itoa(entry.HostPort)
	}
	return hostPort + "->" + target
}

func dedupePortEntries(entries []IPEntry) []IPEntry {
	if len(entries) == 0 {
		return nil
	}

	bestByKey := make(map[string]IPEntry, len(entries))
	for _, entry := range entries {
		key := makeKey(entry, keyPortDisplay)
		current, exists := bestByKey[key]
		if !exists {
			bestByKey[key] = entry
			continue
		}

		entryScore := portSourceScore(entry.Source)
		currentScore := portSourceScore(current.Source)
		if entryScore > currentScore || (entryScore == currentScore && entry.Running && !current.Running) {
			bestByKey[key] = entry
		}
	}

	deduped := make([]IPEntry, 0, len(bestByKey))
	for _, entry := range bestByKey {
		deduped = append(deduped, entry)
	}
	return deduped
}

var portSourceScoreMap = map[string]int{"compose": 1, "docker": 2, "both": 3}

func portSourceScore(source string) int {
	score, ok := portSourceScoreMap[source]
	if ok {
		return score
	}
	return 0
}

func dockerPortConfigMatchesCompose(composeEntry, dockerEntry IPEntry) bool {
	if normalizePortProtocol(composeEntry.Protocol) != normalizePortProtocol(dockerEntry.Protocol) ||
		composeEntry.ContainerPort != dockerEntry.ContainerPort ||
		composeEntry.Published != dockerEntry.Published ||
		(composeEntry.HostPort > 0 && composeEntry.HostPort != dockerEntry.HostPort) {
		return false
	}
	if !composeEntry.Published {
		return true
	}
	composeHostIP := strings.TrimSpace(composeEntry.HostIP)
	if composeHostIP != "" && composeHostIP != strings.TrimSpace(dockerEntry.HostIP) {
		return false
	}
	return true
}

func collectCheckConflicts(
	composeEntries, dockerEntries []IPEntry,
	scopeNetworks map[string]struct{},
	groupName string,
	groupRange *IPRange,
	groups map[string]IPRange,
) []checkConflict {
	defer terminalOut.PerfStart("Collect check conflicts")()
	conflicts := make([]checkConflict, 0)

	filteredCompose := make([]IPEntry, 0, len(composeEntries))
	for _, composeEntry := range composeEntries {
		_, ok := scopeNetworks[composeEntry.Network]
		if ok && !isListOnlyNetwork(composeEntry.Network) {
			filteredCompose = append(filteredCompose, composeEntry)
		}
	}
	terminalOut.PerfStart("Collect check conflicts: filter compose")()

	if groupRange != nil {
		inRangeCompose := make([]IPEntry, 0, len(filteredCompose))
		for _, composeEntry := range filteredCompose {
			ipAddr, err := netip.ParseAddr(composeEntry.IP)
			if err != nil {
				conflicts = append(conflicts, outOfGroupConflict(composeEntry, groupName))
				continue
			}
			if ipInRange(ipAddr, *groupRange) {
				inRangeCompose = append(inRangeCompose, composeEntry)
				continue
			}
			if linkedGroup := findMatchingGroupName(ipAddr, groups, nil); linkedGroup == "" {
				conflicts = append(conflicts, outOfGroupConflict(composeEntry, groupName))
			}
		}
		filteredCompose = inRangeCompose
	} else if len(groups) > 0 {
		inAnyGroupCompose := make([]IPEntry, 0, len(filteredCompose))
		for _, composeEntry := range filteredCompose {
			ipAddr, err := netip.ParseAddr(composeEntry.IP)
			if err != nil || findMatchingGroupName(ipAddr, groups, nil) == "" {
				conflicts = append(conflicts, outOfGroupConflict(composeEntry, "unassigned"))
				continue
			}
			inAnyGroupCompose = append(inAnyGroupCompose, composeEntry)
		}
		filteredCompose = inAnyGroupCompose
	}
	terminalOut.PerfStart("Collect check conflicts: apply group filter")()

	composeByKey := make(map[string][]IPEntry)
	for _, composeEntry := range filteredCompose {
		composeByKey[makeKey(composeEntry, keyEntry)] = append(composeByKey[makeKey(composeEntry, keyEntry)], composeEntry)
	}
	terminalOut.PerfStart("Collect check conflicts: index compose")()

	runningByKey := make(map[string][]IPEntry)
	for _, dockerEntry := range dockerEntries {
		if _, ok := scopeNetworks[dockerEntry.Network]; ok && dockerEntry.Running && !isListOnlyNetwork(dockerEntry.Network) {
			runningByKey[makeKey(dockerEntry, keyEntry)] = append(runningByKey[makeKey(dockerEntry, keyEntry)], dockerEntry)
		}
	}
	terminalOut.PerfStart("Collect check conflicts: index running")()

	duplicateConflictKeys := make(map[string]struct{})
	for key, entries := range composeByKey {
		if len(entries) < 2 {
			continue
		}
		duplicateConflictKeys[key] = struct{}{}
		sortEntries(entries, SortIPEntries)
		names := conflictNamesForKey(key, composeByKey, runningByKey)
		conflicts = append(conflicts, checkConflict{
			Type:    "duplicate_compose_ip",
			Network: entries[0].Network,
			IP:      entries[0].IP,
			Details: []string{fmt.Sprintf("%s", strings.Join(names, ", "))},
		})
	}
	terminalOut.PerfStart("Collect check conflicts: find duplicates")()

	runningConflictKeys := make(map[string]struct{})
	for _, composeEntry := range filteredCompose {
		key := makeKey(composeEntry, keyEntry)
		for _, dockerEntry := range runningByKey[key] {
			if !ownerMatchesCompose(composeEntry.ContainerName, composeEntry.Project, composeEntry.Service, dockerEntry.ContainerName) {
				runningConflictKeys[key] = struct{}{}
				break
			}
		}
	}
	for key := range runningConflictKeys {
		if _, duplicate := duplicateConflictKeys[key]; duplicate {
			continue
		}
		entries := composeByKey[key]
		if len(entries) != 0 {
			sortEntries(entries, SortIPEntries)
			names := conflictNamesForKey(key, composeByKey, runningByKey)
			conflicts = append(conflicts, checkConflict{
				Type:    "running_ip_taken",
				Network: entries[0].Network,
				IP:      entries[0].IP,
				Details: []string{fmt.Sprintf("%s", strings.Join(names, ", "))},
			})
		}
	}
	terminalOut.PerfStart("Collect check conflicts: find running conflicts")()

	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].Type != conflicts[j].Type {
			return conflicts[i].Type < conflicts[j].Type
		}
		if conflicts[i].Network != conflicts[j].Network {
			return conflicts[i].Network < conflicts[j].Network
		}
		return compareIPStrings(conflicts[i].IP, conflicts[j].IP) < 0
	})
	return conflicts
}

func outOfGroupConflict(entry IPEntry, groupName string) checkConflict {
	return checkConflict{
		Type:    "out_of_group",
		Network: entry.Network,
		IP:      entry.IP,
		Details: []string{
			fmt.Sprintf("service=%s", entry.Service),
			fmt.Sprintf("group=%s", groupName),
		},
	}
}

func conflictNamesForKey(key string, composeByKey map[string][]IPEntry, runningByKey map[string][]IPEntry) []string {
	seen := make(map[string]struct{})
	for _, composeEntry := range composeByKey[key] {
		name := getEntryName(composeEntry)
		if name != "" {
			seen[name] = struct{}{}
		}
	}
	for _, dockerEntry := range runningByKey[key] {
		name := getEntryName(dockerEntry)
		if name != "" {
			seen[name] = struct{}{}
		}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func ownerMatchesCompose(composeContainerName, composeProject, composeService, dockerContainerName string) bool {
	dockerContainerName = strings.TrimSpace(dockerContainerName)
	if dockerContainerName == "" {
		return false
	}

	composeContainerName = strings.TrimSpace(composeContainerName)
	if composeContainerName != "" {
		return dockerContainerName == composeContainerName
	}

	for _, prefix := range composeManagedPrefixes(composeProject, composeService) {
		if strings.HasPrefix(dockerContainerName, prefix) {
			suffix := strings.TrimPrefix(dockerContainerName, prefix)
			if _, err := strconv.Atoi(suffix); err == nil {
				return true
			}
		}
	}
	return false
}

func buildUsedIPv4ByNetwork(composeEntries, dockerEntries []IPEntry) map[string]map[string]struct{} {
	used := make(map[string]map[string]struct{})
	add := func(network, ip string) {
		if strings.TrimSpace(network) == "" || strings.TrimSpace(ip) == "" || ip == "host" || isListOnlyNetwork(network) {
			return
		}
		if _, err := netip.ParseAddr(ip); err != nil {
			return
		}
		if _, ok := used[network]; !ok {
			used[network] = make(map[string]struct{})
		}
		used[network][ip] = struct{}{}
	}

	for _, entry := range composeEntries {
		if entry.IPVersion == 4 {
			add(entry.Network, entry.IP)
		}
	}
	for _, entry := range dockerEntries {
		if entry.IPVersion == 4 {
			add(entry.Network, entry.IP)
		}
	}
	return used
}

func collectFreeAddresses(ipRange IPRange, used map[string]struct{}, limit int) []netip.Addr {
	if limit <= 0 {
		return nil
	}
	if used == nil {
		used = make(map[string]struct{})
	}

	result := make([]netip.Addr, 0, limit)
	for addr := ipRange.Start; ; addr = addr.Next() {
		if addr.Is4() && !isReservedIPv4(addr) {
			if _, exists := used[addr.String()]; !exists {
				result = append(result, addr)
				if len(result) == limit {
					return result
				}
			}
		}

		if addr == ipRange.End {
			break
		}
	}
	return result
}

func addrStrings(addrs []netip.Addr) []string {
	result := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		result = append(result, addr.String())
	}
	return result
}

func isReservedIPv4(addr netip.Addr) bool {
	if !addr.Is4() {
		return false
	}
	octets := addr.As4()
	return octets[3] == 0 || octets[3] == 255
}

func resolveScopedNetworks(explicit string, configured []string, discovered []string, includeHost bool) []string {
	if strings.TrimSpace(explicit) != "" {
		candidate := strings.TrimSpace(explicit)
		if (!includeHost && isListOnlyNetwork(candidate)) || candidate == "none" {
			return nil
		}
		return []string{candidate}
	}

	base := discovered
	if len(configured) > 0 {
		base = configured
	}

	scope := make([]string, 0, len(base))
	for _, network := range base {
		network = strings.TrimSpace(network)
		if network != "" && network != "none" && (includeHost || !isListOnlyNetwork(network)) {
			scope = append(scope, network)
		}
	}
	return dedupeStrings(scope)
}

func isListOnlyNetwork(network string) bool {
	return strings.TrimSpace(network) == "host"
}

type namedGroupRange struct {
	name string
	rng  IPRange
}

func validateGroupOverlaps(groups map[string]IPRange) []string {
	errorsList := make([]string, 0)
	ipv4Ranges := make([]namedGroupRange, 0, len(groups))
	ipv6Ranges := make([]namedGroupRange, 0, len(groups))
	for name, groupRange := range groups {
		namedRange := namedGroupRange{name: name, rng: groupRange}
		if groupRange.Start.Is4() {
			ipv4Ranges = append(ipv4Ranges, namedRange)
			continue
		}
		ipv6Ranges = append(ipv6Ranges, namedRange)
	}

	errorsList = append(errorsList, collectGroupOverlapErrors(ipv4Ranges)...)
	errorsList = append(errorsList, collectGroupOverlapErrors(ipv6Ranges)...)
	sort.Strings(errorsList)
	return errorsList
}

func collectGroupOverlapErrors(ranges []namedGroupRange) []string {
	if len(ranges) < 2 {
		return nil
	}

	sort.Slice(ranges, func(i, j int) bool {
		if cmp := ranges[i].rng.Start.Compare(ranges[j].rng.Start); cmp != 0 {
			return cmp < 0
		}
		if cmp := ranges[i].rng.End.Compare(ranges[j].rng.End); cmp != 0 {
			return cmp < 0
		}
		return ranges[i].name < ranges[j].name
	})

	errorsList := make([]string, 0)
	for i, current := range ranges {
		for _, candidate := range ranges[i+1:] {
			if candidate.rng.Start.Compare(current.rng.End) > 0 {
				break
			}
			if rangesOverlap(current.rng, candidate.rng) {
				errorsList = append(errorsList, fmt.Sprintf("overlap: %s (%s-%s) with %s (%s-%s)",
					current.name, current.rng.Start, current.rng.End,
					candidate.name, candidate.rng.Start, candidate.rng.End))
			}
		}
	}
	return errorsList
}

func rangesOverlap(a, b IPRange) bool {
	return a.Start.Compare(b.End) <= 0 && b.Start.Compare(a.End) <= 0
}

func compareIPStrings(a, b string) int {
	addrA, errA := netip.ParseAddr(a)
	addrB, errB := netip.ParseAddr(b)
	switch {
	case errA == nil && errB == nil:
		return addrA.Compare(addrB)
	case errA == nil:
		return -1
	case errB == nil:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

func emitDiscoveryWarnings(stderr io.Writer, state *discoveryResult, quiet bool, jsonOutput bool) {
	if jsonOutput {
		return
	}
	for _, warning := range state.Warnings {
		lower := strings.ToLower(warning)
		if !quiet || strings.Contains(lower, "docker unavailable") {
			fmt.Fprintln(stderr, terminalOut.WarningLine(stderr, warning))
		}
	}
}

func writeJSON(w io.Writer, payload any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(payload)
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; !exists {
			seen[trimmed] = struct{}{}
			result = append(result, trimmed)
		}
	}
	sort.Strings(result)
	return result
}

func resolveHostNetworkIPs(rows []IPEntry) []IPEntry {
	hostIPv4 := detectLocalIPv4()
	if hostIPv4 == "" {
		return rows
	}
	resolved := make([]IPEntry, len(rows))
	copy(resolved, rows)
	for idx := range resolved {
		if !strings.EqualFold(strings.TrimSpace(resolved[idx].Network), "host") {
			continue
		}
		resolved[idx].IP = hostIPv4
		if resolved[idx].IPVersion == 0 {
			resolved[idx].IPVersion = 4
		}
	}
	return resolved
}

func detectLocalIPv4() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	var fallback string
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || isContainerBridgeInterface(iface.Name) {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			prefix, err := netip.ParsePrefix(addr.String())
			if err != nil {
				continue
			}
			candidate := prefix.Addr()
			if !candidate.Is4() || !candidate.IsGlobalUnicast() {
				continue
			}
			if isPrivateIPv4(candidate) {
				return candidate.String()
			}
			if fallback == "" {
				fallback = candidate.String()
			}
		}
	}
	return fallback
}

func isContainerBridgeInterface(ifaceName string) bool {
	name := strings.ToLower(strings.TrimSpace(ifaceName))
	if name == "" {
		return false
	}
	for _, prefix := range []string{"docker", "br-", "veth", "cni", "podman", "virbr"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func isPrivateIPv4(addr netip.Addr) bool {
	if !addr.Is4() {
		return false
	}
	octets := addr.As4()
	return (octets[0] == 10 ||
		(octets[0] == 192 && octets[1] == 168) ||
		(octets[0] == 172 && octets[1] >= 16 && octets[1] <= 31))
}

type keyKind uint8

const (
	keyEntry keyKind = iota
	keyPSCompose
	keyPSDisplay
	keyPortBase
	keyPortDisplay
)

func makeKey(entry IPEntry, kind keyKind) string {
	basic := func() string {
		return fmt.Sprintf("%s|%d|%s", entry.Network, entry.IPVersion, entry.IP)
	}
	portBasic := func() string {
		return fmt.Sprintf("%s|%d|%t", normalizePortProtocol(entry.Protocol), entry.ContainerPort, entry.Published)
	}
	display := func() string {
		return fmt.Sprintf("%s|%s|%s", strings.TrimSpace(entry.Service), strings.TrimSpace(entry.Project), strings.TrimSpace(entry.ComposeFile))
	}
	switch kind {
	case keyEntry:
		return basic()
	case keyPSCompose:
		return fmt.Sprintf("%s|%s", basic(), psComposeIdentity(entry))
	case keyPSDisplay:
		return fmt.Sprintf("%s|%s|%t|%s|%s",
			basic(),
			getEntryName(entry),
			entry.Running,
			entry.Source,
			display(),
		)
	case keyPortBase:
		return portBasic()
	case keyPortDisplay:
		return fmt.Sprintf(
			"%s|%s|%s|%s|%d|%s",
			getEntryName(entry),
			display(),
			portBasic(),
			strings.TrimSpace(entry.HostIP),
			entry.HostPort,
			strings.TrimSpace(entry.Origin),
		)
	default:
		panic(fmt.Sprintf("unsupported key kind: %d", kind))
	}
}
