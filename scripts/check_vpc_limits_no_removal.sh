#!/usr/bin/env bash
# Copyright Amazon.com Inc. or its affiliates. All Rights Reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License"). You may
# not use this file except in compliance with the License. A copy of the
# License is located at
#
#     http://aws.amazon.com/apache2.0/
#
# or in the "license" file accompanying this file. This file is distributed
# on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
# express or implied. See the License for the specific language governing
# permissions and limitations under the License.
#
# Verifies that pkg/aws/vpc/limits.go only gains new instance-type entries
# relative to a base ref, and never removes or renames an existing one.
#
# Usage: check_vpc_limits_no_removal.sh <base-ref> [file]
#
# Exits non-zero (and prints the removed instance types) if any key present
# in <base-ref>'s copy of the file is missing from the working tree's copy.

set -euo pipefail

BASE_REF="${1:-}"
FILE="${2:-pkg/aws/vpc/limits.go}"

if [[ -z "${BASE_REF}" ]]; then
  echo "Usage: $0 <base-ref> [file]" >&2
  exit 2
fi

if [[ ! -f "${FILE}" ]]; then
  echo "File not found: ${FILE}" >&2
  exit 2
fi

# extract_keys reads a limits.go-style file (via stdin) and prints one
# instance-type key per line, e.g. the "c5.xlarge" in `"c5.xlarge": {`.
extract_keys() {
  grep -oE '^\s*"[^"]+":\s*\{' | sed -E 's/^\s*"([^"]+)":.*/\1/'
}

# Base ref may not contain this file (e.g. it's newly added) — treat that as
# "no prior keys" rather than an error.
if git cat-file -e "${BASE_REF}:${FILE}" 2>/dev/null; then
  BASE_KEYS="$(git show "${BASE_REF}:${FILE}" | extract_keys | sort -u)"
else
  BASE_KEYS=""
fi

CURRENT_KEYS="$(extract_keys < "${FILE}" | sort -u)"

REMOVED="$(comm -23 <(echo "${BASE_KEYS}") <(echo "${CURRENT_KEYS}") | sed '/^$/d' || true)"
ADDED="$(comm -13 <(echo "${BASE_KEYS}") <(echo "${CURRENT_KEYS}") | sed '/^$/d' || true)"

BASE_COUNT=$(echo "${BASE_KEYS}" | sed '/^$/d' | wc -l | tr -d ' ')
CURRENT_COUNT=$(echo "${CURRENT_KEYS}" | sed '/^$/d' | wc -l | tr -d ' ')

echo "Base (${BASE_REF}) instance type count: ${BASE_COUNT}"
echo "Current instance type count: ${CURRENT_COUNT}"

if [[ -n "${ADDED}" ]]; then
  echo ""
  echo "Added instance types ($(echo "${ADDED}" | wc -l | tr -d ' ')):"
  echo "${ADDED}" | sed 's/^/  + /'
fi

if [[ -n "${REMOVED}" ]]; then
  echo ""
  echo "ERROR: The following instance type(s) were removed from ${FILE}:"
  echo "${REMOVED}" | sed 's/^/  - /'
  echo ""
  echo "${FILE} is a generated file that should only ever gain new instance"
  echo "types. If this removal is intentional (e.g. deduping a mistaken"
  echo "duplicate entry, or AWS deprecating an instance type), please call"
  echo "this out explicitly in the PR description."
  exit 1
fi

echo ""
echo "OK: no instance types were removed."
