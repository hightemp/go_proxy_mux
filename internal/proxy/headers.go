package proxy

import (
	"net/http"
	"strings"
)

var hopHeaders = []string{
	"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
	"Proxy-Connection", "TE", "Trailer", "Transfer-Encoding", "Upgrade",
}

func removeHopHeaders(header http.Header) {
	for _, field := range []string{"Connection", "Proxy-Connection"} {
		for _, value := range header.Values(field) {
			for _, token := range strings.Split(value, ",") {
				if name := strings.TrimSpace(token); name != "" {
					header.Del(name)
				}
			}
		}
	}
	for _, name := range hopHeaders {
		header.Del(name)
	}
}
