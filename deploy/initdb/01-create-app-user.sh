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

set -euo pipefail

if [[ -z "${POSTGRES_APP_USER_PASSWORD:-}" ]]; then
  echo "ERROR: POSTGRES_APP_USER_PASSWORD is not set. Cannot create app_user." >&2
  exit 1
fi

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
  DO \$\$
  BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'app_user') THEN
      CREATE ROLE app_user WITH LOGIN PASSWORD '${POSTGRES_APP_USER_PASSWORD}';
    ELSE
      -- Update password in case the env var changed (idempotent on re-init).
      ALTER ROLE app_user WITH LOGIN PASSWORD '${POSTGRES_APP_USER_PASSWORD}';
    END IF;
  END;
  \$\$;
EOSQL

echo "app_user role created/updated."
