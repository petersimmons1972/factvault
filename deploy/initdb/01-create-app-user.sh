#!/bin/bash
# deploy/initdb/01-create-app-user.sh
#
# Creates the app_user login role before migrations run.
# Executed by docker-entrypoint-initdb.d on FIRST Postgres initialisation only
# (when pgdata is empty). Subsequent restarts skip this script entirely —
# the role persists in the database.
#
# Required environment variable:
#   POSTGRES_APP_USER_PASSWORD — password for the app_user login role.
#                                MUST be overridden for any non-local deployment.
#                                The compose default ("dev_only_local_password")
#                                is not safe outside of local development.
#
# On Kubernetes, this script is NOT used. The K8s init container reads the
# password from the factvault-db-credentials Secret instead.
#
# Security notes:
#   - The heredoc delimiter is single-quoted ('SQL') so the shell performs NO
#     expansion on the body — the password is never interpolated into the
#     stream as a shell-visible string.
#   - The password is passed to psql via `-v password=$VALUE`, and the SQL
#     references it as `:'password'`. psql's `:'var'` form applies SQL string
#     quoting (escapes embedded single quotes, backslashes, dollar signs).
#   - `psql -v` puts the value in argv, briefly visible to `ps` inside the
#     Postgres container. This is acceptable: the script only runs during
#     initdb, where the postgres user already controls the cluster. The
#     password is NEVER echoed to logs.

set -euo pipefail

if [[ -z "${POSTGRES_APP_USER_PASSWORD:-}" ]]; then
  echo "ERROR: POSTGRES_APP_USER_PASSWORD is not set. Cannot create app_user." >&2
  exit 1
fi

# Decide CREATE vs ALTER without exposing the password.
role_exists=$(
  psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
    -tAc "SELECT 1 FROM pg_roles WHERE rolname = 'app_user'"
)

if [[ "$role_exists" == "1" ]]; then
  sql_verb="ALTER"
else
  sql_verb="CREATE"
fi

# IMPORTANT: heredoc delimiter is single-quoted to suppress shell expansion.
# psql receives the literal text and substitutes :'password' (SQL-quoted) and
# :sql_verb (unquoted identifier) at parse time.
psql -v ON_ERROR_STOP=1 \
     -v password="$POSTGRES_APP_USER_PASSWORD" \
     -v sql_verb="$sql_verb" \
     --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-'SQL'
	\set ON_ERROR_STOP on
	:sql_verb ROLE app_user WITH LOGIN PASSWORD :'password';
SQL

echo "app_user role: ${sql_verb} succeeded."
