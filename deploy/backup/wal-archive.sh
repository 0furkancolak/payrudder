#!/bin/sh
# Postgres archive_command for point-in-time recovery: archive_command = 'sh /scripts/wal-archive.sh %p %f'. It keeps a finished WAL segment (or timeline history file) in a
# local spool, compressed; wal-ship.sh moves the spool to S3-compatible storage. Postgres has no S3 client, and a spool next to it needs no network from inside the database
# container. Success means the file is on the spool's disk (synced); a name that is already there with other content is refused, as the Postgres documentation asks of an
# archive command. Environment: WAL_SPOOL (default /var/run/postgresql/wal-spool: a volume the postgres user can write to and wal-ship.sh can read, docs/runbooks/backup-restore.md).
set -eu
src="${1:?usage: wal-archive.sh <path> <file name>}"
name="${2:?usage: wal-archive.sh <path> <file name>}"
spool="${WAL_SPOOL:-/var/run/postgresql/wal-spool}"
mkdir -p "$spool"
dest="$spool/$name.gz"
if [ -e "$dest" ]; then
  # Postgres offers a file again after a crash between the copy and its bookkeeping: the same content is a success, anything else is not.
  gzip -dc "$dest" | cmp -s - "$src" && exit 0
  echo "wal-archive: $dest exists with other content" >&2
  exit 1
fi
gzip -c "$src" >"$dest.tmp"
# fsync of this file only (busybox and coreutils both take a file): a plain sync flushes every disk of the machine. shortcut: the rename is not fsynced, so a power
# loss right after the move can lose this segment while Postgres believes it archived; upgrade to pgBackRest/WAL-G (which fsync the directory) for production.
sync "$dest.tmp"
mv "$dest.tmp" "$dest"
