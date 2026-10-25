from http.server import BaseHTTPRequestHandler, HTTPServer
import itertools

counter = itertools.count()

class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length)
        index = next(counter)
        with open(f"/tmp/qiyun-rate-{index}.json", "wb") as handle:
            handle.write(body)
        self.send_response(201)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(b'{"ok":true}')
    def log_message(self, *args):
        pass

HTTPServer(("127.0.0.1", 8808), Handler).serve_forever()