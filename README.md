# Go Proxy Mux

A simple HTTP forward proxy server written in Go with configurable load balancing (to http, https) and authentication.

## Project layout

```text
cmd/go_proxy_mux/   Application entry point
internal/config/    YAML configuration types and loading
internal/balancer/  Upstream selection
internal/proxy/     Authentication, HTTP forwarding, and CONNECT handling
```

The configuration example and build files remain at the repository root.

## Build and run

```sh
cp config.example.yaml config.yaml
# Set your listening address and credentials in config.yaml.
make build
./go_proxy_mux -config config.yaml
```

To run without building a binary first, use `go run ./cmd/go_proxy_mux -config config.yaml`.

## Configuration

Edit `config.yaml` to configure the proxy:

```yaml
server:
  port: 8380
  host: "0.0.0.0"

auth:
  enabled: true
  username: "admin"
  password: "password"

proxy:
  algorithm: "roundrobin"  # or "random"
  timeout: 30

upstreams:
  - url: "http://proxy1.example.com:8080"
    auth:
      enabled: false
      username: ""
      password: ""
  - url: "http://proxy2.example.com:8080"
    auth:
      enabled: true
      username: "user1"
      password: "pass1"
```

## Load Balancing Algorithms

- `roundrobin`: Distributes requests evenly across all upstream servers
- `random`: Randomly selects an upstream server for each request
