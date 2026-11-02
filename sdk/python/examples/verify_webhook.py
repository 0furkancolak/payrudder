"""Receive webhook deliveries and check their signature.

Set PAYRUDDER_WEBHOOK_SECRET to the secret of the endpoint that posts here.
"""

import os
from http.server import BaseHTTPRequestHandler, HTTPServer

from payrudder import WebhookSignatureError, construct_event

SECRET = os.environ["PAYRUDDER_WEBHOOK_SECRET"]


class Handler(BaseHTTPRequestHandler):
    def do_POST(self) -> None:
        # The signature is over the raw bytes: read the body as it arrived, parse it only after it verified.
        body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        try:
            event = construct_event(body, self.headers.get("Payrudder-Signature"), SECRET)
        except WebhookSignatureError:
            self.send_response(400)  # not signed by this endpoint's secret, or too old
            self.end_headers()
            return
        # Deliveries are at least once: ignore an event id you have already handled.
        print(event["id"], event["type"])
        self.send_response(204)
        self.end_headers()


if __name__ == "__main__":
    print("listening on http://localhost:3000/")
    HTTPServer(("", 3000), Handler).serve_forever()
