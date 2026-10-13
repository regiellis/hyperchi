---
title: Restore files from the nightly backup
tags: [backup, restic, storage]
owner: homelab
---

# Restore files from the nightly backup

The nightly job in `scripts/backup.sh` sends snapshots of `/srv` to the restic
repository on the NAS. Snapshots are kept for 14 days, 8 weeks, and 12 months.

## Find the snapshot

```sh
export RESTIC_REPOSITORY=sftp:backup@nas.lab.home.arpa:/volume1/restic
export RESTIC_PASSWORD_FILE=/root/.config/restic/password
restic snapshots --host atlas --path /srv
```

Pick the newest snapshot taken before the problem started. Note its short ID.

## Restore to a scratch directory first

Never restore straight over live data. Restore to a scratch directory, compare,
then copy back what you need.

```sh
restic restore 4f2a9c1e --target /tmp/restore --include /srv/gitea
diff -r /tmp/restore/srv/gitea /srv/gitea | less
```

## Restore a database dump

Postgres dumps live in `/srv/dumps/` inside each snapshot. Stop the app, load
the dump, start the app:

```sh
docker compose -f /srv/compose/docker-compose.yml stop gitea
docker exec -i postgres psql -U gitea gitea < /tmp/restore/srv/dumps/gitea.sql
docker compose -f /srv/compose/docker-compose.yml start gitea
```

## Afterwards

- Delete `/tmp/restore` when you are done; it holds plain copies of secrets.
- Write down what failed in `notes/incidents.md`.
