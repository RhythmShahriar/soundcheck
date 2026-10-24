#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Starts the test site (site.py) on 127.0.0.1:PORT in the background and
# waits until it answers. Request headers go to $RUNNER_TEMP/headers.jsonl.
#
#   serve.sh PORT
set -euo pipefail

port=${1:?usage: serve.sh PORT}
here=$(cd "$(dirname "$0")" && pwd)
nohup python3 "$here/site.py" "$port" "$RUNNER_TEMP/headers.jsonl" > "$RUNNER_TEMP/site.log" 2>&1 &
for _ in $(seq 30); do
  curl -fsS -o /dev/null "http://127.0.0.1:$port/" && exit 0
  sleep 1
done
cat "$RUNNER_TEMP/site.log"
exit 1
