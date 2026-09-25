# Go Proxy Mux

[![GitHub Repo](https://img.shields.io/badge/github-hightemp%2Fgo__proxy__mux-blue?logo=github)](https://github.com/hightemp/go_proxy_mux)
[![Go Version](https://img.shields.io/github/go-mod/go-version/hightemp/go_proxy_mux)](go.mod)
[![GitHub release](https://img.shields.io/github/v/release/hightemp/go_proxy_mux)](https://github.com/hightemp/go_proxy_mux/releases)
[![GitHub Downloads](https://img.shields.io/github/downloads/hightemp/go_proxy_mux/total)](https://github.com/hightemp/go_proxy_mux/releases)
[![Docker Pulls](https://img.shields.io/docker/pulls/hightemp/go_proxy_mux.svg)](https://hub.docker.com/r/hightemp/go_proxy_mux)
[![Tests](https://github.com/hightemp/go_proxy_mux/actions/workflows/test.yml/badge.svg)](https://github.com/hightemp/go_proxy_mux/actions/workflows/test.yml)
[![Release](https://github.com/hightemp/go_proxy_mux/actions/workflows/release.yml/badge.svg)](https://github.com/hightemp/go_proxy_mux/actions/workflows/release.yml)
[![](https://asdertasd.site/counter/go_proxy_mux)](https://asdertasd.site/counter/go_proxy_mux)

An HTTP/HTTPS forward proxy that distributes client requests across HTTP, HTTPS, SOCKS4, and SOCKS5 upstream proxies. It supports Basic authentication, HTTP/1.1 and HTTP/2 CONNECT, and bounded failover.

## Features

- HTTP or TLS-protected HTTPS listener; HTTP/2 CONNECT is available over TLS
- Separate Basic authentication for clients and upstream proxies
- Round-robin or random selection across HTTP, HTTPS, SOCKS4, and SOCKS5 upstreams
- SOCKS4a for destination hostnames, and SOCKS5 for hostnames and IPv6 destinations
- Temporary exclusion of an upstream after a network or CONNECT setup failure
- One alternate attempt for bodyless GET/HEAD and for CONNECT before success; requests with bodies are not replayed
- Limits for client TCP connections and CONNECT tunnels, tunnel idle timeout, and graceful shutdown
- Configuration through `.env`, process environment, or strict YAML

## Installation

### From release

Download a binary and `SHA256SUMS` from [GitHub Releases](https://github.com/hightemp/go_proxy_mux/releases). On Linux, check the downloaded files in the same directory:

```sh
sha256sum --ignore-missing -c SHA256SUMS
```

Release assets include binaries for Linux, macOS, and Windows on amd64 and arm64. Configure the proxy before starting it; the assets contain examples but no working credentials or TLS keys.

### Docker

Images are published to [Docker Hub](https://hub.docker.com/r/hightemp/go_proxy_mux). To build the version from this checkout locally:

```sh
make docker-build
```

After preparing `.env` and certificate files, run the local image:

```sh
docker run -d --name go_proxy_mux -p 8380:8380 \
  --env-file .env \
  -v "$PWD/certs:/app/certs:ro" \
  "hightemp/go_proxy_mux:$(cat VERSION)"
```

Use [Docker Compose](#docker-compose) for either `.env` or `config.yaml` configuration.

### Build from source

```sh
git clone https://github.com/hightemp/go_proxy_mux.git
cd go_proxy_mux
make build
```

`make build-static` creates `go_proxy_mux_static` with CGO disabled. Both targets build for the current system. GitHub Actions builds the cross-platform release binaries.

### Project structure

```text
cmd/go_proxy_mux/   application entry point
internal/config/    YAML, .env, environment overrides, and validation
internal/balancer/  upstream selection and cooldown
internal/proxy/     authentication, forwarding, tunnels, and server lifecycle
internal/socks/     SOCKS4/SOCKS4a and SOCKS5 TCP dialing
```

## Configuration

Copy [`.env.example`](.env.example) to `.env`, replace all placeholder credentials and upstream addresses, and provide a certificate and key if the listener is exposed over the network:

```sh
cp .env.example .env
chmod 600 .env
```

Set `MUX_UPSTREAM_COUNT` to the number of upstream entries. Number their fields consecutively from `MUX_UPSTREAM_1_*` to `MUX_UPSTREAM_N_*`. The example intentionally fails validation until its placeholders and TLS files are replaced.

Settings take precedence in this order: **process environment → `.env` → YAML → built-in defaults**. The `-env` flag selects an env file (`.env` by default); `-env ""` disables it. The `-config` flag selects a YAML file (`config.yaml` by default). If YAML is absent, an env-only configuration must provide the upstream list.

`.env` uses literal `KEY=VALUE` lines. Do not wrap values in shell quotes. Values may contain `#`, `$`, or `=`; multiline values are not supported. `MUX_PROXY_TIMEOUT` is an integer number of seconds. Other timeout settings use Go duration syntax such as `15s` or `2m`.

### Environment variables

Every application setting is represented in [`.env.example`](.env.example). `MUX_PUBLISH_HOST` and `MUX_PUBLISH_PORT` affect only Docker Compose port publishing.

| Variable | Purpose |
|---|---|
| `MUX_SERVER_HOST`, `MUX_SERVER_PORT` | Listener address and port |
| `MUX_SERVER_TLS_CERT_FILE`, `MUX_SERVER_TLS_KEY_FILE` | TLS certificate and private key paths; both empty selects HTTP |
| `MUX_SERVER_ALLOW_INSECURE_PUBLIC_HTTP` | Explicitly allow non-loopback HTTP |
| `MUX_SERVER_ALLOW_UNAUTHENTICATED_PUBLIC_PROXY` | Explicitly allow a non-loopback listener without client auth |
| `MUX_SERVER_MAX_CONNECTIONS` | Maximum active client TCP connections |
| `MUX_SERVER_READ_HEADER_TIMEOUT`, `MUX_SERVER_IDLE_TIMEOUT`, `MUX_SERVER_SHUTDOWN_TIMEOUT` | Listener and shutdown timeouts |
| `MUX_AUTH_ENABLED`, `MUX_AUTH_USERNAME`, `MUX_AUTH_PASSWORD` | Client Basic authentication |
| `MUX_PROXY_ALGORITHM` | `roundrobin` or `random` |
| `MUX_PROXY_TIMEOUT` | HTTP request and CONNECT setup timeout, in seconds |
| `MUX_PROXY_MAX_TUNNELS`, `MUX_PROXY_TUNNEL_IDLE_TIMEOUT` | Active tunnel limit and idle timeout |
| `MUX_PROXY_FAILOVER_COOLDOWN` | Time to exclude a failed upstream |
| `MUX_UPSTREAM_COUNT` | Number of upstream entries |
| `MUX_UPSTREAM_N_URL` | Upstream URL: `http://`, `https://`, `socks4://`, or `socks5://` |
| `MUX_UPSTREAM_N_AUTH_ENABLED`, `MUX_UPSTREAM_N_AUTH_USERNAME`, `MUX_UPSTREAM_N_AUTH_PASSWORD` | Credentials for upstream N |
| `MUX_UPSTREAM_N_TLS_CA_FILE` | Optional private CA file for an HTTPS upstream |
| `MUX_PUBLISH_HOST`, `MUX_PUBLISH_PORT` | Host-side Compose binding only |

### YAML configuration

YAML is also supported; see [config.example.yaml](config.example.yaml). A minimal TLS example is:

```yaml
server:
  host: "0.0.0.0"
  port: 8380
  tls:
    cert_file: "./certs/fullchain.pem"
    key_file: "./certs/privkey.pem"
auth:
  enabled: true
  username: "<set-username>"
  password: "<set-strong-password>"
proxy:
  algorithm: roundrobin
  timeout: 30
upstreams:
  - url: "socks5://socks.example.net:1080"
    auth:
      enabled: true
      username: "<upstream-username>"
      password: "<upstream-password>"
```

Replace the placeholders before use. Unknown YAML keys and invalid settings stop startup. To run only from YAML when `.env` is present in the project directory:

```sh
./go_proxy_mux -config config.yaml -env ""
```

### Listener security and TLS

For local HTTP, bind `127.0.0.1` and leave both TLS paths empty. A listener on another address requires TLS unless `allow_insecure_public_http` is explicitly enabled. Public access without client authentication separately requires `allow_unauthenticated_public_proxy`. The shipped examples use TLS and Basic authentication.

The certificate must match the hostname or IP used by clients. Certificate and key files are validated at startup. Renew them externally and restart the proxy to load replacements. The Docker image contains neither configuration secrets nor TLS keys.

### Upstream proxies and failover

Each upstream is selected by its URL scheme. HTTP and HTTPS upstreams can use Basic authentication. An HTTPS upstream validates its certificate against system roots; set `tls_ca_file` only for a private CA.

SOCKS4 supports an optional `USERID` in `auth.username` and no password. Domain destinations use SOCKS4a; IPv6 destinations require SOCKS5. SOCKS5 supports either no authentication or a username/password. Destination hostnames are sent to the SOCKS upstream for resolution. SOCKS5 username/password is sent to that upstream without encryption. Keep upstream credentials in `auth`, not in the URL.

`roundrobin` and `random` choose among available upstreams. A network or CONNECT setup failure excludes an upstream for `failover_cooldown` (30 seconds by default). Before responding to the client, a bodyless GET/HEAD or CONNECT may try one alternate upstream. POST and other requests with bodies are never replayed. An upstream `407` becomes a client-facing `502`, so clients are not asked for the upstream's credentials. There is no automatic direct connection if all upstreams fail.

### Resource limits

`max_connections` bounds active client TCP connections; `max_tunnels` bounds concurrent CONNECT tunnels across HTTP/1.1 and HTTP/2. A tunnel closes after `tunnel_idle_timeout` without traffic. SIGINT and SIGTERM stop new requests and close tracked tunnels within `shutdown_timeout`.

## Docker Compose

Two Compose files provide separate configuration paths:

| Configuration | Command | Notes |
|---|---|---|
| `.env` | `docker compose up --build` | [docker-compose.yml](docker-compose.yml) passes `.env` as raw values; Docker Compose 2.30+ required. `MUX_PUBLISH_HOST:MUX_PUBLISH_PORT` maps to `MUX_SERVER_PORT`. |
| `config.yaml` | `docker compose -f docker-compose.config.yml up --build` | [docker-compose.config.yml](docker-compose.config.yml) mounts YAML read-only and disables `.env` loading in the application. It publishes port 8380; update the mapping if `server.port` differs. |

Both variants mount `certs/` read-only. Set `server.host` (or `MUX_SERVER_HOST`) to `0.0.0.0` inside the container. The certificate and key paths in the examples resolve under `/app/certs/`.

## Usage

Start with `.env`:

```sh
./go_proxy_mux -env .env
```

A local HTTP listener with client authentication disabled can proxy an HTTPS website using CONNECT:

```sh
curl --proxy http://127.0.0.1:8380 https://example.com
```

For a TLS listener, configure clients with an HTTPS proxy URL, the certificate's hostname, and client credentials. Both incoming modes can carry HTTPS site traffic; TLS on the listener protects the client-to-proxy connection.

## Makefile commands

| Command | Description |
|---|---|
| `make build` | Build a normal binary for the current system |
| `make build-static` | Build `go_proxy_mux_static` with CGO disabled |
| `make run` | Run from the project directory; `.env` is loaded when present |
| `make ci` | Check formatting, vet, lint, and run race-enabled tests |
| `make docker-build` | Build `hightemp/go_proxy_mux:<VERSION>` locally |
| `make docker-push` | Push that local image to Docker Hub |
| `make clean` | Remove local normal and static binaries |
| `make release` | Check, build, commit, tag `VERSION`, and atomically push `main` and the tag |

## Testing

```sh
make ci
```

CI validates both Compose variants and builds the Docker image. Integration tests cover HTTP forwarding, HTTP/1.1 and HTTP/2 CONNECT, upstream TLS, SOCKS4/SOCKS5, authentication, failover, cancellation, and shutdown.

## Release

1. Set the plain semantic version in [VERSION](VERSION).
2. Configure the repository secrets `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN` for `hightemp/go_proxy_mux`.
3. From an up-to-date `main` branch, run `make release`.

The Make target runs CI and builds locally, commits project changes, creates an annotated `v<version>` tag, then pushes the branch and tag atomically. It refuses to overwrite an existing tag. A failed push can be retried with the same command if the local commit and tag are intact.

The tag triggers [GitHub Actions release](.github/workflows/release.yml) after [CI](.github/workflows/test.yml) passes. The workflow publishes six binaries, examples, and `SHA256SUMS` to GitHub Releases; it also publishes `linux/amd64` and `linux/arm64` images to Docker Hub. Stable releases update `latest`. The workflow checks the published image platforms and release asset checksums.
