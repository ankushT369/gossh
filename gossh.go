package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

const (
	version = "1.1.0"

	defaultSSHPort  = 22
	defaultHTTPPort = 7777
	defaultTCPPort  = 8888

	serverMode = "server"
	clientMode = "client"

	wsPath = "/ws"
)

var (
	verbosity int
	logger    = log.New(os.Stderr, "", log.LstdFlags)
)

type Config struct {
	mode           string
	httpServerPort int
	sshdPort       int
	tcpServerPort  int
	wsURL          string
}

type Session struct {
	tcp net.Conn
	ws  *websocket.Conn

	mu   sync.Mutex
	once sync.Once
	done chan struct{}
}

func NewSession() *Session {
	return &Session{done: make(chan struct{})}
}

func (s *Session) close() {
	s.once.Do(func() {
		close(s.done)

		s.mu.Lock()
		defer s.mu.Unlock()

		if s.tcp != nil {
			_ = s.tcp.Close()
		}
		if s.ws != nil {
			_ = s.ws.Close()
		}
	})
}

func (s *Session) setTCP(c net.Conn) {
	s.mu.Lock()
	s.tcp = c
	s.mu.Unlock()
}

func (s *Session) setWS(c *websocket.Conn) {
	s.mu.Lock()
	s.ws = c
	s.mu.Unlock()
}

func (s *Session) sendWS(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ws == nil {
		return fmt.Errorf("websocket is not connected")
	}

	return s.ws.WriteMessage(websocket.BinaryMessage, data)
}

func (s *Session) sendTCP(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.tcp == nil {
		return fmt.Errorf("tcp connection is not connected")
	}

	_, err := s.tcp.Write(data)
	return err
}

func debugf(format string, args ...any) {
	if verbosity >= 2 {
		logger.Printf("[DEBUG] "+format, args...)
	}
}

func verbosef(format string, args ...any) {
	if verbosity >= 3 {
		logger.Printf("[VERBOSE] "+format, args...)
	}
}

func infof(format string, args ...any) {
	if verbosity >= 1 {
		logger.Printf("[INFO] "+format, args...)
	}
}

func errorf(format string, args ...any) {
	logger.Printf("[ERROR] "+format, args...)
}

func usage() {
	fmt.Println(`Usage: gossh <mode> [OPTIONS]

Version:
  version		Displays the version

Modes:
  server                Run in server mode
  client                Run in client mode

Verbosity control:
  -q, --quiet           Suppress info/debug output
  -v                    Info level logging
  -vv                   Debug level logging
  -vvv                  Verbose logging
  --verbose             Same as -v

Server mode options:
  --port <port>         HTTP/WebSocket server port (default: 7777)
  --ssh <port>          SSH daemon port (default: 22)

Client mode options:
  --connect <url>       Remote WebSocket/HTTP URL (required)
  --port <port>         Local port to expose (default: 8888)

Examples:
  gossh server --port 7777 --ssh 22
  gossh client --connect https://example.com --port 8888
  gossh server -v --port 7777
  gossh client -vv --connect https://example.com --port 8888

SSH connection:
  ssh user@localhost -p 8888`)
}

