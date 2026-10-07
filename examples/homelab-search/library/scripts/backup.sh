#!/usr/bin/env bash
# Nightly backup of /srv to the restic repository on the NAS.
# Dumps Postgres first so the snapshot holds a consistent copy.
set -euo pipefail

export RESTIC_REPOSITORY="sftp:backup@nas.lab.home.arpa:/volume1/restic"
export RESTIC_PASSWORD_FILE="/root/.config/restic/password"

mkdir -p /srv/dumps
docker exec postgres pg_dump -U gitea gitea > /srv/dumps/gitea.sql

restic backup /srv --host atlas --exclude /srv/postgres --tag nightly
restic forget --host atlas --keep-daily 14 --keep-weekly 8 --keep-monthly 12 --prune
restic check --read-data-subset=2%
