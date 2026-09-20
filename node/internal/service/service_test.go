package service

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func request(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, path, bytes.NewBufferString(body)))
	return recorder
}

func TestNodeAPI(t *testing.T) {
	handler := New("test-node").Handler()

	health := request(t, handler, http.MethodGet, "/health", "")
	if health.Code != http.StatusOK || !bytes.Contains(health.Body.Bytes(), []byte(`"node_id":"test-node"`)) {
		t.Fatalf("health response = %d: %s", health.Code, health.Body.String())
	}

	presence := request(t, handler, http.MethodPost, "/presence", `{"user_id":"alice","status":"online"}`)
	if presence.Code != http.StatusOK {
		t.Fatalf("presence response = %d: %s", presence.Code, presence.Body.String())
	}

	users := request(t, handler, http.MethodGet, "/users", "")
	if users.Code != http.StatusOK || !bytes.Contains(users.Body.Bytes(), []byte(`"user_id":"alice"`)) {
		t.Fatalf("users response = %d: %s", users.Code, users.Body.String())
	}

	message := request(t, handler, http.MethodPost, "/message", `{"from":"alice","to":"bob","body":"hello"}`)
	if message.Code != http.StatusAccepted {
		t.Fatalf("message response = %d: %s", message.Code, message.Body.String())
	}
	var received Message
	if err := json.Unmarshal(message.Body.Bytes(), &received); err != nil {
		t.Fatal(err)
	}
	if received.State != "buffered" || received.ID != "test-node-1" {
		t.Fatalf("unexpected message: %#v", received)
	}
}

