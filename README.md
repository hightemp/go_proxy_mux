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

Copy `config.example.yaml` to `config.yaml`. Replace the client credentials, upstream addresses, and credentials. Provide a valid certificate and key for the hostname clients use, at `./certs/fullchain.pem` and `./certs/privkey.pem`, or change those paths in the config. The TLS certificate and key are loaded at startup; restart the service after renewing them.

```sh
cp config.example.yaml config.yaml
chmod 600 config.yaml
make build
./go_proxy_mux -config config.yaml
```

The example intentionally fails validation until its placeholders and TLS files are replaced. The `-config` flag also accepts another path. `proxy.timeout` remains an integer in seconds. New timeout fields accept Go duration strings such as `15s` and `2m`.

For local development, bind `server.host` to `127.0.0.1` and omit `server.tls`; client authentication is optional on loopback. A non-loopback HTTP listener requires `server.allow_insecure_public_http: true`. A non-loopback listener without client authentication requires `server.allow_unauthenticated_public_proxy: true`. Each exception must be enabled explicitly.

Choose each upstream with a URL scheme: `http://`, `https://`, `socks4://`, or `socks5://`. HTTPS upstreams use certificate verification against system roots; for a private CA, set `tls_ca_file`. SOCKS4 uses an optional `auth.username` as USERID and has no password; domain destinations use SOCKS4a, while IPv6 destinations require SOCKS5. SOCKS5 supports no authentication or username/password authentication. Destination hostnames are resolved by the SOCKS upstream. Keep credentials in `auth`, not in the URL. SOCKS5 username/password is sent to that upstream without encryption.

## Docker Compose

Prepare `config.yaml` and a `certs/` directory containing the configured certificate and key, then run `docker compose up --build`. Compose publishes port 8380 as TLS and mounts the configuration and certificates read-only. The image contains neither the working configuration nor TLS keys. Certificate acquisition is external to this project.

## Request handling

`roundrobin` and `random` select among available upstreams. A network or CONNECT setup failure excludes an upstream for `proxy.failover_cooldown`. A bodyless GET or HEAD and a CONNECT request before its success response may try one alternate upstream. Requests with bodies are never replayed. An upstream `407` becomes a client-facing `502` so the client is not prompted for the upstream's credentials.

The listener limits active TCP connections and CONNECT tunnels. A tunnel closes after `proxy.tunnel_idle_timeout` without traffic. SIGINT and SIGTERM stop new requests and close tracked tunnels. If credentials or TLS files are still placeholders, startup fails with a configuration error.

## Checks

`make ci` runs formatting, `go vet`, `golangci-lint`, and race-enabled tests. `make docker-build` builds the container image.
