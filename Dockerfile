FROM golang:1.27.1-alpine3.24@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o go_proxy_mux ./cmd/go_proxy_mux

FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6

RUN apk --no-cache add ca-certificates \
    && addgroup -S -g 1000 proxy \
    && adduser -S -D -H -u 1000 -G proxy proxy
WORKDIR /app

COPY --from=builder /app/go_proxy_mux /usr/local/bin/go_proxy_mux

EXPOSE 8380

USER 1000:1000

ENTRYPOINT ["/usr/local/bin/go_proxy_mux"]
CMD ["-config", "/app/config.yaml"]
