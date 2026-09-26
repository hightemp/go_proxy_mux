#!/usr/bin/env bash
set -euo pipefail

image=${1:?usage: container-smoke.sh IMAGE}
if [[ $(id -u) == 0 ]]; then
  echo "run this smoke test as a non-root user who owns its temporary files" >&2
  exit 1
fi
fixture_dir=$(mktemp -d)
container_id=
cleanup() {
  if [[ -n "$container_id" ]]; then
    docker stop "$container_id" >/dev/null 2>&1 || true
  fi
  if [[ -d "$fixture_dir" ]]; then
    rm -r -- "$fixture_dir"
  fi
}
trap cleanup EXIT

image_user=$(docker image inspect "$image" --format '{{.Config.User}}')
case "$image_user" in
  ''|0|0:*|root|root:*)
    echo "image runs as root" >&2
    exit 1
    ;;
esac

mkdir "$fixture_dir/certs"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -keyout "$fixture_dir/certs/key.pem" \
  -out "$fixture_dir/certs/cert.pem" \
  -subj '/CN=localhost' \
  -addext 'subjectAltName=DNS:localhost' >/dev/null 2>&1
chmod 600 "$fixture_dir/certs/key.pem" "$fixture_dir/certs/cert.pem"
cat > "$fixture_dir/config.yaml" <<'YAML'
server:
  host: 0.0.0.0
  port: 8380
  tls:
    cert_file: /app/certs/cert.pem
    key_file: /app/certs/key.pem
auth:
  enabled: true
  username: smoke-user
  password: smoke-password
upstreams:
  - url: http://127.0.0.1:9
YAML
chmod 600 "$fixture_dir/config.yaml"

container_id=$(docker run -d --rm \
  --user "$(id -u):$(id -g)" \
  --read-only --cap-drop ALL --security-opt no-new-privileges \
  -p 127.0.0.1::8380 \
  --mount "type=bind,src=$fixture_dir/config.yaml,dst=/app/config.yaml,readonly" \
  --mount "type=bind,src=$fixture_dir/certs,dst=/app/certs,readonly" \
  "$image" -config /app/config.yaml -env "")
port=$(docker port "$container_id" 8380/tcp | awk -F: 'NR==1 {print $NF}')

unauthorized=
for _ in {1..30}; do
  unauthorized=$(curl --silent --max-time 3 --noproxy '' \
    --proxy-cacert "$fixture_dir/certs/cert.pem" \
    --proxy "https://localhost:$port" \
    --output /dev/null --write-out '%{http_code}' \
    http://example.test/ || true)
  if [[ "$unauthorized" == 407 ]]; then
    break
  fi
  sleep 0.1
done
if [[ "$unauthorized" != 407 ]]; then
  echo "unauthorized HTTPS proxy request returned $unauthorized, want 407" >&2
  exit 1
fi

authorized=$(curl --silent --show-error --max-time 5 --noproxy '' \
  --proxy-cacert "$fixture_dir/certs/cert.pem" \
  --proxy "https://localhost:$port" \
  --proxy-user smoke-user:smoke-password \
  --output /dev/null --write-out '%{http_code}' \
  http://example.test/)
if [[ "$authorized" != 502 ]]; then
  echo "authenticated HTTPS proxy request returned $authorized, want 502 for unavailable fixture upstream" >&2
  exit 1
fi
echo "Container smoke test passed: non-root image, read-only TLS key, 407 and 502"
