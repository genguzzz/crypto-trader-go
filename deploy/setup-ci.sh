#!/usr/bin/env bash
# One-time: let GitHub Actions deploy to the server. Run on a machine that can already SSH to it.
#
#   deploy/setup-ci.sh            create a dedicated deploy key, authorize it on the server,
#                                 store host / key / host key as GitHub Secrets (nothing printed)
#   deploy/setup-ci.sh --revoke   remove the deploy key from the server and delete the secrets
#
# Env: CTG_HOST (ssh alias or user@host, default "old-server"). Needs `gh auth login` with repo access.
set -euo pipefail
HOST=${CTG_HOST:-old-server}
TAG=gha-deploy@crypto-trader-go
cd "$(dirname "$0")/.."

# Resolve the real hostname from ssh config so it is stored only in the secret.
HOSTNAME=$(ssh -G "$HOST" | awk '/^hostname /{print $2}')

if [ "${1:-}" = --revoke ]; then
  ssh -o BatchMode=yes "$HOST" "sed -i '/$TAG/d' ~/.ssh/authorized_keys"
  for s in DEPLOY_HOST DEPLOY_SSH_KEY DEPLOY_KNOWN_HOSTS; do gh secret delete "$s" || true; done
  echo "✓ revoked"; exit
fi

TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT
ssh-keygen -q -t ed25519 -N '' -C "$TAG" -f "$TMP/k"
ssh -o BatchMode=yes "$HOST" "mkdir -p ~/.ssh && sed -i '/$TAG/d' ~/.ssh/authorized_keys 2>/dev/null; cat >> ~/.ssh/authorized_keys" < "$TMP/k.pub"
ssh-keyscan -t ed25519,ecdsa,rsa "$HOSTNAME" 2>/dev/null > "$TMP/kh"
[ -s "$TMP/kh" ] || { echo "✗ ssh-keyscan returned nothing"; exit 1; }

# Prove the new key works exactly as CI will use it.
ssh -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile="$TMP/kh" \
  -i "$TMP/k" "root@$HOSTNAME" true
echo "  deploy key authorized"

gh secret set DEPLOY_SSH_KEY < "$TMP/k"
gh secret set DEPLOY_KNOWN_HOSTS < "$TMP/kh"
printf '%s' "$HOSTNAME" | gh secret set DEPLOY_HOST
echo "✓ secrets set (private key deleted locally). Trigger: gh workflow run deploy"
