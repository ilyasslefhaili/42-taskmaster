# CLAUDE.md

Guidance for working in this repository.

## Project

**Taskmaster** — a job-control daemon (42 project, subject v3.1), inspired by
`supervisor`. It launches and supervises child processes described in a config
file, keeps them alive per policy, and exposes an interactive control shell.
Written in **Go** (module `42-taskmaster`, Go 1.23+).

### Constraint: standard library only

External libraries are permitted **only** for: parsing the config file, the
readline/line-editing equivalent, and the client/server bonus. Everything else
must use the Go standard library. Prefer stdlib even where a dependency is
allowed unless it clearly pays for itself.

## Layout

- `cmd/taskmaster/` — main daemon entrypoint (foreground process + control shell).
- `cmd/taskmasterctl/` — control client entrypoint (client/server bonus).
- `internal/` — implementation packages (not importable outside this module).
  - `internal/parser/shellparser/` — control-shell command parser.

The two-binary layout anticipates the client/server bonus, but the mandatory
part only requires the control shell inside `taskmaster` itself.

## Build & run

```sh
go build ./...                 # build everything
go build -o taskmaster ./cmd/taskmaster
go run ./cmd/taskmaster <config-file>
go vet ./...
go test ./...
gofmt -l .                     # list unformatted files; should be empty
```

## Conventions

- Format with `gofmt` (tabs, standard Go style). Keep `go vet` clean.
- Package names are lowercase, no underscores; exported identifiers documented
  with a doc comment starting with the identifier name.
- Keep the daemon in the foreground; SIGHUP reloads config, and a reload must
  **not** de-spawn processes whose config is unchanged.
- All lifecycle events (start/stop/restart/unexpected death/reload) are logged
  to a local log file.

## Status

Early scaffold. Supervisor engine, config loader, control shell, and logging are
not yet implemented. See the project plan discussion for phasing.
