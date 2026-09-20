#!/usr/bin/env python3
"""Standalone captive portal responder for the Xenia-Lab hotspot.

Responds to Android/iOS connectivity checks so phones don't
drop the Wi-Fi connection due to "no internet" detection.
"""

from http.server import HTTPServer, BaseHTTPRequestHandler
import sys


class CaptiveHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        if "generate_204" in self.path or "gen_204" in self.path:
            self.send_response(204)
            self.end_headers()
        elif "hotspot-detect" in self.path or "captive" in self.path or "success" in self.path:
            self.send_response(200)
            self.send_header("Content-Type", "text/html")
            self.end_headers()
            self.wfile.write(
                b"<HTML><HEAD><TITLE>Success</TITLE></HEAD><BODY>Success</BODY></HTML>"
            )
        else:
            self.send_response(204)
            self.end_headers()

    def do_HEAD(self):
        self.do_GET()

    def log_message(self, format, *args):
        pass


if __name__ == "__main__":
    addr = sys.argv[1] if len(sys.argv) > 1 else "10.42.0.1"
    port = int(sys.argv[2]) if len(sys.argv) > 2 else 80
    server = HTTPServer((addr, port), CaptiveHandler)
    print(f"Captive portal responder on {addr}:{port}", flush=True)
    server.serve_forever()
