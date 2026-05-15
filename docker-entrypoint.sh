#!/bin/sh
set -e

SECRETS_FILE=/app/data/.secrets

# First boot: generate KEY_PEPPER and SESSION_SECRET and persist them in
# the data volume so they survive container restarts and re-creates.
if [ ! -f "$SECRETS_FILE" ]; then
    echo "mylogger42: first boot — generating secrets" >&2
    /app/mylogger42 admin generate-secrets > "$SECRETS_FILE"
    chmod 600 "$SECRETS_FILE"
fi

# Load the persisted secrets into the environment.
set -a
. "$SECRETS_FILE"
set +a

# Accept a plaintext ADMIN_PASSWORD and hash it into ADMIN_PASSWORD_HASH,
# which is what the server expects for first-start admin bootstrapping.
# Only done when ADMIN_PASSWORD is provided and ADMIN_PASSWORD_HASH isn't.
if [ -n "$ADMIN_PASSWORD" ] && [ -z "$ADMIN_PASSWORD_HASH" ]; then
    ADMIN_PASSWORD_HASH=$(printf '%s' "$ADMIN_PASSWORD" | /app/mylogger42 admin hash-password)
    export ADMIN_PASSWORD_HASH
fi
unset ADMIN_PASSWORD

exec /app/mylogger42 "$@"
