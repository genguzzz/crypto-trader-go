"""NDJSON over a Unix socket."""

from __future__ import annotations

import json
import socket
import threading

MAX_LINE = 1 << 20


class ProtocolError(Exception):
    pass


class Conn:
    def __init__(self, sock: socket.socket, max_line: int = MAX_LINE):
        self.sock = sock
        self.rfile = sock.makefile("rb")
        self.lock = threading.Lock()
        self.max_line = max_line

    @classmethod
    def connect(cls, path: str, max_line: int = MAX_LINE) -> "Conn":
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        s.connect(path)
        return cls(s, max_line)

    def send(self, msg: dict) -> None:
        data = json.dumps(msg, separators=(",", ":"), allow_nan=False).encode() + b"\n"
        if len(data) > self.max_line:
            raise ProtocolError(f"message of {len(data)} bytes exceeds {self.max_line}")
        with self.lock:
            self.sock.sendall(data)

    def recv(self) -> dict | None:
        """Next message, or None at EOF."""
        line = self.rfile.readline(self.max_line + 1)
        if not line:
            return None
        if len(line) > self.max_line:
            raise ProtocolError("line too long")
        return json.loads(line)

    def close(self) -> None:
        try:
            self.rfile.close()
            self.sock.close()
        except OSError:
            pass
