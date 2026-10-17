package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestRegisterDevice(t *testing.T) {
	tempDir := t.TempDir()
	server := New("testnode", WithStorageDir(tempDir))
	defer server.Close()

	// 1. Initial registration generates valid UUID
	dev1, err := server.registerDevice("aditya", "")
	if err != nil {
		t.Fatalf("unexpected error registering aditya: %v", err)
	}
	if !validDeviceID(dev1) {
		t.Fatalf("expected valid UUID, got: %s", dev1)
	}

	// 2. Re-registering with the same device ID succeeds
	dev2, err := server.registerDevice("aditya", dev1)
	if err != nil {
		t.Fatalf("re-registration failed: %v", err)
	}
	if dev2 != dev1 {
		t.Fatalf("expected same device ID, got %s != %s", dev2, dev1)
	}

	// 3. Registering same username with different device ID must fail
	otherID, _ := newDeviceID()
	if _, err := server.registerDevice("aditya", otherID); err == nil {
		t.Fatal("expected error when registering aditya with mismatched device ID")
	}

	// 4. Registering a different username with an already assigned device ID must fail
	if _, err := server.registerDevice("prachi46", dev1); err == nil {
		t.Fatal("expected error when registering different username with existing device ID")
	}

	// 5. Registering a new user succeeds
	prachiDev, err := server.registerDevice("prachi46", "")
	if err != nil {
		t.Fatalf("failed registering prachi46: %v", err)
	}
	if prachiDev == dev1 {
		t.Fatal("device IDs must be unique across different users")
	}

	// 6. Test persistence across server restart
	server2 := New("testnode", WithStorageDir(tempDir))
	defer server2.Close()
	if id := server2.deviceIDs["aditya"]; id != dev1 {
		t.Fatalf("expected persisted device ID %s for aditya, got %s", dev1, id)
	}
	if id := server2.deviceIDs["prachi46"]; id != prachiDev {
		t.Fatalf("expected persisted device ID %s for prachi46, got %s", prachiDev, id)
	}
}

func TestStorageAndCompaction(t *testing.T) {
	tempDir := t.TempDir()
	server := New("testnode", WithStorageDir(tempDir))

	// Add messages and persist
	for i := 1; i <= 5; i++ {
		msg := Message{
			ID:        fmt.Sprintf("testnode-%d", i),
			From:      "aditya",
			To:        "prachi46",
			Body:      fmt.Sprintf("hello %d", i),
			CreatedAt: time.Now().UTC(),
			State:     "sent",
		}
		if err := server.persistMessage(msg); err != nil {
			t.Fatalf("persistMessage error: %v", err)
		}
		server.messages = append(server.messages, msg)
		server.messageSet[msg.ID] = true
	}

	// Verify messages saved to disk
	path := filepath.Join(tempDir, "messages.jsonl")
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		t.Fatalf("message log not written or empty: %v", err)
	}

	// Test compaction
	server.mu.Lock()
	if err := server.compactMessageLog(); err != nil {
		server.mu.Unlock()
		t.Fatalf("compactMessageLog error: %v", err)
	}
	server.mu.Unlock()
	server.Close()

	// Reopen server and verify all messages are reloaded
	server2 := New("testnode", WithStorageDir(tempDir))
	defer server2.Close()
	if len(server2.messages) != 5 {
		t.Fatalf("expected 5 messages after reload, got %d", len(server2.messages))
	}
	if server2.messages[0].Body != "hello 1" {
		t.Fatalf("unexpected message content: %s", server2.messages[0].Body)
	}
}

func dialWS(t *testing.T, srv *httptest.Server, deviceID string) *websocket.Conn {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	u.Scheme = "ws"
	u.Path = "/ws"
	q := u.Query()
	q.Set("device_id", deviceID)
	u.RawQuery = q.Encode()

	header := http.Header{}
	header.Set("Origin", srv.URL)

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), header)
	if err != nil {
		t.Fatalf("dialWS error: %v", err)
	}
	return conn
}

func readWS(t *testing.T, conn *websocket.Conn) WSMessage {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, p, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("readWS error: %v", err)
	}
	var msg WSMessage
	if err := json.Unmarshal(p, &msg); err != nil {
		t.Fatalf("json unmarshal WSMessage error: %v", err)
	}
	return msg
}

