# dockernet (Extended Go Edition)

Self-contained Docker network IP management tool.

## Features

- Auto-discovers Docker + Compose network usage
- Supports `.yml` and `.yaml` compose files
- Follows symlinks while scanning compose roots
- Parses `.env` alongside compose files (process env has precedence)
- Includes running and non-running containers in `ps`
- `ps --compose-only` includes `source=compose` and `source=both`
- Configurable colored output (`ENABLE_COLOR` / `DOCKERNET_COLOR`)
- Supports JSON output with `schema_version`

## Usage

Global flags:

- `-c, --config <path>`
- `-r, --root <path[,path...]>`
- `-6, --ipv6`
- `-j, --json`
- `-q, --quiet`

Commands:

- `dockernet ls`
- `dockernet ps [--running] [--compose-only] [--network <name>] [--ip-prefix <x.y.z>]`
- `dockernet check [--group <name>] [--network <name>]`
- `dockernet free --group <name> [--network <name>] [--limit <n>]`
- `dockernet nextFree [--group <name>] [--network <name>] [count]`
- `dockernet sections [--validate] [--edit] [--path]`

## Config

Example `~/.dockernet.conf`:

```ini
NETWORKS="bridge,host"
COMPOSE_ROOTS="/srv/compose,/home/luka/projects"
IGNORE_PATHS="node_modules,.git,volumes,data"
ENABLE_IPV6="false"
ENABLE_COLOR="true"

GROUP_INFRA="192.168.1.1-192.168.1.254"
GROUP_APPS="192.168.2.1-192.168.2.254"
```

Environment overrides (higher priority than config):

- `DOCKERNET_CONFIG`
- `DOCKERNET_ROOTS`
- `DOCKERNET_IPV6`
- `DOCKERNET_COLOR`
- `DOCKERNET_JSON`
- `DOCKERNET_QUIET`

## Build

CGO_ENABLED=0 go build -ldflags="-s -w"
