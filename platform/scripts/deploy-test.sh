#!/bin/sh
set -eu
cd "$(dirname "$0")/../.."
test -z "$(git status --porcelain)" || { echo "Working tree must be clean" >&2; exit 1; }
test "$(git branch --show-current)" = main || { echo "Deploy requires main branch" >&2; exit 1; }
git pull --ff-only origin main
docker compose --env-file /home/ubuntu/.config/school-test/database.env -f platform/deploy/compose.test.yaml up -d --build --wait --wait-timeout 180
