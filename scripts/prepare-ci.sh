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
# `make system-info` fail on every later run. This reports the FUSE state the
# runner starts with, unmounts every leftover FUSE mount of a test
# (t.TempDir() names start with /tmp/Test), then reports what survived.
#
# Clearing a mount can fail: fusermount3 refuses one that is missing from
# /etc/mtab, and a mount helper wedged in a kernel call survives being killed.
# Those cases are reported with their process state instead of failing the
# build, because only the runner can be repaired. Everything here reads
# /proc/mounts and process metadata rather than touching a mount point, so a
# wedged mount cannot block this script the way it blocks a test run.
#
# Usage: scripts/prepare-ci.sh

set -euo pipefail

# Reports the FUSE state at the given stage of the run: the mounts, the mount
# helpers serving them, and the test binaries that are their FUSE daemons. A
# process in the "D" state waits inside the kernel and cannot be killed until
# that call returns.
function ReportFuseState() {
  local stage="$1"

  local mounts
  mounts=$(awk '$3 ~ "^fuse" { print "  " $2 " (" $3 ")" }' /proc/mounts | sort -u)
  if [[ -n "${mounts}" ]]; then
    echo "FUSE mounts active ${stage}:"
    echo "${mounts}"
  else
    echo "No FUSE mounts active ${stage}."
  fi

  local helpers
  helpers=$(ps -C fusermount,fusermount3 -o pid=,stat=,etime=,user=,args= |
    sed 's/^/  /') || true
  if [[ -n "${helpers}" ]]; then
    echo "Mount helpers running ${stage}:"
    echo "${helpers}"
  else
    echo "No mount helpers running ${stage}."
  fi

  # A test binary still running holds a FUSE connection open, which is what
  # keeps a leftover mount busy. Each one belongs to an interrupted run: one
  # earlier run was only stopped when the runner terminated its orphaned
  # host.test process.
  local tests
  tests=$(ps -eo pid=,stat=,etime=,user=,args= | awk '$5 ~ /\.test$/ { print "  " $0 }')
  if [[ -n "${tests}" ]]; then
    echo "Test binaries running ${stage}:"
    echo "${tests}"
  else
    echo "No test binaries running ${stage}."
  fi
}

# True while the kernel still lists a mount point.
function IsMounted() {
  awk -v mount="$1" '$2 == mount { found = 1 } END { exit !found }' /proc/mounts
}

# Stops the mount helpers whose command line names a mount point, so that they
# stop holding it busy. A helper that survives is reported with its state.
function StopMountHelpers() {
  local mount="$1"
  local -a pids=()
  local pid args
  while read -r pid args; do
    if [[ " ${args} " == *" ${mount} "* ]]; then
      pids+=("${pid}")
    fi
  done < <(ps -C fusermount,fusermount3 -o pid=,args=)
  if [[ "${#pids[@]}" -eq 0 ]]; then
    return
  fi

  echo "Stopping ${#pids[@]} mount helper(s) for ${mount}"
  local pid
  for pid in "${pids[@]}"; do
    kill -TERM "${pid}" 2>/dev/null || true
  done
  sleep 2
  for pid in "${pids[@]}"; do
    kill -KILL "${pid}" 2>/dev/null || true
  done
  for pid in "${pids[@]}"; do
    local state
    state=$(ps -o stat= -p "${pid}" 2>/dev/null || true)
    if [[ -n "${state}" ]]; then
      echo "Mount helper ${pid} survived, process state ${state}" >&2
    fi
  done
}

# Unmounts one leftover mount, escalating until the kernel drops it. The plain
# unmount comes first because it is the attempt that keeps /etc/mtab straight,
# and `umount -l` comes last because it needs privileges the runner user may
# not have: its refusal is part of the report. Whatever is still mounted
# afterwards is listed by the closing report.
function RemoveStuckFuseMount() {
  local mount="$1"
  echo "Clearing stale FUSE test mount ${mount}"
  StopMountHelpers "${mount}"

  fusermount3 -u "${mount}" ||
    { IsMounted "${mount}" && fusermount3 -u -z "${mount}"; } ||
    { IsMounted "${mount}" && umount -l "${mount}"; } ||
    true
}

function Main() {
  ReportFuseState "before this run"

  # A leftover can stack two mounts on one mount point, so each point is
  # cleared once.
  local mount
  while read -r mount; do
    RemoveStuckFuseMount "${mount}"
  done < <(
    awk '$2 ~ "^/tmp/Test" && $3 ~ "^fuse" { print $2 }' /proc/mounts | sort -u
  )

  ReportFuseState "after this run"
}

Main
