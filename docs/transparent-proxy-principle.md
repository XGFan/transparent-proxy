# OpenWrt Transparent Proxy - Technical Implementation Principle

## 1. Overview

This project is an OpenWrt transparent proxy management tool with **frontend-backend separation architecture**, implementing dynamic configuration of transparent proxy rules through Linux **nftables sets** management.

### Core Components

| Component | Tech Stack | Responsibility |
|-----------|------------|----------------|
| Backend | Go 1.22 + Gin | nftables rule management, OpenWrt config management, Web API |
| Frontend | React 18 + TypeScript | Management UI, rule configuration, status monitoring |
| Firewall | nftables (fw4) | Traffic interception, transparent proxy redirection |

---

## 2. Transparent Proxy Implementation Principle

### 2.1 Core Technology: TPROXY

The project uses **TPROXY** (Transparent Proxy) to implement transparent proxy, which is a mechanism provided by nftables.

#### TPROXY vs REDIRECT Comparison

| Feature | TPROXY | REDIRECT |
|---------|--------|----------|
| Principle | Does not modify IP header, delivers directly to local socket | Special case of DNAT, changes destination IP to local |
| Protocol Support | TCP + UDP perfect support | Mainly TCP, UDP handling is complex |
| Original Destination | Directly obtained via `getsockname()` | Requires `getsockopt(SO_ORIGINAL_DST)` |
| Dependencies | Policy routing + fwmark | No additional dependencies |

**Reasons for choosing TPROXY**:
1. Complete UDP support (required for modern proxy protocols like QUIC)
2. Preserves original destination IP, simpler proxy logic
3. More aligned with modern transparent proxy architecture

### 2.2 Traffic Interception Flow

```
+---------------------------------------------------------------------+
|                        OpenWrt Router                               |
+---------------------------------------------------------------------+
|                                                                     |
|  br-lan (LAN Port)                                                  |
|       |                                                             |
|       v                                                             |
|  +---------------------+                                            |
|  | mangle_prerouting   |  <-- PREROUTING hook (mangle priority)     |
|  | chain transparent_proxy  |  <-- PREROUTING hook (mangle priority)     |
|  +----------+----------+                                            |
|             |                                                       |
|    +--------+--------+                                              |
|    |                 |                                              |
|    v                 v                                              |
|  Match Rules      Match Rules                                       |
|  (by priority)    (by priority)                                     |
|    |                 |                                              |
|  +-+-+             +-+-+                                            |
|  |   |             |   |                                            |
|  v   v             v   v                                            |
|RETURN TPROXY    RETURN TPROXY                                       |
|(Direct)(Proxy)  (Direct)(Proxy)                                     |
|  |     |           |     |                                          |
|  v     v           v     v                                          |
|Normal Proxy      Normal Proxy                                       |
|Routing Process   Routing Process                                    |
|      (1081/1082)       (1081/1082)                                  |
|                                                                     |
|  +-----------------------------------------------------------+      |
|  |         Policy Routing (fwmark=1 -> table 100)            |      |
|  |   ip rule add fwmark 1 table 100                          |      |
|  |   ip route add local 0.0.0.0/0 dev lo table 100           |      |
|  +-----------------------------------------------------------+      |
|                                                                     |
+---------------------------------------------------------------------+
```

### 2.3 nftables Rule Chain Details

#### 2.3.1 Core Rule Chain (`proxy.nft`)

```nftables
chain transparent_proxy {
    mark 0xff return                                    # Exclude proxy itself
    ip daddr @reserved_ip return                        # Bypass reserved addresses
    meta l4proto {tcp, udp} ip saddr @proxy_src         # Proxy by source IP
        mark set 1 tproxy ip to 127.0.0.1:1082 accept
    ip saddr @direct_src return                         # Direct by source IP
    meta l4proto {tcp, udp} ip daddr @proxy_dst         # Proxy by dest IP
        mark set 1 tproxy ip to 127.0.0.1:1082 accept
    ip daddr @direct_dst return                         # Direct by dest IP
    ip daddr @chnroute return                           # Direct for China IPs
    meta l4proto {tcp, udp}                             # Default proxy
        mark set 1 tproxy ip to 127.0.0.1:1081 accept
}

chain transparent_proxy_mask {
    mark 0xff return                                    # Exclude proxy itself
    oifname "lo" return                                 # Exclude loopback
    ip daddr @reserved_ip return                        # Bypass reserved addresses
    ip daddr @direct_dst return                         # Direct by dest IP
    ip daddr @chnroute return                           # Direct for China IPs
    meta l4proto {tcp, udp} mark set 1 accept           # Mark for proxy
}
```

