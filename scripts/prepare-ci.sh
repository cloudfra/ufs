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
# The report also names why something could not be cleared: the call each
# waiting process is stopped in, the kernel stack of every thread that can
# hold a mount, the kernel's own per-connection view of each mount, and the
# kernel log. It comes before the clears because the clears remove what it
# describes. Run the script with sudo so that /proc/<pid>/stack and
# /sys/fs/fuse/connections are readable; without sudo it reports the refusal
# instead of nothing.
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

# Reports every process the kernel has put to sleep. A test wedged inside a
# FUSE request appears here, and no signal reaches it until that request
# returns.
function ReportBlockedProcesses() {
  local blocked
  blocked=$(ps -eo pid=,stat=,etime=,user=,args= | awk '$2 ~ /^D/ { print "  " $0 }')
  if [[ -n "${blocked}" ]]; then
    echo "Processes waiting inside the kernel:"
    echo "${blocked}"
  else
    echo "No processes waiting inside the kernel."
  fi
}

# Reports what the kernel knows about the processes that can hold a mount
# busy, how the kernel sees each mount, and what it logged about them.
function ReportStuckState() {
  ReportSuspectProcesses
  ReportFuseModule
  ReportFuseConnections
  ReportKernelLog
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

# Stops every test binary that an earlier run left behind, since one holds a
# FUSE connection open and so keeps a leftover mount busy. SIGQUIT comes first
# because a Go binary answers it by printing the stack of every goroutine,
# which names the call it cannot finish, and most binaries exit on it.
function StopStuckTestBinaries() {
  local -a pids=()
  local pid
  while read -r pid; do
    pids+=("${pid}")
  done < <(ps -eo pid=,args= | awk '$2 ~ /\.test$/ { print $1 }')
  if [[ "${#pids[@]}" -eq 0 ]]; then
    echo "No test binary to stop."
    return
  fi

  echo "Stopping ${#pids[@]} test binary(ies): ${pids[*]}"
  for pid in "${pids[@]}"; do
    kill -QUIT "${pid}" 2>/dev/null || true
  done
  # Leaves the runner a moment to write the goroutine dumps, and the binaries
  # a chance to exit on their own before they are killed.
  sleep 2
  for pid in "${pids[@]}"; do
    kill -KILL "${pid}" 2>/dev/null || true
  done
  for pid in "${pids[@]}"; do
    local state
    state=$(ps -o stat= -p "${pid}" 2>/dev/null || true)
    if [[ -n "${state}" ]]; then
      echo "Test binary ${pid} survived, process state ${state}" >&2
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

# Prints a kernel-owned file along with the reason it could not be read,
# because a process can exit while this report is written and
# /proc/<pid>/stack is readable only by root.
function ShowKernelFile() {
  local label="$1" path="$2"
  echo "  ${label}:"
  # The trailing echo ends the section on a line of its own, because a kernel
  # file such as wchan has no newline of its own.
  { cat "${path}" 2>&1; echo; } | sed 's/^/    /' || true
}

# Reports what the kernel knows about one process: the call it is stopped in,
# the function it is waiting on, the kernel stack of each of its threads, and
# the descriptors that keep a FUSE connection open.
function ReportProcessDetail() {
  local pid="$1"
  echo "Process ${pid} $(tr '\0' ' ' < "/proc/${pid}/cmdline" 2>/dev/null)"
  echo "  parent, state, run time, threads:"
  ps -p "${pid}" -o ppid=,stat=,etime=,thcount= 2>&1 | sed 's/^/    /' || true
  ShowKernelFile "wait channel" "/proc/${pid}/wchan"
  ShowKernelFile "syscall" "/proc/${pid}/syscall"
  local stack
  for stack in "/proc/${pid}"/task/*/stack; do
    if [[ -e "${stack}" ]]; then
      ShowKernelFile "kernel stack of ${stack}" "${stack}"
    fi
  done
  # Each descriptor is reported by readlink alone, because an open file of a
  # dead mount would block a stat of its target the way it blocks a test.
  echo "  descriptors:"
  local fd
  for fd in "/proc/${pid}"/fd/*; do
    if [[ -L "${fd}" ]]; then
      echo "    ${fd##*/} -> $(readlink "${fd}" 2>&1)"
    fi
  done
  # A report is not a test, so it ends successfully even when the last probe
  # found nothing: the runner would otherwise stop reporting halfway through.
  :
}

# Reports the detail of every process that can hold a FUSE mount busy: the
# mount helpers, the test binaries that are their daemons, and the make and go
# test runs that started them.
function ReportSuspectProcesses() {
  local pids
  pids=$(
    {
      ps -C fusermount,fusermount3 -o pid=
      ps -eo pid=,args= | awk '$2 ~ /\.test$/ || ($2 == "go" && $3 == "test") || $2 == "make" { print $1 }'
    } | sort -un
  ) || true
  if [[ -z "${pids}" ]]; then
    echo "No mount helpers or test runs to report."
    return
  fi
  local pid
  while read -r pid; do
    if [[ -d "/proc/${pid}" ]]; then
      ReportProcessDetail "${pid}"
    fi
  done <<< "${pids}"
}

# Reports the kernel's own view of each mount: the requests it has queued for
# a daemon that is not answering, and the limits negotiated with it. This
# directory is readable only by root.
function ReportFuseConnections() {
  echo "FUSE connections:"
  if [[ -d /sys/fs/fuse/connections && ! -r /sys/fs/fuse/connections ]]; then
    echo "  /sys/fs/fuse/connections is readable only with sudo."
    return
  fi
  local connection
  for connection in /sys/fs/fuse/connections/*/; do
    [[ -d "${connection}" ]] || continue
    echo "  ${connection}"
    local value
    for value in "${connection}"*; do
      if [[ -f "${value}" ]]; then
        echo "    ${value##*/}=$(cat "${value}" 2>&1)"
      fi
    done
  done
  :
}

# Reports the settings of the runner's FUSE module, which decide what a mount
# created here is allowed to do.
function ReportFuseModule() {
  echo "FUSE module parameters:"
  local parameter
  for parameter in /sys/module/fuse/parameters/*; do
    if [[ -f "${parameter}" ]]; then
      echo "  ${parameter##*/}=$(cat "${parameter}" 2>&1)"
    fi
  done
  :
}

# Reports what the kernel logged around a stuck mount, which is where a
# refused or wedged mount request names itself.
function ReportKernelLog() {
  echo "Last 200 kernel log lines:"
  dmesg 2>&1 | tail -n 200 | sed 's/^/  /' || true
}

function Main() {
  ReportFuseState "before this run"
  ReportBlockedProcesses
  ReportStuckState

  # A test binary left running holds a mount busy, so it is stopped before the
  # mount is cleared.
  StopStuckTestBinaries

  # A leftover can stack two mounts on one mount point, so each point is
  # cleared once.
  local mount
  while read -r mount; do
    RemoveStuckFuseMount "${mount}"
  done < <(
    awk '$2 ~ "^/tmp/Test" && $3 ~ "^fuse" { print $2 }' /proc/mounts | sort -u
  )

  ReportFuseState "after this run"
  ReportBlockedProcesses
}

Main
