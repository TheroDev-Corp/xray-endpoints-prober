# Xray Endpoints Prober

[![CI & Release Container](https://github.com/TheroDev-Corp/xray-endpoints-prober/actions/workflows/ci.yml/badge.svg)](https://github.com/TheroDev-Corp/xray-endpoints-prober/actions/workflows/ci.yml)
[![License: GPL-3.0](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)

A cloud-native, Kubernetes-ready Prometheus prober written in Go for continuous health, latency (RTT/TTFB/DNS), and throughput monitoring of Xray proxy endpoints (VLESS, VMess, Trojan, Shadowsocks) across multi-region clusters.

---

## Key Features

- **Automated Xray-Core Lifecycle**: Automatically discovers, downloads, verifies, and caches official releases (v26+ compatible) from GitHub Releases for current OS/architecture (`linux/amd64`, `linux/arm64`), or reuses a pre-installed binary.
- **Dynamic Multi-Format Subscription Ingestion**:
  - Ingests Base64 URI lists (`vless://`, `vmess://`, `trojan://`, `ss://`), Clash YAML, and Sing-box JSON.
  - Full support for modern transports and security: **Reality**, **xhttp**, **TCP**, **WS**, **gRPC**, `flow=xtls-rprx-vision`, SNI, Fingerprint, public keys, and short IDs.
  - Continuous background polling (`SUBSCRIPTION_POLL_INTERVAL`) with change-detection hashing and hot reconfiguration.
  - Resilient local disk cache fallback (`SUBSCRIPTION_CACHE_FILE`) if remote subscription endpoints experience temporary network failures.
  - Protocol filtering via `PROTOCOLS_EXCLUDE` (e.g. `hysteria2,shadowsocks`).
- **Dynamic Inbound & Routing Generation**:
  - Dynamically provisions isolated local SOCKS5 inbounds on `127.0.0.1:<10800 + i>` mapped to unique outbound proxy tags.
- **Advanced Network Probing**:
  - **Multi-Target Parallel Latency Runner**: Concurrent probes against multiple critical targets (e.g., Cloudflare, Google, Gstatic) measuring TCP connect, TLS handshake, TTFB, and overall RTT.
  - **Real DNS-over-TCP Measurement**: Directly measures end-to-end DNS lookup latency through each proxy tunnel to specified recursive resolvers (`PROBE_DNS_SERVERS`).
  - **Sequential Speedtest Runner**: Single-flight download throughput tester (`PROBE_SPEEDTEST_URL`) that avoids inter-thread bandwidth saturation.
- **Prometheus Metrics & Percentiles**:
  - Standard text exposition format on `/metrics` with full multi-dimensional labels (`region`, `id`, `name`, `protocol`, `server`, `port`, `target`).
  - Native **Prometheus Histograms** (`_bucket`, `_sum`, `_count`) allowing dynamic p50, p90, p95, and p99 percentile queries in Grafana via `histogram_quantile()`.
- **Zero External Go Dependencies**: Implemented strictly with Go standard library packages for minimal attack surface and reproducible builds.
- **Kubernetes & Cloud-Native Ready**:
  - Built-in `/healthz` (liveness) and `/readyz` (readiness) endpoints.
  - Helm chart with `ServiceMonitor` for Prometheus Operator / VictoriaMetrics Operator.
  - Hardened Docker container with non-root security contexts and read-only root filesystems.

---

## Architecture Overview

```
                      +-----------------------------+
                      |     Remote Subscription     |
                      +--------------+--------------+
                                     |
                                     v
+------------------------------------+------------------------------------+
|  xray-endpoints-prober Container                                       |
|                                                                         |
|  +------------------------+      +-----------------------------------+  |
|  |  Subscription Manager  | ---> |   Xray Config & Process Manager   |  |
|  |  (poll + disk cache)   |      |  - Auto-fetch Xray-core (v26+)    |  |
|  +------------------------+      |  - Spawns inbounds: 10800..108xx  |  |
|                                  +-----------------+-----------------+  |
|                                                    |                    |
|  +-------------------------------------------------+-----------------+  |
|  |  Prober Engine (Multi-Target Workers)                             |  |
|  |  - Latency & TTFB (Cloudflare, Google, Gstatic)                   |  |
|  |  - Real DNS-over-TCP (1.1.1.1, 8.8.8.8)                           |  |
|  |  - Sequential Speedtest (Throughput Mbps)                         |  |
|  +-------------------------------------------------+-----------------+  |
|                                                    |                    |
|                                                    v                    |
|  +-------------------------------------------------+-----------------+  |
|  |  HTTP Server (:8080)                                              |  |
|  |  - /metrics  (Prometheus Gauges, Counters, Histograms)            |  |
|  |  - /healthz  (Liveness probe)                                     |  |
|  |  - /readyz   (Readiness probe)                                    |  |
|  +-------------------------------------------------------------------+  |
+------------------------------------+------------------------------------+
                                     |
                                     v
                       +---------------------------+
                       |    Prometheus / Grafana   |
                       +---------------------------+
```

---

## Configuration Reference

All configuration is supplied via environment variables:

| Variable | Default | Description |
|---|---|---|
| `REGION` | `default` | Geographical/cluster region tag attached to all metrics (e.g. `eu-central-1`, `fra-k8s-01`) |
| `SUBSCRIPTION_URL` | *(required)* | Remote HTTP/HTTPS subscription URL containing node configurations |
| `SUBSCRIPTION_POLL_INTERVAL` | `10m` | Interval between background polls for updated endpoints |
| `SUBSCRIPTION_CACHE_FILE` | `/tmp/subscription.cache` | Local path where the latest valid subscription snapshot is persisted |
| `PROTOCOLS_EXCLUDE` | `""` | Comma-separated list of protocols to filter out (e.g. `hysteria2,shadowsocks`) |
| `XRAY_VERSION` | `v26.3.27` | Xray-core release version tag to download from GitHub Releases |
| `XRAY_CACHE_DIR` | `/tmp/xray-cache` | Directory to store the downloaded Xray binary and generated configs |
| `PROBE_TARGET_URLS` | `https://cp.cloudflare.com/generate_204,https://www.gstatic.com/generate_204,https://www.google.com/generate_204` | Comma-separated URLs to probe for availability, TTFB, and latency |
| `PROBE_PING_INTERVAL` | `30s` | Interval between latency probe cycles |
| `PROBE_PING_TIMEOUT` | `5s` | Timeout for each individual HTTP probe |
| `PROBE_DNS_SERVERS` | `1.1.1.1:53,8.8.8.8:53` | Comma-separated recursive DNS resolvers used to measure tunnel DNS latency |
| `PROBE_SPEEDTEST_ENABLED` | `true` | Enable or disable periodic download speed measurement (`true`/`false`) |
| `PROBE_SPEEDTEST_URL` | `https://speed.cloudflare.com/__down?bytes=10000000` | Target URL used for download speed tests |
| `PROBE_SPEEDTEST_INTERVAL` | `10m` | Interval between sequential throughput measurement sweeps |
| `PROBE_SPEEDTEST_TIMEOUT` | `15s` | Timeout allocated per endpoint during speed testing |
| `PROBE_CONCURRENCY` | `5` | Concurrency worker pool size for parallel latency probing |
| `INBOUND_BASE_PORT` | `10800` | Starting local port number allocated for Xray SOCKS5 inbounds |
| `LISTEN_ADDR` | `:8080` | Address and port bound by the HTTP metrics and health check server |

---

## Prometheus Metrics

### Gauges and Counters

| Metric | Type | Labels | Description |
|---|---|---|---|
| `xray_prober_endpoint_up` | Gauge | `region`, `id`, `name`, `protocol`, `server`, `port`, `target` | Reachability status (1 = healthy, 0 = unhealthy) |
| `xray_prober_endpoint_latency_seconds` | Gauge | `region`, `id`, `name`, `protocol`, `server`, `port`, `target` | Total HTTP request round-trip time in seconds |
| `xray_prober_endpoint_ttfb_seconds` | Gauge | `region`, `id`, `name`, `protocol`, `server`, `port`, `target` | Time to First Byte (TTFB) in seconds |
| `xray_prober_endpoint_dns_seconds` | Gauge | `region`, `id`, `name`, `protocol`, `server`, `port`, `target` | Real end-to-end DNS lookup latency through the tunnel |
| `xray_prober_endpoint_consecutive_failures` | Gauge | `region`, `id`, `name`, `protocol`, `server`, `port`, `target` | Number of consecutive failed probes (ideal for instant alerts) |
| `xray_prober_endpoint_probe_errors_total` | Counter | `region`, `id`, `name`, `protocol`, `server`, `port`, `target` | Total count of failed probe attempts |
| `xray_prober_endpoint_speed_bytes_per_second` | Gauge | `region`, `id`, `name`, `protocol`, `server`, `port` | Download throughput in bytes per second |
| `xray_prober_endpoint_speed_duration_seconds` | Gauge | `region`, `id`, `name`, `protocol`, `server`, `port` | Elapsed wall time of the last speedtest run |
| `xray_prober_subscription_endpoints_count` | Gauge | `region` | Total count of active, non-excluded endpoints |
| `xray_prober_subscription_last_poll_timestamp_seconds` | Gauge | `region` | Unix timestamp of the last successful subscription sync |
| `xray_prober_subscription_poll_errors_total` | Counter | `region` | Total errors encountered when polling remote subscription |
| `xray_prober_core_version_info` | Gauge | `region`, `version` | Constant value `1` containing running Xray-core metadata |

### Latency Percentiles (Histograms)

Latency measurements are recorded in standard Prometheus Histogram format:
- `xray_prober_endpoint_http_duration_seconds_bucket{le="..."}`
- `xray_prober_endpoint_http_duration_seconds_sum`
- `xray_prober_endpoint_http_duration_seconds_count`

**Buckets**: `[0.025, 0.05, 0.1, 0.2, 0.35, 0.5, 0.75, 1.0, 1.5, 2.5, 5.0, 10.0, +Inf]`

**Example PromQL Queries**:

- **95th Percentile Latency across endpoints**:
  ```promql
  histogram_quantile(0.95, sum(rate(xray_prober_endpoint_http_duration_seconds_bucket[5m])) by (le, name, target))
  ```

- **Endpoint Success Rate (SLO)**:
  ```promql
  sum(rate(xray_prober_endpoint_up[5m])) / count(xray_prober_endpoint_up) * 100
  ```

- **Average Download Speed in Mbps**:
  ```promql
  (xray_prober_endpoint_speed_bytes_per_second * 8) / 1000000
  ```

---

## Quick Start

### 1. Run with Docker Compose

Create a `.env` file or export your environment:

```bash
cat << 'EOF' > .env
REGION=eu-central-1
SUBSCRIPTION_URL=https://your-subscription-url.com/sub
PROTOCOLS_EXCLUDE=hysteria2,shadowsocks
PROBE_DNS_SERVERS=1.1.1.1:53,8.8.8.8:53
EOF
```

Start the container stack:

```bash
docker compose up -d
```

View structured UTC lifecycle logs:

```bash
docker compose logs -f
```

Inspect metrics:

```bash
curl -s http://localhost:8080/metrics
```

---

### 2. Run with Docker

```bash
docker run -d \
  --name xray-prober \
  --network host \
  -e REGION="eu-central-1" \
  -e SUBSCRIPTION_URL="https://your-subscription-url.com/sub" \
  -e PROTOCOLS_EXCLUDE="hysteria2,shadowsocks" \
  -e PROBE_DNS_SERVERS="1.1.1.1:53,8.8.8.8:53" \
  ghcr.io/therodev-corp/xray-endpoints-prober:latest
```

---

### 3. Deploy to Kubernetes with Helm

A production-grade Helm chart is included under `deploy/charts/xray-endpoints-prober`.

```bash
helm upgrade --install xray-prober ./deploy/charts/xray-endpoints-prober \
  --namespace monitoring \
  --create-namespace \
  --set config.region="fra-k8s-01" \
  --set config.subscriptionUrl="https://your-subscription-url.com/sub" \
  --set serviceMonitor.enabled=true
```

To supply the subscription URL from an existing Kubernetes Secret:

```bash
helm upgrade --install xray-prober ./deploy/charts/xray-endpoints-prober \
  --namespace monitoring \
  --create-namespace \
  --set config.region="fra-k8s-01" \
  --set config.secretRef.name="my-proxy-secret" \
  --set config.secretRef.key="SUBSCRIPTION_URL"
```

---

## Local Development & Testing

Run all unit and integration test suites:

```bash
go test -v -race -cover ./...
```

Inspect coverage report:

```bash
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

Build the standalone binary:

```bash
CGO_ENABLED=0 go build -ldflags="-s -w" -o xray-prober ./cmd/prober
```

---

## License

This project is licensed under the [GNU General Public License v3.0](LICENSE).
