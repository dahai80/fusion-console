# Remote access: Tailscale + a self-hosted relay

[中文（更详细）](TAILSCALE_CN.md)

Goal: use the console on the office machine from a phone or laptop anywhere,
without changing its guarantees — it listens on `127.0.0.1:11480` only, no
public port is ever opened for it, remote traffic goes through Tailscale only,
and the app login stays on.

The setup below is one office machine, one small VPS (Hong Kong in our case,
close to mainland China) and a phone. The VPS runs a self-hosted Tailscale
relay (DERP) used whenever the phone and the office machine cannot connect
peer-to-peer.

## 1. Architecture

```
 Phone (mobile data / any Wi-Fi)                    Office machine
 Tailscale app ── WireGuard ── direct if possible ──▶ Tailscale
        │                                            │
        │ NAT traversal failed                       ▼
        └──▶ VPS: derper relay ◀──────────── tailscale serve (HTTPS :443)
             TCP 8443 (relay, TLS)                     │
             UDP 3478 (STUN)                           ▼
                                             fusion-console 127.0.0.1:11480
```

- Traffic is end-to-end WireGuard; the relay only forwards ciphertext.
- Direct connections are preferred; the relay is the fallback. Mobile
  carriers often block hole punching, so the relay's latency matters.
- Why self-host: Tailscale has no relay inside mainland China and the nearest
  shared one is often congested. Your own VPS usually has a better route and
  serves only your devices.
- The control plane stays Tailscale's; no Headscale needed.

## 2. Checklist

| Item | Requirement |
|---|---|
| Office machine | `http://127.0.0.1:11480` works locally |
| VPS | 1 vCPU / 1 GB is plenty; Ubuntu 22.04+; public IPv4; SSH with sudo |
| Tailscale | Free plan; office machine, VPS and phone in the **same** tailnet |
| Phone | Tailscale app (iOS / Android) |

Open on the VPS provider's firewall: **TCP 8443** (relay; any free port works
if 443 is taken) and **UDP 3478** (STUN). Never open 11480 anywhere.

## 3. Office machine

Install Tailscale (macOS: App Store or tailscale.com/download; the CLI is
`/Applications/Tailscale.app/Contents/MacOS/Tailscale`. Ubuntu:
`curl -fsSL https://tailscale.com/install.sh | sh && sudo tailscale up`.
Windows: installer from tailscale.com). In the admin console → **DNS**, enable
**MagicDNS** and **HTTPS Certificates**.

## 4. VPS: deploy the relay

[`tailscale/setup-derp.sh`](tailscale/setup-derp.sh) installs the Tailscale
client, builds `derper`, writes a systemd unit, creates a self-signed
certificate for the IP and prints the `derpMap` snippet. It is idempotent.

```bash
scp deploy/tailscale/setup-derp.sh ubuntu@<VPS-IP>:/tmp/
ssh ubuntu@<VPS-IP> 'sudo bash /tmp/setup-derp.sh <VPS-IP>'      # [derp-port] [stun-port]
```

The relay runs with `-verify-clients`, so it only admits devices from your
tailnet — the VPS itself must be logged in:

```bash
sudo tailscale up --hostname=derp-relay --accept-dns=false --accept-routes=false
```

Approve the printed login URL with the same account, then rerun the script.
`--accept-dns=false --accept-routes=false` leave the VPS's own DNS and routes
untouched, so other services on it keep working.

With `-hostname` set to an IP, derper generates a self-signed certificate in
`/var/lib/derper/`; clients pin it through `CertName: "sha256-raw:…"`, so no
domain name or Let's Encrypt is needed. Keep that directory — a new
certificate means a new fingerprint.

Allow TCP 8443 and UDP 3478 in the provider's firewall / security group.
Check on the VPS:

```bash
systemctl status derper --no-pager
curl -sk -o /dev/null -w '%{http_code}\n' https://127.0.0.1:8443/derp/probe   # 200
```

Check reachability from outside with an external port checker. Do not test
with `nc` from a machine running a Clash / mihomo TUN: TUN reports every port
as open.

## 5. Add the relay to the tailnet policy

Admin console → **Access controls**, add the printed `derpMap` at the top
level:

```jsonc
"derpMap": {
  "OmitDefaultRegions": false,
  "Regions": {
    "900": {
      "RegionID": 900, "RegionCode": "own", "RegionName": "Self-hosted relay",
      "Nodes": [{
        "Name": "900a", "RegionID": 900,
        "HostName": "<VPS-IP>", "IPv4": "<VPS-IP>",
        "DERPPort": 8443, "STUNPort": 3478,
        "CertName": "sha256-raw:<fingerprint printed by the script>"
      }]
    }
  }
}
```

