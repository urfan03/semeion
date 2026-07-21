"""semeion — Python model plane HTTP server.

Exposes the model_provider functions over JSON/HTTP so the Go engine's
`model.HTTPProvider` can call them. Stdlib only (http.server) — no framework.

    python server.py [port]        # default 8899, or $SEMEION_MODEL_PORT

Binds 127.0.0.1 by default; set SEMEION_MODEL_HOST=0.0.0.0 to expose it (the
container image does exactly that).

Endpoints (POST, JSON in/out):
    /detect_seasonality  {"series":[...]}            -> {"periods":[...]}
    /decompose           {"series":[...],"period":N} -> {"trend","seasonal","resid"}
    /forecast            {"series":[...],"horizon":N}-> {"forecast":[...]}
    /change_points       {"series":[...]}            -> {"change_points":[...]}
    /fit_distribution    {"samples":[...]}           -> {"family","params","loglik"}
"""

import json
import os
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import model_provider as mp


ROUTES = {
    "/detect_seasonality": lambda b: {"periods": mp.detect_seasonality(b["series"])},
    "/decompose": lambda b: mp.decompose(b["series"], int(b["period"])),
    "/forecast": lambda b: {"forecast": mp.forecast(b["series"], int(b["horizon"]))},
    "/change_points": lambda b: {"change_points": mp.change_points(b["series"])},
    "/fit_distribution": lambda b: mp.fit_distribution(b["samples"]),
}


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):  # quiet
        pass

    def do_GET(self):
        if self.path != "/healthz":
            self.send_error(404, "unknown method")
            return
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", "2")
        self.end_headers()
        self.wfile.write(b"ok")

    def do_POST(self):
        route = ROUTES.get(self.path)
        if route is None:
            self.send_error(404, "unknown method")
            return
        try:
            n = int(self.headers.get("Content-Length", 0))
            body = json.loads(self.rfile.read(n) or b"{}")
            result = route(body)
            payload = json.dumps(result).encode()
        except Exception as exc:  # noqa: BLE001 — surface any error as 400
            self.send_error(400, str(exc))
            return
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)


def main() -> None:
    port = int(sys.argv[1]) if len(sys.argv) > 1 else int(os.getenv("SEMEION_MODEL_PORT", "8899"))
    host = os.getenv("SEMEION_MODEL_HOST", "127.0.0.1")
    srv = ThreadingHTTPServer((host, port), Handler)
    print(f"semeion model plane on http://{host}:{port}", flush=True)
    srv.serve_forever()


if __name__ == "__main__":
    main()
