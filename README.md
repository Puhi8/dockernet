# dockernet

Docker + Compose IP discovery, conflict checks, and free IP allocation for self-hosted environments.

## Features

- Discovers IP usage from Docker runtime and Compose files
- Detects duplicate/static-IP conflicts
- Finds next-free addresses from configured IP groups / ranges
- Falls back to compose-only mode when Docker is unavailable
- JSON output (`--json`)

## Install

### Prebuilt binary (Linux/macOS)
```bash
curl -fsSL https://raw.githubusercontent.com/Puhi8/dockernet/main/installbin.sh | bash
```

### Go install
Requires: Go `1.24+`

```bash
go install github.com/Puhi8/dockernet@latest

# Ensure Go exists
export PATH="$PATH:$(go env GOPATH)/bin"

# Copy create default config
curl -fsSL https://raw.githubusercontent.com/Puhi8/dockernet/main/dockernet.conf.example -o ~/.dockernet.conf
```

## Build
Requires: Go `1.24+` (for building from source)

```bash
# Build binary
go build -o dockernet .

# Create config
cp dockernet.conf.example ~/.dockernet.conf
```

for static/minimized build:

```bash
CGO_ENABLED=0 go build -ldflags="-s -w" -o dockernet .
```

## Examples

```bash
# List running entries in bridge network
dockernet ps --running --network bridge

# Check conflicts only in one network
dockernet check --network bridge

# Get next 3 free IPs for all groups
dockernet nextFree 3

# Get exactly 1 free IP
dockernet nextFree 1

# Get free IPs by group index (0-based config order)
dockernet nextFree --group-number 1 2

# Validate group overlaps/ranges
dockernet sections --validate
```

## Configuration

Default config path:

- `$DOCKERNET_CONFIG` if set
- otherwise `~/.dockernet.conf`

Example config (in `dockernet.conf.example`):

```ini
NETWORKS="bridge,host"
COMPOSE_ROOTS="/var/composeFiles,/home/username/projects"
IGNORE_PATHS="node_modules,.git,volumes,data"
ENABLE_IPV6="false"
ENABLE_COLOR="true"

GROUP_INFRA="172.18.1.1-172.18.1.254"
GROUP_APPS="172.18.2.1-172.18.2.254"
```

Config keys:

- `NETWORKS` comma-separated network scope (host is list-only)
- `COMPOSE_ROOTS` comma-separated compose scan roots
- `IGNORE_PATHS` path ignored during compose discovery
- `ENABLE_IPV6` enable IPv6 discovery and checks
- `ENABLE_COLOR` enable colorized terminal output
- `GROUP_<NAME>` allocation/check range in full form (`a.b.c.d-a.b.c.d`)

Config values can be overridden with environment variables:

- `DOCKERNET_CONFIG`
- `DOCKERNET_ROOTS`
- `DOCKERNET_IPV6`
- `DOCKERNET_COLOR`
- `DOCKERNET_JSON`
- `DOCKERNET_QUIET`

## Output And Exit Codes

Output modes:

- Plain text (default)
- JSON (`--json`)

Exit codes:

- `0` success
- `1` runtime/config error
- `2` conflicts found (`check`)
- `3` degraded compose-only mode (Docker unavailable)
