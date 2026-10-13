#!/bin/sh
set -eu
: "${DATABASE_URL:?Set DATABASE_URL}"
: "${BACKUP_DIR:?Set BACKUP_DIR to storage separate from application server}"
: "${UPLOAD_DIR:?Set UPLOAD_DIR}"
umask 077
stamp=$(date -u +%Y%m%dT%H%M%SZ)
mkdir -p "$BACKUP_DIR"
target="$BACKUP_DIR/$stamp"
mkdir "$target.partial"
trap 'rm -rf "$target.partial"' EXIT HUP INT TERM
pg_dump --dbname="$DATABASE_URL" --format=custom --file="$target.partial/database.dump"
tar -C "$UPLOAD_DIR" -czf "$target.partial/uploads.tar.gz" .
(cd "$target.partial" && sha256sum database.dump uploads.tar.gz > SHA256SUMS)
mv "$target.partial" "$target"
find "$BACKUP_DIR" -mindepth 1 -maxdepth 1 -type d -name '20*T*Z' -mtime +13 -exec rm -rf -- {} +
