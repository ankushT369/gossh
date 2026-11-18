package server

import (
	"context"
	"encoding/json"
	"fmt"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	"gossh/internal/log"
	"gossh/internal/tunnel"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
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
)

// GoSSHServer serves WebSocket tunnels to the local SSH daemon and reports server status.
type GoSSHServer struct {
	conf   GoSSHServerConfiguration
	logger log.Logger

	startedAt      time.Time
	activeSessions int32

	bytesIn  int64
	bytesOut int64
}

// GoSSHServerConfiguration contains listener, SSH, timeout, and connection-limit settings.
type GoSSHServerConfiguration struct {
	Port     int
	SSHHost  string
	SSHPort  int
	Timeout  time.Duration
	MaxConns int
	Web      string
}

// NewGoSSHServer creates a server with the supplied configuration and logger.
func NewGoSSHServer(config GoSSHServerConfiguration, logger log.Logger) *GoSSHServer {
	return &GoSSHServer{
		conf:      config,
		logger:    logger,
		startedAt: time.Now(),
	}
}

// Run registers the WebSocket and status endpoints, then serves until shutdown.
func (gossh *GoSSHServer) Run() error {
	gossh.startedAt = time.Now()
	addr := fmt.Sprintf(":%d", gossh.conf.Port)

	mux := http.NewServeMux()

	mux.HandleFunc("/term", func(w http.ResponseWriter, r *http.Request) {
		if gossh.conf.Web == "" {
			http.NotFound(w, r)
			return
		}

		if r.URL.Path != "/term" {
			http.NotFound(w, r)
			return
		}

		terminalPageHandler(w, r)
	})

	// '/webterm-ws' is the general endpoint for ssh connections via browser
	mux.HandleFunc("/webterm-ws", func(w http.ResponseWriter, r *http.Request) {
		if gossh.conf.Web == "" {
			http.NotFound(w, r)
			return
		}

		gossh.handleTerminalWebSocket(w, r)
	})

	// '/ws' is the general endpoint for ssh connections
	mux.HandleFunc("/ws", gossh.handleWSSession)

	// '/status' endpoint is for server status
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		gossh.handleServerStatus(w, r)
	})

	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	gossh.logger.Info("gossh started in SERVER mode")
	gossh.logger.Info("HTTP/WebSocket port: %d", gossh.conf.Port)

	if gossh.conf.MaxConns != math.MaxInt {
		gossh.logger.Info("Max connections: %d", gossh.conf.MaxConns)
	}
	gossh.logger.Info("SSH port: %d", gossh.conf.SSHPort)
	gossh.logger.Info("Checking SSH connection")

	sshUrl := fmt.Sprintf("%s:%d", gossh.conf.SSHHost, gossh.conf.SSHPort)
	conn, err := net.DialTimeout("tcp", sshUrl, gossh.conf.Timeout)

	if err != nil {
		gossh.logger.Warn("SSH server is down or unreachable: %v", err)
	} else {
		gossh.logger.Info("SSH server port is open and reachable")
		conn.Close()
	}

	gossh.logger.Info("Listening on http://localhost:%d", gossh.conf.Port)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-stop
		gossh.logger.Info("Shutting down server...")
		ctx, cancel := shutdownContext()
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	err = server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// handleWSSession admits a /ws connection within the configured limit and
// releases its slot when the request ends, including early failures such as a
// refused upgrade or an unreachable sshd.
func (gossh *GoSSHServer) handleWSSession(w http.ResponseWriter, r *http.Request) {
	newSessionCounter := atomic.AddInt32(&gossh.activeSessions, 1)

	if int64(newSessionCounter) > int64(gossh.conf.MaxConns) {
		atomic.AddInt32(&gossh.activeSessions, -1)

		http.Error(w, "maximum connections reached", http.StatusServiceUnavailable)
		return
	}

	defer atomic.AddInt32(&gossh.activeSessions, -1)

	gossh.handleServerWebSocket(w, r)
}

