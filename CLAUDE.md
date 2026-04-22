# devtui

Go + Bubble Tea TUI for managing local dev services (docker compose, dotnet, npm). Designed to be project-agnostic — runs out of the box via auto-discovery, with an optional `devtui.yaml` for overrides.

## Build

```bash
make build     # builds ./devtui
make run       # build + run
make install   # go install ./cmd/devtui
```

## Structure

- `cmd/devtui/main.go` — entry point, config-file walk + git-root fallback
- `internal/config/config.go` — YAML schema + `Load`
- `internal/config/discover.go` — docker / dotnet / npm auto-discovery + overlay merge
- `internal/service/` — process lifecycle, log tailing, resource monitoring, re-adoption via state file
- `internal/ui/` — Bubble Tea model, panels, keybindings, styles
- `internal/msgs/` — cross-package message types

## Runtime state

Persisted to `<project_root>/.devtui/` — logs, state.json for re-adoption. Gitignore it.

## Key design choices

- **Zero-config default**: discovery runs even when no `devtui.yaml` exists; user config overlays.
- **Generic hooks**: `pre_start_cmd` / `post_start_cmd` on any service, executed via `sh -c`. No named hooks.
- **Dotnet runnability signal**: presence of `Properties/launchSettings.json` beside the csproj. Libraries / test projects are filtered out (no launchSettings, plus `<IsTestProject>` / `<OutputType>Library</OutputType>` guards).
- **Platform**: Linux + macOS (POSIX process groups, `lsof`, `pgrep`, `ps`). Not Windows.
