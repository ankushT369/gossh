package config

import (
	"flag"
	"fmt"
	"gossh/internal/log"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"go.yaml.in/yaml/v4"
)

// Config holds settings parsed from the command line, environment, and config file.
type Config struct {
	Mode           string
	HttpServerPort int
	SSHDHost       string
	SSHDPort       int
	TcpServerPort  int
	WSURL          string
	Verbosity      log.LogLevel
	Daemon         bool
	MaxConns       int
	Web            string
}

const (
	// Version is the current gossh release version.
	Version = "1.9.1"

	// Installation URL
	InstallerUnixURL = "https://raw.githubusercontent.com/ankushT369/gossh/main/install.sh"
	InstallerWinURL  = "https://raw.githubusercontent.com/ankushT369/gossh/main/install.ps1"

	// DefaultSSHHost is the SSH daemon host used by server mode.
	DefaultSSHHost = "127.0.0.1"
	// DefaultSSHPort is the SSH daemon port used by server mode.
	DefaultSSHPort = 22
	// DefaultHTTPPort is the HTTP/WebSocket listener port used by server mode.
	DefaultHTTPPort = 7777
	// DefaultTCPPort is the local TCP listener port used by client mode.
	DefaultTCPPort = 8888

	// ServerMode selects server operation.
	ServerMode = "server"
	// ClientMode selects client operation.
	ClientMode = "client"
	// StatusMode selects remote server status reporting.
	StatusMode = "status"
	// UpgradeMode upgrades gossh
	UpgradeMode = "upgrade"
)

// findConfigFile returns the explicit config path or the first existing default
// path, checking the working directory, user config directory, and /etc.
func findConfigFile(configFile string) (string, error) {
	if configFile != "" {
		if _, err := os.Stat(configFile); err != nil {
			return "", fmt.Errorf("config file %q: %w", configFile, err)
		}
		return configFile, nil
	}

	candidates := []string{
		"gossh.yaml",
		"gossh.toml",
	}

	if homeDir, err := os.UserHomeDir(); err == nil {
		candidates = append(
			candidates,
			filepath.Join(homeDir, ".config", "gossh", "config.yaml"),
			filepath.Join(homeDir, ".config", "gossh", "config.toml"),
		)
	}

	candidates = append(
		candidates,
		"/etc/gossh/config.yaml",
		"/etc/gossh/config.toml",
	)
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", nil
}

// applyFileConfig decodes YAML or TOML settings and applies the section matching
// cfg.Mode. Command-line and environment overrides are applied later by ParseArgs.
func applyFileConfig(configFile string, cfg *Config) error {
	type ServerConfig struct {
		HttpServerPort int          `yaml:"port" toml:"port"`
		SSHDHost       string       `yaml:"ssh_host" toml:"ssh_host"`
		SSHDPort       int          `yaml:"ssh" toml:"ssh"`
		Verbosity      log.LogLevel `yaml:"verbosity" toml:"verbosity"`
		Daemon         bool         `yaml:"daemon" toml:"daemon"`
	}
	type ClientConfig struct {
		TcpServerPort int          `yaml:"port" toml:"port"`
		WSURL         string       `yaml:"connect" toml:"connect"`
		Verbosity     log.LogLevel `yaml:"verbosity" toml:"verbosity"`
		Daemon        bool         `yaml:"daemon" toml:"daemon"`
	}
	type StatusConfig struct {
		Target string `yaml:"target" toml:"target"`
	}
	type FileConfig struct {
		Serverconfig ServerConfig `yaml:"server" toml:"server"`
		Clientconfig ClientConfig `yaml:"client" toml:"client"`
		Statusconfig StatusConfig `yaml:"status" toml:"status"`
	}
	fileconfig := FileConfig{}

	ext := strings.ToLower(filepath.Ext(configFile))

	switch ext {
	case ".yaml", ".yml":
		b, err := os.ReadFile(configFile)
		if err != nil {
			return fmt.Errorf("failed to read yaml config: %w", err)
		}
		err = yaml.Unmarshal(b, &fileconfig)
		if err != nil {
			return fmt.Errorf("failed to parse yaml config: %w", err)
		}
	case ".toml":
		_, err := toml.DecodeFile(configFile, &fileconfig)
		if err != nil {
			return fmt.Errorf("failed to parse toml config: %w", err)
		}
	default:
		return fmt.Errorf("unknown config file format")
	}
	switch cfg.Mode {
	case ServerMode:
		if fileconfig.Serverconfig.HttpServerPort != 0 {
			cfg.HttpServerPort = fileconfig.Serverconfig.HttpServerPort
		}
		if fileconfig.Serverconfig.SSHDHost != "" {
			cfg.SSHDHost = fileconfig.Serverconfig.SSHDHost
		}
		if fileconfig.Serverconfig.SSHDPort != 0 {
			cfg.SSHDPort = fileconfig.Serverconfig.SSHDPort
		}

		cfg.Daemon = fileconfig.Serverconfig.Daemon

		if fileconfig.Serverconfig.Verbosity != "" {
			cfg.Verbosity = fileconfig.Serverconfig.Verbosity
		}
	case ClientMode:
		if fileconfig.Clientconfig.WSURL != "" {
			cfg.WSURL = fileconfig.Clientconfig.WSURL
		}
		if fileconfig.Clientconfig.TcpServerPort != 0 {
			cfg.TcpServerPort = fileconfig.Clientconfig.TcpServerPort
		}

		cfg.Daemon = fileconfig.Clientconfig.Daemon

		if fileconfig.Clientconfig.Verbosity != "" {
			cfg.Verbosity = fileconfig.Clientconfig.Verbosity
		}
	case StatusMode:
		if fileconfig.Statusconfig.Target != "" {
			cfg.WSURL = fileconfig.Statusconfig.Target
		}
	default:
		return fmt.Errorf("unknown mode of execution")
	}

	return nil
}