// handleServerWebSocket upgrades /ws requests and connects each WebSocket to the
// configured local SSH daemon before starting a tunnel session.
func (gossh *GoSSHServer) handleServerWebSocket(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/ws" {
		http.NotFound(w, r)
		return
	}

	upgrader := websocket.Upgrader{
		ReadBufferSize:  ReadBufSize,
		WriteBufferSize: WriteBufSize,
		CheckOrigin: func(r *http.Request) bool {
			// Reject browser-originated requests for the non-web SSH endpoint
			// by allowing only connections without an Origin header.
			return r.Header.Get("Origin") == ""
		},
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		gossh.logger.Error("WebSocket upgrade failed: %v", err)
		return
	}
	gossh.logger.Info("WebSocket connection established")

	sshAddr := fmt.Sprintf("localhost:%d", gossh.conf.SSHPort)
	tcp, err := net.Dial("tcp", sshAddr)
	if err != nil {
		gossh.logger.Warn("Cannot connect to sshd on %s: %v", sshAddr, err)
		_ = ws.WriteMessage(websocket.TextMessage, []byte(sshUnavailable))
		ws.Close()
		return
	}
	gossh.logger.Info("Connected to sshd successfully")

	session := tunnel.NewSession(ws, tcp)
	gossh.runServerSession(session)
}

