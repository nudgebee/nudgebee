#!/usr/bin/env python3
"""Single-worker API shared by both namespaces.

One request at a time, each held for 12s. That is enough for the batch flood to
occupy it permanently, so any other caller — including web-app's liveness probe —
times out. The server itself never errors, so its own logs look fine.
"""

import time
from http.server import BaseHTTPRequestHandler, HTTPServer


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        # Deliberately serialised: this is the contended resource.
        time.sleep(12)
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"ok")

    def log_message(self, fmt, *args):
        print("shared-api %s" % (fmt % args), flush=True)


HTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
