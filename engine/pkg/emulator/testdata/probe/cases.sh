#!/usr/bin/env bash
# The four cases, each one run the same way, so the difference between two
# rows of the guide is one thing changed and not two.
set -uo pipefail
cd "$(dirname "$0")"

CA="$PWD/ca.crt"
SA="$PWD/sa.json"
if [ ! -f "$SA" ]; then
  openssl genrsa -out sa.key 2048 2>/dev/null
  python3 -c "
import json
open('sa.json','w').write(json.dumps({
  'type':'service_account','project_id':'af-probe',
  'private_key_id':'0'*40,'private_key':open('sa.key').read(),
  'client_email':'af-probe@af-probe.iam.gserviceaccount.com','client_id':'0'*21,
  'auth_uri':'https://accounts.google.com/o/oauth2/auth',
  'token_uri':'https://oauth2.googleapis.com/token',
}, indent=2))"
fi

# A backend for the storage half. CONTAINERS=1 uses the real emulator, which
# is what the surface actually ships; anything else uses the observer, which
# records the client's requests and answers nothing of substance.
STORAGE_BACKEND=127.0.0.1:19997
if [ "${CONTAINERS:-0}" = "1" ]; then
  docker rm -f af-probe-gcs >/dev/null 2>&1
  t0=$(python3 -c 'import time;print(time.time())')
  docker run -d --name af-probe-gcs -p 14443:4443 \
    -e FAKE_GCS_SCHEME=http -e FAKE_GCS_PUBLIC_HOST=storage.googleapis.com \
    -e FAKE_GCS_BACKEND=memory \
    fsouza/fake-gcs-server@sha256:797ce226d62f947c009dc40246b30cfb456b8473d8241407f9d6f2c04e4d69ef \
    >/dev/null || { echo "docker refused to start the emulator, so the storage half is NOT measured"; exit 1; }
  for i in $(seq 1 120); do
    curl -sf --max-time 2 "http://127.0.0.1:14443/storage/v1/b?project=p" >/dev/null 2>&1 && break
    sleep 1
  done
  t1=$(python3 -c 'import time;print(time.time())')
  echo ""
  echo "### fake-gcs-server, the real emulator"
  echo ""
  echo "    ready after $(python3 -c "print(round($t1-$t0,2))") seconds"
  docker stats --no-stream --format "    memory {{.MemUsage}}" af-probe-gcs
  STORAGE_BACKEND=127.0.0.1:14443
fi

# Every helper this script starts is killed by PID and never by pattern. The
# process table is shared with whatever else is running on the machine, and an
# unscoped pkill on this repository has already killed four other sessions'
# test suites.
PIDS=()
spawn() { "$@" >/dev/null 2>&1 & PIDS+=($!); }
start_observers() {
  spawn node gcs-observer.js
  spawn node token-observer.js
  sleep 1
}
stop_all() {
  for pid in "${PIDS[@]:-}"; do [ -n "$pid" ] && kill "$pid" 2>/dev/null; done
  PIDS=()
  sleep 1
}
trap stop_all EXIT

wait_port() { for i in $(seq 1 60); do nc -z 127.0.0.1 "$1" 2>/dev/null && return 0; sleep 0.25; done; return 1; }

echo ""
echo "## Cloud Storage, two languages, no endpoint override"
echo ""
start_observers
spawn env \
  AF_PROBE_ROUTES="{\"storage.googleapis.com\":\"$STORAGE_BACKEND\",\"*.storage.googleapis.com\":\"$STORAGE_BACKEND\",\"www.googleapis.com\":\"127.0.0.1:19996\",\"oauth2.googleapis.com\":\"127.0.0.1:19996\"}" \
  AF_PROBE_PORT=18091 AF_PROBE_LOG=sidecar-storage.json node sidecar.js
