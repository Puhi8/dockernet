package app

import (
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func parseComposeFile(path string, includeIPv6 bool) (composeParseResult, error) {
	var result composeParseResult

	data, err := os.ReadFile(path)
	if err != nil {
		return result, err
	}
	if !hasTopLevelServicesKey(data) {
		return result, nil
	}

	composeDir := filepath.Dir(path)
	dotenv, _ := loadDotEnvFile(composeDir)
	interpolated := interpolateComposeEnv(string(data), dotenv)

	var root yaml.Node
	if err := yaml.Unmarshal([]byte(interpolated), &root); err != nil {
		return result, err
	}
	if len(root.Content) == 0 {
		return result, nil
	}

	document := root.Content[0]
	if document.Kind != yaml.MappingNode {
		return result, nil
	}

	project := resolveComposeProjectName(document, composeDir, dotenv)
	networkAliases := parseComposeNetworkAliases(document)
	for _, resolvedName := range networkAliases {
		if strings.TrimSpace(resolvedName) != "" {
			result.Networks = append(result.Networks, resolvedName)
		}
	}

	servicesNode := yamlMapLookup(document, "services")
	if servicesNode == nil || servicesNode.Kind != yaml.MappingNode {
		result.Networks = dedupeStrings(result.Networks)
		result.VolumePaths = dedupeStrings(result.VolumePaths)
		return result, nil
	}
	result.IsCompose = true

	for serviceName, serviceNode := range yamlMapPairs(servicesNode) {
		containerName := strings.TrimSpace(yamlScalar(yamlMapLookup(serviceNode, "container_name")))
		result.VolumePaths = append(result.VolumePaths, extractVolumeHostPaths(serviceNode, composeDir)...)

		for _, networkRef := range parseServiceNetworkRefs(serviceNode) {
			resolvedNetwork := networkRef.Name
			if aliasName, ok := networkAliases[networkRef.Name]; ok {
				resolvedNetwork = aliasName
			}
			resolvedNetwork = strings.TrimSpace(resolvedNetwork)
			if resolvedNetwork == "" || resolvedNetwork == "none" {
				continue
			}
			result.Networks = append(result.Networks, resolvedNetwork)

			if networkRef.IPv4 != "" {
				result.Entries = appendComposeIPEntry(
					result.Entries,
					resolvedNetwork,
					networkRef.IPv4,
					4,
					serviceName,
					containerName,
					project,
					path,
				)
			}
			if includeIPv6 && networkRef.IPv6 != "" {
				result.Entries = appendComposeIPEntry(
					result.Entries,
					resolvedNetwork,
					networkRef.IPv6,
					6,
					serviceName,
					containerName,
					project,
					path,
				)
			}
		}
	}

	result.Networks = dedupeStrings(result.Networks)
	result.VolumePaths = dedupeStrings(result.VolumePaths)
	sortEntries(result.Entries, "ip_entries")

	return result, nil
}

type serviceNetworkRef struct {
	Name string
	IPv4 string
	IPv6 string
}

func appendComposeIPEntry(entries []IPEntry, network, ip string, ipVersion int, service, containerName, project, composeFile string) []IPEntry {
	return append(entries, IPEntry{
		Network:       network,
		IP:            ip,
		IPVersion:     ipVersion,
		Service:       service,
		ContainerName: containerName,
		Project:       project,
		ComposeFile:   composeFile,
		Running:       false,
		Source:        "compose",
	})
}

func parseServiceNetworkRefs(serviceNode *yaml.Node) []serviceNetworkRef {
	networkNode := yamlMapLookup(serviceNode, "networks")
	if networkNode == nil {
		return nil
	}

	refs := make([]serviceNetworkRef, 0)
	switch networkNode.Kind {
	case yaml.SequenceNode:
		for _, item := range networkNode.Content {
			name := strings.TrimSpace(yamlScalar(item))
			if name != "" {
				refs = append(refs, serviceNetworkRef{Name: name})
			}
		}
	case yaml.MappingNode:
		for alias, valueNode := range yamlMapPairs(networkNode) {
			ref := serviceNetworkRef{Name: strings.TrimSpace(alias)}
			if valueNode != nil && valueNode.Kind == yaml.MappingNode {
				ref.IPv4 = normalizeValidIP(stripCIDR(strings.TrimSpace(yamlScalar(yamlMapLookup(valueNode, "ipv4_address")))), 4)
				ref.IPv6 = normalizeValidIP(stripCIDR(strings.TrimSpace(yamlScalar(yamlMapLookup(valueNode, "ipv6_address")))), 6)
			}
			if ref.Name != "" {
				refs = append(refs, ref)
			}
		}
	}

	sort.Slice(refs, func(i, j int) bool { return refs[i].Name < refs[j].Name })
	return refs
}

func parseComposeNetworkAliases(document *yaml.Node) map[string]string {
	aliases := make(map[string]string)
	networksNode := yamlMapLookup(document, "networks")
	if networksNode == nil || networksNode.Kind != yaml.MappingNode {
		return aliases
	}

	for alias, valueNode := range yamlMapPairs(networksNode) {
		resolved := strings.TrimSpace(alias)
		if valueNode != nil && valueNode.Kind == yaml.MappingNode {
			if named := strings.TrimSpace(yamlScalar(yamlMapLookup(valueNode, "name"))); named != "" {
				resolved = named
			}
		}
		if resolved != "" {
			aliases[alias] = resolved
		}
	}
	return aliases
}

func resolveComposeProjectName(document *yaml.Node, composeDir string, dotenv map[string]string) string {
	if configured := strings.TrimSpace(yamlScalar(yamlMapLookup(document, "name"))); configured != "" {
		return configured
	}

	if fromEnv := strings.TrimSpace(os.Getenv("COMPOSE_PROJECT_NAME")); fromEnv != "" {
		return fromEnv
	}
	if fromDotenv := strings.TrimSpace(dotenv["COMPOSE_PROJECT_NAME"]); fromDotenv != "" {
		return fromDotenv
	}
	return filepath.Base(composeDir)
}

func loadDotEnvFile(composeDir string) (map[string]string, error) {
	result := make(map[string]string)
	dotenvPath := filepath.Join(composeDir, ".env")

	data, err := os.ReadFile(dotenvPath)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return nil, err
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		raw := strings.TrimSpace(line)
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		if strings.HasPrefix(raw, "export ") {
			raw = strings.TrimSpace(strings.TrimPrefix(raw, "export "))
		}

		key, value, ok := strings.Cut(raw, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" {
			result[key] = value
		}
	}
	return result, nil
}

