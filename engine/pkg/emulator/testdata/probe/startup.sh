#!/bin/bash
# Start each emulator with NO account, NO token and NO cloud credential, and
# record whether it binds a port at all, how long that takes and what it holds.
# The first question is L3.1's supply chain question: LocalStack's community
# images now exit 55 on licence activation before binding, so an image that
# pulls is not an image that starts.
set -uo pipefail
GCS=fsouza/fake-gcs-server@sha256:797ce226d62f947c009dc40246b30cfb456b8473d8241407f9d6f2c04e4d69ef
CLI=gcr.io/google.com/cloudsdktool/google-cloud-cli@sha256:07e4b8c3075ca793552fcfaf4808f104ef155d7805d87ade8e01b440463be262
SPN=gcr.io/cloud-spanner-emulator/emulator@sha256:4987860c9f8ecf1fffbbcdac115cb88cb9d1a42bd966c235a9ab843aea34fbd1

one() {
  local name="$1" port="$2" hostport="$3"; shift 3
  docker rm -f "af-l33-$name" >/dev/null 2>&1
  local t0 t1
  t0=$(python3 -c 'import time;print(time.time())')
  if ! docker run -d --name "af-l33-$name" -p "$hostport:$port" "$@" >/dev/null 2>&1; then
    echo "$name  DOCKER_REFUSED_TO_CREATE"; return
  fi
  local ok=0
  for i in $(seq 1 150); do
    if nc -z 127.0.0.1 "$hostport" 2>/dev/null; then ok=1; break; fi
    if [ "$(docker inspect -f '{{.State.Running}}' "af-l33-$name" 2>/dev/null)" = "false" ]; then
      echo "$name  EXITED code=$(docker inspect -f '{{.State.ExitCode}}' "af-l33-$name" 2>/dev/null)"
      docker logs "af-l33-$name" 2>&1 | tail -4 | sed 's/^/      /'
      docker rm -f "af-l33-$name" >/dev/null 2>&1
      return
    fi
    sleep 1
  done
  t1=$(python3 -c 'import time;print(time.time())')
  if [ "$ok" = "0" ]; then echo "$name  NEVER_BOUND after 150s"; docker rm -f "af-l33-$name" >/dev/null 2>&1; return; fi
  local mem
  mem=$(docker stats --no-stream --format '{{.MemUsage}}' "af-l33-$name" 2>/dev/null)
  printf '%-12s STARTED  ready=%ss  memory=%s\n' "$name" "$(python3 -c "print(round($t1-$t0,1))")" "$mem"
  docker rm -f "af-l33-$name" >/dev/null 2>&1
}

echo "### does each image start with no account, and what does it cost"
one gcs 4443 14443 -e FAKE_GCS_SCHEME=http -e FAKE_GCS_PUBLIC_HOST=storage.googleapis.com -e FAKE_GCS_BACKEND=memory "$GCS"
one spanner 9010 19010 "$SPN"
one pubsub 8085 18085 -e CLOUDSDK_CORE_DISABLE_PROMPTS=1 "$CLI" gcloud beta emulators pubsub start --host-port=0.0.0.0:8085 --project=af-environment
one firestore 8086 18186 -e CLOUDSDK_CORE_DISABLE_PROMPTS=1 "$CLI" gcloud emulators firestore start --host-port=0.0.0.0:8086
one datastore 8087 18187 -e CLOUDSDK_CORE_DISABLE_PROMPTS=1 "$CLI" gcloud beta emulators datastore start --host-port=0.0.0.0:8087 --project=af-environment
one bigtable 8088 18188 -e CLOUDSDK_CORE_DISABLE_PROMPTS=1 "$CLI" gcloud beta emulators bigtable start --host-port=0.0.0.0:8088
echo "### done"
