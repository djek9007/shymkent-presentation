#!/bin/sh
set -eu
curl --fail --silent --show-error --max-time 10 "${PORTAL_URL:-https://school.edu.gov.kz}/healthz" >/dev/null
