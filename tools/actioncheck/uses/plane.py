"""A stand in for GitHub's identity endpoint and for the control plane.

For the job in ci.yml that runs action.yml whole. It answers the three requests
the action makes of somebody else, and records each one so the job can assert
the action made it and made it correctly:

  GET  /identity?...&audience=...   GitHub minting a workflow identity
  POST <prefix>/v1/pr/callback-token  the claim
  POST <prefix>/v1/pr/report          the report

The prefix picks the answer, so one server serves every case the job runs.
Under /accept the control plane takes the report. Under /refuse it issues a
credential and then refuses the report with a 409, which is the one answer the
action is written to fail a build over.

It never records a credential or an identity. It records whether the request
carried the one it was issued, as a yes or a no, because the record is printed
into a public build log and the point is the wiring, not the value.

Usage: python3 plane.py <port> <record file>
"""

import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import parse_qs, urlsplit

IDENTITY = "stand-in-identity"
CREDENTIAL = "stand-in-credential"


class Handler(BaseHTTPRequestHandler):
    record = ""

    def log_message(self, *args):
        # The default writes every request line to stderr. The record below is
        # the log, and it is the one that leaves out the Authorization header.
        pass

    def keep(self, entry):
        with open(self.record, "a", encoding="utf-8") as f:
            f.write(json.dumps(entry, sort_keys=True) + "\n")

    def answer(self, code, body):
        data = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def bearer(self, want):
        return self.headers.get("Authorization", "") == "Bearer " + want

    def body(self):
        length = int(self.headers.get("content-length") or 0)
        raw = self.rfile.read(length) if length else b""
        try:
            return json.loads(raw or b"{}")
        except ValueError:
            return None

    def do_GET(self):
        url = urlsplit(self.path)
        if url.path != "/identity":
            self.answer(404, {"error": "no such route " + url.path})
            return
        audience = parse_qs(url.query).get("audience", [""])
        self.keep({"request": "identity", "audience": audience[-1],
                   "bearer": bool(self.headers.get("Authorization"))})
        self.answer(200, {"value": IDENTITY})

    def do_POST(self):
        url = urlsplit(self.path)
        mode, _, route = url.path.lstrip("/").partition("/")
        route = "/" + route
        body = self.body()
        if mode not in ("accept", "refuse"):
            self.answer(404, {"error": "no such control plane " + mode})
            return
        if route == "/v1/pr/callback-token":
            self.keep({"request": "claim", "plane": mode,
                       "identity": self.bearer(IDENTITY),
                       "head_sha": (body or {}).get("head_sha", "")})
            self.answer(200, {"token": CREDENTIAL})
            return
        if route == "/v1/pr/report":
            report = (body or {}).get("report")
            self.keep({"request": "report", "plane": mode,
                       "credential": self.bearer(CREDENTIAL),
                       "head_sha": (body or {}).get("head_sha", ""),
                       "markdown": bool((body or {}).get("markdown")),
                       "verdict": (report or {}).get("verdict", "")})
            if mode == "refuse":
                self.answer(409, {"error": "the check on this commit is already passed"})
            else:
                self.answer(200, {"ok": True})
            return
        self.answer(404, {"error": "no such route " + route})


def main():
    port, record = int(sys.argv[1]), sys.argv[2]
    Handler.record = record
    HTTPServer(("127.0.0.1", port), Handler).serve_forever()


if __name__ == "__main__":
    main()
