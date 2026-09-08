#!/bin/bash
# The mutation matrix for the GCP emulator declaration.
#
# A test that stays green when the line it is meant to catch is broken is
# worthless, so every assertion here is pointed at the exact change it exists
# to refuse and required to go red. One break per cell, because `require`
# stops at the first failure and a later assertion in the same test can be
# unreachable and still look alive.
#
# Two things it does that a naive version does not, both of which this
# repository has been fooled by before:
#
#   `go test -run` matching nothing exits 0 and reads exactly like a pass, so
#   each cell counts the === RUN lines and reports NO_TEST_RAN as its own
#   verdict rather than letting it read as green.
#
#   An anchor that no longer appears in the source means the cell mutated
#   nothing, which would also read as a pass. That is ANCHOR_MISSING, and it
#   is a signal that the declaration moved and this matrix is stale.
#
# It restores the source between cells and never mutates a file under a
# running suite: each cell breaks, runs to completion, restores, re-runs to
# confirm green came back, and only then moves on.
#
# Run it under the build lock. This machine has 16GB and parallel Go builds
# have crashed it:
#
#     flock /private/tmp/af-gobuild.lock engine/pkg/emulator/testdata/probe/mutate.sh
# The mutation matrix for L3.3. One break per assertion, restored between
# cells, never mutating source under a running suite: each cell breaks, runs
# to completion, restores, and only then moves on.
#
# `go test -run` matching nothing exits 0 and reads exactly like a pass, so
# every cell counts the === RUN lines and a cell that ran no test is reported
# as NO_TEST rather than as a pass.
cd "$(dirname "$0")/../../../.." || exit 1
cd engine || exit 1
GCP=pkg/emulator/gcp.go
AWS=pkg/emulator/aws.go
cp "$GCP" /tmp/af-l33-gcp.bak; cp "$AWS" /tmp/af-l33-aws.bak
restore() { cp /tmp/af-l33-gcp.bak "$GCP"; cp /tmp/af-l33-aws.bak "$AWS"; }
trap restore EXIT

cell() {
  local name="$1" test="$2" file="$3" from="$4" to="$5"
  python3 - "$file" "$from" "$to" <<'PY' || { echo "$name  ANCHOR_MISSING"; return; }
import sys
p,f,t=sys.argv[1],sys.argv[2],sys.argv[3]
s=open(p).read()
if f not in s:
    raise SystemExit(1)
open(p,'w').write(s.replace(f,t,1))
PY
  out=$(go test ./pkg/emulator/ -run "$test" -count=1 -v 2>&1)
  ran=$(printf '%s' "$out" | grep -c '^=== RUN')
  if [ "$ran" -eq 0 ]; then
    verdict="NO_TEST_RAN"
  elif printf '%s' "$out" | grep -q '^--- FAIL'; then
    verdict="RED"
  else
    verdict="STILL_GREEN"
  fi
  restore
  out2=$(go test ./pkg/emulator/ -run "$test" -count=1 -v 2>&1)
  ran2=$(printf '%s' "$out2" | grep -c '^=== RUN')
  if printf '%s' "$out2" | grep -q '^--- FAIL'; then back="STILL_RED"; else back="GREEN"; fi
  printf '%-58s ran=%s %-12s restored=%s\n' "$name" "$ran" "$verdict" "$back"
}

echo "### mutation matrix"
cell "registers six: drop register(spanner)" TestGCP_RegistersSixEmulators "$GCP" $'\tregister(spanner)\n' ''
cell "pinned by digest: gcs image to a tag" TestGCP_EveryImageIsPinnedByDigest "$GCP" 'fsouza/fake-gcs-server@sha256:' 'fsouza/fake-gcs-server:latest//'
cell "registry validation: gcs answers no host" TestGCP_AllSixPassTheRegistryValidation "$GCP" 'Hosts:  []string{"storage.googleapis.com", "*.storage.googleapis.com"},' 'Hosts:  nil,'
cell "surface list: rename Cloud Spanner" TestGCP_CoversEveryServiceTheLaneOwes "$GCP" 'Name:   "Cloud Spanner",' 'Name:   "Cloud Spanner Database",'
cell "who ships it: mark gcs as Google's" TestGCP_RecordsWhichEmulatorsGoogleActuallyShips "$GCP" $'\tOfficial:     false,' $'\tOfficial:     true,'
cell "every emulator names a licence: blank spanner holder" TestEveryBuiltInEmulatorNamesItsProject "$GCP" 'Holder: "Copyright Google LLC",' 'Holder: "",'
cell "localstack is not Amazon's: mark it official" TestEveryBuiltInEmulatorNamesItsProject "$AWS" $'\tOfficial: false,' $'\tOfficial: true,'
cell "the real licence: call the CLI proprietary" TestGCP_RecordsTheLicenceEachProjectActuallyShips "$GCP" $'\tName:   "Apache License 2.0",\n\tHolder: "Copyright Google LLC. /google-cloud-sdk/LICENSE' $'\tName:   "Proprietary",\n\tHolder: "Copyright Google LLC. /google-cloud-sdk/LICENSE'
cell "bigtable admin host: drop it" TestGCP_AnswersForTheBigtableAdminHost "$GCP" 'Hosts:  []string{"bigtable.googleapis.com", "bigtableadmin.googleapis.com"},' 'Hosts:  []string{"bigtable.googleapis.com"},'
cell "both addressing styles: drop virtual hosted" TestGCP_AnswersForBothCloudStorageAddressingStyles "$GCP" 'Hosts:  []string{"storage.googleapis.com", "*.storage.googleapis.com"},' 'Hosts:  []string{"storage.googleapis.com"},'
cell "refuses outside the surface: claim www.googleapis.com" TestGCP_RefusesGoogleHostsOutsideTheSurface "$GCP" 'Hosts:  []string{"storage.googleapis.com", "*.storage.googleapis.com"},' 'Hosts:  []string{"storage.googleapis.com", "*.storage.googleapis.com", "www.googleapis.com"},'
cell "no two claim one host: firestore claims pubsub" TestGCP_NoTwoEmulatorsClaimTheSameHost "$GCP" 'Hosts:  []string{"firestore.googleapis.com"},' 'Hosts:  []string{"firestore.googleapis.com", "pubsub.googleapis.com"},'
cell "every service is proved: blank spanner Proves" TestGCP_EveryCoveredServiceNamesTheCall "$GCP" 'Proves: "create an instance and a database, insert a row and read it back",' 'Proves: "",'
cell "one of six: call pubsub REST" TestGCP_OnlyCloudStorageIsReachableWithoutHTTP2 "$GCP" '"Cloud Pub/Sub":   TransportGRPC,' '"Cloud Pub/Sub":   TransportREST,'
cell "the real policy engine decides: drop the apex" TestGCP_HostPatternsDecideTheEndpoints "$GCP" 'Hosts:  []string{"storage.googleapis.com", "*.storage.googleapis.com"},' 'Hosts:  []string{"*.storage.googleapis.com"},'
cell "three images: point spanner at the CLI image" TestGCP_TheThreeImagesAreTheThreeThisSurfacePulls "$GCP" $'\tImage:        spannerImage,' $'\tImage:        gcloudImage,'
cell "the command selects the emulator: drop pubsub's" TestGCP_OnlyTheSharedImageNeedsACommand "$GCP" '"gcloud", "beta", "emulators", "pubsub", "start",' '"true",//'
echo "### matrix done"
