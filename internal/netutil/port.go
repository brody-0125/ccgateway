package netutil

import (
	"errors"
	"net"
)

func IsLocalPortFree(port int) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func FindAvailablePort(preferred, scan int, isHealthy func(int) bool) (int, error) {
	if preferred <= 0 || preferred > 65535 {
		preferred = 8317
	}
	if scan < 0 {
		scan = 20
	}
	for i := 0; i <= scan; i++ {
		p := preferred + i
		if p <= 0 || p > 65535 {
			break
		}
		if isHealthy != nil && isHealthy(p) {
			return p, nil
		}
		if IsLocalPortFree(p) {
			return p, nil
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, errors.New("failed to find ephemeral port")
	}
	defer ln.Close()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.New("failed to resolve ephemeral port")
	}
	return addr.Port, nil
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	buf := make([]byte, 0, 11)
	for v > 0 {
		buf = append([]byte{byte('0' + (v % 10))}, buf...)
		v /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}
