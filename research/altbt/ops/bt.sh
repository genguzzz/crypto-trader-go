#!/bin/sh
# Research backtests in a throwaway, resource-capped container (does not touch /opt/ctg).
#   /opt/ctg-research/bt.sh research/altbt/run.py strategies/library/alt_regime.py --params ... --out out/x
exec docker run --rm --cpus 1 --memory 1200m --network none --user 0 \
  -v /opt/ctg-research/repo:/w -w /w -e PYTHONDONTWRITEBYTECODE=1 \
  --entrypoint nice ctg-strategy:local -n 15 python3 "$@"