func TestRejectsInvalidRequests(t *testing.T) {
	handler := New("test-node").Handler()
	if response := request(t, handler, http.MethodPost, "/presence", `{"user_id":"alice"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("presence response = %d", response.Code)
	}
	if response := request(t, handler, http.MethodPost, "/message", `{"from":"alice","to":"bob"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("message response = %d", response.Code)
	}
}

func TestCallSignallingBetweenPeers(t *testing.T) {
	n1 := New("n1")
	n3 := New("n3")
	n1.peerURL = "http://n3.test"
	n3.peerURL = "http://n1.test"
	n1.http = &http.Client{Transport: handlerTransport{handler: n3.Handler()}}
	n3.http = &http.Client{Transport: handlerTransport{handler: n1.Handler()}}

	invite := request(t, n1.Handler(), http.MethodPost, "/calls", `{"from":"alice","to":"bob"}`)
	if invite.Code != http.StatusAccepted {
		t.Fatalf("invite response = %d: %s", invite.Code, invite.Body.String())
	}
	var call Call
	if err := json.Unmarshal(invite.Body.Bytes(), &call); err != nil {
		t.Fatal(err)
	}
	if remote := request(t, n3.Handler(), http.MethodGet, "/calls/"+call.ID, ""); remote.Code != http.StatusOK {
		t.Fatalf("remote call response = %d: %s", remote.Code, remote.Body.String())
	}
	answer := request(t, n3.Handler(), http.MethodPost, "/calls/"+call.ID+"/answer", `{"accepted":true}`)
	if answer.Code != http.StatusOK {
		t.Fatalf("answer response = %d: %s", answer.Code, answer.Body.String())
	}
	updated := request(t, n1.Handler(), http.MethodGet, "/calls/"+call.ID, "")
	if !bytes.Contains(updated.Body.Bytes(), []byte(`"state":"accepted"`)) {
		t.Fatalf("origin call response = %d: %s", updated.Code, updated.Body.String())
	}
}

func TestMessageForwardingBetweenPeers(t *testing.T) {
	n1 := New("n1")
	n3 := New("n3")
	n1.peerURL = "http://n3.test"
	n3.peerURL = "http://n1.test"
	n1.http = &http.Client{Transport: handlerTransport{handler: n3.Handler()}}
	n3.http = &http.Client{Transport: handlerTransport{handler: n1.Handler()}}

	// Send a message from alice (on n1) to bob (on n3).
	send := request(t, n1.Handler(), http.MethodPost, "/message", `{"from":"alice","to":"bob","body":"hello mesh"}`)
	if send.Code != http.StatusAccepted {
		t.Fatalf("send response = %d: %s", send.Code, send.Body.String())
	}
	var sent Message
	if err := json.Unmarshal(send.Body.Bytes(), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.State != "forwarded" {
		t.Fatalf("expected state=forwarded, got %s", sent.State)
	}

	// Verify the message arrived at n3.
	inbox := request(t, n3.Handler(), http.MethodGet, "/messages/bob", "")
	if inbox.Code != http.StatusOK {
		t.Fatalf("inbox response = %d: %s", inbox.Code, inbox.Body.String())
	}
	if !bytes.Contains(inbox.Body.Bytes(), []byte(`"state":"delivered"`)) {
		t.Fatalf("expected delivered message on n3: %s", inbox.Body.String())
	}
	if !bytes.Contains(inbox.Body.Bytes(), []byte(`"body":"hello mesh"`)) {
		t.Fatalf("message body not found on n3: %s", inbox.Body.String())
	}
}

func TestMessageWithoutPeerStaysBuffered(t *testing.T) {
	handler := New("standalone").Handler()
	send := request(t, handler, http.MethodPost, "/message", `{"from":"alice","to":"bob","body":"local only"}`)
	if send.Code != http.StatusAccepted {
		t.Fatalf("send response = %d: %s", send.Code, send.Body.String())
	}
	var msg Message
	if err := json.Unmarshal(send.Body.Bytes(), &msg); err != nil {
		t.Fatal(err)
	}
	if msg.State != "buffered" {
		t.Fatalf("expected state=buffered, got %s", msg.State)
	}
}

func TestOfflineMessagePersistence(t *testing.T) {
	storageDir := t.TempDir()

	// 1. Start node 1 and create a message
	n1 := New("persist-node", WithStorageDir(storageDir))
	send := request(t, n1.Handler(), http.MethodPost, "/message", `{"from":"alice","to":"bob","body":"persisted message"}`)
	if send.Code != http.StatusAccepted {
		t.Fatalf("send response = %d: %s", send.Code, send.Body.String())
	}
	var msg Message
	if err := json.Unmarshal(send.Body.Bytes(), &msg); err != nil {
		t.Fatal(err)
	}

	// Wait briefly to ensure file write is complete (though Sync() makes it synchronous)
	if n1.storageFile != nil {
		n1.storageFile.Close() // Simulate shutdown by closing the file
	}

	// 2. Start a NEW node 1 instance pointing to the same storage dir
	n1Restarted := New("persist-node", WithStorageDir(storageDir))
	
	// 3. Verify the message was recovered
	inbox := request(t, n1Restarted.Handler(), http.MethodGet, "/messages/bob", "")
	if inbox.Code != http.StatusOK {
		t.Fatalf("inbox response = %d", inbox.Code)
	}
	
	if !bytes.Contains(inbox.Body.Bytes(), []byte(msg.ID)) {
		t.Fatalf("expected recovered message %s not found in inbox: %s", msg.ID, inbox.Body.String())
	}
	
	// Verify message counter recovered properly by creating another message
	send2 := request(t, n1Restarted.Handler(), http.MethodPost, "/message", `{"from":"alice","to":"bob","body":"second message"}`)
	var msg2 Message
	if err := json.Unmarshal(send2.Body.Bytes(), &msg2); err != nil {
		t.Fatal(err)
	}
	if msg2.ID != "persist-node-2" {
		t.Fatalf("expected message counter to resume at 2, got ID: %s", msg2.ID)
	}
	
	if n1Restarted.storageFile != nil {
		n1Restarted.storageFile.Close()
	}
}

type handlerTransport struct{ handler http.Handler }

func (transport handlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	transport.handler.ServeHTTP(recorder, request)
	return recorder.Result(), nil
}
