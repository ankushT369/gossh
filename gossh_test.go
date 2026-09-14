package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// newWSTestServer serves a WebSocket endpoint that hands every upgraded
// connection to handle.
func newWSTestServer(t *testing.T, handle func(*websocket.Conn)) *httptest.Server {
	t.Helper()

	upgrader := websocket.Upgrader{}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("WebSocket upgrade failed: %v", err)
			return
		}

		handle(ws)
	}))
}

func dialWS(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()

	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+wsPath, nil)
	if err != nil {
		t.Fatalf("cannot dial test WebSocket server: %v", err)
	}

	return ws
}

// newTestSession wires a session to ws and to one end of an in-memory pipe,
// and returns the far end of that pipe as the stand-in for sshd.
func newTestSession(t *testing.T, ws *websocket.Conn) (*Session, net.Conn) {
	t.Helper()

	local, remote := net.Pipe()

	s := NewSession()
	if err := s.setTCP(local); err != nil {
		t.Fatalf("setTCP: %v", err)
	}
	if err := s.setWS(ws); err != nil {
		t.Fatalf("setWS: %v", err)
	}

	return s, remote
}

// A WebSocket peer going away must tear down the TCP side as well, otherwise
// the TCP pump stays parked in a read that nothing will ever satisfy and the
// session leaks both goroutines and the connection to sshd.
func TestRunSessionEndsWhenWebSocketCloses(t *testing.T) {
	srv := newWSTestServer(t, func(ws *websocket.Conn) {
		_ = ws.Close()
	})
	defer srv.Close()

	s, remote := newTestSession(t, dialWS(t, srv))
	defer remote.Close()

	done := make(chan struct{})
	go func() {
		runSession(s, serverMode)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runSession did not return after the WebSocket side closed")
	}

	if !s.closed() {
		t.Fatal("session was not closed")
	}

	if tcp := s.getTCP(); tcp != nil {
		if _, err := tcp.Write([]byte("x")); err == nil {
			t.Fatal("TCP connection was left open after the session ended")
		}
	}
}

// The mirror image: the TCP peer hanging up must end the session too.
func TestRunSessionEndsWhenTCPCloses(t *testing.T) {
	srv := newWSTestServer(t, func(ws *websocket.Conn) {
		defer ws.Close()

		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	})
	defer srv.Close()

	s, remote := newTestSession(t, dialWS(t, srv))

	done := make(chan struct{})
	go func() {
		runSession(s, clientMode)
		close(done)
	}()

	if err := remote.Close(); err != nil {
		t.Fatalf("cannot close the TCP peer: %v", err)
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runSession did not return after the TCP side closed")
	}

	if !s.closed() {
		t.Fatal("session was not closed")
	}
}

func TestRunSessionForwardsBothDirections(t *testing.T) {
	srv := newWSTestServer(t, func(ws *websocket.Conn) {
		defer ws.Close()

		for {
			messageType, data, err := ws.ReadMessage()
			if err != nil {
				return
			}

			if err := ws.WriteMessage(messageType, append([]byte("echo:"), data...)); err != nil {
				return
			}
		}
	})
	defer srv.Close()

	s, remote := newTestSession(t, dialWS(t, srv))
	defer s.close()
	defer remote.Close()

	go runSession(s, clientMode)

	if err := remote.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("cannot set a deadline on the TCP peer: %v", err)
	}

	if _, err := remote.Write([]byte("hello")); err != nil {
		t.Fatalf("cannot write to the TCP peer: %v", err)
	}

	buf := make([]byte, 64)
	n, err := remote.Read(buf)
	if err != nil {
		t.Fatalf("cannot read the echoed data: %v", err)
	}

	if got := string(buf[:n]); got != "echo:hello" {
		t.Fatalf("got %q, want %q", got, "echo:hello")
	}
}

// close() must never be blocked behind an in-flight send, and senders must
// never be blocked behind close(). Run with -race to also cover the field
// accesses.
func TestSessionConcurrentSendsAndClose(t *testing.T) {
	srv := newWSTestServer(t, func(ws *websocket.Conn) {
		defer ws.Close()

		for {
			if _, _, err := ws.ReadMessage(); err != nil {
				return
			}
		}
	})
	defer srv.Close()

	s, remote := newTestSession(t, dialWS(t, srv))
	defer remote.Close()

	go func() {
		_, _ = io.Copy(io.Discard, remote)
	}()

	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(2)

		go func() {
			defer wg.Done()

			for j := 0; j < 50; j++ {
				_ = s.sendWS([]byte("ping"))
			}
		}()

		go func() {
			defer wg.Done()

			for j := 0; j < 50; j++ {
				_ = s.sendTCP([]byte("ping"))
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()

		s.close()
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("senders or close() deadlocked")
	}
}

// A connection that is attached after the session was torn down must be
// closed instead of stored, so a shutdown that races the dial cannot leak it.
func TestSetConnectionAfterCloseIsRejected(t *testing.T) {
	s := NewSession()
	s.close()

	local, remote := net.Pipe()
	defer remote.Close()

	if err := s.setTCP(local); err == nil {
		t.Fatal("setTCP accepted a connection on a closed session")
	}

	if s.getTCP() != nil {
		t.Fatal("closed session stored the TCP connection")
	}

	if _, err := local.Write([]byte("x")); err == nil {
		t.Fatal("the rejected connection was left open")
	}
}

func TestSendOnUnconnectedSessionFails(t *testing.T) {
	s := NewSession()

	if err := s.sendWS([]byte("x")); err == nil {
		t.Fatal("sendWS succeeded without a WebSocket connection")
	}

	if err := s.sendTCP([]byte("x")); err == nil {
		t.Fatal("sendTCP succeeded without a TCP connection")
	}
}
