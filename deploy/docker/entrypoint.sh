#!/bin/sh
set -eu

auth_dir="${FACTVAULT_AUTH_DIR:-/var/lib/factvault/auth}"
public_key="${FACTVAULT_JWT_PUBLIC_KEY:-$auth_dir/factvault-jwt-public.pem}"
private_key="${FACTVAULT_JWT_PRIVATE_KEY:-$auth_dir/factvault-jwt-private.pem}"

if [ "${FACTVAULT_BOOTSTRAP_AUTH:-1}" != "0" ]; then
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
	export FACTVAULT_JWT_PUBLIC_KEY="$public_key"
	if [ "${FACTVAULT_EXPORT_JWT_PRIVATE_KEY:-0}" = "1" ]; then
		export FACTVAULT_JWT_PRIVATE_KEY="$private_key"
	fi
fi

exec "$@"
