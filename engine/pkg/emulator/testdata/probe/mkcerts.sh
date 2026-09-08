#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")"
rm -f ca.key ca.crt leaf.key leaf.crt leaf.csr leaf.ext
# The extensions are copied from engine/internal/envcert/envcert.go rather
# than left to openssl's defaults, and the difference is not cosmetic. The
# default x509 CA carries basicConstraints and no keyUsage, and Python's
# OpenSSL refuses such a CA outright with "CA cert does not include key usage
# extension". The engine's own authority sets KeyUsageCertSign, so a stand in
# without it would have measured a Python failure the product does not have.
openssl req -x509 -newkey rsa:2048 -nodes -keyout ca.key -out ca.crt -days 2 \
  -addext "basicConstraints=critical,CA:TRUE" \
  -addext "keyUsage=critical,keyCertSign,digitalSignature" \
  -subj "/CN=Antifailure environment authority (L3.3 probe)" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes -keyout leaf.key -out leaf.csr \
  -subj "/CN=storage.googleapis.com" >/dev/null 2>&1
cat > leaf.ext <<'X'
subjectAltName = DNS:storage.googleapis.com, DNS:*.storage.googleapis.com, DNS:pubsub.googleapis.com, DNS:oauth2.googleapis.com, DNS:accounts.google.com, DNS:www.googleapis.com, DNS:iamcredentials.googleapis.com
extendedKeyUsage = serverAuth
X
openssl x509 -req -in leaf.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
  -out leaf.crt -days 2 -extfile leaf.ext >/dev/null 2>&1
echo "ca.crt and leaf.crt written"