Keep `OmitDefaultRegions: false` at first (Tailscale's relays stay as
backup). Once yours has proven stable you may set it to `true` to force all
relayed traffic through it — at the cost of no fallback if the VPS is down.

Verify: `tailscale netcheck` lists `own` with a latency; `tailscale status`
shows `relay "own"` or `direct …` for your phone; `tailscale ping <device>`
shows which path each probe took.

## 6. Publish the console (HTTPS)

```bash
tailscale serve --bg 11480
tailscale serve status    # https://<machine>.<tailnet>.ts.net (tailnet only) -> http://127.0.0.1:11480
```

Do not use `tailscale serve --tcp 11480`: without HTTPS the page is not a
secure context (copy buttons and voice input break), session cookies are not
`Secure`, and the console cannot tell the request is remote. Remove an
existing TCP forward with `tailscale serve --tcp=11480 off` (or
`tailscale serve reset`). Never use `tailscale funnel`, which publishes to the
internet.

## 7. Phone

Install Tailscale, sign in with the same account, open
`https://<machine>.<tailnet>.ts.net`, sign in to the console, and add it to
the home screen. iOS runs one VPN at a time, so Tailscale and a proxy app
cannot be on together. Either switch between them, or make the VPS an exit
node (`net.ipv4.ip_forward=1`, `sudo tailscale set --advertise-exit-node`,
approve it in the admin console, pick it on the phone) — exit-node traffic is
plain WireGuard across the border and may be throttled on some networks.

## 8. Hardening (required)

1. **Change the factory password** `fusion-admin-2026`:
   `htpasswd -bnBC 10 "" 'new-password' | tr -d ':\n'`, put the hash in
   `password_hash` for `admin` in `~/.fusion/fusion-console/config.json`,
   restart the console.
2. **Restrict the tailnet policy** so only your devices reach the console
   machine, e.g. tag it `tag:console` and grant
   `{"src": ["autogroup:member"], "dst": ["tag:console"], "ip": ["tcp:443"]}`.
   Keep rules for your other traffic (SSH etc.) so you do not lock yourself out.
3. **Device approval** on; remove lost devices immediately.
4. **Host firewall**: `lsof -iTCP:11480 -sTCP:LISTEN` must show `127.0.0.1` only.
5. Keep dispatch approval on *confirm* when you work from the phone.

## 9. Living with Clash / mihomo on the office machine

A TUN-mode proxy can capture Tailscale traffic (symptoms: always relayed,
`tailscale ping` fails, `*.ts.net` does not resolve). Exclude Tailscale:

```yaml
tun:
  route-exclude-address: [100.64.0.0/10, fd7a:115c:a1e0::/48]
dns:
  fake-ip-filter: ["+.ts.net"]
rules:
  - IP-CIDR,100.64.0.0/10,DIRECT,no-resolve
  - IP-CIDR6,fd7a:115c:a1e0::/48,DIRECT,no-resolve
  - DOMAIN-SUFFIX,ts.net,DIRECT
  - IP-CIDR,<VPS-IP>/32,DIRECT,no-resolve
```

## 10. Troubleshooting

| Symptom | Check |
|---|---|
| `own` missing or timing out in `netcheck` | Provider firewall (TCP 8443 / UDP 3478); `systemctl status derper`; IP, ports and `CertName` in the policy match the script output |
| Peers still use `relay "hkg"` | Policy not pushed yet (wait, or restart Tailscale), or the official relay measured faster; consider `OmitDefaultRegions: true` once stable |
| derper logs rejections | The VPS is not logged in to your tailnet (`tailscale status`) |
| Everything broke after touching `/var/lib/derper` | New certificate, new fingerprint — rerun the script and update `CertName` |
| VPS IP changed | `sudo bash setup-derp.sh <new-IP>` and update `HostName`, `IPv4`, `CertName` |
| `https://…ts.net` does not load | MagicDNS and HTTPS Certificates enabled? `tailscale serve status` shows an `https://` line? |

## 11. Maintenance

```bash
journalctl -u derper -f                                   # relay log
sudo TS_VERSION=<version> bash /tmp/setup-derp.sh <VPS-IP> # upgrade derper
```

Uninstall: `sudo systemctl disable --now derper`, remove
`/etc/systemd/system/derper.service`, `/usr/local/bin/derper` and
`/var/lib/derper`, then delete the `derpMap` from the policy. Stop remote
access to the console with `tailscale serve reset` on the office machine.
