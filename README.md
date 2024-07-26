# docre

[![build and publish](https://github.com/lifeofguenter/docre/actions/workflows/build-and-publish.yml/badge.svg)](https://github.com/lifeofguenter/docre/actions/workflows/build-and-publish.yml)
[![Quality Gate Status](https://sonarcloud.io/api/project_badges/measure?project=lifeofguenter_docre&metric=alert_status)](https://sonarcloud.io/summary/new_code?id=lifeofguenter_docre)
[![Docker Pulls](https://img.shields.io/docker/pulls/lifeofguenter/docre?style=flat)](https://hub.docker.com/r/lifeofguenter/docre)

_Docre_ is a simple docker crontab runner as entrypoint.

## Usage

```bash
CRONTAB="* * * * *" ./docre "date"

Sat 21 May 2022 10:11:00 AM CEST
Sat 21 May 2022 10:12:00 AM CEST
Sat 21 May 2022 10:13:00 AM CEST
...
```

When receiving SIGINT or SIGTERM, _docre_ will wait up to 110 seconds for the current job to finish before exiting.