#### 2.3.2 Rule Priority (High to Low)

```
Priority  Rule                      Action
========================================================================
  1       mark=0xff (proxy itself) -> RETURN (Direct)
  2       dest IP in reserved_ip   -> RETURN (Direct)
  3       source IP in proxy_src   -> TPROXY -> Proxy
  4       source IP in direct_src  -> RETURN (Direct)
  5       dest IP in proxy_dst     -> TPROXY -> Proxy
  6       dest IP in direct_dst    -> RETURN (Direct)
  7       dest IP in chnroute      -> RETURN (Direct for China)
  8       All other traffic        -> TPROXY -> Proxy (default)
```

### 2.4 Four IP Sets

The project uses **nftables sets** (not ipset) for efficient IP matching:

| Set Name | Purpose | Example |
|----------|---------|---------|
| `proxy_src` | Force proxy by source IP | Specific LAN devices |
| `direct_src` | Force direct by source IP | Servers, IoT devices |
| `proxy_dst` | Force proxy by dest IP | Blocked websites IPs |
| `direct_dst` | Force direct by dest IP | Specific service IPs |

**Sets Definition** (example: `proxy_dst.nft`):
```nftables
set proxy_dst {
    type ipv4_addr
    flags interval      # Support CIDR ranges
    auto-merge          # Auto-merge adjacent ranges
}
```

### 2.5 Policy Routing Configuration

TPROXY must work with policy routing:

```bash
# Auto-configured on WAN interface up (80-ifup-wan)
ip rule add fwmark 1 table 100
ip route add local 0.0.0.0/0 dev lo table 100
```

**How it works**:
1. `nftables` marks traffic needing proxy with `mark=1`
2. `ip rule` matches packets with `fwmark=1`, uses routing table 100
3. Routing table 100 routes all traffic to `lo` (loopback interface)
4. TPROXY delivers traffic to proxy process at `mangle_prerouting` chain

---

## 3. System Architecture

### 3.1 Backend Service Architecture

```
+------------------------------------------------------------------+
|                         App (main.go)                            |
|                          :1444                                   |
+------------------------------------------------------------------+
|                                                                  |
|  +--------------+  +--------------+  +----------------------+    |
|  | NftService   |  |CheckerService|  | OpenWrtService       |    |
|  |              |  |              |  |                      |    |
|  | - GetSetJSON |  | - Status()   |  | - ApplyOverlay()     |    |
|  | - AddToSet   |  | - Start()    |  | - ListConfigs()      |    |
|  | - RemoveSet  |  |              |  | - Rollback()         |    |
|  | - SyncSet    |  | (netguard)   |  |                      |    |
|  +------+-------+  +--------------+  +----------+-----------+    |
|         |                                       |                |
|         v                                       v                |
|  +---------------------------------------------------------+     |
|  |                    Runtime                              |     |
|  |  - nft()      -> Execute nft commands                   |     |
|  |  - command()  -> Execute system commands                |     |
|  |  - fetch()    -> HTTP fetch (e.g., CHNRoute)            |     |
|  |  - writeFile()-> File write operations                  |     |
|  +---------------------------------------------------------+     |
|                                                                  |
+------------------------------------------------------------------+
```

`CheckerService` drives proxy state changes. When the failure threshold is hit, it calls `runtime.disableProxy()`. When recovery conditions are met, it calls `runtime.enableProxy()`. The `/api/status` fields `proxy.enabled` and `proxy.status` come from this runtime state, not hard-coded values.

### 3.2 API Design

| Route | Method | Function |
|-------|--------|----------|
| `/api/rules` | GET | Get all IP Sets |
| `/api/rules/add` | POST | Add IP to specified Set |
| `/api/rules/remove` | POST | Remove IP from Set |
| `/api/rules/sync` | POST | Sync Sets to persistent files |
| `/api/health` | GET | Health check + network status |
| `/api/config` | GET/POST | Config management |
| `/api/config/apply` | POST | Apply config preview/execute |

### 3.3 Frontend Modules

```
portal/src/features/
+-- status/     # Status page - network connectivity, proxy status
+-- rules/      # Rule management - IP Sets CRUD
+-- config/     # Config management - YAML editor
+-- ops/        # Operations - sync, rollback, restart
```

---

## 4. Key Implementation Details

### 4.1 nft Command Wrapper