func parseArgs(args []string) (Config, error) {
	if len(args) < 1 {
		return Config{}, fmt.Errorf("mode is required")
	}

	cfg := Config{
		httpServerPort: defaultHTTPPort,
		sshdPort:       defaultSSHPort,
		tcpServerPort:  defaultTCPPort,
	}

	switch args[0] {
	case "version", "--version":
		fmt.Printf("v%s\n", version)
		os.Exit(0)
	case serverMode:
		cfg.mode = serverMode
	case clientMode:
		cfg.mode = clientMode
	default:
		return Config{}, fmt.Errorf("unknown mode: %s", args[0])
	}

	// Keep the CLI close to the C version.
	fs := flag.NewFlagSet("gossh", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	// We cannot use a normal bool flag for -vvv because the original CLI
	// treats -v/-vv/-vvv as explicit verbosity levels.
	var (
		port    int
		sshPort int
		connect string
		quiet   bool
		v       bool
		vv      bool
		vvv     bool
		verbose bool
	)

	fs.IntVar(&port, "port", 0, "port")
	fs.IntVar(&sshPort, "ssh", defaultSSHPort, "SSH port")
	fs.StringVar(&connect, "connect", "", "remote URL")
	fs.BoolVar(&quiet, "quiet", false, "quiet")
	fs.BoolVar(&v, "v", false, "info")
	fs.BoolVar(&vv, "vv", false, "debug")
	fs.BoolVar(&vvv, "vvv", false, "verbose")
	fs.BoolVar(&verbose, "verbose", false, "info")

	if err := fs.Parse(args[1:]); err != nil {
		return Config{}, err
	}

	if quiet {
		verbosity = 0
	} else if vvv {
		verbosity = 3
	} else if vv {
		verbosity = 2
	} else if v || verbose {
		verbosity = 1
	} else {
		verbosity = 1
	}

	if cfg.mode == serverMode {
		if port != 0 {
			cfg.httpServerPort = port
		}
		cfg.sshdPort = sshPort
	} else {
		if port != 0 {
			cfg.tcpServerPort = port
		}
		cfg.wsURL = connect
		if cfg.wsURL == "" {
			return Config{}, fmt.Errorf("--connect is required in client mode")
		}
	}

	return cfg, nil
}

// The C implementation always turns the remote URL into wss://.../ws.
// This function preserves that behavior.
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

	if !strings.HasSuffix(u.Path, wsPath) {
		u.Path = strings.TrimRight(u.Path, "/") + wsPath
	}

	u.RawQuery = ""
	u.Fragment = ""

	return u.String(), nil
}

func createLocalURL(network string, port int) string {
	return fmt.Sprintf("%s://localhost:%d", network, port)
}

func tlsHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}

	host := u.Hostname()
	return host
}

func runServer(cfg Config) error {
	addr := fmt.Sprintf(":%d", cfg.httpServerPort)

	mux := http.NewServeMux()
	mux.HandleFunc(wsPath, func(w http.ResponseWriter, r *http.Request) {
		handleServerWebSocket(w, r, cfg)
	})

	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	infof("gossh started in SERVER mode")
	infof("HTTP/WebSocket port: %d", cfg.httpServerPort)
	infof("SSH port: %d", cfg.sshdPort)
	infof("Listening on %s", createLocalURL("http", cfg.httpServerPort))

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-stop
		infof("Shutting down server...")
		ctx, cancel := shutdownContext()
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	err := server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func shutdownContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

func handleServerWebSocket(w http.ResponseWriter, r *http.Request, cfg Config) {
	if r.URL.Path != wsPath {
		http.NotFound(w, r)
		return
	}

	upgrader := websocket.Upgrader{
		ReadBufferSize:  32 * 1024,
		WriteBufferSize: 32 * 1024,
		CheckOrigin: func(r *http.Request) bool {
			// The original Mongoose handler does not enforce an Origin.
			return true
		},
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		errorf("WebSocket upgrade failed: %v", err)
		return
	}

	session := NewSession()
	session.setWS(ws)

	infof("WebSocket connection established")

	sshAddr := fmt.Sprintf("localhost:%d", cfg.sshdPort)
	tcp, err := net.Dial("tcp", sshAddr)
	if err != nil {
		errorf("Cannot connect to sshd on %s: %v", sshAddr, err)
		session.close()
		return
	}

	session.setTCP(tcp)
	infof("Connected to sshd successfully")

	runServerSession(session)
}

func runServerSession(s *Session) {
	defer s.close()

	var wg sync.WaitGroup
	wg.Add(2)

	// WebSocket -> SSH
	go func() {
		defer wg.Done()

		for {
			messageType, data, err := s.ws.ReadMessage()
			if err != nil {
				debugf("WebSocket read ended: %v", err)
				return
			}

			if messageType != websocket.BinaryMessage && messageType != websocket.TextMessage {
				continue
			}

			debugf("Data from websocket in SERVER mode: %d bytes", len(data))

			if err := s.sendTCP(data); err != nil {
				debugf("TCP write failed: %v", err)
				return
			}

			verbosef("Forwarded %d bytes WS -> TCP", len(data))
		}
	}()

	// SSH -> WebSocket
	go func() {
		defer wg.Done()

		buf := make([]byte, 32*1024)

		for {
			n, err := s.tcp.Read(buf)
			if n > 0 {
				debugf("SSH read %d bytes", n)

				if err := s.sendWS(buf[:n]); err != nil {
					debugf("WebSocket write failed: %v", err)
					return
				}

				verbosef("Forwarded %d bytes TCP -> WS", n)
			}

			if err != nil {
				if err != io.EOF {
					debugf("TCP read ended: %v", err)
				}
				return
			}
		}
	}()

	wg.Wait()
	infof("Client disconnected")
}

func runClient(cfg Config) error {
	listenAddr := fmt.Sprintf("localhost:%d", cfg.tcpServerPort)

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %w", listenAddr, err)
	}
	defer ln.Close()

	wsURL, err := convertToWSS(cfg.wsURL)
	if err != nil {
		return err
	}

	infof("gossh started in CLIENT mode")
	infof("Local port: %d", cfg.tcpServerPort)
	infof("Remote URL: %s", wsURL)
	infof("Listening on %s", listenAddr)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-stop
		infof("Shutting down client...")
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

		infof("Client accepted via TCP successfully")

		go handleClientTCP(tcp, wsURL, cfg.wsURL)
	}
}

