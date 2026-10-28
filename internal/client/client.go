package client

import (
	"crypto/tls"
	"fmt"
	"gossh/internal/log"
	"gossh/internal/tunnel"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

// TODO: do we want these to be configurable? Then add to config
// Or are they shared invariants between client/server? Move to a shared package for using in both client.go and server.go
const (
	// ReadBufSize is the buffer size used when reading TCP and WebSocket data.
	ReadBufSize = 32 * 1024
	// WriteBufSize is the WebSocket write buffer size.
	WriteBufSize   = 32 * 1024
	sshUnavailable = "SSH_UNAVAILABLE"

	// HandShakeTime is the WebSocket handshake timeout in seconds.
	HandShakeTime = 15
)

// GoSSHClient forwards connections from a local TCP listener to a remote WebSocket.
type GoSSHClient struct {
	conf   GoSSHClientConfiguration
	logger log.Logger
}

// GoSSHClientConfiguration contains the local listener and remote endpoint settings.
type GoSSHClientConfiguration struct {
	Port            int
	RawWebsocketURL string
}

// NewGoSSHClient creates a client with the supplied configuration and logger.
func NewGoSSHClient(config GoSSHClientConfiguration, logger log.Logger) GoSSHClient {
	return GoSSHClient{
		conf:   config,
		logger: logger,
	}
}

// Run listens for local TCP connections and forwards each through a WebSocket session.
func (gossh GoSSHClient) Run() error {
	listenAddr := fmt.Sprintf("localhost:%d", gossh.conf.Port)

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %w", listenAddr, err)
	}
	defer ln.Close()

	wsURL, err := convertToWSS(gossh.conf.RawWebsocketURL)
	if err != nil {
		return err
	}

	gossh.logger.Info("gossh started in CLIENT mode")
	gossh.logger.Info("Remote URL: %s", wsURL)
	gossh.logger.Info("Listening on %s", listenAddr)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-stop
		gossh.logger.Info("Shutting down client...")
		_ = ln.Close()
	}()

	for {
		tcp, err := ln.Accept()
		if err != nil {
			if isClosedNetworkError(err) {
				return nil
			}
			return err
		}

		gossh.logger.Info("Client accepted via TCP successfully")

		go gossh.handleClientTCP(tcp, wsURL, gossh.conf.RawWebsocketURL)
	}
}

// handleClientTCP opens the remote WebSocket for an accepted local TCP connection
// and passes both connections to tunnel.NewSession from gossh/internal/tunnel.
func (gossh GoSSHClient) handleClientTCP(tcp net.Conn, wsURL string, originalURL string) {

	host := tlsHost(originalURL)

	dialer := websocket.Dialer{
		ReadBufferSize:   ReadBufSize,
		WriteBufferSize:  WriteBufSize,
		HandshakeTimeout: HandShakeTime * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: host,
		},
	}

	gossh.logger.Info("Connecting to remote WebSocket: %s", wsURL)

	ws, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		gossh.logger.Error("Failed to create WebSocket connection: %v", err)
		tcp.Close()
		return
	}

	session := tunnel.NewSession(ws, tcp)
	gossh.logger.Info("WebSocket connection successfully established")

	gossh.runClientSession(session)
}

// runClientSession copies data in both directions through the session API provided
// by gossh/internal/tunnel, then closes the session when either side ends.
func (gossh GoSSHClient) runClientSession(s *tunnel.Session) {
	defer s.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	// Local SSH client -> WebSocket
	go func() {
		defer wg.Done()
		defer s.Close()

		buf := make([]byte, ReadBufSize)

		for {
			n, err := s.ReadTCP(buf)

			if n > 0 {
				gossh.logger.Debug("Client read %d bytes", n)

				if err := s.SendWS(buf[:n]); err != nil {
					gossh.logger.Debug("WebSocket write failed: %v", err)
					return
				}

				gossh.logger.Trace("Forwarded %d bytes TCP -> WS", n)
			}

			if err != nil {
				if err != io.EOF {
					gossh.logger.Debug("TCP read ended: %v", err)
				}
				return
			}
		}
	}()

	// WebSocket -> local SSH client
	go func() {
		defer wg.Done()
		defer s.Close()

		for {
			messageType, data, err := s.ReadWS()
			if err != nil {
				gossh.logger.Debug("WebSocket read ended: %v", err)
				return
			}

			if messageType == websocket.TextMessage &&
				string(data) == sshUnavailable {

				gossh.logger.Error("SSH server is unavailable")

				s.Close()
				return
			}

			if messageType != websocket.BinaryMessage && messageType != websocket.TextMessage {
				continue
			}

			gossh.logger.Debug("Data from websocket in CLIENT mode: %d bytes", len(data))

			if err := s.SendTCP(data); err != nil {
				gossh.logger.Debug("TCP write failed: %v", err)
				return
			}

			gossh.logger.Trace("Forwarded %d bytes WS -> TCP", len(data))
		}
	}()

	wg.Wait()
	gossh.logger.Info("Client disconnected")
}

// TODO: these are copied directly from cmd/gossh/main.go
// find better packaging of these helper/until below

// tlsHost extracts the hostname used as the TLS server name for a remote URL.
func tlsHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}

	host := u.Hostname()
	return host
}

// convertToWSS converts an HTTP(S) endpoint to its WebSocket URL and appends /ws.
func convertToWSS(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty URL")
	}

	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}

	host := u.Host
	if host == "" {
		return "", fmt.Errorf("URL has no host")
	}

	// WebSocket URL normalization
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}

	if !strings.HasSuffix(u.Path, "/ws") {
		u.Path = strings.TrimRight(u.Path, "/") + "/ws"
	}

	u.RawQuery = ""
	u.Fragment = ""

	return u.String(), nil
}

// isClosedNetworkError reports whether a listener error means it was closed
// during client shutdown.
func isClosedNetworkError(err error) bool {
	if err == net.ErrClosed {
		return true
	}

	s := strings.ToLower(err.Error())
	return strings.Contains(s, "use of closed network connection")
}
