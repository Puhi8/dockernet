# Dockernet CLI Spec (v1, Confirmed Decisions)

## Goal
Build `dockernet` around the intent in `main.go`: discover Docker/Compose IP usage, detect conflicts, and allocate next free addresses by configured sections.

## Command Set
1. Supported commands: `check`, `free`, `nextFree`, `ps`, `ls`, `sections`.
2. `scan` does not exist.

## Precedence
1. Value precedence is `CLI flags > environment variables > config file`.

## Global Flags
1. `-c, --config <path>`: config file path (default: `$DOCKERNET_CONFIG` or `~/.dockernet.conf`).
2. `-r, --root <path[,path...]>`: compose scan roots (overrides config/env).
3. `--ipv6`: include IPv6 in scan/check/allocation logic.
4. `--json`: JSON output (default plain text).
5. `-q, --quiet`: only essential output.

## Command Contract

### `check`
Detect IP conflicts and invalid assignments.

Checks:
1. Duplicate static IPs across compose files (same network + same IP).
2. Compose static IP already used by a running container on the same network.
3. Static IP outside configured group range (when a group is targeted).

Exit codes:
1. `0`: no conflicts.
2. `2`: conflicts found.
3. `1`: runtime/config error.
4. `3`: degraded mode (Docker unavailable, compose-only run).

### `free`
Return free IPs in a given group/network.

Rules:
1. Reserved addresses are excluded.
2. Output uses ranges where possible.
3. Range style should support full IP notation like `192.168.1.1-192.168.1.254`.
4. If not enough addresses are available, return all possible values and print warning: `not enough space`.

Flags:
1. `--group <name>` (required for deterministic result).
2. `--network <name>` (optional; defaults to discovered scope).
3. `--limit <n>` (default `1`).

### `nextFree`
Return next N free IPs for one or more groups.

Rules:
1. Reserved addresses are excluded.
2. Range style should support full IP notation like `192.168.1.1-192.168.1.254`.
3. If not enough addresses are available, return all possible values and print warning: `not enough space`.

Flags:
1. `--group <name>` (optional; all groups if omitted).
2. Positional `count` argument (default `2`).
3. `--network <name>` (optional).

### `ps`
Show merged runtime + compose state.

Behavior:
1. Include running and non-running containers (`stopped`, `exited`, `paused`).
2. Use multi-row output (one row per container-network).

Columns:
1. `container/service`
2. `network`
3. `ip`
4. `running` (`yes/no`)
5. `source` (`docker`, `compose`, `both`)

Filters:
1. `--running`
2. `--compose-only`
3. `--network <name>`
4. `--ip-prefix <x.y.z>`

### `ls`
List discovered resources (lighter than `ps`).

Default output:
1. networks discovered
2. compose files scanned
3. static IP count
4. running IP count

### `sections`
Show configured groups and ranges.

Flags:
1. `--edit`: open config in `$EDITOR` (no custom editor workflow).
2. `--validate`: validate group overlaps and range syntax.

## Data Model (Canonical)
Every command operates on one merged record model:

```text
type IPEntry {
  Network       string
  IP            string
  IPVersion     4|6
  Service       string
  ContainerName string
  Project       string
  ComposeFile   string
  Running       bool
  Source        compose|docker|both
}
```

## Discovery Rules
1. Networks are auto-detected from Docker and compose files.
2. Auto-detected Docker networks include `bridge` and `host`; exclude `none`.
3. `host` network is list-only (included in `ps`/`ls`, excluded from conflict detection and allocation).
4. Compose discovery supports all `.yml` and `.yaml` files.
5. Conflicts are evaluated per network, not globally.
6. Compose roots fallback to current directory when not specified by CLI/env/config.
7. Symlinks are allowed during compose discovery.
8. When discovering compose files, exclude paths that are known compose volume targets.
9. If Docker is unavailable, run in compose-only mode and show warning.

## Compose Parsing Rules
1. Parse YAML structure (no line-only parsing).
2. Support service `container_name`.
3. Support Compose project naming.
4. Parse `ipv4_address` and `ipv6_address` (when enabled).
5. Support environment interpolation.
6. Use process environment first, then `.env` next to compose files as fallback.
7. If one compose file has parse errors, continue with warning.
8. Parse warnings do not fail the command by themselves (exit `0` if command otherwise succeeds).

## Config Spec
Keep `KEY=value` format and extend:

```ini
COMPOSE_ROOTS="/srv/compose,/home/luka/projects"
ENABLE_IPV6="false"
GROUP_INFRA="192.168.1.1-192.168.1.254"
GROUP_APPS="192.168.2.1-192.168.2.254"
```

Legacy short group ranges like `1-10` are not supported.

## Output Contract
1. Output must be stable and sorted.
2. Plain text remains grep-friendly.
3. JSON output must include `schema_version`.
4. JSON output must not include `generated_at`.

## Implementation Order
1. Stabilize command parser + global flags.
2. Implement unified discovery pipeline (docker + compose parser + merge).
3. Implement `ls` and `ps`.
4. Implement `check`.
5. Implement `free` and `nextFree`.
6. Implement `sections --validate` and `sections --edit`.
7. Add tests per command and parser path.

## Status
All current design decisions from this thread are captured in this document.
