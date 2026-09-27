#!/usr/bin/env python3
"""Local OAuth stub for E2E login — the Python twin of cmd/e2e's
startStubProvider, standalone so a manual run can log in without driving
the whole E2E stage machine.

  python3 e2e/stub-oauth.py &          # listens on 127.0.0.1:8098

Point the backend at it with AUTH_GITHUB_AUTHORIZE_ENDPOINT /
AUTH_GITHUB_TOKEN_ENDPOINT / AUTH_GITHUB_PROFILE_ENDPOINT (see run-live.sh).
The consent screen approves instantly — the run measures the studio's
machinery, not GitHub's consent page.
"""
import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import parse_qs, urlparse

ADDR = ("127.0.0.1", 8098)
PROFILE = {
    "id": 42000, "login": "e2e-live", "name": "E2E Live Run",
    "email": "e2e@talon.test", "avatar_url": "",
}


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        sys.stderr.write("stub-oauth: %s\n" % (fmt % args))

    def _redirect(self, loc):
        self.send_response(302)
        self.send_header("Location", loc)
        self.end_headers()

    def _json(self, obj, code=200):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _profile(self):
        if self.headers.get("Authorization") != "Bearer stub-access-token":
            self._json({"error": "unauthorized"}, 401)
            return
        self._json(PROFILE)

    def do_GET(self):
        u = urlparse(self.path)
        if u.path == "/profile":
            # The backend fetches the profile with GET (the Go stub's
            # HandleFunc answers any method — match that).
            self._profile()
            return
        if u.path != "/authorize":
            self.send_error(404)
            return
        q = parse_qs(u.query)
        redirect_uri = q.get("redirect_uri", [""])[0]
        state = q.get("state", [""])[0]
        # Mirrors the Go stub: bounce back to the backend's callback with
        # the consent code and the state it handed us.
        self._redirect(f"{redirect_uri}?code=stub-consent-code&state={state}")

    def do_POST(self):
        u = urlparse(self.path)
        n = int(self.headers.get("Content-Length") or 0)
        form = parse_qs(self.rfile.read(n).decode(errors="replace"))
        if u.path == "/token":
            if form.get("code", [""])[0] != "stub-consent-code":
                self._json({"error": "bad code"}, 400)
                return
            self._json({"access_token": "stub-access-token"})
        elif u.path == "/profile":
            self._profile()
        else:
            self.send_error(404)


if __name__ == "__main__":
    HTTPServer(ADDR, Handler).serve_forever()
