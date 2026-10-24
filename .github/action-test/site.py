#!/usr/bin/env python3
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT
"""A two-page test site for action-test.yml that records request headers.

    site.py PORT HEADERS_LOG

Serves / and /about.html on 127.0.0.1:PORT and appends every request's
headers to HEADERS_LOG as one JSON object per line, so a job can check
that a header from the Action's headers input arrived unchanged.
"""

import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PAGE = (
    '<!doctype html><html lang="en"><head><meta charset="utf-8"><title>{0}</title></head>'
    '<body><h1>{0}</h1><a href="/">Home</a> <a href="/about.html">About</a></body></html>\n'
)
PAGES = {"/": "Home", "/index.html": "Home", "/about.html": "About"}


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):  # noqa: N802 (http.server's name)
        with open(LOG, "a", encoding="utf-8") as f:
            f.write(json.dumps({"path": self.path, "headers": dict(self.headers.items())}) + "\n")
        path = self.path.split("?", 1)[0]
        if path == "/favicon.ico":
            self.send_response(204)
            self.end_headers()
            return
        if path not in PAGES:
            self.send_error(404)
            return
        body = PAGE.format(PAGES[path]).encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


if __name__ == "__main__":
    LOG = sys.argv[2]
    ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), Handler).serve_forever()
