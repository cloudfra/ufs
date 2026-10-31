#!/usr/bin/env bash
#
# Copyright 2026 Cloudfra
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Prepares a self-hosted Linux CI runner for a build by cleaning up what an
# earlier run may have left behind.
#
# A FUSE test that fails to unmount leaves a dead mount under /tmp
# ("Transport endpoint is not connected"), which makes `df` in
# `make system-info` fail on every later run. This lazily unmounts every
# leftover FUSE mount of a test (t.TempDir() names start with /tmp/Test).
#
# Usage: scripts/prepare-ci.sh

set -euo pipefail

function RemoveStuckFuseMounts() {
  awk '$2 ~ "^/tmp/Test" && $3 ~ "^fuse" { print $2 }' /proc/mounts |
    while read -r mount; do
      echo "Unmounting stale FUSE test mount ${mount}"
      fusermount3 -u -z "${mount}" || echo "Cannot unmount ${mount}" >&2
    done
}

function Main() {
  RemoveStuckFuseMounts
}

Main
