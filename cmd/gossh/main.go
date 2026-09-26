package main

import (
	"flag"
	"fmt"
	"gossh/internal/client"
	"gossh/internal/log"
	"gossh/internal/server"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	version = "1.5.0"

	defaultSSHPort  = 22
	defaultHTTPPort = 7777
	defaultTCPPort  = 8888

	serverMode = "server"
	clientMode = "client"

	appDir = ".gossh"
)

type Config struct {
	mode           string
	httpServerPort int
	sshdPort       int
	tcpServerPort  int
	wsURL          string
	verbosity      log.LogLevel
	daemon         bool
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

	fs := flag.NewFlagSet("gossh", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	// We cannot use a normal bool flag for -vvv because the original CLI
	// treats -v/-vv/-vvv as explicit verbosity levels.
	var (
		port    int
		sshPort int
		connect string
		daemon  bool
		quiet   bool
		v       bool
		vv      bool
		vvv     bool
		verbose bool
	)

	fs.IntVar(&port, "port", 0, "port")
	fs.IntVar(&sshPort, "ssh", defaultSSHPort, "SSH port")
	fs.StringVar(&connect, "connect", "", "remote URL")
	fs.BoolVar(&daemon, "daemon", false, "daemonize")
	fs.BoolVar(&quiet, "quiet", false, "quiet")
	fs.BoolVar(&v, "v", false, "info")
	fs.BoolVar(&vv, "vv", false, "debug")
	fs.BoolVar(&vvv, "vvv", false, "verbose")
	fs.BoolVar(&verbose, "verbose", false, "info")

	if err := fs.Parse(args[1:]); err != nil {
		return Config{}, err
	}

	if daemon {
		cfg.daemon = true
	}

	if quiet {
		cfg.verbosity = log.ERROR
	} else if vvv {
		cfg.verbosity = log.TRACE
	} else if vv {
		cfg.verbosity = log.DEBUG
	} else if v || verbose {
		cfg.verbosity = log.INFO
	} else {
		cfg.verbosity = log.INFO
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

func main() {
	var logFile *os.File

	cfg, err := parseArgs(os.Args[1:])
	if err != nil {
		usage()
		fmt.Printf("%v\n", err)
		os.Exit(1)
	}

	homeDir, err := os.UserHomeDir()
	if err == nil {
		root := filepath.Join(homeDir, appDir)
		if err := os.MkdirAll(root, os.ModePerm); err != nil {
			fmt.Print(err)
		} else {
			logFile, err = os.OpenFile(filepath.Join(homeDir, appDir, "log.txt"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				fmt.Print(err)
			}
		}
	}

	// Logger could be replaced OR a custom writer could be added to
	logger := log.NewLogger(cfg.verbosity, &log.LogWriter{File: logFile})
	logger.Trace("Initialized logger")
	if logFile != nil {
		logger.Info("Log file: %s", logFile.Name())
	}

	var runErr error

	switch cfg.mode {
	case serverMode:
		runErr = server.NewGoSSHServer(
			server.GoSSHServerConfiguration{
				Port:    cfg.httpServerPort,
				SSHPort: cfg.sshdPort,
				Timeout: time.Second * 3,
			},
			logger,
		).Run()
	case clientMode:
		runErr = client.NewGoSSHClient(
			client.GoSSHClientConfiguration{
				Port:            cfg.tcpServerPort,
				RawWebsocketURL: cfg.wsURL,
			},
			logger,
		).Run()
	default:
		usage()
		os.Exit(1)
	}

	if runErr != nil {
		logger.Error("%v", runErr)
		os.Exit(1)
	}
}
