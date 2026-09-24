# go_proxy_mux

An authenticated HTTP proxy that distributes HTTP and CONNECT requests across HTTP, HTTPS, SOCKS4, or SOCKS5 upstream proxies. Its TLS listener supports HTTP/1.1 and HTTP/2 CONNECT.

## Project layout

```text
cmd/go_proxy_mux/   Application entry point
internal/config/    YAML loading and validation
internal/balancer/  Upstream selection and cooldown
internal/proxy/     Authentication, forwarding, tunnels, and server lifecycle
```

## Configure and run

Copy `.env.example` to `.env`, then replace the client credentials, upstream addresses, and credentials. Set `MUX_UPSTREAM_COUNT` to the number of entries and fill `MUX_UPSTREAM_1_*` through `MUX_UPSTREAM_N_*`. Provide a valid certificate and key for the hostname clients use, at `./certs/fullchain.pem` and `./certs/privkey.pem`, or change the paths in `.env`. The TLS certificate and key are loaded at startup; restart the service after renewing them.

```sh
cp .env.example .env
chmod 600 .env
make build
./go_proxy_mux -env .env
```

The example intentionally fails validation until its placeholders and TLS files are replaced. All application settings are available as `MUX_*` variables in `.env.example`. Values are literal `KEY=VALUE` lines; do not add shell quotes around passwords. A value may contain `#`, `$`, or `=`; multiline values are unsupported.

Configuration precedence is **process environment → `.env` → YAML → built-in defaults**. The optional `-config` flag selects the YAML fallback, and `-env` selects the env file (`.env` by default; `-env ""` disables it). A missing YAML file is allowed when env settings provide the upstream list. `MUX_PROXY_TIMEOUT` and YAML `proxy.timeout` remain integers in seconds. Other timeout variables accept Go duration strings such as `15s` and `2m`.

For local HTTP development, set `MUX_SERVER_HOST=127.0.0.1` and leave both `MUX_SERVER_TLS_*_FILE` values empty; client authentication is optional on loopback. A non-loopback HTTP listener requires `MUX_SERVER_ALLOW_INSECURE_PUBLIC_HTTP=true`. A non-loopback listener without client authentication requires `MUX_SERVER_ALLOW_UNAUTHENTICATED_PUBLIC_PROXY=true`. Each exception must be enabled explicitly.

Choose each upstream with a URL scheme: `http://`, `https://`, `socks4://`, or `socks5://`. HTTPS upstreams use certificate verification against system roots; for a private CA, set `MUX_UPSTREAM_N_TLS_CA_FILE`. SOCKS4 uses an optional `MUX_UPSTREAM_N_AUTH_USERNAME` as USERID and has no password; domain destinations use SOCKS4a, while IPv6 destinations require SOCKS5. SOCKS5 supports no authentication or username/password authentication. Destination hostnames are resolved by the SOCKS upstream. Keep credentials in the `AUTH_*` variables, not in the URL. SOCKS5 username/password is sent to that upstream without encryption.

## Docker Compose

Prepare `.env` and a `certs/` directory containing the configured certificate and key, then run `docker compose up --build`. Compose passes `.env` to the container with raw values and publishes `MUX_PUBLISH_HOST:MUX_PUBLISH_PORT` to `MUX_SERVER_PORT`. It requires Docker Compose 2.30 or newer. Edit `.env` to change application settings in Compose; a shell override of `MUX_SERVER_PORT` also updates the published port. Certificates are mounted read-only; neither `.env` nor TLS keys are included in the image. Certificate acquisition is external to this project.

## Request handling

`roundrobin` and `random` select among available upstreams. A network or CONNECT setup failure excludes an upstream for `proxy.failover_cooldown`. A bodyless GET or HEAD and a CONNECT request before its success response may try one alternate upstream. Requests with bodies are never replayed. An upstream `407` becomes a client-facing `502` so the client is not prompted for the upstream's credentials.

The listener limits active TCP connections and CONNECT tunnels. A tunnel closes after `proxy.tunnel_idle_timeout` without traffic. SIGINT and SIGTERM stop new requests and close tracked tunnels. If credentials or TLS files are still placeholders, startup fails with a configuration error.

## Checks

`make ci` runs formatting, `go vet`, `golangci-lint`, and race-enabled tests. `make docker-build` builds the container image.
