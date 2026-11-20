# 远程访问：Tailscale + 自建香港中继

[English](TAILSCALE.md)

目标：人在外面，用手机或笔记本打开办公室那台机器上的 fusion-console，像在本机一样用。约束不变：console 只监听 `127.0.0.1:11480`，公网上不开任何端口给它；远程只走 Tailscale；应用自己的登录始终开着。

这份指南按“办公室一台主机 + 一台香港 VPS + 手机”来写。香港 VPS 跑一个自建的 Tailscale 中继（DERP），手机和主机打洞失败时走它，不再挤 Tailscale 官方的共享中继。

## 目录

1. [整体结构](#1-整体结构)
2. [准备清单](#2-准备清单)
3. [办公室主机：安装并登录 Tailscale](#3-办公室主机安装并登录-tailscale)
4. [香港 VPS：部署中继](#4-香港-vps部署中继)
5. [把中继写进 tailnet 策略](#5-把中继写进-tailnet-策略)
6. [把 console 发布到 tailnet（HTTPS）](#6-把-console-发布到-tailnethttps)
7. [手机接入](#7-手机接入)
8. [安全加固（必做）](#8-安全加固必做)
9. [和 Clash / mihomo 等本机代理共存](#9-和-clash--mihomo-等本机代理共存)
10. [排错](#10-排错)
11. [日常维护与卸载](#11-日常维护与卸载)

## 1. 整体结构

```
 手机（4G/5G 或任意 Wi-Fi）                       办公室主机
 Tailscale App ── WireGuard 加密隧道 ── 能打洞 ──▶ Tailscale
        │                                         │
        │ 打洞失败（对称 NAT、运营商限制）              ▼
        └──▶ 香港 VPS：derper 中继 ◀───────── tailscale serve（HTTPS :443）
             TCP 8443（中继，TLS）                  │
             UDP 3478（STUN，帮两端找公网地址）       ▼
                                             fusion-console 127.0.0.1:11480
```

- **数据面**：手机和主机之间永远是端到端 WireGuard 加密。中继只转发密文，看不到内容。
- **直连优先**：两端能打洞就直连，中继只是兜底。大陆移动网络经常打不通，这时延迟和稳定性取决于中继。
- **为什么自建**：Tailscale 官方中继在大陆没有节点，最近的是共享的香港节点（`hkg`），高峰时丢包、限速明显。自己的香港 VPS 到大陆一般有更好的线路，而且只服务你自己的设备。
- **控制面**不变：登录、设备列表、策略仍由 Tailscale 官方管理。自建中继不需要 Headscale。

## 2. 准备清单

| 项目 | 要求 |
|---|---|
| 办公室主机 | 已能在本机打开 `http://127.0.0.1:11480`（macOS / Ubuntu / Windows 均可） |
| 香港 VPS | 1 核 1 GB 足够；Ubuntu 22.04 及以上；有公网 IPv4；能 SSH 登录并 `sudo` |
| Tailscale 账号 | 免费版即可；主机、VPS、手机登录**同一个** tailnet |
| 手机 | iOS 或 Android，装 Tailscale App |

VPS 需要在云厂商控制台放行的端口：

| 端口 | 协议 | 用途 |
|---|---|---|
| 8443 | TCP | DERP 中继（TLS）。443 被占用时用它；可换成任意空闲端口 |
| 3478 | UDP | STUN，帮两端探测公网地址、提高打洞成功率 |

console 本身的 11480 **不需要**、也**不应该**在任何防火墙上放行。

## 3. 办公室主机：安装并登录 Tailscale

**macOS**：从 App Store 或 <https://tailscale.com/download/mac> 安装，菜单栏图标里登录。命令行在 App 包里：

```bash
alias tailscale=/Applications/Tailscale.app/Contents/MacOS/Tailscale
tailscale status          # 能看到自己这台机器就行
```

**Ubuntu**：

```bash
curl -fsSL https://tailscale.com/install.sh | sh
sudo tailscale up
```

**Windows**：安装 <https://tailscale.com/download/windows>，托盘图标登录；命令行是 `tailscale.exe`。

然后在 Tailscale 管理后台 → **DNS** 页面：

1. 打开 **MagicDNS**（让 `<机器名>.<tailnet>.ts.net` 能解析）。
2. 打开 **HTTPS Certificates**（第 6 步的 `tailscale serve` 要用它自动签证书）。

记下主机的 tailnet 域名：`tailscale status --json | grep DNSName`，形如 `office-mac.tail1234.ts.net.`。

## 4. 香港 VPS：部署中继

仓库里的 [`deploy/tailscale/setup-derp.sh`](tailscale/setup-derp.sh) 一次做完：装 Tailscale 客户端、编译 derper、写 systemd 服务、生成自签证书、打印要贴进策略的配置。可以重复运行（升级、换端口、重新打印配置）。

### 4.1 上传并运行

在你自己的电脑上（仓库根目录）：

```bash
VPS=ubuntu@<香港VPS公网IP>
scp deploy/tailscale/setup-derp.sh $VPS:/tmp/
ssh $VPS 'sudo bash /tmp/setup-derp.sh <香港VPS公网IP>'
# 端口要换：sudo bash /tmp/setup-derp.sh <IP> <中继TCP端口> <STUN UDP端口>
```

VPS 能访问 GitHub 时也可以直接下载：`curl -fsSLO https://raw.githubusercontent.com/dahai80/fusion-console/main/deploy/tailscale/setup-derp.sh`。

第一次运行要几分钟：Ubuntu 自带的 Go 偏旧，脚本会让 Go 自动下载 tailscale 要求的工具链再编译。

脚本做的事：

| 步骤 | 内容 |
|---|---|
| 1/5 | 装 Tailscale 客户端。中继开了 `-verify-clients`，只放行**同一 tailnet** 的设备，所以 VPS 自己也要登录 |
| 2/5 | `go install tailscale.com/cmd/derper@v<版本>`，装到 `/usr/local/bin/derper` |
| 3/5 | 写 `/etc/systemd/system/derper.service` 并启动：`-a :8443 -http-port -1 -stun-port 3478 -certmode manual -hostname <IP> -verify-clients` |
| 4/5 | 如果 `ufw` 开着，放行两个端口；云厂商防火墙要你自己去控制台放行 |
| 5/5 | 打印 `derpMap` 片段，其中 `CertName` 是自签证书的指纹 |

关于证书：`-hostname` 填 IP 时，derper 会给这个 IP 生成自签证书（存在 `/var/lib/derper/`），客户端靠 `CertName: "sha256-raw:…"` 钉住这张证书，所以**不需要域名，也不需要 Let's Encrypt**。证书文件不要删，删了会生成新的，指纹随之变化，要重新贴策略。

### 4.2 让 VPS 登录 Tailscale

脚本发现还没登录时会提示。另开一个 SSH 窗口：

```bash
sudo tailscale up --hostname=derp-relay --accept-dns=false --accept-routes=false
```

打开它打印的 `https://login.tailscale.com/a/…` 链接，用和主机相同的账号批准。`--accept-dns=false --accept-routes=false` 保证 VPS 原有的 DNS 和路由不变，上面跑的其他服务（比如代理）不受影响。

登录后再跑一次脚本，确认第 1 步显示 `tailscale already logged in`。

### 4.3 云厂商防火墙

在控制台放行入站 **TCP 8443** 和 **UDP 3478**，来源 `0.0.0.0/0`：

- 腾讯云轻量应用服务器：实例详情 → 防火墙 → 添加规则。
- 腾讯云 CVM / 阿里云 ECS：安全组 → 入站规则。

### 4.4 自检

在 VPS 上：

```bash
systemctl status derper --no-pager              # active (running)
sudo ss -lntup | grep derper                    # *:8443 (tcp) 和 *:3478 (udp)
curl -sk -o /dev/null -w '%{http_code}\n' https://127.0.0.1:8443/derp/probe   # 200
journalctl -u derper -n 20 --no-pager
```

从外部确认端口真的通：用 <https://ping.pe> 之类的外部探测工具测 `<IP>:8443`。**不要在开着 Clash / mihomo TUN 模式的电脑上用 `nc`、`telnet` 测**，TUN 会把任何端口都伪装成“开放”，结果不可信。

## 5. 把中继写进 tailnet 策略

Tailscale 管理后台 → **Access controls**（策略文件，JSON）。在最外层加入脚本打印的 `derpMap`，例如：

```jsonc
{
  // …原有的 acls / grants / tagOwners 保持不动…
  "derpMap": {
    "OmitDefaultRegions": false,
    "Regions": {
      "900": {
        "RegionID": 900,
        "RegionCode": "own",
        "RegionName": "Self-hosted relay",
        "Nodes": [{
          "Name": "900a",
          "RegionID": 900,
          "HostName": "<香港VPS公网IP>",
          "IPv4": "<香港VPS公网IP>",
          "DERPPort": 8443,
          "STUNPort": 3478,
          "CertName": "sha256-raw:<脚本打印的指纹>"
        }]
      }
    }
  }
}
```

保存后一两分钟内所有设备会收到新的中继表。

- `RegionID` 用 900–999（官方保留给自建）。
- `OmitDefaultRegions: false`：官方中继仍保留作备用。先这样跑几天。
- 确认自建中继稳定后，可以改成 `true`，强制所有中继流量只走香港 VPS。代价是 VPS 宕机时打不通洞的设备会完全连不上，所以改之前确保 VPS 有监控或你能随时登录处理。

### 验证

在主机上：

```bash
tailscale netcheck
#   * Nearest DERP: Self-hosted relay      ← 理想情况
#   * DERP latency:
#       - own: 35ms (Self-hosted relay)    ← 有延迟数字就说明 TLS 和 STUN 都通

tailscale status
#   100.x.y.z  iphone  …  active; relay "own"   ← 走自建中继
#   100.x.y.z  iphone  …  active; direct 1.2.3.4:41641  ← 直连（最好）

tailscale ping iphone          # 看每一跳走 DERP(own) 还是 direct
```

`own` 不出现或显示超时：回到 4.3 检查云防火墙，再看 10 节。

## 6. 把 console 发布到 tailnet（HTTPS）

在办公室主机上：

```bash
tailscale serve --bg 11480
tailscale serve status
# https://office-mac.tail1234.ts.net (tailnet only)
# |-- / proxy http://127.0.0.1:11480
```

之后 tailnet 内任何设备都用 `https://<机器名>.<tailnet>.ts.net` 打开 console，证书由 Tailscale 自动签发和续期。`(tailnet only)` 表示只有你 tailnet 里的设备能访问。

**不要用 `tailscale serve --tcp 11480`（纯 TCP 转发）**，原因：

- 没有 HTTPS，浏览器不把页面当安全上下文：复制按钮、语音输入等功能会失效；
- 会话 cookie 不带 `Secure`；
- console 从请求头里认不出这是远程访问。

已经配过 TCP 转发的，先关掉再用上面的命令：

```bash
tailscale serve --tcp=11480 off    # 或者全部清空：tailscale serve reset
```

`tailscale serve` 的配置会持久保存，主机重启、Tailscale 重启后自动恢复。console 本身的开机自启见 [README.md 第 2 节](README.md#2-auto-start-on-boot)。

**千万不要用 `tailscale funnel`**：funnel 会把服务发布到公网，违背本项目“永不公网暴露”的原则。

## 7. 手机接入

1. 装 Tailscale（App Store / Google Play），登录同一个账号。
2. 浏览器打开 `https://<机器名>.<tailnet>.ts.net`，用 console 账号登录。
3. iOS Safari：分享 → 添加到主屏幕；Android Chrome：菜单 → 添加到主屏幕。之后像 App 一样打开。

**iOS 同一时间只能开一个 VPN**。Tailscale 和 Shadowrocket、Stash 等代理 App 不能同时开，二选一：

- 用 console 时开 Tailscale，用完切回代理；或
- 让香港 VPS 兼任出口节点，一个 Tailscale 同时解决“回办公室”和“出境”：
  ```bash
  # VPS 上
  echo 'net.ipv4.ip_forward = 1' | sudo tee /etc/sysctl.d/99-tailscale.conf
  echo 'net.ipv6.conf.all.forwarding = 1' | sudo tee -a /etc/sysctl.d/99-tailscale.conf
  sudo sysctl -p /etc/sysctl.d/99-tailscale.conf
  sudo tailscale set --advertise-exit-node
  ```
  管理后台 → Machines → derp-relay → Edit route settings → 勾选 **Use as exit node**；手机 Tailscale 里选它当 Exit Node。注意：出口流量是从大陆直连境外的 WireGuard，部分网络下会被限速或干扰，效果不如专门的代理协议，按实际体验决定用不用。

## 8. 安全加固（必做）

Tailscale 是网络层的保险，不能代替 console 自己的登录。

**1）改掉 admin 出厂密码。** 出厂密码 `fusion-admin-2026` 写在公开文档里。设置页还没有改密码入口，按下面手动改：

```bash
# 生成 bcrypt 哈希（macOS / Ubuntu 的 htpasswd 都行；Ubuntu 需 apt install apache2-utils）
HASH=$(htpasswd -bnBC 10 "" '换成你的新密码' | tr -d ':\n' | sed 's/^\$2y/\$2a/')
echo "$HASH"
```

编辑 `~/.fusion/fusion-console/config.json`（设置过 `FUSION_CONSOLE_DIR` 的就在那个目录），把 `users` 里 admin 的 `password_hash` 换成上面的值，然后重启 console。全新安装可以在首次启动前设 `FUSION_CONSOLE_ADMIN_PASSWORD`，跳过出厂密码。

**2）收紧 tailnet 策略。** 默认策略允许 tailnet 内所有设备互访。只想让自己的设备访问 console 时，在策略里限定（`grants` 写法）：

```jsonc
"tagOwners": { "tag:console": ["autogroup:admin"] },
"grants": [
  // 自己的设备可以访问 console 主机的 HTTPS
  { "src": ["autogroup:member"], "dst": ["tag:console"], "ip": ["tcp:443"] },
  // …其余规则…
]
```

给主机打标签：`sudo tailscale up --advertise-tags=tag:console`（会重新认证），或在管理后台给机器加 tag。改策略前先确认你的其他设备（SSH 等）对应的规则还在，不然可能把自己锁在外面。

**3）设备管理。** 打开 Device approval（新设备要你批准才能入网）；手机丢了立刻在后台删掉；服务器类设备（VPS、办公室主机）可以关掉 key expiry，个人设备保留。

**4）主机防火墙。** 确认 11480 没有监听在 `0.0.0.0`（`lsof -iTCP:11480 -sTCP:LISTEN` 应该只看到 `127.0.0.1`）。Ubuntu 可以 `ufw default deny incoming` 再按需放行。

**5）Agent 边界不变。** Worker 只在声明的工作目录里跑，预算上限保持开启。远程能访问，意味着手机上一句话就能让 Agent 改代码，审批模式建议保持“每次派工先问我”。

## 9. 和 Clash / mihomo 等本机代理共存

办公室主机如果开着 Clash / mihomo 的 TUN 模式，常见症状是 Tailscale 一直走中继、`tailscale ping` 不通、`*.ts.net` 解析失败。把 Tailscale 的网段和域名排除出代理：

```yaml
# mihomo / Clash Meta 配置
tun:
  route-exclude-address:
    - 100.64.0.0/10
    - fd7a:115c:a1e0::/48
dns:
  fake-ip-filter:
    - "+.ts.net"
rules:
  - IP-CIDR,100.64.0.0/10,DIRECT,no-resolve
  - IP-CIDR6,fd7a:115c:a1e0::/48,DIRECT,no-resolve
  - DOMAIN-SUFFIX,ts.net,DIRECT
  - IP-CIDR,<香港VPS公网IP>/32,DIRECT,no-resolve   # 去中继的流量不要再绕代理
  # …原有规则…
```

改完重载配置，再 `tailscale netcheck` 看一次。

## 10. 排错

| 现象 | 检查 / 处理 |
|---|---|
| `netcheck` 里没有 `own`，或 `own` 超时 | 云防火墙没放行 TCP 8443 / UDP 3478；`systemctl status derper`；策略里 IP、端口、`CertName` 是否和脚本输出一致 |
| `status` 显示 `relay "hkg"` 而不是 `own` | 策略还没下发（等一两分钟，或重启 Tailscale）；或者 `own` 延迟比官方高，客户端选了更近的。确认稳定后可设 `OmitDefaultRegions: true` |
| derper 日志里大量 `rejected` / `not in tailnet` | VPS 的 Tailscale 没登录，或登录到了别的 tailnet。`tailscale status` 检查 |
| 换了证书或删了 `/var/lib/derper` 后全部连不上 | 指纹变了。重跑脚本，把新的 `CertName` 更新到策略 |
| VPS 换了公网 IP | `sudo bash setup-derp.sh <新IP>`（会按新 IP 生成新证书），更新策略里的 `HostName`、`IPv4`、`CertName` |
| 浏览器打开 `https://…ts.net` 证书错误或打不开 | 管理后台 DNS 页 MagicDNS、HTTPS Certificates 是否都开了；`tailscale serve status` 有没有 `https://` 那一行 |
| 页面能开但复制按钮没反应 | 用的是 `http://` 或 `--tcp` 转发，换成第 6 节的 HTTPS 方式 |
| 登录页提示密码错误 | 用的是改过的密码吗？忘了就按第 8 节重新生成哈希写进 `config.json` |
| 手机上一直“连接中” | iOS 是否同时开着别的 VPN；Tailscale App 里看该设备是否 Connected |
| 一切正常但很慢 | `tailscale ping <主机>` 看走的是不是 `own`；VPS 带宽是否打满（`vnstat` / 云监控） |

## 11. 日常维护与卸载

```bash
# 看中继日志
journalctl -u derper -f

# 升级 derper（版本号见 https://github.com/tailscale/tailscale/releases）
sudo TS_VERSION=1.104.7 bash /tmp/setup-derp.sh <IP>

# 升级 VPS 上的 Tailscale 客户端
sudo apt update && sudo apt install --only-upgrade tailscale
```

卸载中继：

```bash
sudo systemctl disable --now derper
sudo rm /etc/systemd/system/derper.service /usr/local/bin/derper
sudo rm -rf /var/lib/derper
sudo systemctl daemon-reload
# 要连 Tailscale 一起卸：sudo tailscale logout && sudo apt remove tailscale
```

然后把策略里的 `derpMap` 删掉（或把 `OmitDefaultRegions` 改回 `false`），否则设备还会尝试连已经不存在的中继。

撤掉 console 的远程访问：在主机上 `tailscale serve reset`。