func TestWebSocketCommunication(t *testing.T) {
	tempDir := t.TempDir()
	server := New("testnode", WithStorageDir(tempDir))
	defer server.Close()

	// Register users
	devAditya, err := server.registerDevice("aditya", "")
	if err != nil {
		t.Fatalf("register aditya error: %v", err)
	}
	devPrachi, err := server.registerDevice("prachi46", "")
	if err != nil {
		t.Fatalf("register prachi46 error: %v", err)
	}

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	// Connect Aditya
	wsAditya := dialWS(t, ts, devAditya)
	defer wsAditya.Close()

	// Aditya receives presence_sync
	initMsg := readWS(t, wsAditya)
	if initMsg.Type != "presence_sync" {
		t.Fatalf("expected presence_sync, got %s", initMsg.Type)
	}

	// Connect Prachi46
	wsPrachi := dialWS(t, ts, devPrachi)
	defer wsPrachi.Close()

	// Prachi receives presence_sync
	_ = readWS(t, wsPrachi)

	// Aditya receives user_online event for Prachi46
	onlineEvt := readWS(t, wsAditya)
	if onlineEvt.Type != "user_online" || onlineEvt.User == nil || onlineEvt.User.Username != "prachi46" {
		t.Fatalf("expected user_online for prachi46, got %+v", onlineEvt)
	}

	// Aditya sends chat message to Prachi46
	sendPayload := WSMessage{
		Type: "send_message",
		To:   "prachi46",
		Body: "Hey Prachi!",
	}
	if err := wsAditya.WriteJSON(sendPayload); err != nil {
		t.Fatalf("write chat msg error: %v", err)
	}

	// Aditya receives message_sent confirmation
	sentConfirm := readWS(t, wsAditya)
	if sentConfirm.Type != "message_sent" || sentConfirm.Message == nil || sentConfirm.Message.Body != "Hey Prachi!" {
		t.Fatalf("expected message_sent confirmation, got %+v", sentConfirm)
	}

	// Prachi receives new_message
	recvdMsg := readWS(t, wsPrachi)
	if recvdMsg.Type != "new_message" || recvdMsg.Message == nil || recvdMsg.Message.From != "aditya" {
		t.Fatalf("expected new_message from aditya, got %+v", recvdMsg)
	}

	// Prachi sends message_ack
	ackPayload := WSMessage{
		Type:      "message_ack",
		MessageID: recvdMsg.Message.ID,
	}
	if err := wsPrachi.WriteJSON(ackPayload); err != nil {
		t.Fatalf("write ack error: %v", err)
	}

	// Aditya receives message_status update (delivered)
	statusMsg := readWS(t, wsAditya)
	if statusMsg.Type != "message_status" || statusMsg.Message == nil || statusMsg.Message.State != "delivered" {
		t.Fatalf("expected delivered state, got %+v", statusMsg)
	}
}

func TestWebRTCSignalling(t *testing.T) {
	tempDir := t.TempDir()
	server := New("testnode", WithStorageDir(tempDir))
	defer server.Close()

	devAditya, _ := server.registerDevice("aditya", "")
	devPrachi, _ := server.registerDevice("prachi46", "")

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	wsAditya := dialWS(t, ts, devAditya)
	defer wsAditya.Close()
	_ = readWS(t, wsAditya) // presence_sync

	wsPrachi := dialWS(t, ts, devPrachi)
	defer wsPrachi.Close()
	_ = readWS(t, wsPrachi) // presence_sync
	_ = readWS(t, wsAditya) // user_online

	// Aditya calls Prachi
	callOffer := WSMessage{
		Type:   "call_offer",
		To:     "prachi46",
		CallID: "call-12345",
		SDP:    "v=0\r\no=- 123 456 IN IP4 10.20.1.1\r\ns=-\r\nt=0 0\r\n",
	}
	if err := wsAditya.WriteJSON(callOffer); err != nil {
		t.Fatalf("write call_offer error: %v", err)
	}

	// Prachi receives call_offer
	offerRecvd := readWS(t, wsPrachi)
	if offerRecvd.Type != "call_offer" || offerRecvd.From != "aditya" || offerRecvd.CallID != "call-12345" {
		t.Fatalf("expected call_offer from aditya, got %+v", offerRecvd)
	}

	// Prachi sends answer
	callAnswer := WSMessage{
		Type:   "call_answer",
		To:     "aditya",
		CallID: "call-12345",
		SDP:    "v=0\r\no=- 789 101 IN IP4 10.20.2.1\r\ns=-\r\nt=0 0\r\n",
	}
	if err := wsPrachi.WriteJSON(callAnswer); err != nil {
		t.Fatalf("write call_answer error: %v", err)
	}

	// Aditya receives call_answer
	answerRecvd := readWS(t, wsAditya)
	if answerRecvd.Type != "call_answer" || answerRecvd.From != "prachi46" {
		t.Fatalf("expected call_answer from prachi46, got %+v", answerRecvd)
	}

	// Test ICE candidate relay
	iceCandidate := WSMessage{
		Type:      "ice_candidate",
		To:        "prachi46",
		CallID:    "call-12345",
		Candidate: `{"candidate":"candidate:1 1 UDP 2130706431 10.20.1.1 50000 typ host"}`,
	}
	if err := wsAditya.WriteJSON(iceCandidate); err != nil {
		t.Fatalf("write ice_candidate error: %v", err)
	}

	candRecvd := readWS(t, wsPrachi)
	if candRecvd.Type != "ice_candidate" || candRecvd.From != "aditya" {
		t.Fatalf("expected ice_candidate from aditya, got %+v", candRecvd)
	}
}

func TestMeshTokenSecurity(t *testing.T) {
	server := New("testnode", WithMeshToken("secret-token-12345678901234567890123456789012"))
	defer server.Close()

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	// 1. Without token, /internal/sync should return 401 Unauthorized
	req, _ := http.NewRequest("POST", ts.URL+"/internal/sync", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 2. With invalid token, returns 401
	req, _ = http.NewRequest("POST", ts.URL+"/internal/sync", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Xenia-Mesh-Token", "wrong-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong token, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 3. With correct token, returns 200 OK
	req, _ = http.NewRequest("POST", ts.URL+"/internal/sync", strings.NewReader(`{"node_id":"peer1","users":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Xenia-Mesh-Token", "secret-token-12345678901234567890123456789012")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with valid token, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}
