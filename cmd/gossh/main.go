package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"gossh/internal/client"
	"gossh/internal/config"
	"gossh/internal/daemon"
	"gossh/internal/log"
	"gossh/internal/server"
	"gossh/internal/versioncheck"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const appDir = ".gossh"

func checkForUpdates() {
	latest, releaseURL, err := versioncheck.Check(config.Version)
	if err != nil {
		// Update checking is optional.
		return
	}

	if latest != "" {
		fmt.Printf("A new gossh version is available: %s -> %s\n",
			config.Version, latest)
		fmt.Println("Upgrade with: `gossh upgrade`")
		fmt.Println("Or run:")
		fmt.Println("   curl -fsSL https://raw.githubusercontent.com/ankushT369/gossh/main/install.sh | bash")
		fmt.Printf("   Release notes: %s\n\n", releaseURL)
	}
}

// usage prints the command-line syntax and examples when argument parsing fails
// or an unsupported mode is selected.
func usage() {
	fmt.Println(`Usage: gossh <mode> [OPTIONS]

Version:
  version		Displays the version

Modes:
  server                Run in server mode
  client                Run in client mode
  status                Show the status of a daemonized gossh process
  upgrade               Upgrades gossh

Verbosity control:
  -q, --quiet           Suppress info/debug output
  -v                    Info level logging
  -vv                   Debug level logging
  -vvv                  Verbose logging
  --verbose             Same as -v

Server mode options:
  --port <port>         HTTP/WebSocket server port (default: 7777)
  --ssh <port>          SSH daemon port (default: 22)
  --daemon              Run the server in the background
  --max-conns           Maximum connections server can hold
  --web <origin>        Enable browser terminal for this HTTPS origin

Client mode options:
  --connect <url>       Remote WebSocket/HTTP URL (required)
  --port <port>         Local port to expose (default: 8888)
  --daemon              Run the client in the background

Status mode options:
  --target <url>        Remote WebSocket/HTTP URL (required)

Config File option:
  --config <filepath>   Path to config file

Examples:
  gossh server --port 7777 --ssh 22
  gossh client --connect https://example.com --port 8888
  gossh server --daemon
  gossh status
  gossh server -v --port 7777
  gossh client -vv --connect https://example.com --port 8888

  # Connect via browser
  gossh server --port 7777 --web https://example.com

  # Upgrade gossh
  gossh upgrade

SSH connection:
  ssh user@localhost -p 8888`)
}

// main parses CLI settings, initializes logging, and starts the selected mode.
func main() {
	var logFile *os.File
	args := os.Args
	cfg, err := config.ParseArgs(args[1:])
	if err != nil {
		usage()
		fmt.Printf("%v\n", err)
		os.Exit(1)
	}

	if cfg.Mode != config.UpgradeMode {
		checkForUpdates()
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
	if cfg.Mode != config.UpgradeMode && cfg.Mode != config.StatusMode {
		logger.Info("v%s", config.Version)
		logger.Trace("Initialized logger")
		if logFile != nil {
			logger.Info("Log file: %s", logFile.Name())
		}
	}

	var runErr error
	switch cfg.Mode {
	case config.ServerMode:
		if cfg.Daemon {
			if os.Getenv("DAEMON_ENV") == "1" {
				pidFile := filepath.Join(homeDir, appDir,
					fmt.Sprintf("server-%d.pid", cfg.HttpServerPort))
				defer os.Remove(pidFile)

				runErr = server.NewGoSSHServer(
					server.GoSSHServerConfiguration{
						Port:    cfg.HttpServerPort,
						SSHPort: cfg.SSHDPort,
						Timeout: time.Second * 3,

						MaxConns: cfg.MaxConns,
						Web:      cfg.Web,
					},
					logger,
				).Run()
			} else {
				pidFile := filepath.Join(homeDir, appDir,
					fmt.Sprintf("server-%d.pid", cfg.HttpServerPort))

				daemon.Daemonize(logger, args, pidFile)
			}
		} else {
			runErr = server.NewGoSSHServer(
				server.GoSSHServerConfiguration{
					Port:    cfg.HttpServerPort,
					SSHPort: cfg.SSHDPort,
					Timeout: time.Second * 3,

					MaxConns: cfg.MaxConns,
					Web:      cfg.Web,
				},
				logger,
			).Run()
		}
	case config.ClientMode:
		if cfg.Daemon {
			if os.Getenv("DAEMON_ENV") == "1" {
				pidFile := filepath.Join(homeDir, appDir,
					fmt.Sprintf("server-%d.pid", cfg.HttpServerPort))
				defer os.Remove(pidFile)

				runErr = client.NewGoSSHClient(
					client.GoSSHClientConfiguration{
						Port:            cfg.TcpServerPort,
						RawWebsocketURL: cfg.WSURL,
					},
					logger,
				).Run()
			} else {
				pidFile := filepath.Join(homeDir, appDir,
					fmt.Sprintf("client-%d.pid", cfg.TcpServerPort))

				daemon.Daemonize(logger, args, pidFile)
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

		// Get the time
		start := time.Now()

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
		// measure the duration
		duration := time.Since(start)

		jsonBytes := []byte(body)

		var data map[string]any
		if err := json.Unmarshal(jsonBytes, &data); err != nil {
			runErr = fmt.Errorf("cannot parse status JSON: %w", err)
			break
		}

		// Add round-trip latency in milliseconds
		data["rtt_ms"] = math.Round(float64(duration)/float64(time.Millisecond)*100) / 100

		updatedBytes, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			runErr = fmt.Errorf("cannot encode status JSON: %w", err)
			break
		}

		fmt.Println(string(updatedBytes))
	case config.UpgradeMode:
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Get(config.InstallerURL)
		if err != nil {
			runErr = fmt.Errorf("cannot download installer: %w", err)
			break
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			runErr = fmt.Errorf("installer download failed: %s", resp.Status)
			break
		}

		script, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			runErr = fmt.Errorf("cannot read installer: %w", err)
			break
		}

		cmd := exec.Command("bash")
		cmd.Stdin = bytes.NewReader(script)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		fmt.Println("Installing latest gossh release...")
		if err := cmd.Run(); err != nil {
			runErr = fmt.Errorf("upgrade failed: %w", err)
			break
		}

		fmt.Println("gossh upgrade completed.")

	default:
		usage()
		os.Exit(1)
	}

	if runErr != nil {
		logger.Error("%v", runErr)
		os.Exit(1)
	}
}
