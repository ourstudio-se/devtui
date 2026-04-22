# devtui

Terminal UI for managing local development services. Built with Go using [Bubble Tea](https://github.com/charmbracelet/bubbletea).

Works out of the box in any project that has some combination of:

- a docker compose file (`compose.yaml`, `compose.yml`, `docker-compose.yaml`, `docker-compose.yml`)
- runnable .NET projects (`*.csproj` with a sibling `Properties/launchSettings.json`)
- npm packages with a `dev`, `watch`, or `start` script

## Install

```bash
go install github.com/ourstudio-se/devtui/cmd/devtui@latest
```

## Run

```bash
cd your-project
devtui
```

With no `devtui.yaml` present, `devtui` auto-discovers services from the project layout.

## Optional config: `devtui.yaml`

All fields are optional. Anything left unset falls back to discovery.

```yaml
# devtui.yaml
project_root: "."            # default: directory of this file
compose_file: "compose.yaml" # default: auto-detect
env_file: ".env"

discover:
  docker: true
  dotnet:
    enabled: true
    glob: "*.csproj"         # e.g. "MyCompany.*.csproj" to scope the walk
    strip_prefix: ""         # stripped from service name
  npm:
    enabled: true
    scripts: ["dev", "watch", "start"]

# Groups override / augment what discovery produces. Matched by name.
groups:
  - name: "Dependencies"
    kind: docker
    services:
      - name: mssql
        compose_service: mssql
        post_start_cmd: "./dev_scripts/create-databases.sh"
      - name: servicebus
        compose_service: emulator
        pre_start_cmd: "./dev_scripts/generate-servicebus-config.sh"
        depends_on: [mssql]
```

### Hooks

Any service kind (docker, dotnet, npm) accepts `pre_start_cmd` and `post_start_cmd`. These are arbitrary shell snippets run via `sh -c`, with working directory set to `project_root` and `.env` + container colour env vars available.

A non-zero exit from a pre-start hook marks the service as errored and aborts the start.

## Runtime state

State (PIDs, logs) is persisted to `<project_root>/.devtui/`. Gitignore it.

## Features

- Start, stop, and monitor services from a single terminal
- Live log tailing with ANSI colour support
- CPU and memory usage monitoring
- Detects externally running processes on configured ports
- Re-adopts services across `devtui` restarts (via state file)

## Key bindings

| Key | Action |
|-----|--------|
| `↑/k` `↓/j` | Navigate services / scroll logs |
| `Enter/Space` | Start/stop selected service |
| `Tab` | Switch between panels |
| `g/G` | Start/stop all in group |
| `a/A` | Start/stop all services |
| `r` | Rebuild selected service |
| `b` | Build dotnet solution |
| `D` | Stop all Docker services |
| `o` | Open logs in pager |
| `?` | Help |
| `q` | Detach (leave services running) |
| `Q / Ctrl+C` | Quit (stop all services) |

## Platform

Linux and macOS. Windows is not supported.

## License

MIT. See [LICENSE](LICENSE).
