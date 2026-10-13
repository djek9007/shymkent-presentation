#!/bin/sh
set -eu
: "${RESTORE_DATABASE_URL:?Set RESTORE_DATABASE_URL to an empty database}"
: "${RESTORE_UPLOAD_DIR:?Set RESTORE_UPLOAD_DIR to an empty directory}"
backup=${1:?Usage: restore.sh BACKUP_DIRECTORY}
(cd "$backup" && sha256sum -c SHA256SUMS)
[ ! -e "$RESTORE_UPLOAD_DIR" ] || [ -z "$(ls -A "$RESTORE_UPLOAD_DIR")" ] || { echo 'Upload destination must be empty' >&2; exit 1; }
count=$(psql "$RESTORE_DATABASE_URL" -XAt -v ON_ERROR_STOP=1 -c "SELECT count(*) FROM information_schema.tables WHERE table_schema='public'")
[ "$count" = 0 ] || { echo 'Restore database must be empty' >&2; exit 1; }
mkdir -p "$RESTORE_UPLOAD_DIR"
pg_restore --dbname="$RESTORE_DATABASE_URL" --exit-on-error --single-transaction "$backup/database.dump"
tar -C "$RESTORE_UPLOAD_DIR" -xzf "$backup/uploads.tar.gz"
echo 'Restore complete; verify application before switching traffic.'