wait_port 18091 || echo "the stand in never listened"
echo '### JavaScript'
echo ''
HTTPS_PROXY=http://127.0.0.1:18091 NODE_EXTRA_CA_CERTS="$CA" \
  GOOGLE_CLOUD_PROJECT=af-probe GOOGLE_APPLICATION_CREDENTIALS="$SA" \
  timeout 90 node gcs-node.js 2>&1 | sed 's/^/    /'

if [ -d .venv ] || python3 -m venv .venv >/dev/null 2>&1; then
  ./.venv/bin/pip install -q google-cloud-storage >/dev/null 2>&1
  # The authority goes into the trust store the way an environment injects it,
  # rather than through a client option, because a client option would be the
  # application change this whole harness exists to avoid.
  CB=$(./.venv/bin/python -c "import certifi;print(certifi.where())")
  [ -f "$CB.orig" ] || cp "$CB" "$CB.orig"
  cp "$CB.orig" "$CB"; cat "$CA" >> "$CB"
  echo ''
  echo '### Python'
  echo ''
  HTTPS_PROXY=http://127.0.0.1:18091 GOOGLE_CLOUD_PROJECT=af-probe \
    GOOGLE_APPLICATION_CREDENTIALS="$SA" \
    timeout 90 ./.venv/bin/python gcs_python.py 2>&1 | sed 's/^/    /'
fi
stop_all
echo ''
echo '### Hosts each client asked for before its first storage call'
echo ''
python3 -c "
import json
try:
    for r in json.load(open('token-observed.json')):
        print('   ', r['host'] + r['url'])
except Exception as e:
    print('    no token request was recorded')
"

echo ""
echo "## gRPC through a proxy that reads HTTP/1.1, three runs"
echo ""
for mode in noalpn h2alpn forward; do
  stop_all
  spawn node -e 'require("net").createServer(s=>s.on("data",()=>{})).listen(19999,"127.0.0.1")' 
  case "$mode" in
    noalpn)
      spawn env AF_PROBE_ROUTES='{"pubsub.googleapis.com":"127.0.0.1:19999"}' AF_PROBE_PORT=18092 \
        AF_PROBE_LOG=sidecar-grpc-noalpn.json node sidecar.js
      label="reads HTTP/1.1, no ALPN, which is what af-proxy does" ;;
    h2alpn)
      spawn env AF_PROBE_ROUTES='{"pubsub.googleapis.com":"127.0.0.1:19999"}' AF_PROBE_PORT=18092 \
        AF_PROBE_ALPN=h2 AF_PROBE_LOG=sidecar-grpc-h2alpn.json node sidecar.js
      label="reads HTTP/1.1, offers ALPN h2" ;;
    forward)
      spawn node h2sink.js
      spawn env AF_PROBE_ROUTES='{"pubsub.googleapis.com":"127.0.0.1:19998"}' AF_PROBE_PORT=18092 \
        AF_PROBE_LOG=sidecar-grpc-forward.json node sidecar-h2.js
      label="forwards the terminated socket to an HTTP/2 backend" ;;
  esac
  wait_port 18092 || echo "the stand in never listened"
  echo "### $label"
  echo ""
  t0=$(python3 -c 'import time;print(time.time())')
  grpc_proxy=http://127.0.0.1:18092 NODE_EXTRA_CA_CERTS="$CA" \
    GOOGLE_CLOUD_PROJECT=af-probe GOOGLE_APPLICATION_CREDENTIALS="$SA" \
    timeout 120 node grpc-probe.js 2>&1 | python3 -c "
import json,sys
try:
    d=json.load(sys.stdin)
except Exception:
    print('    the client produced nothing'); raise SystemExit
print('    ok:', d.get('ok'), ' code:', d.get('code'))
print('   ', str(d.get('error'))[:160])
"
  t1=$(python3 -c 'import time;print(time.time())')
  echo "    elapsed $(python3 -c "print(round($t1-$t0,2))") seconds"
  echo ""
done
stop_all
[ "${CONTAINERS:-0}" = "1" ] && docker rm -f af-probe-gcs >/dev/null 2>&1
echo "done"
