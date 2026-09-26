FROM golang:1.26.7-alpine3.24@sha256:28d89ee9cc0ff9fec75c82ca201e6bf7fdf9a679d4b7b24dfa04f2bb766bb468 AS builder

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
