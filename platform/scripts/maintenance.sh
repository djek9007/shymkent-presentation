#!/bin/sh
set -eu
: "${DATABASE_URL:?Set DATABASE_URL}"
psql "$DATABASE_URL" -X -v ON_ERROR_STOP=1 -c "DELETE FROM sessions WHERE expires_at < now(); DELETE FROM login_attempts WHERE window_start < now()-interval '1 day';"
