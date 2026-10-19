package stun

import (
	"encoding/binary"
	"log"
	"net"
)

const (
	bindingRequest  = 0x0001
	bindingResponse = 0x0101
	magicCookie     = 0x2112A442
	attrXorMapped   = 0x0020
)

// StartServer starts a lightweight RFC 5389 STUN responder on the given UDP address (e.g. ":3478").
func StartServer(addr string) (*net.UDPConn, error) {
	udpAddr, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp4", udpAddr)
	if err != nil {
		return nil, err
	}

	go func() {
		buf := make([]byte, 1024)
		for {
			n, remoteAddr, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if n < 20 {
				continue
			}

			// Validate STUN header
			msgType := binary.BigEndian.Uint16(buf[0:2])
			cookie := binary.BigEndian.Uint32(buf[4:8])
			if msgType != bindingRequest || cookie != magicCookie {
				continue
			}

			// Construct XOR-MAPPED-ADDRESS attribute (RFC 5389)
			// Header (20) + Attr Type (2) + Attr Len (2) + Reserved (1) + Family (1) + X-Port (2) + X-Address (4) = 32 bytes
			resp := make([]byte, 32)
			binary.BigEndian.PutUint16(resp[0:2], bindingResponse)
			binary.BigEndian.PutUint16(resp[2:4], 12) // attribute length
			binary.BigEndian.PutUint32(resp[4:8], magicCookie)
			copy(resp[8:20], buf[8:20]) // echo transaction ID (12 bytes)

			resp[20] = 0x00
			resp[21] = byte(attrXorMapped) // 0x0020
			resp[22] = 0x00
			resp[23] = 8 // attribute value length
			resp[24] = 0 // reserved
			resp[25] = 1 // IPv4 family

			// XOR Port
			xPort := uint16(remoteAddr.Port) ^ uint16(magicCookie>>16)
			binary.BigEndian.PutUint16(resp[26:28], xPort)

			// XOR Address
			ip := remoteAddr.IP.To4()
			if ip == nil {
				continue
			}
			rawIP := binary.BigEndian.Uint32(ip)
			binary.BigEndian.PutUint32(resp[28:32], rawIP^magicCookie)

			_, _ = conn.WriteToUDP(resp, remoteAddr)
		}
	}()

	log.Printf("STUN server listening on %s (UDP)", addr)
	return conn, nil
}