// handleTerminalWebSocket upgrades /webterm-ws requests and connects each WebSocket
// to the configured local SSH daemon to establish an interactive terminal session.
func (gossh *GoSSHServer) handleTerminalWebSocket(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  ReadBufSize,
		WriteBufferSize: WriteBufSize,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return false
			}

			u, err := url.Parse(origin)
			if err != nil {
				return false
			}

			configured, err := url.Parse(gossh.conf.Web)
			if err != nil || configured.Scheme != "https" ||
				configured.Host == "" || configured.User != nil {
				return false
			}

			return u.Scheme == configured.Scheme &&
				u.Host == configured.Host &&
				u.User == nil &&
				u.Path == "" &&
				u.RawQuery == "" &&
				u.Fragment == ""
		},
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		gossh.logger.Warn("Terminal WebSocket upgrade failed: %v", err)
		return
	}
	defer ws.Close()

	// The first message must contain the login credentials.
	var auth struct {
		Type     string `json:"type"`
		Username string `json:"username"`
		Password string `json:"password"`
		Cols     int    `json:"cols"`
		Rows     int    `json:"rows"`
	}

	if err := ws.ReadJSON(&auth); err != nil ||
		auth.Type != "auth" ||
		auth.Username == "" ||
		auth.Password == "" {
		_ = ws.WriteJSON(map[string]string{
			"type": "error",
			"data": "Invalid login message",
		})
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		_ = ws.WriteJSON(map[string]string{
			"type": "error", "data": "Cannot locate SSH known_hosts",
		})
		return
	}

	hostKeyCallback, err := knownhosts.New(
		filepath.Join(home, ".ssh", "known_hosts"),
	)
	if err != nil {
		_ = ws.WriteJSON(map[string]string{
			"type": "error",
			"data": "SSH known_hosts file is missing or unreadable",
		})
		return
	}

	sshConfig := &ssh.ClientConfig{
		User: auth.Username,
		Auth: []ssh.AuthMethod{
			ssh.Password(auth.Password),
		},
		HostKeyCallback: hostKeyCallback,
		Timeout:         5 * time.Second,
	}

	addr := net.JoinHostPort("localhost", strconv.Itoa(gossh.conf.SSHPort))
	tcpConn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		_ = ws.WriteJSON(map[string]string{
			"type": "error", "data": "Cannot connect to SSH server",
		})
		return
	}
	defer tcpConn.Close()

	// Use localhost:port for known_hosts verification.
	sshConn, chans, reqs, err := ssh.NewClientConn(tcpConn, addr, sshConfig)
	if err != nil {
		gossh.logger.Warn("Browser SSH authentication failed: %v", err)
		_ = ws.WriteJSON(map[string]string{
			"type": "error",
			"data": "SSH authentication failed or host key verification failed",
		})
		return
	}

	client := ssh.NewClient(sshConn, chans, reqs)
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		_ = ws.WriteJSON(map[string]string{
			"type": "error", "data": "Cannot create SSH session",
		})
		return
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return
	}

	cols, rows := auth.Cols, auth.Rows
	if cols < 1 || cols > 500 {
		cols = 80
	}
	if rows < 1 || rows > 300 {
		rows = 24
	}

	// Right now its bare-min UI configuration
	if err := session.RequestPty(
		"xterm-256color",
		rows,
		cols,
		ssh.TerminalModes{
			ssh.ECHO:          1,
			ssh.TTY_OP_ISPEED: 14400,
			ssh.TTY_OP_OSPEED: 14400,
		},
	); err != nil {
		_ = ws.WriteJSON(map[string]string{
			"type": "error", "data": "Cannot allocate SSH terminal",
		})
		return
	}

	if err := session.Shell(); err != nil {
		_ = ws.WriteJSON(map[string]string{
			"type": "error", "data": "Cannot start SSH shell",
		})
		return
	}

	var writeMu sync.Mutex
	writeJSON := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return ws.WriteJSON(v)
	}

	_ = writeJSON(map[string]string{
		"type": "status", "data": "Connected",
	})

	output := func(reader io.Reader) {
		buf := make([]byte, ReadBufSize)
		for {
			n, readErr := reader.Read(buf)
			if n > 0 {
				if err := writeJSON(map[string]string{
					"type": "output",
					"data": string(buf[:n]),
				}); err != nil {
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}

	var outputWG sync.WaitGroup
	outputWG.Add(2)
	go func() {
		defer outputWG.Done()
		output(stdout)
	}()
	go func() {
		defer outputWG.Done()
		output(stderr)
	}()

	// Read terminal input and resize events from the browser.
	for {
		var msg struct {
			Type string `json:"type"`
			Data string `json:"data"`
			Cols int    `json:"cols"`
			Rows int    `json:"rows"`
		}

		if err := ws.ReadJSON(&msg); err != nil {
			break
		}

		switch msg.Type {
		case "input":
			if _, err := io.WriteString(stdin, msg.Data); err != nil {
				return
			}
		case "resize":
			if msg.Cols >= 1 && msg.Cols <= 500 &&
				msg.Rows >= 1 && msg.Rows <= 300 {
				_ = session.WindowChange(msg.Rows, msg.Cols)
			}
		}
	}

	_ = session.Close()
	_ = client.Close()
	outputWG.Wait()
}

// handleServerStatus writes uptime, active session count, and byte totals as JSON.
func (gossh *GoSSHServer) handleServerStatus(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/status" {
		http.NotFound(w, r)
		return
	}

	resp := struct {
		Uptime         string `json:"uptime"`
		ActiveSessions int32  `json:"active_sessions"`
		In             int64  `json:"bytes_in"`
		Out            int64  `json:"bytes_out"`
	}{
		Uptime:         time.Since(gossh.startedAt).Round(time.Second).String(),
		ActiveSessions: atomic.LoadInt32(&gossh.activeSessions),
		In:             atomic.LoadInt64(&gossh.bytesIn),
		Out:            atomic.LoadInt64(&gossh.bytesOut),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// runServerSession copies data between the SSH connection and WebSocket using
// the session API from gossh/internal/tunnel, then updates session and byte counts.
func (gossh *GoSSHServer) runServerSession(s *tunnel.Session) {
	defer s.Close()

	var wg sync.WaitGroup
	wg.Add(2)

	// WebSocket -> SSH
	go func() {
		defer wg.Done()
		defer s.Close()

		for {
			messageType, data, err := s.ReadWS()
			if err != nil {
				gossh.logger.Debug("WebSocket read ended: %v", err)
				return
			}

			if messageType != websocket.BinaryMessage && messageType != websocket.TextMessage {
				continue
			}

			gossh.logger.Debug("Data from websocket in SERVER mode: %d bytes", len(data))

			if err := s.SendTCP(data); err != nil {
				gossh.logger.Debug("TCP write failed: %v", err)
				return
			}

			gossh.logger.Trace("Forwarded %d bytes WS -> TCP", len(data))
			atomic.AddInt64(&gossh.bytesIn, int64(len(data)))
		}
	}()

	// SSH -> WebSocket
	go func() {
		defer wg.Done()
		defer s.Close()

		buf := make([]byte, ReadBufSize)

		for {
			n, err := s.ReadTCP(buf)
			if n > 0 {
				gossh.logger.Debug("SSH read %d bytes", n)

				if err := s.SendWS(buf[:n]); err != nil {
					gossh.logger.Debug("WebSocket write failed: %v", err)
					return
				}

				gossh.logger.Trace("Forwarded %d bytes TCP -> WS", n)
				atomic.AddInt64(&gossh.bytesOut, int64(n))
			}

			if err != nil {
				if err != io.EOF {
					gossh.logger.Debug("TCP read ended: %v", err)
				}
				return
			}
		}
	}()

	wg.Wait()
	gossh.logger.Info("Client disconnected")
}

// shutdownContext returns a context that bounds graceful HTTP shutdown to five seconds.
func shutdownContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}
