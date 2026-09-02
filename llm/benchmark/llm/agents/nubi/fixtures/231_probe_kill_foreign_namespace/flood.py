#!/usr/bin/env python3
"""The batch job. Reindexes by hammering shared-api continuously.

Logs look like normal healthy batch progress — it has no idea it is starving
anyone, and nothing here says 'error'.
"""

import time
import urllib.request

batch = 0
while True:
    batch += 1
    try:
        urllib.request.urlopen(
            "http://shared-api.namespace-231-batch:8080/reindex", timeout=60
        ).read()
        print(f"reindex batch {batch} complete", flush=True)
    except Exception as exc:  # keep going; the job is 'healthy' from its own view
        print(f"reindex batch {batch} retry: {exc}", flush=True)
    time.sleep(0.2)
