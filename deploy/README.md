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

## 3. Tailscale serve (HTTPS on the tailnet)

Same command on all three platforms:

```bash
tailscale serve --bg 127.0.0.1:11480
```

The console is then reachable at `https://<host>.<tailnet>.ts.net` with an
automatic certificate. All tailnet devices (laptop, phone) use this URL.

## 4. Security checklist (non-negotiable)

- [ ] Inbound firewall: allow the console port only on the Tailscale interface
      (Windows: Defender Firewall scoped to the Tailscale adapter;
      Ubuntu: `ufw` default deny in on non-`tailscale0` interfaces)
- [ ] Tailscale ACL: members → office hosts on port 11480 only
- [ ] Tailscale device approval enabled; remove devices when people leave
- [ ] App-level login stays on (shared/per-user passwords) — the tailnet is
      network-layer insurance, not a replacement for authentication
- [ ] Agents run inside their declared workdir only; budget cap stays set

## 5. First run

On first start the server creates an `admin` user with a generated password
and prints it once to the log. Change it after the first login. Additional
users: edit `<data-dir>/config.json`, add an entry under `users` with a
bcrypt hash (`htpasswd -bnBC 10 "" 'password'` prints one), then restart.

## 6. Host naming

Name office hosts `office-win-01`, `office-ubuntu-01`, `office-mac-01` in
Tailscale. Config + SQLite live under `~/.fusion-console` and can be copied
between hosts (rsync/scp) to migrate — the server is otherwise stateless.
