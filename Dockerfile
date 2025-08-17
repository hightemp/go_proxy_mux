FROM golang:1.23-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o go_proxy_mux .

FROM alpine:latest

RUN apk --no-cache add ca-certificates
WORKDIR /root/

COPY --from=builder /app/go_proxy_mux .
COPY --from=builder /app/config.example.yaml ./config.yaml

EXPOSE 8380

CMD ["./go_proxy_mux"]