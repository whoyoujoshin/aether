#!/usr/bin/env python3
"""A trivial "tool" API for scripts/demo-agent-payment.sh to charge for:
one fact per request. Not meant to be run standalone.

    demo_upstream.py <host> <port>
"""
import http.server
import json
import random
import sys

FACTS = [
    "Octopuses have three hearts.",
    "Honey never spoils.",
    "A day on Venus is longer than its year.",
    "Bananas are berries; strawberries aren't.",
    "Sharks predate trees.",
]


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        body = json.dumps({"fact": random.choice(FACTS)}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *a):
        pass


if __name__ == "__main__":
    host, port = sys.argv[1], int(sys.argv[2])
    http.server.HTTPServer((host, port), Handler).serve_forever()
