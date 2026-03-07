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
)

func buildPSRows(composeEntries, dockerEntries []IPEntry) []IPEntry {
	rows := make([]IPEntry, 0, len(composeEntries)+len(dockerEntries))

	dockerIndex := buildDockerPSMatchIndex(dockerEntries)
	matchedDocker := make([]bool, len(dockerEntries))

	for _, composeEntry := range composeEntries {
		key := entryKey(composeEntry)
		matched := -1

		composeContainerName := strings.TrimSpace(composeEntry.ContainerName)
		if composeContainerName != "" {
			matched = firstUnmatchedDockerIndex(dockerIndex.byKeyByContainerName[key][composeContainerName], matchedDocker)
		} else {
			for _, prefix := range composeManagedPrefixes(composeEntry) {
				matched = firstUnmatchedDockerIndex(dockerIndex.byKeyByPrefix[key][prefix], matchedDocker)
				if matched != -1 {
					break
				}
			}
		}

		if matched == -1 {
			for _, dockerIdx := range dockerIndex.byKey[key] {
				if !matchedDocker[dockerIdx] && dockerMatchesCompose(composeEntry, dockerEntries[dockerIdx]) {
					matched = dockerIdx
					break
				}
			}
		}

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
		rows = append(rows, row)
	}

	for idx, dockerEntry := range dockerEntries {
		if !matchedDocker[idx] {
			rows = append(rows, dockerEntry)
		}
	}

	rows = dedupePSRows(rows)
	sortIPEntries(rows)
	return rows
}

type dockerPSMatchIndex struct {
	byKey                map[string][]int
	byKeyByContainerName map[string]map[string][]int
	byKeyByPrefix        map[string]map[string][]int
}

