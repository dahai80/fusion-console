# Deploy & remote access (Tailscale)

fusion-console binds to `127.0.0.1:11480` and never exposes a public port.
Remote access goes through Tailscale only — no Cloudflare Tunnel, no ngrok,
no port forwarding (see `docs/` in the design repo for the rationale).

## 1. Install the binary

```bash
# macOS (Apple Silicon)
./scripts/build.sh darwin-arm64 && sudo cp bin/fusion-console-darwin-arm64 /usr/local/bin/fusion-console

# Ubuntu
./scripts/build.sh linux-amd64 && sudo cp bin/fusion-console-linux-amd64 /usr/local/bin/fusion-console

# Windows: build or download fusion-console-windows-amd64.exe, e.g. C:\Tools\fusion-console.exe
```

## 2. Auto-start on boot

### macOS (launchd)

```bash
sudo cp deploy/com.dahai.fusion-console.plist /Library/LaunchDaemons/
sudo launchctl load /Library/LaunchDaemons/com.dahai.fusion-console.plist
```

### macOS user installation with updates

From a checkout of this repository, run the following as your normal login user.
Python 3.9+ and GitHub CLI signed in to github.com with read access to Actions artifacts are required.

```bash
python3 scripts/install-macos-updater.py
```

A fresh install downloads the latest successful upstream main build for Apple Silicon or Intel and registers two per-user LaunchAgents. To attach updates to an existing user LaunchAgent, use `--console-plist ~/Library/LaunchAgents/YOUR-CONSOLE.plist`. It must run an updater-capable Console directly, have a writable binary directory with the matching `sidecar/harnessd.py`, and expose its revision at `/api/health`. Older builds need a one-time upgrade to a build containing this feature. The system-wide LaunchDaemon above uses a different installation method.

The update menu next to the theme switch checks every ten minutes and offers manual installation or opt-in automatic updates. It uses the successful main workflow's artifact and verifies its SHA-256 digest. Installation waits for active chat requests and queued or running dispatches. A failed startup restores the previous executable and sidecar. The helper stores its state in the Console data directory under `updater/`. Rerun the installer to refresh the helper. To stop background updates entirely, unload the user job with `launchctl bootout gui/$(id -u)/YOUR-CONSOLE-LABEL-updater`.

### Ubuntu (systemd)

```bash
sudo cp deploy/fusion-console.service /etc/systemd/system/fusion-console@.service
sudo systemctl daemon-reload
sudo systemctl enable --now fusion-console@$USER
```

### Windows (schtasks, run at logon)

```powershell
schtasks /Create /TN "fusion-console" /TR "C:\Tools\fusion-console.exe" /SC ONLOGON /RL HIGHEST
```

(NSSM service install is an alternative if a pre-logon service is required.)

## 3. Remote access (Tailscale)

Step-by-step guide, including a self-hosted relay on a small VPS for
networks where peer-to-peer fails: [TAILSCALE.md](TAILSCALE.md) ·
[中文](TAILSCALE_CN.md). First time? [Quickstart](TAILSCALE_QUICKSTART.md) ·
[小白版](TAILSCALE_QUICKSTART_CN.md). Short version, on the office host:

```bash
tailscale serve --bg 11480
```

The console is then reachable at `https://<host>.<tailnet>.ts.net` with an
automatic certificate. Use this HTTPS form, not `tailscale serve --tcp`.

## 4. Security checklist (non-negotiable)

- [ ] Inbound firewall: allow the console port only on the Tailscale interface
      (Windows: Defender Firewall scoped to the Tailscale adapter;
      Ubuntu: `ufw` default deny in on non-`tailscale0` interfaces)
- [ ] Tailscale ACL: members → office hosts on port 443 (tailscale serve) only
- [ ] Tailscale device approval enabled; remove devices when people leave
- [ ] App-level login stays on (shared/per-user passwords) — the tailnet is
      network-layer insurance, not a replacement for authentication
- [ ] Agents run inside their declared workdir only; budget cap stays set

## 5. First run

On first start the server creates `admin` with the factory default password
`fusion-admin-2026` (or `FUSION_CONSOLE_ADMIN_PASSWORD` if set before the
first start). Change it before enabling remote access: generate a hash with
`htpasswd -bnBC 10 "" 'password' | tr -d ':\n'`, put it in the `admin`
entry's `password_hash` in `<data-dir>/config.json`, restart. Additional
users: add entries under `users` the same way.

## 6. Host naming

Name office hosts `office-win-01`, `office-ubuntu-01`, `office-mac-01` in
Tailscale. Config + SQLite live under `~/.fusion/fusion-console` and can be copied
between hosts (rsync/scp) to migrate — the server is otherwise stateless.