// ParseArgs parses the mode and its flags, then combines defaults, file settings,
// environment variables, and explicit flags in increasing order of precedence.
func ParseArgs(args []string) (Config, error) {
	if len(args) < 1 {
		return Config{}, fmt.Errorf("mode is required")
	}

	cfg := Config{
		HttpServerPort: DefaultHTTPPort,
		SSHDHost:       DefaultSSHHost,
		SSHDPort:       DefaultSSHPort,
		TcpServerPort:  DefaultTCPPort,
	}

	switch args[0] {
	case "version", "--version":
		fmt.Printf("v%s\n", Version)
		os.Exit(0)
	case ServerMode:
		cfg.Mode = ServerMode
	case ClientMode:
		cfg.Mode = ClientMode
	case StatusMode:
		cfg.Mode = StatusMode
	case UpgradeMode:
		cfg.Mode = UpgradeMode
	default:
		return Config{}, fmt.Errorf("unknown mode: %s", args[0])
	}
	// Default Verbosity is set to INFO
	cfg.Verbosity = log.INFO
	fs := flag.NewFlagSet("gossh", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	// We cannot use a normal bool flag for -vvv because the original CLI
	// treats -v/-vv/-vvv as explicit verbosity levels.
	var (
		port       int
		sshHost    string
		sshPort    int
		connect    string
		target     string
		daemon     bool
		web        string
		quiet      bool
		v          bool
		vv         bool
		vvv        bool
		verbose    bool
		configFile string
		maxConns   int
	)

	fs.IntVar(&port, "port", 0, "port")
	// Initializing sshPort to 0 helps in checking for CLI option checking later
	fs.IntVar(&sshPort, "ssh", 0, "SSH port")
	fs.StringVar(&sshHost, "ssh-host", "", "SSH host")
	fs.StringVar(&connect, "connect", "", "remote URL")
	fs.StringVar(&target, "target", "", "remote URL")
	fs.BoolVar(&daemon, "daemon", false, "daemonize")
	fs.StringVar(&web, "web", "", "remote URL")
	fs.IntVar(&maxConns, "max-conns", math.MaxInt, "max-conns")
	fs.BoolVar(&quiet, "quiet", false, "quiet")
	fs.BoolVar(&v, "v", false, "info")
	fs.BoolVar(&vv, "vv", false, "debug")
	fs.BoolVar(&vvv, "vvv", false, "verbose")
	fs.BoolVar(&verbose, "verbose", false, "info")
	fs.StringVar(&configFile, "config", "", "config file path")

	if err := fs.Parse(args[1:]); err != nil {
		return Config{}, err
	}

	configFile, err := findConfigFile(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}

	if configFile != "" {
		if err := applyFileConfig(configFile, &cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
		}
	}

	switch cfg.Mode {
	case ServerMode:
		portStr := os.Getenv("GOSSH_PORT")
		http_port, err := strconv.Atoi(portStr)
		if err == nil {
			cfg.HttpServerPort = http_port
		}
		portStr = os.Getenv("GOSSH_SSH_PORT")
		ssh_port, err := strconv.Atoi(portStr)
		if err == nil {
			cfg.SSHDPort = ssh_port
		}
		hostStr := os.Getenv("GOSSH_SSH_HOST")
		if hostStr != "" {
			cfg.SSHDHost = hostStr
		}
	case ClientMode:
		wsURL := os.Getenv("GOSSH_CONNECT")
		if wsURL != "" {
			cfg.WSURL = wsURL
		}
		portStr := os.Getenv("GOSSH_PORT")
		tcp_port, err := strconv.Atoi(portStr)
		if err == nil {
			cfg.TcpServerPort = tcp_port
		}
	case StatusMode:
		wsURL := os.Getenv("GOSSH_CONNECT")
		if wsURL != "" {
			cfg.WSURL = wsURL
		}
	}

	if daemon {
		cfg.Daemon = true
	}

	if web != "" {
		cfg.Web = strings.TrimRight(web, "/")
	}

	cfg.MaxConns = maxConns

	if quiet {
		cfg.Verbosity = log.ERROR
	} else if vvv {
		cfg.Verbosity = log.TRACE
	} else if vv {
		cfg.Verbosity = log.DEBUG
	} else if v || verbose {
		cfg.Verbosity = log.INFO
	}

	switch cfg.Mode {
	case ServerMode:
		if port != 0 {
			cfg.HttpServerPort = port
		}
		if sshHost != "" {
			cfg.SSHDHost = sshHost
		}
		if sshPort != 0 {
			cfg.SSHDPort = sshPort
		}
	case ClientMode:
		if port != 0 {
			cfg.TcpServerPort = port
		}
		if connect != "" {
			cfg.WSURL = connect
		}
		if cfg.WSURL == "" {
			return Config{}, fmt.Errorf("--connect is required in client mode")
		}
	case StatusMode:
		if port != 0 {
			cfg.TcpServerPort = port
		}
		if target != "" {
			cfg.WSURL = target
		}
		if cfg.WSURL == "" {
			return Config{}, fmt.Errorf("--target is required in status mode")
		}
	}
	return cfg, nil
}
