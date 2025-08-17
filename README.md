# Go Proxy Mux

A simple HTTP forward proxy server written in Go with configurable load balancing and authentication.

## Configuration

Edit `config.yaml` to configure the proxy:

```yaml
server:
  port: 8080
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