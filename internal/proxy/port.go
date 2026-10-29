package proxy

import (
	"fmt"
	"net"
	"strconv"
)

// portAvailable sengaja mengikat alamat yang akan dipakai Caddy. Ini menangkap
// listener yang ada sekaligus reservasi platform-specific. Hanya preflight;
// Caddy tetap otoritas saat startup (port bisa race).
func portAvailable(port int) bool {
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

// SelectCaddyPort memilih port Caddy: configured bila tersedia dan berbeda
// dari port Laravel, jika tidak fallback deterministik ke atas.
func SelectCaddyPort(configured, laravel int) (int, error) {
	if configured < 1 || configured > 65535 {
		return 0, fmt.Errorf("caddy port harus antara 1 dan 65535")
	}
	if configured != laravel && portAvailable(configured) {
		return configured, nil
	}
	// Fallback deterministik dan ramah manusia. Hindari ephemeral port 0:
	// port terpilih juga bagian dari URL virtual-domain yang harus dibuka
	// user.
	for port := configured + 1; port <= configured+100; port++ {
		if port > 65535 || port == laravel {
			continue
		}
		if portAvailable(port) {
			return port, nil
		}
	}
	return 0, fmt.Errorf("port Caddy %d tidak tersedia dan fallback %d-%d juga tidak bisa dipakai; pilih port lain dengan --caddy-port (cek Windows excluded port range dengan `netsh interface ipv4 show excludedportrange protocol=tcp`)", configured, configured+1, configured+100)
}
