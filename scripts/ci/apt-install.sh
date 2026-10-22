#!/bin/sh
# Installs Ubuntu packages on a fresh runner, tolerating a mirror caught mid-sync. Every job on
# the ephemeral pool runs apt-get update, and an index fetched while a mirror is updating fails
# with "Hash Sum mismatch" (seen on main, 2026-10-22); the same request succeeds a little later.
# A stalled connection or a held dpkg lock can also hang apt with no error (a GitHub-hosted job
# sat 40 minutes in this script, 2026-10-22), so every apt call has a time limit and a hang
# counts as a failed attempt.
# So: up to three attempts with growing pauses, dropping partial downloads between them.
#   scripts/ci/apt-install.sh <package> ...
set -eu
[ "$#" -gt 0 ] || { echo "usage: apt-install.sh <package> ..." >&2; exit 2; }
apt_opts="-o Acquire::Retries=3 -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30 -o DPkg::Lock::Timeout=120"
attempt=1
# shellcheck disable=SC2086 # apt_opts is a list of options
until timeout 300 sudo apt-get update -qq $apt_opts &&
  timeout 600 sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq $apt_opts "$@"; do
  if [ "$attempt" -ge 3 ]; then
    echo "apt-get failed $attempt times" >&2
    exit 1
  fi
  echo "apt-get failed or timed out (attempt $attempt); retrying in $((attempt * 20))s" >&2
  sudo rm -rf /var/lib/apt/lists/partial
  sleep $((attempt * 20))
  attempt=$((attempt + 1))
done
