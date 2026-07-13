# Taskmaster

A job-control daemon inspired by [supervisor](http://supervisord.org/), written
in Go. Taskmaster launches and supervises child processes described in a YAML
config file, keeps them alive according to per-program policy, and exposes three
ways to control them: an interactive shell, a web dashboard, and a CLI client.

> 42 project, subject v3.1.

## Features

- **Process supervision** — starts programs as child processes, tracks their
  live/dead state accurately, and restarts them per policy.
- **Rich per-program configuration** — command, instance count, restart policy,
  expected exit codes, start/stop timing, stop signal, environment, working
  directory, umask, and stdout/stderr redirection.
- **Hot reload** — reload the configuration with `SIGHUP` or the `reload`
  command; unchanged programs are **never** de-spawned.
- **Event logging** — every lifecycle event (start, stop, restart, unexpected
  death, reload, shutdown) is written to a local log file.
- **Control shell** — interactive shell with line editing, history, and tab
  completion (falls back to plain line reading when piped).
- **Web dashboard + HTTP API** *(bonus)* — a live dashboard and a JSON control
  API served by the daemon.
- **`taskmasterctl` client** *(bonus)* — a CLI that drives the daemon over the
  same HTTP API.

Only the standard library is used, except for YAML parsing
(`gopkg.in/yaml.v3`) and the readline line editor
(`github.com/chzyer/readline`), both permitted by the subject.

## Build

Requires Go 1.23+.

```sh
go build -o taskmaster ./cmd/taskmaster
go build -o taskmasterctl ./cmd/taskmasterctl
# or build everything:
go build ./...
```

## Run

```sh
./taskmaster config.yaml
```

The daemon stays in the foreground and gives you a control shell:

```
taskmaster: logging events to taskmaster.log (tail -f to follow)
taskmaster: dashboard at http://127.0.0.1:9001
taskmaster> status
NAME      STATE    PID    UPTIME  RETRIES  LAST-EXIT
sleeper   RUNNING  1234   2m10s   0        -
ticker:0  RUNNING  1235   2m10s   0        -
ticker:1  RUNNING  1236   2m10s   0        -
taskmaster>
```

Follow events live in another terminal:

```sh
tail -f taskmaster.log
```

## Control shell

| Command             | Description                                        |
| ------------------- | -------------------------------------------------- |
| `status`            | Show the state of every program/instance.          |
| `start <target>`    | Start a program or a single instance.              |
| `stop <target>`     | Gracefully stop a program or a single instance.    |
| `restart <target>`  | Restart a program or a single instance.            |
| `reload`            | Re-read and apply the configuration file.          |
| `help`              | List commands.                                     |
| `quit` / `exit`     | Stop all programs and exit taskmaster.             |

A `<target>` is a **program name** (affects all its instances) or a single
**instance** like `ticker:0`. Tab completion offers both. `Ctrl-C` abandons the
current line; `Ctrl-D` shuts down.

### Signals

- `SIGHUP` — reload the configuration.
- `SIGINT` / `SIGTERM` — graceful shutdown.

## Web dashboard *(bonus)*

By default the daemon serves a dashboard at **http://127.0.0.1:9001**. It shows
a live, auto-refreshing status table with per-program Start/Stop/Restart buttons
and a Reload button. Set `httpaddr: "off"` in the config to disable it.

### HTTP API

| Method & path                            | Action                          |
| ---------------------------------------- | ------------------------------- |
| `GET  /api/status`                       | JSON status of every instance.  |
| `POST /api/programs/{target}/start`      | Start a program/instance.       |
| `POST /api/programs/{target}/stop`       | Stop a program/instance.        |
| `POST /api/programs/{target}/restart`    | Restart a program/instance.     |
| `POST /api/reload`                       | Reload the configuration.       |
| `GET  /`                                 | The web dashboard.              |

```sh
curl -s http://127.0.0.1:9001/api/status
curl -s -X POST http://127.0.0.1:9001/api/programs/ticker/restart
```

## `taskmasterctl` client *(bonus)*

A CLI that talks to a running daemon over the HTTP API:

```sh
taskmasterctl status
taskmasterctl start ticker
taskmasterctl stop ticker:0
taskmasterctl restart ticker
taskmasterctl reload
taskmasterctl -addr 127.0.0.1:9001 status   # non-default address
```

## Configuration

The config file is YAML. Top-level keys:

| Key        | Default            | Description                                       |
| ---------- | ------------------ | ------------------------------------------------- |
| `logfile`  | `taskmaster.log`   | Local file lifecycle events are logged to.        |
| `httpaddr` | `127.0.0.1:9001`   | Listen address for the API/dashboard; `off` disables it. |
| `programs` | —                  | Map of program name to program spec (see below).  |

Each program supports the following options. Everything except `cmd` has a
default, so a minimal program is just `cmd: "..."`.

| Option         | Default      | Description                                                          |
| -------------- | ------------ | ------------------------------------------------------------------- |
| `cmd`          | *(required)* | Command line to launch (run directly, not via a shell).             |
| `numprocs`     | `1`          | Number of instances to start and keep running.                      |
| `autostart`    | `true`       | Start the program when taskmaster launches.                         |
| `autorestart`  | `unexpected` | `always`, `never`, or `unexpected` (restart only on unexpected exit).|
| `exitcodes`    | `[0]`        | Exit codes considered "expected". Scalar or list.                   |
| `starttime`    | `1`          | Seconds the program must stay up to be considered "started".        |
| `startretries` | `3`          | Failed start attempts before giving up (FATAL).                     |
| `stopsignal`   | `TERM`       | Signal used to gracefully stop (e.g. `TERM`, `USR1`, `INT`).        |
| `stoptime`     | `10`         | Seconds to wait after `stopsignal` before sending `SIGKILL`.        |
| `stdout`       | *(discard)*  | File to redirect stdout to; omit to discard.                        |
| `stderr`       | *(discard)*  | File to redirect stderr to; omit to discard.                        |
| `env`          | *(none)*     | Environment variables to set before launching.                      |
| `workingdir`   | *(inherit)*  | Working directory to `chdir` into before launching.                 |
| `umask`        | *(inherit)*  | Octal umask to set before launching (e.g. `022`).                   |

### Example

```yaml
logfile: taskmaster.log
httpaddr: "127.0.0.1:9001"

programs:
  nginx:
    cmd: "/usr/local/bin/nginx -c /etc/nginx/test.conf"
    numprocs: 1
    umask: 022
    workingdir: /tmp
    autostart: true
    autorestart: unexpected
    exitcodes:
      - 0
      - 2
    startretries: 3
    starttime: 5
    stopsignal: TERM
    stoptime: 10
    stdout: /tmp/nginx.stdout
    stderr: /tmp/nginx.stderr
    env:
      STARTED_BY: taskmaster
      ANSWER: 42

  vogsphere:
    cmd: "/usr/local/bin/vogsphere-worker --no-prefork"
    numprocs: 8
    umask: 077
    workingdir: /tmp
    autostart: true
    autorestart: unexpected
    exitcodes: 0
    startretries: 3
    starttime: 5
    stopsignal: USR1
    stoptime: 10
```

## How reload works

On `SIGHUP` or `reload`, taskmaster re-reads the config and diffs it against the
running state:

- **Unchanged programs** are left completely alone — their processes keep
  running with the same PIDs.
- **Removed programs** are stopped and dropped.
- **Added programs** are created and autostarted per their spec.
- **Changed programs** are stopped and recreated with the new spec.

If the new config fails to parse or validate, the error is reported and the
running configuration is kept unchanged.

## Project layout

```
cmd/taskmaster/       Daemon entrypoint: supervisor, shell, HTTP server.
cmd/taskmasterctl/    CLI client for the HTTP control API.
internal/config/      Config loading, defaults, and validation.
internal/supervisor/  Process-supervision engine (state machine, policies).
internal/shell/        Interactive control shell (readline + fallback).
internal/parser/shellparser/  Control-shell command tokenizer.
internal/api/          HTTP control API and embedded web dashboard.
```

## Development

```sh
go build ./...   # build everything
go vet ./...     # static analysis
gofmt -l .       # list unformatted files (should be empty)
```
