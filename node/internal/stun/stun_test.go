package stun

import (
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestSTUNServer(t *testing.T) {
	conn, err := StartServer("127.0.0.1:0")
	if err != nil {
		t.Fatalf("StartServer: %v", err)
	}
	defer conn.Close()

	serverAddr := conn.LocalAddr().String()

	clientConn, err := net.Dial("udp4", serverAddr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer clientConn.Close()

	// Send STUN binding request
	req := make([]byte, 20)
	binary.BigEndian.PutUint16(req[0:2], bindingRequest)
	binary.BigEndian.PutUint16(req[2:4], 0)
	binary.BigEndian.PutUint32(req[4:8], magicCookie)
	copy(req[8:20], []byte("123456789012"))

	if _, err := clientConn.Write(req); err != nil {
		t.Fatalf("Write: %v", err)
	}

	_ = clientConn.SetReadDeadline(time.Now().Add(time.Second))
	resp := make([]byte, 64)
	n, err := clientConn.Read(resp)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if n != 32 {
		t.Fatalf("expected 32 bytes, got %d", n)
	}
	if binary.BigEndian.Uint16(resp[0:2]) != bindingResponse {
		t.Fatalf("expected binding response 0x0101, got 0x%04x", binary.BigEndian.Uint16(resp[0:2]))
	}
	if binary.BigEndian.Uint32(resp[4:8]) != magicCookie {
		t.Fatalf("magic cookie mismatch")
	}

	// Validate XOR-MAPPED-ADDRESS
	xPort := binary.BigEndian.Uint16(resp[26:28])
	port := xPort ^ uint16(magicCookie>>16)
	xIP := binary.BigEndian.Uint32(resp[28:32])
	rawIP := xIP ^ magicCookie
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, rawIP)

	clientLocalPort := clientConn.LocalAddr().(*net.UDPAddr).Port
	if int(port) != clientLocalPort {
		t.Fatalf("expected port %d, got %d", clientLocalPort, port)
	}
	if !ip.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Fatalf("expected 127.0.0.1, got %s", ip.String())
	}
}
