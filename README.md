# docre

[![Build and Publish](https://github.com/lifeofguenter/docre/workflows/build%20and%20publish/badge.svg?branch=master)](https://github.com/lifeofguenter/docre/actions?query=branch%3Amaster+workflow%3A%22build+and+publish%22)
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
