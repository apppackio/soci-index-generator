#!/usr/bin/env bash
# Verify that an ECR tag resolves to an OCI image index carrying a SOCI v2
# child descriptor, with artifactType on the descriptor inside the parent index.
# That is the exact field IsSociConvertedIndex (the loop guard) keys on.
#
# Usage:
#   ./index_structure.sh <repository> <tag>      # live: calls aws ecr batch-get-image
#   ./index_structure.sh --response <file.json>  # offline: a saved batch-get-image response
#
# The raw manifest JSON is always printed to stdout (when one was obtained);
# diagnostics go to stderr. It is the evidence; paste it into RESULTS.md.
#
# DELIBERATELY STRICTER than the Go loop guard. This script accepts only an OCI
# image index (application/vnd.oci.image.index.v1+json). The guard
# (IsSociConvertedIndex) also accepts a Docker manifest list. That is on purpose:
# the guard must err toward "skip" (a false positive costs one image its lazy
# loading), while this script answers "did Convert() emit what Fargate expects",
# where a Docker manifest list is a FAIL. Do not loosen this script to match the
# guard, and do not tighten the guard to match this script (the guard would then
# let a converted index back into conversion, which is the loop).
#
# Exit codes: 0 pass; 1 verdict is "no"; 2 usage error, unresolved tag, or
# unparseable response.
set -uo pipefail

WANT_INDEX="application/vnd.oci.image.index.v1+json"
WANT_ARTIFACT="application/vnd.amazon.soci.index.v2+json"

usage() {
  echo "usage: $0 <repository> <tag>" >&2
  echo "       $0 --response <batch-get-image-response.json>" >&2
  exit 2
}

if [ "${1:-}" = "--response" ]; then
  [ $# -eq 2 ] || usage
  [ -r "$2" ] || { echo "cannot read $2" >&2; exit 2; }
  response=$(cat "$2")
else
  if [ $# -ne 2 ] || [ -z "$1" ] || [ -z "$2" ]; then usage; fi
  command -v aws >/dev/null || { echo "aws CLI not found" >&2; exit 2; }
  response=$(aws ecr batch-get-image \
    --repository-name "$1" \
    --image-ids "imageTag=$2" \
    --accepted-media-types \
      "application/vnd.oci.image.index.v1+json" \
      "application/vnd.oci.image.manifest.v1+json" \
      "application/vnd.docker.distribution.manifest.list.v2+json" \
      "application/vnd.docker.distribution.manifest.v2+json" \
    --output json) || { echo "aws ecr batch-get-image failed" >&2; exit 2; }
fi

if ! echo "$response" | jq -e . >/dev/null 2>&1; then
  echo "response is not valid JSON:" >&2
  echo "$response" >&2
  exit 2
fi

manifest=$(echo "$response" | jq -r '.images[0].imageManifest // empty')
media=$(echo "$response" | jq -r '.images[0].imageManifestMediaType // empty')
if [ -z "$manifest" ]; then
  echo "tag did not resolve; response was:" >&2
  echo "$response" | jq . >&2
  exit 2
fi

echo "imageManifestMediaType: $media" >&2
echo "--- raw manifest ---" >&2
if echo "$manifest" | jq . 2>/dev/null; then :; else
  echo "$manifest"
  echo "imageManifest is not valid JSON" >&2
  exit 2
fi
echo "--------------------" >&2

# Valid JSON that is not an object (a string, array, number) is "could not
# tell", not "the index is wrong".
if ! echo "$manifest" | jq -e 'type == "object"' >/dev/null 2>&1; then
  echo "imageManifest is valid JSON but not an object" >&2
  exit 2
fi

fail=0
if [ "$media" != "$WANT_INDEX" ]; then
  echo "FAIL: media type is '$media', want $WANT_INDEX" >&2
  fail=1
fi
n=$(echo "$manifest" | jq --arg a "$WANT_ARTIFACT" \
  '[.manifests[]? | select(.artifactType == $a)] | length')
if [ "${n:-0}" -lt 1 ]; then
  echo "FAIL: no child descriptor with artifactType $WANT_ARTIFACT" >&2
  fail=1
fi
if [ "$fail" -eq 0 ]; then
  echo "PASS: OCI index with $n SOCI v2 child descriptor(s)" >&2
fi
exit "$fail"
