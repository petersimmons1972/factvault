#!/usr/bin/env sh
set -eu
: "${FACTVAULT_DATABASE_URL:?FACTVAULT_DATABASE_URL is required}"
: "${FACTVAULT_DEV_TENANT_ID:?FACTVAULT_DEV_TENANT_ID is required}"
repo_root="$(git rev-parse --show-toplevel)"
name="$(basename "$(pwd)")"
factvault example load "$name" --root "$repo_root/examples"
