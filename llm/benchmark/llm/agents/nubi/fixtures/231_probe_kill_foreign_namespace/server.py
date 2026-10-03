#!/usr/bin/env python3
"""Single-worker API shared by both namespaces.

One request at a time. /reindex holds the worker for 12s, which is enough for
the batch flood to occupy it permanently. /health is cheap and returns at once
when the worker is free — it times out only because it queues behind an
in-flight /reindex. That is the contention this fixture is about: without the
flood the probe passes. The server itself never errors, so its own logs look
fine.
"""

import time
from http.server import BaseHTTPRequestHandler, HTTPServer


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path.startswith("/reindex"):
            time.sleep(12)
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"ok")

    def log_message(self, fmt, *args):
        print("shared-api %s" % (fmt % args), flush=True)


HTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
