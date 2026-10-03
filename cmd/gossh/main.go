package main

import (
	"fmt"
	"gossh/internal/client"
	"gossh/internal/config"
	"gossh/internal/daemon"
	"gossh/internal/log"
	"gossh/internal/server"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const appDir = ".gossh"

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

Config File option:
  --config <filepath>    Path to config file

Examples:
  gossh server --port 7777 --ssh 22
  gossh client --connect https://example.com --port 8888
  gossh server -v --port 7777
  gossh client -vv --connect https://example.com --port 8888

SSH connection:
  ssh user@localhost -p 8888`)
}

func main() {
	var logFile *os.File
	args := os.Args
	cfg, err := config.ParseArgs(args[1:])
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
	logger := log.NewLogger(cfg.Verbosity, &log.LogWriter{File: logFile})
	logger.Trace("Initialized logger")
	if logFile != nil {
		logger.Info("Log file: %s", logFile.Name())
	}

	var runErr error
	switch cfg.Mode {
	case config.ServerMode:
		if cfg.Daemon {
			if os.Getenv("DAEMON_ENV") == "1" {
				runErr = server.NewGoSSHServer(
					server.GoSSHServerConfiguration{
						Port:    cfg.HttpServerPort,
						SSHPort: cfg.SSHDPort,
						Timeout: time.Second * 3,
					},
					logger,
				).Run()
			} else {
				daemon.Daemonize(logger, args, filepath.Join(homeDir, appDir, "server.pid"))
			}
		} else {
			runErr = server.NewGoSSHServer(
				server.GoSSHServerConfiguration{
					Port:    cfg.HttpServerPort,
					SSHPort: cfg.SSHDPort,
					Timeout: time.Second * 3,
				},
				logger,
			).Run()
		}
	case config.ClientMode:
		if cfg.Daemon {
			if os.Getenv("DAEMON_ENV") == "1" {
				runErr = client.NewGoSSHClient(
					client.GoSSHClientConfiguration{
						Port:            cfg.TcpServerPort,
						RawWebsocketURL: cfg.WSURL,
					},
					logger,
				).Run()
			} else {
				daemon.Daemonize(logger, args, filepath.Join(homeDir, appDir, "client.pid"))
			}
		} else {
			runErr = client.NewGoSSHClient(
				client.GoSSHClientConfiguration{
					Port:            cfg.TcpServerPort,
					RawWebsocketURL: cfg.WSURL,
				},
				logger,
			).Run()
		}
	case config.StatusMode:
		url := strings.TrimRight(cfg.WSURL, "/") + "/status"

		resp, err := http.Get(url)
		if err != nil {
			runErr = fmt.Errorf("cannot reach %s: %w", url, err)
			break
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			runErr = fmt.Errorf("server returned %s", resp.Status)
			break
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			runErr = fmt.Errorf("cannot read response: %w", err)
			break
		}

		fmt.Println(string(body))
	default:
		usage()
		os.Exit(1)
	}

	if runErr != nil {
		logger.Error("%v", runErr)
		os.Exit(1)
	}
}
