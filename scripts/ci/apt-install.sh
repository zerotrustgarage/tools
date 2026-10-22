#!/bin/sh
# Installs Ubuntu packages on a fresh runner, tolerating a mirror caught mid-sync. Every job on
# the ephemeral pool runs apt-get update, and an index fetched while a mirror is updating fails
# with "Hash Sum mismatch" (seen on main, 2026-10-22); the same request succeeds a little later.
# So: up to three attempts with growing pauses, dropping partial downloads between them.
#   scripts/ci/apt-install.sh <package> ...
set -eu
[ "$#" -gt 0 ] || { echo "usage: apt-install.sh <package> ..." >&2; exit 2; }
attempt=1
until sudo apt-get update -qq -o Acquire::Retries=3; do
  if [ "$attempt" -ge 3 ]; then
    echo "apt-get update failed $attempt times" >&2
    exit 1
  fi
  echo "apt-get update failed (attempt $attempt); retrying in $((attempt * 20))s" >&2
  sudo rm -rf /var/lib/apt/lists/partial
  sleep $((attempt * 20))
  attempt=$((attempt + 1))
done
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq -o Acquire::Retries=3 "$@"
