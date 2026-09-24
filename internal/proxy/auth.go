package proxy

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
)

func (ps *ProxyServer) isAuthorized(r *http.Request) bool {
	header := r.Header.Get("Proxy-Authorization")
	if len(header) < 6 || !strings.EqualFold(header[:6], "Basic ") {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(header[6:])
	if err != nil {
		return false
	}
	username, password, ok := strings.Cut(string(decoded), ":")
	if !ok {
		return false
	}
	expectedUser := sha256.Sum256([]byte(ps.config.Auth.Username))
	expectedPassword := sha256.Sum256([]byte(ps.config.Auth.Password))
	userHash := sha256.Sum256([]byte(username))
	passwordHash := sha256.Sum256([]byte(password))
	userOK := subtle.ConstantTimeCompare(expectedUser[:], userHash[:])
	passwordOK := subtle.ConstantTimeCompare(expectedPassword[:], passwordHash[:])
	return userOK&passwordOK == 1
}
