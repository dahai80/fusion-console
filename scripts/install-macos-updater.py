#!/usr/bin/env python3
"""Install per-user Console updates or attach them to an existing macOS LaunchAgent."""
import argparse
import json
import os
from pathlib import Path
import plistlib
import re
import secrets
import shutil
import socket
import subprocess
import sys
import urllib.request

import console_updater as update


def job_info(job):
    proc = subprocess.run(["launchctl", "print", job], capture_output=True, text=True)
    return proc.stdout if proc.returncode == 0 else ""


def write_plist(path, spec):
    path.parent.mkdir(parents=True, exist_ok=True)
    temp = path.with_suffix(".plist.install")
    temp.write_bytes(plistlib.dumps(spec))
    os.replace(temp, path)


def check_existing_job(job, binary, url, port):
    info = job_info(job)
    pid = re.search(r"^\s*pid = (\d+)$", info, re.MULTILINE)
    if not pid or not binary.is_file() or not os.access(binary.parent, os.W_OK):
        raise RuntimeError("Start the user-owned Console LaunchAgent and check its binary directory is writable.")
    listener = subprocess.run(["/usr/sbin/lsof", "-nP", "-a", "-p", pid[1], "-iTCP:" + str(port),
                               "-sTCP:LISTEN", "-t"], capture_output=True, text=True)
    if pid[1] not in listener.stdout.split() or f"program = {binary}\n" not in info:
        raise RuntimeError("The loaded Console job does not match the executable and port in its configuration.")
    with urllib.request.urlopen(url + "/api/health", timeout=4) as r:
        if not update.SHA.fullmatch(json.load(r).get("revision", "")):
            raise RuntimeError("Install an updater-capable Console build, then restart the job and rerun this command.")
    if not (binary.parent / update.FILES[1]).is_file():
        raise RuntimeError("Install the matching sidecar/harnessd.py alongside the Console executable.")


def install(console_plist):
    if sys.platform != "darwin" or os.getuid() == 0:
        raise RuntimeError("Run this command as your normal macOS login user.")
    gh = shutil.which("gh")
    if not gh:
        raise RuntimeError("Install GitHub CLI (gh) and sign in with read access to Actions artifacts.")
    subprocess.run([gh, "auth", "status", "--active", "--hostname", "github.com"],
                   check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    domain = "gui/" + str(os.getuid())
    agents = Path.home() / "Library/LaunchAgents"
    default_dir = Path.home() / ".fusion/fusion-console"
    label = "io.github.dahai80.fusion-console"
    plist = console_plist or agents / (label + ".plist")
    existing = plist.exists()
    if console_plist and not existing:
        raise RuntimeError("Cannot find the specified Console LaunchAgent plist.")
    if existing:
        spec = plistlib.loads(plist.read_bytes())
        label = spec["Label"]
        if not re.fullmatch(r"[A-Za-z0-9_.-]+", label):
            raise ValueError("Invalid LaunchAgent label.")
        binary = Path(spec.get("Program") or spec["ProgramArguments"][0])
        if binary.name != "fusion-console" or not binary.is_absolute():
            raise RuntimeError("The LaunchAgent must run the fusion-console executable directly.")
        base = Path(spec.get("EnvironmentVariables", {}).get("FUSION_CONSOLE_DIR", default_dir))
    else:
        base = default_dir
        binary = base / "bin/fusion-console"
    if not base.is_absolute():
        raise ValueError("FUSION_CONSOLE_DIR must be an absolute path.")
    config_path = base / "config.json"
    cfg = json.loads(config_path.read_text()) if config_path.is_file() else {}
    port = cfg.get("server", {}).get("port") or 11480
    url = "http://127.0.0.1:" + str(port)
    job = domain + "/" + label
    root = base / "updater"
    helper_label = label + "-updater"
    helper_job = domain + "/" + helper_label
    helper_plist = agents / (helper_label + ".plist")
    if existing:
        check_existing_job(job, binary, url, port)
    else:
        if binary.exists() or (binary.parent / update.FILES[1]).exists() or job_info(job):
            raise RuntimeError("An installation already exists. Pass its running user LaunchAgent with --console-plist.")
        with socket.socket() as probe:
            if probe.connect_ex(("127.0.0.1", port)) == 0:
                raise RuntimeError("The Console port is in use. Pass the existing user LaunchAgent with --console-plist.")
    service = root / "service.py"
    if helper_plist.exists():
        prior = plistlib.loads(helper_plist.read_bytes()).get("ProgramArguments", [])
        if str(service) not in prior:
            raise RuntimeError("The updater label belongs to another installation.")
    if job_info(helper_job) and not helper_plist.exists():
        raise RuntimeError("Another LaunchAgent is using the updater label.")
    if (root / "switch.json").exists():
        raise RuntimeError("An update is switching versions. Wait for it to complete before replacing the helper.")
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    key = root / "drain.key"
    if not key.exists():
        with key.open("x") as f:
            f.write(secrets.token_hex(32))
    settings = dict(url=url, bin=str(binary.parent), job=job, gh=gh)
    if job_info(helper_job):
        # Stop only our helper, after checking its live state. Console keeps running.
        result = subprocess.check_output(["/usr/bin/curl", "-fsS", "--max-time", "4",
                                          "--unix-socket", str(root / "control.sock"), "http://localhost/update"])
        if json.loads(result).get("phase") not in ("idle", "error"):
            raise RuntimeError("The updater is busy. Run the installer again when it is idle.")
        subprocess.run(["launchctl", "bootout", helper_job], check=True)
    update.save(root / "settings.json", settings)
    update.place(Path(update.__file__), service)
    if not existing:
        worker = update.Updater(root)
        sha, _, run_id = worker.newest()
        ready = worker.stage(sha, run_id)
        for name in update.FILES:
            update.place(ready / name, binary.parent / name)
        log = base / "log"
        log.mkdir(exist_ok=True)
        write_plist(plist, dict(Label=label, ProgramArguments=[str(binary), "-open"],
                                EnvironmentVariables={"FUSION_CONSOLE_DIR": str(base), "PATH": os.environ["PATH"]},
                                RunAtLoad=True, StandardErrorPath=str(log / "console.log")))
        subprocess.run(["launchctl", "bootstrap", domain, str(plist)], check=True)
        if not worker.wait_health(sha):
            raise RuntimeError("Console did not start. Check its log, then rerun the installer.")
    write_plist(helper_plist, dict(Label=helper_label, ProgramArguments=[sys.executable, str(service), str(root)],
                                   KeepAlive=True, RunAtLoad=True, StandardErrorPath=str(root / "service.log")))
    subprocess.run(["launchctl", "bootstrap", domain, str(helper_plist)], check=True)
    print("Installed Console updates at " + url)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--console-plist", type=Path, help="existing running per-user Console LaunchAgent")
    args = parser.parse_args()
    os.umask(0o077)
    try:
        install(args.console_plist)
    except (OSError, ValueError, RuntimeError, subprocess.CalledProcessError) as error:
        sys.exit(str(error))
