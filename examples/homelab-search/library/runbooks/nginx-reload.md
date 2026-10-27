---
title: Reload the reverse proxy
tags: nginx, proxy, tls
---

# Reload the reverse proxy

Use this after changing a site file under `/etc/nginx/conf.d/` or renewing
certificates. A reload keeps open connections alive; a restart drops them.

## Check the configuration

```sh
sudo nginx -t
```

A clean run prints `syntax is ok` and `test is successful`. If it fails, the
error names the file and line. Fix that before going further: a reload with a
broken file is refused, but a restart takes the proxy down.

## Reload

```sh
sudo systemctl reload nginx
```

Then confirm the upstreams answer through the proxy:

| Site | Check |
| --- | --- |
| Home Assistant | `curl -sI https://ha.lab.home.arpa` returns 200 |
| Gitea | `curl -sI https://git.lab.home.arpa` returns 200 |
| Grafana | `curl -sI https://grafana.lab.home.arpa/login` returns 200 |

## When a reload is not enough

- A new `listen` port or a changed `worker_processes` value needs a restart.
- A certificate renewed by the ACME timer is picked up by a reload. See
  `configs/systemd/acme-renew.timer` for when it runs.
