package proxy

import (
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"time"
)

// ProcessResult adalah hasil exit satu child yang diamati saat race readiness
// — dipakai orchestrator untuk memilih antara "server siap" dan "child mati
// lebih dulu".
type ProcessResult struct {
	Index int
	Err   error
}

// WaitReady menunggu TCP listener siap.
func WaitReady(host string, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 200*time.Millisecond)
		if err == nil {
			c.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("server belum siap pada %s:%d", host, port)
}

// WaitReadyOrProcess menjaga nilai diagnosa child yang exit saat startup.
// Hasil nil berarti readiness menang race.
func WaitReadyOrProcess(host string, port int, timeout time.Duration, done <-chan ProcessResult) (*ProcessResult, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case result := <-done:
			return &result, nil
		case <-deadline.C:
			return nil, fmt.Errorf("server Laravel belum siap pada %s:%d", host, port)
		case <-ticker.C:
			c, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 200*time.Millisecond)
			if err == nil {
				_ = c.Close()
				return nil, nil
			}
		}
	}
}

// WaitTLSReady menunggu listener menyelesaikan handshake TLS.
func WaitTLSReady(serverName string, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	config := &tls.Config{
		// CA internal Caddy dipercaya setelah probe ini. Probe hanya
		// memastikan listener bisa menyelesaikan TLS, tidak bergantung trust
		// store host (yang membuat startup gagal di mesin baru).
		ServerName:         serverName,
		InsecureSkipVerify: true, //nolint:gosec -- deliberate local readiness probe
	}
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 500 * time.Millisecond}, "tcp", address, config)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("TLS listener tidak menyelesaikan handshake pada %s:%d (alamat %s; error terakhir: %v)", serverName, port, address, lastErr)
}
