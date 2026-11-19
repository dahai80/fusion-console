#!/usr/bin/env python3
"""Per-user macOS updater. Console forwards authenticated requests to this private socket."""
import fcntl
import hashlib
import http.server
import json
import logging
import os
from pathlib import Path
import platform
import re
import shutil
import socketserver
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request
import zipfile

REPO = "dahai80/fusion-console"
INTERVAL = 600
SHA = re.compile(r"[a-f0-9]{40}")
FILES = ("fusion-console", "sidecar/harnessd.py")


def save(file, value):
    with tempfile.NamedTemporaryFile(dir=file.parent, mode="w", delete=False) as f:
        json.dump(value, f)
        f.flush()
        os.fsync(f.fileno())
    os.replace(f.name, file)


def place(src, dest):
    dest.parent.mkdir(parents=True, exist_ok=True)
    temp = dest.with_name("." + dest.name + ".install")
    shutil.copy2(src, temp)
    os.replace(temp, dest)


def command(args, cwd=None, timeout=120):
    subprocess.run(args, cwd=cwd, stdin=subprocess.DEVNULL, check=True, timeout=timeout)


class Updater:
    def __init__(self, root):
        self.root = root
        self.config = json.loads((root / "settings.json").read_text())
        self.base = self.config["url"]
        self.bin = Path(self.config["bin"])
        self.job = self.config["job"]
        self.key = (root / "drain.key").read_text()
        self.lock = threading.RLock()
        self.wake = threading.Event()
        self.force = True
        self.preferences = root / "preferences.json"
        self.pref = json.loads(self.preferences.read_text()) if self.preferences.exists() else {}
        self.state = dict(enabled=True, current="", latest="", phase="checking", checked=0, title="", error="", available=False)

    def set(self, **fields):
        with self.lock:
            self.state.update(fields)

    def snapshot(self):
        with self.lock:
            return dict(self.state, auto=bool(self.pref.get("auto")))

    def action(self, data):
        with self.lock:
            action = data.get("action")
            if action == "auto" and type(data.get("enabled")) is bool:
                self.pref["auto"] = data["enabled"]
            elif action == "check":
                self.force = True
            elif action == "install":
                if not self.state["available"] or data.get("target") != self.state["latest"]:
                    raise ValueError("可用版本已变化，请重新检查")
                self.pref["target"] = self.state["latest"]
                self.pref.pop("failed", None)
            else:
                raise ValueError("无效的更新操作")
            save(self.preferences, self.pref)
            self.wake.set()

    def health(self):
        with urllib.request.urlopen(self.base + "/api/health", timeout=3) as r:
            return json.load(r)

    def healthy(self, revision):
        try:
            health = self.health()
            if health.get("status") != "ok" or health.get("revision", "") != revision:
                return False
            with urllib.request.urlopen(self.base + "/", timeout=2) as r:
                return b'type="module"' in r.read(32768)
        except (OSError, ValueError):
            return False

    def github(self, route):
        out = subprocess.check_output([self.config["gh"], "api", "--hostname", "github.com", "repos/" + REPO + "/" + route], timeout=35)
        return json.loads(out)

    def newest(self):
        runs = self.github("actions/workflows/ci.yml/runs?branch=main&event=push&status=success&per_page=1")["workflow_runs"]
        if not runs:
            raise RuntimeError("上游暂时没有通过 CI 的主分支版本")
        run = runs[0]
        if (run["head_branch"], run["event"], run["conclusion"], run["head_repository"]["full_name"]) != ("main", "push", "success", REPO) or not SHA.fullmatch(run["head_sha"]):
            raise RuntimeError("无法确认上游构建来源")
        return run["head_sha"], run["display_title"], run["id"]

    def stage(self, sha, run_id):
        arch = {"arm64": "arm64", "x86_64": "amd64"}[platform.machine()]
        name = "fusion-console-darwin-" + arch
        ready = self.root / "artifact-stage"
        if (ready / "revision").is_file() and (ready / "revision").read_text() == sha:
            return ready
        artifacts = self.github(f"actions/runs/{run_id}/artifacts")["artifacts"]
        artifact = next((a for a in artifacts if a["name"] == name and not a["expired"]
                         and a["workflow_run"]["head_sha"] == sha and a["workflow_run"]["id"] == run_id), None)
        if not artifact or not re.fullmatch(r"sha256:[a-f0-9]{64}", artifact.get("digest") or ""):
            raise RuntimeError("此版本的 macOS 构建不可用或已过期")
        with tempfile.TemporaryDirectory(dir=self.root) as tmp:
            archive = Path(tmp) / "artifact.zip"
            with archive.open("wb") as out:
                subprocess.run([self.config["gh"], "api", "--hostname", "github.com", f"repos/{REPO}/actions/artifacts/{artifact['id']}/zip"],
                               stdout=out, stdin=subprocess.DEVNULL, check=True, timeout=180)
            digest = hashlib.sha256()
            with archive.open("rb") as f:
                for chunk in iter(lambda: f.read(1024 * 1024), b""):
                    digest.update(chunk)
            if "sha256:" + digest.hexdigest() != artifact["digest"]:
                raise RuntimeError("上游构建的 SHA-256 校验失败")
            package = Path(tmp) / "package"
            (package / "sidecar").mkdir(parents=True)
            with zipfile.ZipFile(archive) as z:
                for member, dest in [(name, FILES[0]), (FILES[1], FILES[1])]:
                    if member not in z.namelist():
                        raise RuntimeError("上游构建缺少 Console 或配套的 sidecar")
                    with z.open(member) as src, (package / dest).open("wb") as target:
                        shutil.copyfileobj(src, target)
            (package / FILES[0]).chmod(0o755)
            (package / "revision").write_text(sha)
            if ready.exists():
                shutil.rmtree(ready)
            os.rename(package, ready)
        return ready

    def restart(self):
        command(["/bin/launchctl", "kickstart", "-k", self.job], timeout=30)

    def wait_health(self, sha):
        for _ in range(30):
            if self.healthy(sha):
                return True
            time.sleep(0.5)
        return False

    def recover(self):
        marker = self.root / "switch.json"
        if not marker.exists():
            return
        txn = json.loads(marker.read_text())
        if not self.healthy(txn["to"]):
            for name in FILES:
                place(Path(txn["backup"]) / name, self.bin / name)
            self.restart()
            if not self.wait_health(txn["from"]):
                raise RuntimeError("旧版已恢复到磁盘，Console 尚未通过启动检查")
            marker.unlink()
            raise RuntimeError("新版启动检查失败，已恢复上一版 Console")
        marker.unlink()

    def switch(self, ready, current, sha):
        backup = self.root / "previous-program"
        for name in FILES:
            place(self.bin / name, backup / name)
        save(self.root / "switch.json", {"from": current, "to": sha, "backup": str(backup)})
        self.set(phase="restarting")
        try:
            for name in FILES:
                place(ready / name, self.bin / name)
            self.restart()
            self.wait_health(sha)
        finally:
            self.recover()

    def tick(self):
        self.recover()
        current = self.health().get("revision", "")
        if not SHA.fullmatch(current):
            raise RuntimeError("当前程序没有可核对的上游版本号")
        with self.lock:
            changed = self.state["current"] != current
            self.set(current=current)
            refresh = changed or self.force or time.time() - self.state["checked"] >= INTERVAL
            self.force = False
        if refresh:
            self.set(phase="checking", error="", checked=0, available=False)
            latest, title, run_id = self.newest()
            available = latest != current
            if available:
                relation = self.github("compare/" + current + "..." + latest)["status"]
                if relation == "behind":
                    # The current main commit can be newer than the last green CI.
                    available = False
                elif relation != "ahead":
                    raise RuntimeError("本机提交与上游分支已分叉，请检查提交记录")
            self.set(latest=latest, title=title, run_id=run_id, available=available, checked=int(time.time()))
        with self.lock:
            sha = self.state["latest"]
            if self.pref.get("target") and self.pref["target"] != sha:
                self.pref.pop("target")
                save(self.preferences, self.pref)
            wanted = self.pref.get("target") == sha or self.pref.get("auto")
            if not self.state["available"] or not wanted or self.pref.get("failed") == sha:
                self.set(phase="idle")
                return
        self.set(phase="building", error="")
        try:
            ready = self.stage(sha, self.state["run_id"])
            with self.lock:
                if not (self.pref.get("target") == sha or self.pref.get("auto")):
                    self.set(phase="idle")
                    return
            req = urllib.request.Request(self.base + "/api/console-update/drain", method="POST",
                                         headers={"X-Console-Drain": self.key})
            try:
                with urllib.request.urlopen(req, timeout=4) as response:
                    if response.status != 204:
                        raise RuntimeError("无法确认本机空闲状态")
            except urllib.error.HTTPError as e:
                if e.code == 409:
                    self.set(phase="waiting")
                    return
                raise
            self.switch(ready, current, sha)
        except Exception:
            with self.lock:
                self.pref["failed"] = sha
                self.pref.pop("target", None)
                save(self.preferences, self.pref)
            raise
        with self.lock:
            self.pref.pop("target", None)
            self.pref.pop("failed", None)
            save(self.preferences, self.pref)
        self.set(current=sha, available=False, phase="idle", error="")

    def loop(self):
        while True:
            self.wake.clear()
            try:
                self.tick()
            except Exception as e:
                logging.exception("update cycle failed")
                msg = str(e) if isinstance(e, RuntimeError) else "更新检查或准备失败，可重新检查并重试"
                self.set(phase="error", error=msg)
            phase = self.snapshot()["phase"]
            wait = {"waiting": 10, "error": 60}.get(phase, INTERVAL)
            self.wake.wait(wait)


class Handler(http.server.BaseHTTPRequestHandler):
    def respond(self, code, obj):
        data = json.dumps(obj, ensure_ascii=False).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, *args):
        pass

    def do_GET(self):
        if self.path != "/update":
            return self.respond(404, {})
        self.respond(200, self.server.updater.snapshot())

    def do_POST(self):
        if self.path != "/update":
            return self.respond(404, {})
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if not 0 < length <= 1024:
                raise ValueError("无效的请求")
            data = json.loads(self.rfile.read(length))
            if not isinstance(data, dict):
                raise ValueError("无效的请求")
            self.server.updater.action(data)
        except ValueError as e:
            return self.respond(400, {"error": str(e)})
        self.respond(202, self.server.updater.snapshot())


if __name__ == "__main__":
    os.umask(0o077)
    root = Path(sys.argv[1]).resolve()
    lock = (root / "service.lock").open("a")
    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    updater = Updater(root)
    socket_path = root / "control.sock"
    socket_path.unlink(missing_ok=True)
    with socketserver.UnixStreamServer(str(socket_path), Handler) as service:
        service.updater = updater
        threading.Thread(target=updater.loop, daemon=True).start()
        service.serve_forever()
