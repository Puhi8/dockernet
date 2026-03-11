package app

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
)

func discoverState(ctx context.Context, opts runtimeOptions) (*discoveryResult, error) {
	state := &discoveryResult{}

	composeFiles, walkWarnings := discoverComposeFiles(opts.ComposeRoots, opts.IgnorePaths)
	state.Warnings = append(state.Warnings, walkWarnings...)

	parsedByFile := make(map[string]composeParseResult, len(composeFiles))
	relevantComposeFiles := make([]string, 0, len(composeFiles))
	volumePaths := make([]string, 0)
	for _, composeFile := range composeFiles {
		parsed, err := parseComposeFile(composeFile, opts.IncludeIPv6)
		if err != nil {
			state.Warnings = append(state.Warnings, fmt.Sprintf("compose parse failed for %s: %v", composeFile, err))
			continue
		}
		state.Warnings = append(state.Warnings, parsed.Warnings...)
		if !parsed.IsCompose {
			continue
		}
		parsedByFile[composeFile] = parsed
		relevantComposeFiles = append(relevantComposeFiles, composeFile)
		volumePaths = append(volumePaths, parsed.VolumePaths...)
	}

	filteredComposeFiles := filterComposeFilesByVolumePaths(relevantComposeFiles, volumePaths)
	state.ComposeFiles = filteredComposeFiles

	networkSet := make(map[string]struct{})
	for _, network := range opts.Networks {
		network = strings.TrimSpace(network)
		if network != "" && network != "none" {
			networkSet[network] = struct{}{}
		}
	}

	for _, composeFile := range filteredComposeFiles {
		parsed := parsedByFile[composeFile]

		state.ComposeEntries = append(state.ComposeEntries, parsed.Entries...)
		state.ComposePorts = append(state.ComposePorts, parsed.Ports...)
		for _, network := range parsed.Networks {
			if strings.TrimSpace(network) != "" && network != "none" {
				networkSet[network] = struct{}{}
			}
		}
	}
	sortEntries(state.ComposeEntries, SortIPEntries)
	sortEntries(state.ComposePorts, SortPort)

	dockerData := discoverDocker(ctx, opts.IncludeIPv6)
	state.DockerEntries = dockerData.Entries
	state.DockerPorts = dockerData.Ports
	state.Warnings = append(state.Warnings, dockerData.Warnings...)
	state.Degraded = !dockerData.Available
	for _, network := range dockerData.Networks {
		if strings.TrimSpace(network) != "" && network != "none" {
			networkSet[network] = struct{}{}
		}
	}

	for network := range networkSet {
		state.Networks = append(state.Networks, network)
	}
	sort.Strings(state.Networks)

	return state, nil
}

func discoverDocker(ctx context.Context, includeIPv6 bool) dockerDiscovery {
	result := dockerDiscovery{
		Available: false,
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("docker unavailable: %v", err))
		return result
	}
	defer cli.Close()

	if _, err := cli.Ping(ctx); err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("docker unavailable: %v", err))
		return result
	}

	networks, err := cli.NetworkList(ctx, types.NetworkListOptions{})
	if err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("docker unavailable: %v", err))
		return result
	}

	for _, network := range networks {
		name := strings.TrimSpace(network.Name)
		if name != "" && name != "none" {
			result.Networks = append(result.Networks, name)
		}
	}
	result.Networks = dedupeStrings(result.Networks)

	containers, err := cli.ContainerList(ctx, types.ContainerListOptions{All: true})
	if err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("docker unavailable: %v", err))
		return result
	}

	for _, containerInfo := range containers {
		containerName := normalizeContainerName(containerInfo.Names)
		running := strings.EqualFold(strings.TrimSpace(containerInfo.State), "running")
		project := strings.TrimSpace(containerInfo.Labels["com.docker.compose.project"])
		service := strings.TrimSpace(containerInfo.Labels["com.docker.compose.service"])

		if containerInfo.NetworkSettings != nil && len(containerInfo.NetworkSettings.Networks) > 0 {
			networkNames := make([]string, 0, len(containerInfo.NetworkSettings.Networks))
			for networkName := range containerInfo.NetworkSettings.Networks {
				networkNames = append(networkNames, networkName)
			}
			sort.Strings(networkNames)

			for _, networkName := range networkNames {
				networkName = strings.TrimSpace(networkName)
				if networkName == "" || networkName == "none" {
					continue
				}

				endpoint := containerInfo.NetworkSettings.Networks[networkName]
				if endpoint == nil {
					continue
				}

				if networkName == "host" {
					result.Entries = appendDockerIPEntry(result.Entries, "host", "host", 0, containerName, service, project, running)
					continue
				}
				if ipv4 := stripCIDR(endpoint.IPAddress); ipv4 != "" {
					result.Entries = appendDockerIPEntry(result.Entries, networkName, ipv4, 4, containerName, service, project, running)
				}
				if includeIPv6 {
					if ipv6 := stripCIDR(endpoint.GlobalIPv6Address); ipv6 != "" {
						result.Entries = appendDockerIPEntry(result.Entries, networkName, ipv6, 6, containerName, service, project, running)
					}
				}
			}
		}

		for _, publishedPort := range containerInfo.Ports {
			if publishedPort.PrivatePort == 0 {
				continue
			}
			protocol := normalizePortProtocol(publishedPort.Type)
			containerPort := int(publishedPort.PrivatePort)
			hostPort := int(publishedPort.PublicPort)
			hostIP := strings.TrimSpace(publishedPort.IP)
			published := hostPort > 0
			origin := "exposed"
			if published {
				origin = "published"
			}
			result.Ports = appendDockerPortEntry(
				result.Ports,
				protocol,
				containerPort,
				hostIP,
				hostPort,
				published,
				origin,
				containerName,
				service,
				project,
				running,
			)
		}
	}

	sortEntries(result.Entries, SortIPEntries)
	sortEntries(result.Ports, SortPort)
	result.Available = true
	return result
}

func appendDockerIPEntry(entries []IPEntry, network, ip string, ipVersion int, containerName, service, project string, running bool) []IPEntry {
	return append(entries, IPEntry{
		Network:       network,
		IP:            ip,
		IPVersion:     ipVersion,
		Service:       service,
		ContainerName: containerName,
		Project:       project,
		Running:       running,
		Source:        "docker",
	})
}

func appendDockerPortEntry(
	entries []IPEntry,
	protocol string,
	containerPort int,
	hostIP string,
	hostPort int,
	published bool,
	origin, containerName, service, project string,
	running bool,
) []IPEntry {
	return append(entries, IPEntry{
		Protocol:      protocol,
		ContainerPort: containerPort,
		HostIP:        hostIP,
		HostPort:      hostPort,
		Published:     published,
		Origin:        origin,
		Service:       service,
		ContainerName: containerName,
		Project:       project,
		Running:       running,
		Source:        "docker",
	})
}

func normalizeContainerName(names []string) string {
	if len(names) == 0 {
		return ""
	}

	normalized := make([]string, 0, len(names))
	for _, rawName := range names {
		name := strings.TrimSpace(strings.TrimPrefix(rawName, "/"))
		if name != "" {
			normalized = append(normalized, name)
		}
	}
	if len(normalized) == 0 {
		return ""
	}
	sort.Strings(normalized)
	return normalized[0]
}
