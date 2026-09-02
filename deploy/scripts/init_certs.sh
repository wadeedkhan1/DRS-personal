#!/usr/bin/env bash
#
# Prepares TLS certificates so the stack can start, then obtains a real one.
#
# The ordering problem this solves: nginx will not start if a server block references a
# certificate file that does not exist, but certbot's http-01 challenge needs nginx
# running to serve the challenge. So a self-signed placeholder goes in first, nginx
# comes up, certbot replaces it, and nginx reloads.
#
# Usage:  ./init_certs.sh drs.example.com admin@example.com
#         ./init_certs.sh                  (placeholder only, for local testing)

set -euo pipefail

DOMAIN="${1:-}"
EMAIL="${2:-}"
COMPOSE_DIR="$(cd "$(dirname "$0")/.." && pwd)"
CERT_DIR="/etc/letsencrypt/live/drs"

cd "$COMPOSE_DIR"

echo "==> Creating a self-signed placeholder certificate"
docker compose run --rm --entrypoint sh certbot -c "
  mkdir -p $CERT_DIR &&
  if [ ! -f $CERT_DIR/fullchain.pem ]; then
    openssl req -x509 -nodes -newkey rsa:2048 -days 365 \
      -keyout $CERT_DIR/privkey.pem \
      -out $CERT_DIR/fullchain.pem \
      -subj '/CN=localhost'
    echo 'placeholder created'
  else
    echo 'certificate already present, leaving it alone'
  fi
"

echo "==> Starting nginx"
docker compose up -d nginx

if [ -z "$DOMAIN" ]; then
  echo
  echo "No domain given, so the placeholder is all you get."
  echo "Browsers will warn about the self-signed certificate, which is fine for local testing."
  echo "For a real certificate: ./init_certs.sh your.domain you@example.com"
  exit 0
fi

if [ -z "$EMAIL" ]; then
  echo "An email address is required for Let's Encrypt expiry notices." >&2
  exit 1
fi

echo "==> Requesting a certificate for $DOMAIN"
# The placeholder has to go first, or certbot sees a valid-looking certificate and
# declines to replace it.
docker compose run --rm --entrypoint sh certbot -c "rm -rf $CERT_DIR"

docker compose run --rm certbot certonly \
  --webroot --webroot-path /var/www/certbot \
  --cert-name drs \
  -d "$DOMAIN" \
  --email "$EMAIL" \
  --agree-tos --no-eff-email \
  --non-interactive

echo "==> Reloading nginx with the real certificate"
docker compose exec nginx nginx -s reload

echo
echo "TLS is configured for $DOMAIN."
echo "The certbot service renews it automatically; nginx reloads twice a day to pick it up."
