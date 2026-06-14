#!/bin/sh
set -e

mkdir -p /logs

# Run the app and tee output to both stdout (for `docker logs`) and a
# persistent log file inside the mounted volume.
exec ./goci-dns 2>&1 | tee -a /logs/app.log