```go
// nft_service.go - Core operations

// Get Set content (JSON format)
func (s *NftService) GetSetJSON(setName string) (string, []string, error) {
    output, err := s.runtime.nft("-j", "list", "set", "inet", "fw4", setName)
    // Parse JSON to return element list
}

// Add IP to Set
func (s *NftService) AddToSet(setName, data string) error {
    return s.runtime.nft("add", "element", "inet", "fw4", setName, "{"+data+"}")
}

// Remove IP from Set
func (s *NftService) RemoveFromSet(setName, data string) error {
    return s.runtime.nft("delete", "element", "inet", "fw4", setName, "{"+data+"}")
}

// Sync Set to file (persistence)
func (s *NftService) SyncSet(basePath, setName string) error {
    output, err := s.runtime.nft("list", "set", "inet", "fw4", setName)
    // Write to /etc/nftables.d/{setName}.nft
}
```

### 4.2 Dev Mode Mock

```go
// dev_mode.go - Local dev on Mac without root/VM

func buildDevModeConfig() *devModeConfig {
    if os.Getenv("DEV_MODE") == "" {
        return nil  // Production mode
    }
    return &devModeConfig{
        Runtime: NewMockRuntime(),  // Mock nft commands
        OverlayRoot: tempDir,        // Temp directory
    }
}

// Mock Runtime example
type mockRuntime struct {
    sets map[string][]string  // In-memory storage
}

func (m *mockRuntime) nft(args ...string) ([]byte, error) {
    // Parse commands, operate on m.sets
    // No real nft privileges needed
}
```

### 4.3 OpenWrt Integration

#### Init Script (`/etc/init.d/transparent-proxy`)
```bash
#!/bin/sh /etc/rc.common
START=99
USE_PROCD=1

start_service() {
    procd_open_instance transparent-proxy
    procd_set_param command /etc/transparent-proxy/server -c /etc/transparent-proxy/config.yaml
    procd_set_param respawn 3600 5 5
    procd_set_param stdout 1
    procd_set_param stderr 1
    procd_close_instance
}
```

#### WAN Interface Listener (`/etc/hotplug.d/iface/80-ifup-wan`)
```bash
#!/bin/sh
[ "$ACTION" = "ifup" -a "$INTERFACE" = "wan" ] && {
    ip rule add fwmark 1 table 100
    ip route add local 0.0.0.0/0 dev lo table 100
}
```

#### Enable/Disable Scripts

The main enable/disable flow now lives in Go. `enable.sh` and `disable.sh` remain only as compatibility hooks.

- Default `onSuccess` and `onFailure` script paths are skipped when they are set to `/etc/transparent-proxy/enable.sh` and `/etc/transparent-proxy/disable.sh`
- Custom hook paths can still run, but they are no longer required for the main proxy switch flow

---

## 5. Traffic Routing Strategy

### 5.1 Routing Logic Diagram

```
                    +-------------------+
                    |  LAN Traffic In   |
                    |    (br-lan)       |
                    +--------+----------+
                             |
                             v
                    +-------------------+
                    |   mark=0xff?      |
                    | (proxy itself)    |
                    +--------+----------+
                             |
               +-------------+-------------+
               | YES                       | NO
               v                           v
          +---------+              +-------------+
          |  RETURN |              | dest IP in  |
          | (Direct)|              | reserved_ip?|
          +---------+              +------+------+
                                          |
                             +------------+------------+
                             | YES                      | NO
                             v                          v
                        +---------+            +-------------+
                        | RETURN  |            | source IP   |
                        | (Direct)|            | in proxy_src?|
                        +---------+            +------+------+
                                                     |
                                   +-----------------+-----------------+
                                   | YES                               | NO
                                   v                                   v
                              +----------+                     +-------------+
                              |  TPROXY  |                     | source IP   |
                              | -> Proxy |                     | in direct_src?|
                              |  :1082   |                     +------+------+
                              +----------+                            |
                                                     +----------------+-----------------+
                                                     | YES                              | NO
                                                     v                                  v
                                                +---------+                      +-------------+
                                                | RETURN  |                      | dest IP in  |
                                                | (Direct)|                      | proxy_dst?  |
                                                +---------+                      +------+------+
                                                                                      |
                                                                    +-----------------+-----------------+
                                                                    | YES                               | NO
                                                                    v                                   v
                                                               +----------+                      +-------------+
                                                               |  TPROXY  |                      | dest IP in  |
                                                               | -> Proxy |                      | direct_dst? |
                                                               |  :1082   |                      +------+------+
                                                               +----------+                             |
                                                                                     +----------------+-----------------+
                                                                                     | YES                              | NO
                                                                                     v                                  v
                                                                                +---------+                     +-------------+
                                                                                | RETURN  |                     | dest IP in  |
                                                                                | (Direct)|                     | chnroute?   |
                                                                                +---------+                     +------+------+
                                                                                                                     |
                                                                                          +--------------------------+--------------------------+
                                                                                          | YES                                                 | NO
                                                                                          v                                                     v
                                                                                     +---------+                                          +----------+
                                                                                     | RETURN  |                                          |  TPROXY  |
                                                                                     | (China  |                                          | -> Proxy |
                                                                                     | Direct) |                                          |  :1081   |
                                                                                     +---------+                                          +----------+
```