func buildDockerPSMatchIndex(dockerEntries []IPEntry) dockerPSMatchIndex {
	index := dockerPSMatchIndex{
		byKey:                make(map[string][]int),
		byKeyByContainerName: make(map[string]map[string][]int),
		byKeyByPrefix:        make(map[string]map[string][]int),
	}

	for idx, dockerEntry := range dockerEntries {
		key := entryKey(dockerEntry)
		index.byKey[key] = append(index.byKey[key], idx)

		containerName := strings.TrimSpace(dockerEntry.ContainerName)
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

func composeManagedPrefixes(entry IPEntry) []string {
	project := strings.TrimSpace(entry.Project)
	service := strings.TrimSpace(entry.Service)
	if project == "" || service == "" {
		return nil
	}
	return []string{project + "-" + service + "-", project + "_" + service + "_"}
}

func firstUnmatchedDockerIndex(candidates []int, matched []bool) int {
	for _, idx := range candidates {
		if !matched[idx] {
			return idx
		}
	}
	return -1
}

func dedupePSRows(rows []IPEntry) []IPEntry {
	if len(rows) == 0 {
		return nil
	}

	// If we already have a merged "both" row for the same compose identity/network/ip, skip compose-only duplicates.
	mergedKeys := make(map[string]struct{})
	for _, row := range rows {
		if row.Source == "both" {
			mergedKeys[psComposeKey(row)] = struct{}{}
		}
	}

	seen := make(map[string]struct{}, len(rows))
	deduped := make([]IPEntry, 0, len(rows))
	for _, row := range rows {
		if row.Source == "compose" {
			if _, exists := mergedKeys[psComposeKey(row)]; exists {
				continue
			}
		}

		key := psDisplayKey(row)
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			deduped = append(deduped, row)
		}
	}
	return deduped
}

func psComposeKey(entry IPEntry) string {
	return fmt.Sprintf("%s|%d|%s|%s", entry.Network, entry.IPVersion, entry.IP, psComposeIdentity(entry))
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

func psDisplayKey(entry IPEntry) string {
	name := strings.TrimSpace(entry.ContainerName)
	if name == "" {
		name = strings.TrimSpace(entry.Service)
	}
	return fmt.Sprintf("%s|%d|%s|%s|%t|%s|%s|%s|%s",
		entry.Network,
		entry.IPVersion,
		entry.IP,
		name,
		entry.Running,
		entry.Source,
		strings.TrimSpace(entry.Service),
		strings.TrimSpace(entry.Project),
		strings.TrimSpace(entry.ComposeFile),
	)
}

func psEntryName(entry IPEntry) string {
	name := strings.TrimSpace(entry.ContainerName)
	if name == "" {
		name = strings.TrimSpace(entry.Service)
	}
	if name == "" {
		return "-"
	}
	return name
}

func sortPSRows(entries []IPEntry, style string) {
	psSortName := func(entry IPEntry) string {
		name := strings.TrimSpace(entry.ContainerName)
		if name == "" {
			name = strings.TrimSpace(entry.Service)
		}
		return name
	}
	sort.Slice(entries, func(i, j int) bool {
		nameI := psSortName(entries[i])
		nameJ := psSortName(entries[j])
		cmpName := func() (bool, bool) {
			if nameI != nameJ {
				return nameI < nameJ, true
			}
			return false, false
		}
		cmpNet := func() (bool, bool) {
			if entries[i].Network != entries[j].Network {
				return entries[i].Network < entries[j].Network, true
			}
			return false, false
		}
		cmpIP := func() (bool, bool) {
			if cmp := compareIPStrings(entries[i].IP, entries[j].IP); cmp != 0 {
				return cmp < 0, true
			}
			return false, false
		}
		cmpRegular := func() (bool, bool) {
			if entries[i].Running != entries[j].Running {
				return entries[i].Running && !entries[j].Running, true
			}
			if entries[i].Source != entries[j].Source {
				return entries[i].Source < entries[j].Source, true
			}
			if entries[i].Project != entries[j].Project {
				return entries[i].Project < entries[j].Project, true
			}
			if entries[i].ComposeFile != entries[j].ComposeFile {
				return entries[i].ComposeFile < entries[j].ComposeFile, true
			}
			return false, false
		}

		var order []func() (bool, bool)
		switch style {
		case "name":
			order = []func() (bool, bool){cmpName, cmpNet, cmpIP, cmpRegular}
		case "ip":
			order = []func() (bool, bool){cmpIP, cmpNet, cmpName, cmpRegular}
		default:
			order = []func() (bool, bool){cmpIP, cmpNet, cmpName, cmpRegular}
		}
		for _, cmp := range order {
			if res, ok := cmp(); ok {
				return res
			}
		}
		return false
	})
}

func collectCheckConflicts(
	composeEntries,
	dockerEntries []IPEntry,
	scopeNetworks map[string]struct{},
	groupName string,
	groupRange *IPRange,
	groups map[string]IPRange,
) []checkConflict {
	conflicts := make([]checkConflict, 0)

	filteredCompose := make([]IPEntry, 0, len(composeEntries))
	for _, composeEntry := range composeEntries {
		_, ok := scopeNetworks[composeEntry.Network]
		if ok && !isListOnlyNetwork(composeEntry.Network) {
			filteredCompose = append(filteredCompose, composeEntry)
		}
	}

	if groupRange != nil {
		inRangeCompose := make([]IPEntry, 0, len(filteredCompose))
		for _, composeEntry := range filteredCompose {
			ipAddr, err := netip.ParseAddr(composeEntry.IP)
			if err != nil {
				conflicts = append(conflicts, outOfGroupConflict(composeEntry, groupName))
				continue
			}
			if ipAddr.Is4() == groupRange.Start.Is4() &&
				ipAddr.Compare(groupRange.Start) >= 0 &&
				ipAddr.Compare(groupRange.End) <= 0 {
				inRangeCompose = append(inRangeCompose, composeEntry)
				continue
			}
			if linkedGroup := findMatchingGroupName(ipAddr, groups); linkedGroup == "" {
				conflicts = append(conflicts, outOfGroupConflict(composeEntry, groupName))
			}
		}
		filteredCompose = inRangeCompose
	} else if len(groups) > 0 {
		inAnyGroupCompose := make([]IPEntry, 0, len(filteredCompose))
		for _, composeEntry := range filteredCompose {
			ipAddr, err := netip.ParseAddr(composeEntry.IP)
			if err != nil {
				conflicts = append(conflicts, outOfGroupConflict(composeEntry, "unassigned"))
				continue
			}
			if findMatchingGroupName(ipAddr, groups) == "" {
				conflicts = append(conflicts, outOfGroupConflict(composeEntry, "unassigned"))
				continue
			}
			inAnyGroupCompose = append(inAnyGroupCompose, composeEntry)
		}
		filteredCompose = inAnyGroupCompose
	}

	composeByKey := make(map[string][]IPEntry)
	for _, composeEntry := range filteredCompose {
		composeByKey[entryKey(composeEntry)] = append(composeByKey[entryKey(composeEntry)], composeEntry)
	}

	runningByKey := make(map[string][]IPEntry)
	for _, dockerEntry := range dockerEntries {
		if _, ok := scopeNetworks[dockerEntry.Network]; ok &&
			dockerEntry.Running &&
			!isListOnlyNetwork(dockerEntry.Network) {
			runningByKey[entryKey(dockerEntry)] = append(runningByKey[entryKey(dockerEntry)], dockerEntry)
		}
	}

	duplicateConflictKeys := make(map[string]struct{})
	for key, entries := range composeByKey {
		if len(entries) < 2 {
			continue
		}
		duplicateConflictKeys[key] = struct{}{}
		sortIPEntries(entries)
		names := conflictNamesForKey(key, composeByKey, runningByKey)
		conflicts = append(conflicts, checkConflict{
			Type:    "duplicate_compose_ip",
			Network: entries[0].Network,
			IP:      entries[0].IP,
			Details: []string{fmt.Sprintf("%s", strings.Join(names, ", "))},
		})
	}

	runningConflictKeys := make(map[string]struct{})
	for _, composeEntry := range filteredCompose {
		key := entryKey(composeEntry)
		for _, dockerEntry := range runningByKey[key] {
			if !dockerMatchesCompose(composeEntry, dockerEntry) {
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
			sortIPEntries(entries)
			names := conflictNamesForKey(key, composeByKey, runningByKey)
			conflicts = append(conflicts, checkConflict{
				Type:    "running_ip_taken",
				Network: entries[0].Network,
				IP:      entries[0].IP,
				Details: []string{fmt.Sprintf("%s", strings.Join(names, ", "))},
			})
		}
	}

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

func findMatchingGroupName(addr netip.Addr, groups map[string]IPRange) string {
	for name, group := range groups {
		if addr.Is4() == group.Start.Is4() && (addr.Compare(group.Start) >= 0 && addr.Compare(group.End) <= 0) {
			return name
		}
	}
	return ""
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
		name := strings.TrimSpace(composeEntry.ContainerName)
		if name == "" {
			name = strings.TrimSpace(composeEntry.Service)
		}
		if name != "" {
			seen[name] = struct{}{}
		}
	}
	for _, dockerEntry := range runningByKey[key] {
		name := strings.TrimSpace(dockerEntry.ContainerName)
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

func dockerMatchesCompose(composeEntry, dockerEntry IPEntry) bool {
	dockerContainerName := strings.TrimSpace(dockerEntry.ContainerName)
	if dockerContainerName == "" {
		return false
	}

	if composeContainerName := strings.TrimSpace(composeEntry.ContainerName); composeContainerName != "" {
		return dockerContainerName == composeContainerName
	}

	project := strings.TrimSpace(composeEntry.Project)
	service := strings.TrimSpace(composeEntry.Service)
	if project == "" || service == "" {
		return false
	}

	for _, prefix := range []string{
		project + "-" + service + "-",
		project + "_" + service + "_",
	} {
		if !strings.HasPrefix(dockerContainerName, prefix) {
			continue
		}
		suffix := strings.TrimPrefix(dockerContainerName, prefix)
		if _, err := strconv.Atoi(suffix); err == nil {
			return true
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

func compressIPv4RangeStrings(ips []string) []string {
	if len(ips) == 0 {
		return nil
	}

	addrs := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		addr, err := netip.ParseAddr(ip)
		if err == nil && addr.Is4() {
			addrs = append(addrs, addr)
		}
	}
	if len(addrs) == 0 {
		return nil
	}

	sort.Slice(addrs, func(i, j int) bool { return addrs[i].Compare(addrs[j]) < 0 })

	compressed := make([]string, 0)
	rangeStart := addrs[0]
	rangeEnd := addrs[0]

	flushRange := func() {
		if rangeStart == rangeEnd {
			compressed = append(compressed, rangeStart.String())
			return
		}
		compressed = append(compressed, rangeStart.String()+"-"+rangeEnd.String())
	}

	for idx := 1; idx < len(addrs); idx++ {
		current := addrs[idx]
		if current == rangeEnd.Next() {
			rangeEnd = current
			continue
		}
		flushRange()
		rangeStart = current
		rangeEnd = current
	}
	flushRange()
	return compressed
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
	for i := 0; i < len(ranges); i++ {
		current := ranges[i]
		for j := i + 1; j < len(ranges); j++ {
			candidate := ranges[j]
			if candidate.rng.Start.Compare(current.rng.End) > 0 {
				break
			}
			if rangesOverlap(current.rng, candidate.rng) {
				errorsList = append(errorsList, fmt.Sprintf("overlap: %s (%s-%s) with %s (%s-%s)",
					current.name, current.rng.Start, current.rng.End, candidate.name, candidate.rng.Start, candidate.rng.End))
			}
		}
	}
	return errorsList
}

func rangesOverlap(a, b IPRange) bool {
	return a.Start.Compare(b.End) <= 0 && b.Start.Compare(a.End) <= 0
}

func sortIPEntries(entries []IPEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Network != entries[j].Network {
			return entries[i].Network < entries[j].Network
		}
		if entries[i].IPVersion != entries[j].IPVersion {
			return entries[i].IPVersion < entries[j].IPVersion
		}
		if cmp := compareIPStrings(entries[i].IP, entries[j].IP); cmp != 0 {
			return cmp < 0
		}
		if entries[i].Source != entries[j].Source {
			return entries[i].Source < entries[j].Source
		}
		nameI := entries[i].ContainerName
		if strings.TrimSpace(nameI) == "" {
			nameI = entries[i].Service
		}
		nameJ := entries[j].ContainerName
		if strings.TrimSpace(nameJ) == "" {
			nameJ = entries[j].Service
		}
		if nameI != nameJ {
			return nameI < nameJ
		}
		if entries[i].Project != entries[j].Project {
			return entries[i].Project < entries[j].Project
		}
		return entries[i].ComposeFile < entries[j].ComposeFile
	})
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

func entryKey(entry IPEntry) string {
	return fmt.Sprintf("%s|%d|%s", entry.Network, entry.IPVersion, entry.IP)
}

func emitDiscoveryWarnings(stderr io.Writer, state *discoveryResult, quiet bool, jsonOutput bool) {
	if jsonOutput {
		return
	}
	for _, warning := range state.Warnings {
		lower := strings.ToLower(warning)
		if !quiet || strings.Contains(lower, "docker unavailable") {
			fmt.Fprintln(stderr, warningLine(stderr, warning))
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
	switch {
	case octets[0] == 10:
		return true
	case octets[0] == 172 && octets[1] >= 16 && octets[1] <= 31:
		return true
	case octets[0] == 192 && octets[1] == 168:
		return true
	default:
		return false
	}
}
