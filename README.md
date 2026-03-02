# dockernet (Extended Go Edition)

Self-contained Docker network IP management tool.

## Features

- Auto-discovers Docker + Compose network usage
- Supports `.yml` and `.yaml` compose files
- Follows symlinks while scanning compose roots
- Parses `.env` alongside compose files (process env has precedence)
- Includes running and non-running containers in `ps`
- Supports JSON output with `schema_version`

## Usage

Global flags:

- `-c, --config <path>`
- `-r, --root <path[,path...]>`
- `--ipv6`
- `--json`
- `-q, --quiet`

Commands:

- `dockernet ls`
- `dockernet ps [--running] [--compose-only] [--network <name>] [--ip-prefix <x.y.z>]`
- `dockernet check [--group <name>] [--network <name>]`
- `dockernet free --group <name> [--network <name>] [--limit <n>]`
- `dockernet nextFree [--group <name>] [--network <name>] [count]`
- `dockernet sections [--validate] [--edit]`

## Build

CGO_ENABLED=0 go build -ldflags="-s -w"
