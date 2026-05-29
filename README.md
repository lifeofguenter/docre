## docre

[![build and publish](https://github.com/lifeofguenter/docre/actions/workflows/build-and-publish.yml/badge.svg)](https://github.com/lifeofguenter/docre/actions/workflows/build-and-publish.yml)
[![Coverage Status](https://coveralls.io/repos/github/lifeofguenter/docre/badge.svg?branch=main)](https://coveralls.io/github/lifeofguenter/docre?branch=main)
[![Quality Gate Status](https://sonarcloud.io/api/project_badges/measure?project=lifeofguenter_docre&metric=alert_status)](https://sonarcloud.io/summary/new_code?id=lifeofguenter_docre)
[![Docker Pulls](https://img.shields.io/docker/pulls/lifeofguenter/docre?style=flat)](https://hub.docker.com/r/lifeofguenter/docre)

_docre_ is a simple Docker crontab runner intended for use as a container entrypoint.

### Usage

```bash
CRONTAB="* * * * *" ./docre date
```

`docre` schedules a single command. Each tick of the crontab spec spawns the command; its stdout/stderr stream directly to docre's stdout/stderr, so they land in `docker logs` as the job runs.

### Configuration

| Env var | Required | Default | Description |
| --- | --- | --- | --- |
| `CRONTAB` | yes | — | Standard 5-field crontab spec (e.g. `*/5 * * * *`). |
| `WAIT_TIMEOUT` | no | `110s` | How long to wait for an in-flight job to finish on SIGTERM/SIGINT before killing it. Any [`time.ParseDuration`](https://pkg.go.dev/time#ParseDuration) value. |

### Behavior

- **One command per process.** docre runs exactly the command passed on argv. Multiple cron jobs means multiple containers.
- **Overlapping ticks are skipped, not queued.** If a previous run is still in progress when the next tick fires, that tick is dropped (`cron.SkipIfStillRunning`).
- **Panics in jobs are recovered** (`cron.Recover`). Subprocess failures are logged as `ERROR: exit status N`.
- **Graceful shutdown.** On SIGINT or SIGTERM, docre stops scheduling new ticks and waits up to `WAIT_TIMEOUT` for the running job to finish. If the timeout fires, the running child is killed.
