"""screenshots pages in headless firefox over webdriver bidi, which can set a
device pixel ratio. usage: firefox.py jobs.json, a list of
{"html", "out", "dpr", "width", "height"} with width and height in css px."""

import base64
import json
import subprocess
import sys
import tempfile
import time

import websocket

PORT = 9333

jobs = json.load(open(sys.argv[1]))
profile = tempfile.mkdtemp(prefix="textcompare-")
with open(profile + "/user.js", "w") as f:
    # the pages load the bundled fonts from another directory
    f.write('user_pref("security.fileuri.strict_origin_policy", false);\n')
ff = subprocess.Popen(
    ["firefox", "--headless", "--no-remote", "--profile", profile, f"--remote-debugging-port={PORT}"],
    stdout=subprocess.DEVNULL,
    stderr=subprocess.DEVNULL,
)
try:
    err = None
    for _ in range(200):
        try:
            # firefox refuses connections that carry an origin
            ws = websocket.create_connection(f"ws://127.0.0.1:{PORT}/session", suppress_origin=True)
            break
        except OSError as e:
            err = e
            time.sleep(0.05)
    else:
        sys.exit(f"firefox never listened: {err}")

    n = 0

    def call(method, params):
        global n
        n += 1
        ws.send(json.dumps({"id": n, "method": method, "params": params}))
        while True:
            m = json.loads(ws.recv())
            if m.get("id") == n:
                if m.get("type") == "error":
                    sys.exit(f"{method}: {m.get('message')}")
                return m["result"]

    call("session.new", {"capabilities": {}})
    ctx = call("browsingContext.create", {"type": "tab"})["context"]
    for j in jobs:
        viewport = {"width": j["width"], "height": j["height"]}
        call("browsingContext.setViewport", {"context": ctx, "viewport": viewport, "devicePixelRatio": j["dpr"]})
        call("browsingContext.navigate", {"context": ctx, "url": "file://" + j["html"], "wait": "complete"})
        call("script.evaluate", {
            "expression": "document.fonts.ready.then(() => true)",
            "target": {"context": ctx},
            "awaitPromise": True,
        })
        shot = call("browsingContext.captureScreenshot", {"context": ctx, "origin": "viewport"})
        with open(j["out"], "wb") as f:
            f.write(base64.b64decode(shot["data"]))
finally:
    ff.kill()
