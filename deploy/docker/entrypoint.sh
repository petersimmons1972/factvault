#!/bin/sh
set -eu

auth_dir="${FACTVAULT_AUTH_DIR:-/var/lib/factvault/auth}"
public_key="${FACTVAULT_JWT_PUBLIC_KEY:-$auth_dir/factvault-jwt-public.pem}"
private_key="${FACTVAULT_JWT_PRIVATE_KEY:-$auth_dir/factvault-jwt-private.pem}"

# FACTVAULT_BOOTSTRAP_AUTH controls whether this entrypoint tries to create/write
# JWT key material under $auth_dir before exec'ing the wrapped command.
#
# Only the "factvault api" server needs a JWT keypair to hand out/verify tokens;
# "factvault migrate" and "factvault worker <stage>" never touch auth material.
# Kubernetes app pods run with readOnlyRootFilesystem: true, so attempting the
# bootstrap on those commands fails before the wrapped command even starts
# (see issue #271). Default the bootstrap decision off of the command being
# exec'd ($1/$2) and only honor an explicit FACTVAULT_BOOTSTRAP_AUTH override.
bootstrap_auth="${FACTVAULT_BOOTSTRAP_AUTH:-}"
if [ -z "$bootstrap_auth" ]; then
	if [ "${1:-}" = "factvault" ] && [ "${2:-}" = "api" ]; then
		bootstrap_auth=1
	else
		bootstrap_auth=0
	fi
fi

if [ "$bootstrap_auth" != "0" ]; then
	mkdir -p "$auth_dir"
	if [ ! -s "$public_key" ] || [ ! -s "$private_key" ]; then
		tmp="$(mktemp)"
		factvault auth keys > "$tmp"
		awk -v priv="$private_key" -v pub="$public_key" '
			/-----BEGIN RSA PRIVATE KEY-----/ { section = "priv" }
			section == "priv" { print > priv }
			/-----END RSA PRIVATE KEY-----/ { section = "" }
			/-----BEGIN PUBLIC KEY-----/ { section = "pub" }
			section == "pub" { print > pub }
			/-----END PUBLIC KEY-----/ { section = "" }
		' "$tmp"
		rm -f "$tmp"
	fi
	if [ ! -s "$private_key" ] || [ ! -s "$public_key" ]; then
		echo "entrypoint: failed to bootstrap JWT key pair (private or public key file is empty)" >&2
		exit 1
	fi
	chmod 600 "$private_key"
	chmod 644 "$public_key"
	export FACTVAULT_JWT_PUBLIC_KEY="$public_key"
	if [ "${FACTVAULT_EXPORT_JWT_PRIVATE_KEY:-0}" = "1" ]; then
		export FACTVAULT_JWT_PRIVATE_KEY="$private_key"
	fi
fi

exec "$@"