func interpolateComposeEnv(data string, dotenv map[string]string) string {
	resolve := func(key string) string {
		if value, ok := os.LookupEnv(key); ok {
			return value
		}
		return dotenv[key]
	}

	// Handle ${VAR:-default} and ${VAR-default} before os.Expand.
	output := data
	output = replaceWithDefaultSyntax(output, ":-", func(name, fallback string) string {
		value, ok := os.LookupEnv(name)
		if ok && strings.TrimSpace(value) != "" {
			return value
		}
		if dot := dotenv[name]; strings.TrimSpace(dot) != "" && !ok {
			return dot
		}
		if strings.TrimSpace(value) == "" && strings.TrimSpace(dotenv[name]) != "" {
			return dotenv[name]
		}
		return fallback
	})
	output = replaceWithDefaultSyntax(output, "-", func(name, fallback string) string {
		if value, ok := os.LookupEnv(name); ok {
			return value
		}
		if value, ok := dotenv[name]; ok {
			return value
		}
		return fallback
	})
	return os.Expand(output, resolve)
}

func replaceWithDefaultSyntax(input, operator string, resolve func(name, fallback string) string) string {
	start := 0
	for {
		open := strings.Index(input[start:], "${")
		if open == -1 {
			return input
		}
		open += start

		close := strings.IndexByte(input[open:], '}')
		if close == -1 {
			return input
		}
		close += open

		body := input[open+2 : close]
		parts := strings.SplitN(body, operator, 2)
		if len(parts) != 2 {
			start = close + 1
			continue
		}

		name := strings.TrimSpace(parts[0])
		fallback := parts[1]
		if name == "" {
			start = close + 1
			continue
		}

		replacement := resolve(name, fallback)
		input = input[:open] + replacement + input[close+1:]
		start = open + len(replacement)
	}
}

