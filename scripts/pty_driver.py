#!/usr/bin/env python3

import base64
import os
import pty
import signal
import sys
import threading

MAX_TAIL_BYTES = 128 * 1024


def main() -> int:
    if len(sys.argv) < 2:
        print("usage: pty_driver.py <command> [args...]", file=sys.stderr)
        return 2

    pid, fd = pty.fork()
    if pid == 0:
        os.execv(sys.argv[1], sys.argv[1:])
        return 127

    tail = bytearray()
    lock = threading.Lock()

    def reader() -> None:
        while True:
            try:
                chunk = os.read(fd, 4096)
            except OSError:
                break
            if not chunk:
                break
            with lock:
                tail.extend(chunk)
                if len(tail) > MAX_TAIL_BYTES:
                    del tail[:-MAX_TAIL_BYTES]

    threading.Thread(target=reader, daemon=True).start()
    print("READY", flush=True)

    for raw in sys.stdin:
        line = raw.rstrip("\n")
        if line.startswith("send "):
            payload = base64.b64decode(line[5:].encode("ascii"))
            os.write(fd, payload)
            print("OK", flush=True)
            continue
        if line == "tail":
            with lock:
                encoded = base64.b64encode(bytes(tail)).decode("ascii")
            print(f"TAIL {encoded}", flush=True)
            continue
        if line == "stop":
            stop_child(pid)
            print("STOPPED", flush=True)
            return 0

    stop_child(pid)
    return 0


def stop_child(pid: int) -> None:
    try:
        os.killpg(os.getpgid(pid), signal.SIGTERM)
    except OSError:
        try:
            os.kill(pid, signal.SIGTERM)
        except OSError:
            pass

    try:
        os.waitpid(pid, 0)
    except ChildProcessError:
        pass


if __name__ == "__main__":
    raise SystemExit(main())
