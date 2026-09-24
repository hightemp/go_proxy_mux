// Package socks establishes TCP connections through SOCKS4/SOCKS4a or SOCKS5 proxies.
package socks

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// Credentials configures SOCKS4 USERID or SOCKS5 username/password authentication.
type Credentials struct {
	Enabled  bool
	Username string
	Password string
}

// ErrUnsupportedDestination means this SOCKS version cannot represent the target.
var ErrUnsupportedDestination = errors.New("unsupported SOCKS destination")

// DialContext connects to target through a SOCKS4 or SOCKS5 TCP proxy.
// Domain names are sent to the SOCKS server for resolution.
func DialContext(ctx context.Context, scheme, proxyAddress, targetAddress string, credentials Credentials, timeout time.Duration) (net.Conn, error) {
	if scheme != "socks4" && scheme != "socks5" {
		return nil, fmt.Errorf("unsupported SOCKS scheme")
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("SOCKS timeout must be positive")
	}
	host, portText, err := net.SplitHostPort(targetAddress)
	if err != nil || host == "" {
		return nil, fmt.Errorf("invalid SOCKS destination")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid SOCKS destination port")
	}
	if strings.ContainsRune(host, 0) || (net.ParseIP(host) == nil && len(host) > 255) {
		return nil, fmt.Errorf("%w: invalid hostname", ErrUnsupportedDestination)
	}
	if scheme == "socks4" && ipIsIPv6(host) {
		return nil, fmt.Errorf("%w: SOCKS4 does not support IPv6 destinations", ErrUnsupportedDestination)
	}
	if scheme == "socks4" && credentials.Enabled && credentials.Password != "" {
		return nil, fmt.Errorf("SOCKS4 USERID does not support a password")
	}
	connection, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", proxyAddress)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = connection.Close()
		}
	}()
	stopCancel := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stopCancel()
	deadline := time.Now().Add(timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return nil, err
	}
	if scheme == "socks4" {
		username := ""
		if credentials.Enabled {
			username = credentials.Username
		}
		err = connect4(connection, host, uint16(port), username)
	} else {
		err = connect5(connection, host, uint16(port), credentials)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	if err := connection.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	success = true
	return connection, nil
}

func ipIsIPv6(host string) bool {
	parsed := net.ParseIP(host)
	return parsed != nil && parsed.To4() == nil
}

func connect4(connection net.Conn, host string, port uint16, username string) error {
	if len(username) > 255 || strings.ContainsRune(username, 0) {
		return fmt.Errorf("invalid SOCKS4 USERID")
	}
	request := []byte{4, 1, byte(port >> 8), byte(port), 0, 0, 0, 1}
	if address := net.ParseIP(host); address != nil {
		ipv4 := address.To4()
		if ipv4 == nil {
			return fmt.Errorf("%w: SOCKS4 does not support IPv6 destinations", ErrUnsupportedDestination)
		}
		if ipv4[0] == 0 && ipv4[1] == 0 && ipv4[2] == 0 && ipv4[3] != 0 {
			return fmt.Errorf("%w: IPv4 address reserved for SOCKS4a hostnames", ErrUnsupportedDestination)
		}
		copy(request[4:8], ipv4)
	} else if len(host) == 0 || len(host) > 255 {
		return fmt.Errorf("%w: invalid SOCKS4a hostname", ErrUnsupportedDestination)
	}
	request = append(request, username...)
	request = append(request, 0)
	if net.ParseIP(host) == nil {
		request = append(request, host...)
		request = append(request, 0)
	}
	if err := writeAll(connection, request); err != nil {
		return err
	}
	var reply [8]byte
	if _, err := io.ReadFull(connection, reply[:]); err != nil {
		return err
	}
	if reply[0] != 0 || reply[1] != 0x5a {
		return fmt.Errorf("SOCKS4 CONNECT rejected with status %d", reply[1])
	}
	return nil
}

func connect5(connection net.Conn, host string, port uint16, credentials Credentials) error {
	method := byte(0)
	if credentials.Enabled {
		if len(credentials.Username) == 0 || len(credentials.Username) > 255 || len(credentials.Password) == 0 || len(credentials.Password) > 255 {
			return fmt.Errorf("invalid SOCKS5 credentials")
		}
		method = 2
	}
	if err := writeAll(connection, []byte{5, 1, method}); err != nil {
		return err
	}
	var selected [2]byte
	if _, err := io.ReadFull(connection, selected[:]); err != nil {
		return err
	}
	if selected[0] != 5 || selected[1] != method {
		return fmt.Errorf("SOCKS5 authentication method rejected")
	}
	if credentials.Enabled {
		request := []byte{1, byte(len(credentials.Username))}
		request = append(request, credentials.Username...)
		request = append(request, byte(len(credentials.Password)))
		request = append(request, credentials.Password...)
		if err := writeAll(connection, request); err != nil {
			return err
		}
		var response [2]byte
		if _, err := io.ReadFull(connection, response[:]); err != nil {
			return err
		}
		if response != [2]byte{1, 0} {
			return fmt.Errorf("SOCKS5 authentication rejected")
		}
	}
	request := []byte{5, 1, 0}
	address := net.ParseIP(host)
	switch {
	case address != nil && address.To4() != nil:
		request = append(request, 1)
		request = append(request, address.To4()...)
	case address != nil:
		request = append(request, 4)
		request = append(request, address.To16()...)
	default:
		request = append(request, 3, byte(len(host)))
		request = append(request, host...)
	}
	request = binary.BigEndian.AppendUint16(request, port)
	if err := writeAll(connection, request); err != nil {
		return err
	}
	var reply [4]byte
	if _, err := io.ReadFull(connection, reply[:]); err != nil {
		return err
	}
	if reply[0] != 5 || reply[2] != 0 {
		return fmt.Errorf("invalid SOCKS5 CONNECT reply")
	}
	addressLength := 0
	switch reply[3] {
	case 1:
		addressLength = 4
	case 4:
		addressLength = 16
	case 3:
		var length [1]byte
		if _, err := io.ReadFull(connection, length[:]); err != nil {
			return err
		}
		addressLength = int(length[0])
	default:
		return fmt.Errorf("invalid SOCKS5 reply address type")
	}
	if _, err := io.CopyN(io.Discard, connection, int64(addressLength+2)); err != nil {
		return err
	}
	if reply[1] != 0 {
		return fmt.Errorf("SOCKS5 CONNECT rejected with status %d", reply[1])
	}
	return nil
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return errors.New("short SOCKS write")
		}
		data = data[n:]
	}
	return nil
}