func yamlMapLookup(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for idx := 0; idx+1 < len(node.Content); idx += 2 {
		if strings.TrimSpace(node.Content[idx].Value) == key {
			return node.Content[idx+1]
		}
	}
	return nil
}

func yamlMapPairs(node *yaml.Node) map[string]*yaml.Node {
	pairs := make(map[string]*yaml.Node)
	if node == nil || node.Kind != yaml.MappingNode {
		return pairs
	}
	for idx := 0; idx+1 < len(node.Content); idx += 2 {
		key := strings.TrimSpace(node.Content[idx].Value)
		pairs[key] = node.Content[idx+1]
	}
	return pairs
}

func yamlScalar(node *yaml.Node) string {
	if node != nil && node.Kind == yaml.ScalarNode {
		return strings.TrimSpace(node.Value)
	}
	return ""
}

func extractVolumeHostPaths(serviceNode *yaml.Node, composeDir string) []string {
	volumesNode := yamlMapLookup(serviceNode, "volumes")
	if volumesNode == nil || volumesNode.Kind != yaml.SequenceNode {
		return nil
	}

	paths := make([]string, 0)
	for _, volumeNode := range volumesNode.Content {
		switch volumeNode.Kind {
		case yaml.ScalarNode:
			if hostPath := parseScalarVolumeHostPath(volumeNode.Value, composeDir); hostPath != "" {
				paths = append(paths, hostPath)
			}
		case yaml.MappingNode:
			if hostPath := parseMappingVolumeHostPath(volumeNode, composeDir); hostPath != "" {
				paths = append(paths, hostPath)
			}
		}
	}
	return dedupeStrings(paths)
}

func parseScalarVolumeHostPath(raw, composeDir string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}

	parts := strings.Split(value, ":")
	if len(parts) < 2 {
		return ""
	}
	return resolveBindSourcePath(parts[0], composeDir)
}

func parseMappingVolumeHostPath(node *yaml.Node, composeDir string) string {
	volumeType := strings.ToLower(strings.TrimSpace(yamlScalar(yamlMapLookup(node, "type"))))
	source := strings.TrimSpace(yamlScalar(yamlMapLookup(node, "source")))
	if (volumeType != "" && volumeType != "bind") || source == "" {
		return ""
	}
	return resolveBindSourcePath(source, composeDir)
}

func resolveBindSourcePath(source, composeDir string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return ""
	}

	if strings.HasPrefix(source, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			source = filepath.Join(home, strings.TrimPrefix(source, "~"))
		}
	}

	if filepath.IsAbs(source) {
		return filepath.Clean(source)
	}

	if strings.HasPrefix(source, ".") || strings.Contains(source, string(os.PathSeparator)) {
		return filepath.Clean(filepath.Join(composeDir, source))
	}
	return ""
}

func hasTopLevelServicesKey(data []byte) bool {
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" ||
			strings.HasPrefix(trimmed, "#") ||
			strings.TrimLeft(line, " \t") != line {
			continue
		}

		key, _, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}

		if strings.TrimSpace(key) == "services" {
			return true
		}
	}
	return false
}

func normalizeValidIP(raw string, version int) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return ""
	}

	if (version == 4 && !addr.Is4()) || (version == 6 && !addr.Is6()) {
		return ""
	}
	return addr.String()
}
