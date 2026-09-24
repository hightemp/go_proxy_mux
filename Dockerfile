FROM golang:1.23-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o go_proxy_mux ./cmd/go_proxy_mux

FROM alpine:latest

RUN apk --no-cache add ca-certificates
WORKDIR /app

COPY --from=builder /app/go_proxy_mux /usr/local/bin/go_proxy_mux

EXPOSE 8380

ENTRYPOINT ["/usr/local/bin/go_proxy_mux"]
CMD ["-config", "/app/config.yaml"]
