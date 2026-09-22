#!/usr/bin/env python3
"""本地回显服务 —— httprunnerplatform 实测与单测用的确定性目标。

为什么需要它：引擎实测若依赖 httpbin.org / postman-echo.com 等公网服务，
结果会受网络波动影响，也没法测「超时」这类场景。本服务提供最小可用的
httpbin 风格接口，让实测可重复、可离线运行。

用法::

    python testdata/local-echo/echo_server.py [port]      # 默认 8899
    python testdata/local-echo/echo_server.py 8899 -v     # 打开访问日志

接口::

    ANY  /get /post /anything   回显请求（args/headers/json/form/data/url/method）
    ANY  /status/<code>         返回指定状态码
    ANY  /delay/<seconds>       延迟指定秒数后返回（用于测试超时）
"""

import json
import sys
import time
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

DEFAULT_PORT = 8899


def build_payload(handler: "EchoHandler", body: bytes) -> dict:
    """按 httpbin 风格组装回显载荷。"""
    parsed = urllib.parse.urlparse(handler.path)
    args = dict(urllib.parse.parse_qsl(parsed.query))
    headers = {k: v for k, v in handler.headers.items()}
    content_type = headers.get("Content-Type", "")
    text = body.decode("utf-8", errors="replace")

    payload = {
        "args": args,
        "headers": headers,
        "method": handler.command,
        "path": parsed.path,
        "url": f"http://{headers.get('Host', '')}{handler.path}",
        "data": text,
    }
    if "application/json" in content_type:
        try:
            payload["json"] = json.loads(text) if text else None
        except json.JSONDecodeError:
            payload["json"] = None
    if "application/x-www-form-urlencoded" in content_type:
        payload["form"] = dict(urllib.parse.parse_qsl(text))
    return payload


class EchoHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = "LocalEcho/1.0"

    def _handle(self) -> None:
        length = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(length) if length else b""
        path = urllib.parse.urlparse(self.path).path

        if path.startswith("/status/"):
            try:
                code = int(path.rsplit("/", 1)[1])
            except ValueError:
                code = 400
            self._send(code, {"status": code, "path": path})
            return

        if path.startswith("/delay/"):
            try:
                seconds = float(path.rsplit("/", 1)[1])
            except ValueError:
                seconds = 0.0
            time.sleep(seconds)
            self._send(200, {"delay": seconds, "path": path})
            return

        self._send(200, build_payload(self, body))

    def _send(self, code: int, payload: dict) -> None:
        raw = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = do_HEAD = _handle

    def log_message(self, fmt: str, *args) -> None:
        if "-v" in sys.argv:
            super().log_message(fmt, *args)


def main() -> int:
    port = DEFAULT_PORT
    for arg in sys.argv[1:]:
        if arg.isdigit():
            port = int(arg)

    server = ThreadingHTTPServer(("127.0.0.1", port), EchoHandler)
    print(f"local echo server listening on http://127.0.0.1:{port}", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\nshutting down", flush=True)
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
