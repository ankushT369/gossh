package client

import (
	"net"
	"os"
	"testing"
	"time"

	"gossh/internal/log"
)

// handleClientTCP must close the accepted local TCP connection when the
// remote WebSocket dial fails. Otherwise the local ssh client hangs on a
// dead tunnel instead of failing immediately, and the file descriptor leaks.
func TestHandleClientTCP_ClosesTCPOnDialFailure(t *testing.T) {
	local, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()

	clientConn, err := net.Dial("tcp", local.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()
	tcpConn, err := local.Accept()
	if err != nil {
		t.Fatal(err)
	}

	// A WebSocket URL where nothing is listening any more.
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := dead.Addr().String()
	dead.Close()

	gossh := NewGoSSHClient(GoSSHClientConfiguration{}, log.NewLogger(log.ERROR, os.Stderr))
	gossh.handleClientTCP(tcpConn, "ws://"+deadAddr, deadAddr)

	// The local ssh client sits on clientConn: once the client side of the
	// tunnel closes the connection, its read returns immediately.
	if err := clientConn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 16)
	for {
		_, err = clientConn.Read(buf)
		if err == nil {
			continue // ignore anything buffered; no data was ever sent
		}
		break
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("local TCP connection still open after WebSocket dial failure: read timed out")
	}
}
