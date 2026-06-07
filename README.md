## docre

[![build and publish](https://github.com/lifeofguenter/docre/actions/workflows/build-and-publish.yml/badge.svg)](https://github.com/lifeofguenter/docre/actions/workflows/build-and-publish.yml)
[![Coverage Status](https://coveralls.io/repos/github/lifeofguenter/docre/badge.svg?branch=main)](https://coveralls.io/github/lifeofguenter/docre?branch=main)
[![Quality Gate Status](https://sonarcloud.io/api/project_badges/measure?project=lifeofguenter_docre&metric=alert_status)](https://sonarcloud.io/summary/new_code?id=lifeofguenter_docre)
[![Docker Pulls](https://img.shields.io/docker/pulls/lifeofguenter/docre?style=flat)](https://hub.docker.com/r/lifeofguenter/docre)

_docre_ is a simple Docker crontab runner intended for use as a container entrypoint.

### Usage

Single job — command on argv, `CRONTAB` is the schedule spec:

```bash
CRONTAB="* * * * *" ./docre date
```

Multiple jobs — omit the argv command and put one `<spec> <command>` per line in `CRONTAB`:

```bash
CRONTAB="$(cat <<'EOF'
* * * * * /bin/echo every minute
*/5 * * * * /app/sync.sh
@hourly curl -fsS https://hc-ping.com/xyz
@every 30s /bin/health-check
EOF
)" ./docre
```

Lines beginning with `#` and blank lines are ignored. Each tick streams the job's stdout/stderr straight to docre's stdout/stderr, so output lands in `docker logs` live.

### Configuration

| Env var | Required | Default | Description |
| --- | --- | --- | --- |
| `CRONTAB` | yes | — | Either a single crontab spec (when a command is passed on argv) or one `<spec> <command>` per line (when no argv command is given). Supports 5-field specs, `@hourly`/`@daily`/etc. macros, and `@every <duration>`. |
| `WAIT_TIMEOUT` | no | `110s` | How long to wait for in-flight jobs to finish on SIGTERM/SIGINT before killing them. Any [`time.ParseDuration`](https://pkg.go.dev/time#ParseDuration) value. |

### Behavior

- **Two modes, picked automatically.** If a command is passed on argv, `CRONTAB` is the schedule spec for that single command. Otherwise, `CRONTAB` is parsed as one job per line.
- **Commands are exec'd directly**, not via a shell. For pipes, redirects, or shell expansion, wrap with `sh -c "..."`.
- **Overlapping ticks are skipped, not queued.** Per job: if a previous run is still in progress when its next tick fires, that tick is dropped (`cron.SkipIfStillRunning`).
- **Panics in jobs are recovered** (`cron.Recover`). Subprocess failures are logged as `ERROR: exit status N`.
- **Graceful shutdown.** On SIGINT or SIGTERM, docre stops scheduling new ticks and waits up to `WAIT_TIMEOUT` for the running jobs to finish. If the timeout fires, the running children are killed.