### 5.2 Proxy Port Explanation

| Port | Purpose |
|------|---------|
| `1081` | Default proxy port (handles non-China traffic) |
| `1082` | Designated proxy port (handles proxy_src/proxy_dst traffic) |

**Dual-port Design Purpose**:
- Distinguish between "rule-based proxy" and "forced proxy" traffic
- Enable different proxy strategies (e.g., different outbound nodes)

---

## 6. File Structure

```
/etc/
+-- nftables.d/                 # fw4 auto-loaded rules directory
|   +-- proxy.nft               # Core TPROXY rule chains
|   +-- proxy_src.nft           # Force proxy source IP Set
|   +-- proxy_dst.nft           # Force proxy dest IP Set
|   +-- direct_src.nft          # Direct source IP Set
|   +-- direct_dst.nft          # Direct dest IP Set
|   +-- reserved_ip.nft         # Reserved addresses Set
|   +-- v6block.nft             # IPv6 filter rules
|
+-- transparent-proxy/
|   +-- config.yaml             # Main config file
|   +-- server                  # Go backend binary
|   +-- enable.sh               # Enable transparent proxy
|   +-- disable.sh              # Disable transparent proxy
|   +-- transparent.nft         # Chain injection rules (partial)
|   +-- transparent_full.nft    # Complete rule table
|
+-- init.d/
|   +-- transparent-proxy       # procd service script
|
+-- hotplug.d/iface/
    +-- 80-ifup-wan             # WAN interface listener (policy routing)
```

---

## 7. fw4 Integration Mechanism

OpenWrt uses **fw4** (Firewall4) as firewall framework, fully based on nftables.

### 7.1 Rule Injection Point

```
/etc/nftables.d/  ->  fw4 auto-loads on startup
                    ->  Rules inserted into inet fw4 table
```

### 7.2 Chain Mounting Method

```nftables
# transparent.nft - Mount custom chains to fw4 mangle table

chain mangle_prerouting {
    iifname "br-lan" jump transparent_proxy      # LAN traffic jumps to transparent_proxy chain
    mark 0x1 jump transparent_proxy              # Marked traffic also processed (local output)
}

chain mangle_output {
    jump transparent_proxy_mask                   # Local output traffic processing
}
```

### 7.3 Enable Process

```bash
# enable.sh
nft flush chain inet fw4 mangle_prerouting
nft flush chain inet fw4 mangle_output
nft -f /etc/transparent-proxy/transparent_full.nft  # Load complete rules
cp /etc/transparent-proxy/transparent.nft /usr/share/nftables.d/table-post/
```

### 7.4 Built-in Proxy Toggle Flow

**Disable flow**

1. Flush `inet fw4 mangle_prerouting`
2. Flush `inet fw4 mangle_output`
3. Remove `/usr/share/nftables.d/table-post/transparent.nft`

**Enable flow**

1. Flush `inet fw4 mangle_prerouting`
2. Flush `inet fw4 mangle_output`
3. Apply `/etc/transparent-proxy/transparent_full.nft` with `nft -f`
4. Rewrite the table-post target from `/etc/transparent-proxy/transparent.nft`

---

## 8. Summary

### 8.1 Technical Features

| Feature | Implementation |
|---------|----------------|
| Transparent Proxy Technology | **TPROXY** (TCP + UDP support) |
| Rule Matching | **nftables sets** (high performance, atomic updates) |
| Traffic Routing | Multi-level rule priority + four IP Sets |
| Policy Routing | fwmark + routing table 100 |
| Integration Method | fw4 custom chain injection |
| Config Persistence | `/etc/nftables.d/*.nft` files |

### 8.2 Architecture Advantages

1. **No iptables/ipset required**: Uses modern nftables entirely
2. **Dynamic management**: Real-time IP add/remove via Web UI, no restart needed
3. **Persistence support**: Rules auto-saved, restored after reboot
4. **Dev-friendly**: Mock mode supports Mac local development
5. **OpenWrt native integration**: Standard fw4 interface for rule injection

### 8.3 Applicable Scenarios

- Home/small office network transparent proxy
- Scenarios requiring flexible routing rules
- Scenarios requiring Web UI management
- OpenWrt router deployment

---

**Document Version**: v1.0  
**Last Updated**: 2026-03-16
