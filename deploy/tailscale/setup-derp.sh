#!/usr/bin/env bash
# Self-hosted Tailscale DERP relay + STUN on a small VPS (tested on Ubuntu
# 26.04, x86_64). Used by fusion-console remote access when phone and office
# host cannot connect peer-to-peer: traffic then relays through this server
# instead of Tailscale's shared public relays.
#
# Idempotent: rerun to upgrade derper or reprint the derpMap snippet.
#
# Usage (on the VPS):
#   sudo bash setup-derp.sh <public-ip> [derp-tcp-port] [stun-udp-port]
#   sudo TS_AUTHKEY=tskey-auth-... bash setup-derp.sh 203.0.113.10
#
# Defaults: DERP over TLS on TCP 8443, STUN on UDP 3478. TCP/UDP 443 are left
# untouched so the script coexists with other services on the box.
# The certificate is self-signed for the IP address; clients pin it through
# the CertName printed at the end, so no domain name is needed.
set -euo pipefail

IP="${1:-}"
DERP_PORT="${2:-8443}"
STUN_PORT="${3:-3478}"
TS_VERSION="${TS_VERSION:-1.104.7}"
TS_HOSTNAME="${TS_HOSTNAME:-derp-relay}"
REGION_ID="${REGION_ID:-900}"
REGION_CODE="${REGION_CODE:-own}"
CERT_DIR=/var/lib/derper

die() { echo "setup-derp: $*" >&2; exit 1; }
say() { printf '\n==> %s\n' "$*"; }

[ -n "$IP" ] || die "usage: sudo bash setup-derp.sh <public-ip> [derp-tcp-port] [stun-udp-port]"
[ "$(id -u)" -eq 0 ] || die "run as root (sudo)"
[[ "$IP" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "<public-ip> must be an IPv4 address, got '$IP'"

for p in "$DERP_PORT/tcp" "$STUN_PORT/udp"; do
    proto="${p#*/}"; port="${p%/*}"
    flag=-lnt; [ "$proto" = udp ] && flag=-lnu
    if ss "$flag" "( sport = :$port )" | tail -n +2 | grep -q . \
        && ! ss "${flag}p" "( sport = :$port )" | grep -q derper; then
        die "$port/$proto is already used by another process — pick other ports"
    fi
done

say "1/5 Tailscale client (needed so derper only admits your own tailnet devices)"
if ! command -v tailscale >/dev/null 2>&1; then
    curl -fsSL https://tailscale.com/install.sh | sh
fi
systemctl enable --now tailscaled >/dev/null
# --accept-dns=false / --accept-routes=false: keep the VPS's own DNS and
# routing exactly as they are (other services on the box are unaffected).
if tailscale status --json 2>/dev/null | grep -q '"BackendState": "Running"'; then
    echo "tailscale already logged in: $(tailscale ip -4 2>/dev/null | head -1)"
elif [ -n "${TS_AUTHKEY:-}" ]; then
    tailscale up --authkey="$TS_AUTHKEY" --hostname="$TS_HOSTNAME" --accept-dns=false --accept-routes=false
else
    echo "tailscale is installed but not logged in. In another shell run:"
    echo "  sudo tailscale up --hostname=$TS_HOSTNAME --accept-dns=false --accept-routes=false"
    echo "open the printed URL, approve the device, then rerun this script."
    echo "(derper is still installed below; it rejects every client until this host is logged in.)"
fi

say "2/5 derper v$TS_VERSION"
if ! /usr/local/bin/derper --version 2>/dev/null | grep -q "$TS_VERSION"; then
    command -v go >/dev/null 2>&1 || { apt-get update -qq && apt-get install -y -qq golang-go; }
    # GOTOOLCHAIN=auto lets an older distro Go fetch the toolchain go.mod asks for.
    GOTOOLCHAIN=auto GOPATH=/root/go GOBIN=/usr/local/bin GOFLAGS=-trimpath \
        go install "tailscale.com/cmd/derper@v$TS_VERSION"
fi
/usr/local/bin/derper --version | head -1

say "3/5 systemd unit"
mkdir -p "$CERT_DIR"
chmod 700 "$CERT_DIR"
cat > /etc/systemd/system/derper.service <<EOF
[Unit]
Description=Tailscale DERP relay (self-hosted)
After=network-online.target tailscaled.service
Wants=network-online.target tailscaled.service

[Service]
ExecStart=/usr/local/bin/derper -a :$DERP_PORT -http-port -1 -stun-port $STUN_PORT \\
  -certmode manual -certdir $CERT_DIR -hostname $IP -verify-clients -home blank
Restart=always
RestartSec=5
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=$CERT_DIR
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable derper >/dev/null
systemctl restart derper

say "4/5 host firewall"
if command -v ufw >/dev/null 2>&1 && ufw status | grep -q "Status: active"; then
    ufw allow "$DERP_PORT/tcp" >/dev/null
    ufw allow "$STUN_PORT/udp" >/dev/null
    echo "ufw: allowed $DERP_PORT/tcp and $STUN_PORT/udp"
else
    echo "ufw not active — nothing to change on the host"
fi
echo "Cloud console: also allow inbound TCP $DERP_PORT and UDP $STUN_PORT in the provider's security group / firewall."

say "5/5 derpMap snippet for the tailnet policy file"
for _ in $(seq 1 20); do
    [ -s "$CERT_DIR/$IP.crt" ] && break
    sleep 0.5
done
[ -s "$CERT_DIR/$IP.crt" ] || die "derper did not create its certificate — check: journalctl -u derper -n 50"
systemctl is-active --quiet derper || die "derper is not running — check: journalctl -u derper -n 50"
CERT_NAME="sha256-raw:$(openssl x509 -in "$CERT_DIR/$IP.crt" -outform DER | sha256sum | cut -d' ' -f1)"
cat <<EOF

Paste into the tailnet policy file (admin console -> Access controls), top level:

  "derpMap": {
    "OmitDefaultRegions": false,
    "Regions": {
      "$REGION_ID": {
        "RegionID": $REGION_ID,
        "RegionCode": "$REGION_CODE",
        "RegionName": "Self-hosted relay",
        "Nodes": [{
          "Name": "${REGION_ID}a",
          "RegionID": $REGION_ID,
          "HostName": "$IP",
          "IPv4": "$IP",
          "DERPPort": $DERP_PORT,
          "STUNPort": $STUN_PORT,
          "CertName": "$CERT_NAME"
        }]
      }
    }
  }

Then on any tailnet device: tailscale netcheck  (the region should report a latency).
EOF
