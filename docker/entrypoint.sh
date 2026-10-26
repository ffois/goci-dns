#!/bin/sh
set -e

# config.ini is mounted read-only at /config; ip_state.json lives in the
# /data volume so it survives container re-creation. The app logs to stdout
# (for `docker logs`) and to a rotating file under /logs.
exec ./goci-dns /config/config.ini /data/ip_state.json