func handleClientTCP(tcp net.Conn, wsURL string, originalURL string) {
	session := NewSession()
	session.setTCP(tcp)
	defer session.close()

	host := tlsHost(originalURL)

	dialer := websocket.Dialer{
		ReadBufferSize:   32 * 1024,
		WriteBufferSize:  32 * 1024,
		HandshakeTimeout: 15 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: host,
		},
	}

	infof("Connecting to remote WebSocket: %s", wsURL)

	ws, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		errorf("Failed to create WebSocket connection: %v", err)
		return
	}

	session.setWS(ws)
	infof("WebSocket connection successfully established")

	runClientSession(session)
}

func runClientSession(s *Session) {
	defer s.close()

	var wg sync.WaitGroup
	wg.Add(2)

	// Local SSH client -> WebSocket
	go func() {
		defer wg.Done()

		buf := make([]byte, 32*1024)

		for {
			n, err := s.tcp.Read(buf)

			if n > 0 {
				debugf("Client read %d bytes", n)

				if err := s.sendWS(buf[:n]); err != nil {
					debugf("WebSocket write failed: %v", err)
					return
				}

				verbosef("Forwarded %d bytes TCP -> WS", n)
			}

			if err != nil {
				if err != io.EOF {
					debugf("TCP read ended: %v", err)
				}
				return
			}
		}
	}()

	// WebSocket -> local SSH client
	go func() {
		defer wg.Done()

		for {
			messageType, data, err := s.ws.ReadMessage()
			if err != nil {
				debugf("WebSocket read ended: %v", err)
				return
			}

			if messageType != websocket.BinaryMessage && messageType != websocket.TextMessage {
				continue
			}

			debugf("Data from websocket in CLIENT mode: %d bytes", len(data))

			if err := s.sendTCP(data); err != nil {
				debugf("TCP write failed: %v", err)
				return
			}

			verbosef("Forwarded %d bytes WS -> TCP", len(data))
		}
	}()

	wg.Wait()
	infof("Client disconnected")
}

func isClosedNetworkError(err error) bool {
	if err == net.ErrClosed {
		return true
	}

	s := strings.ToLower(err.Error())
	return strings.Contains(s, "use of closed network connection")
}

func main() {
	cfg, err := parseArgs(os.Args[1:])
	if err != nil {
		usage()
		errorf("%v", err)
		os.Exit(1)
	}

	var runErr error

	switch cfg.mode {
	case serverMode:
		runErr = runServer(cfg)
	case clientMode:
		runErr = runClient(cfg)
	default:
		usage()
		os.Exit(1)
	}

	if runErr != nil {
		errorf("%v", runErr)
		os.Exit(1)
	}
}
