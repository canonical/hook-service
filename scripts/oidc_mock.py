#!/usr/bin/env python3
# Copyright 2026 Canonical Ltd.
# SPDX-License-Identifier: Apache-2.0

"""
Mock OIDC Discovery & Metadata Server.
Provides discovery endpoints (/.well-known/openid-configuration) and empty JWKS (/jwks)
on port 8888 so that Secure Token Service (Janus) can initialize its OIDC client provider.
"""

import http.server
import json
import socketserver
import sys

PORT = 8888

class OIDCHandler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path.startswith("/.well-known/openid-configuration"):
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            data = {
                "issuer": f"http://localhost:{PORT}",
                "authorization_endpoint": f"http://localhost:{PORT}/auth",
                "token_endpoint": f"http://localhost:{PORT}/token",
                "jwks_uri": f"http://localhost:{PORT}/jwks",
                "response_types_supported": ["code"],
                "subject_types_supported": ["public"],
                "id_token_signing_alg_values_supported": ["RS256", "ES256"]
            }
            self.wfile.write(json.dumps(data).encode())
        elif self.path.startswith("/jwks"):
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps({"keys": []}).encode())
        elif self.path.startswith("/healthz"):
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.end_headers()
            self.wfile.write(b"ok")
        else:
            self.send_response(404)
            self.end_headers()

    def log_message(self, format, *args):
        # Silence verbose request logs
        pass

if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else PORT
    socketserver.TCPServer.allow_reuse_address = True
    with socketserver.TCPServer(("0.0.0.0", port), OIDCHandler) as httpd:
        print(f"Mock OIDC Server listening on 0.0.0.0:{port}")
        httpd.serve_forever()
