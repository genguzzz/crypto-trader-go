#!/bin/sh
# Weekly: extend the research panel with new OKX public candles / funding (research only, capped container).
cd /opt/ctg-research/repo && cp data/altbt/panel.npz data/altbt/panel.prev.npz
exec docker run --rm --cpus 1 --memory 1200m -v /opt/ctg-research/repo:/w -w /w ctg-research \
  nice -n 15 python3 research/altbt/fetch.py --update --workers 6 >> /opt/ctg-research/update-data.log 2>&1
