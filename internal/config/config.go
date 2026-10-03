package config

import (
	"flag"
	"fmt"
	"gossh/internal/log"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"go.yaml.in/yaml/v4"
)

type Config struct {
	Mode           string
	HttpServerPort int
	SSHDPort       int
	TcpServerPort  int
	WSURL          string
	Verbosity      log.LogLevel
	Daemon         bool
}

const (
	Version = "1.5.0"

	DefaultSSHPort  = 22
	DefaultHTTPPort = 7777
	DefaultTCPPort  = 8888

	ServerMode = "server"
	ClientMode = "client"
	StatusMode = "status"
)

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

func applyFileConfig(configFile string, cfg *Config) error {
	type ServerConfig struct {
		HttpServerPort int          `yaml:"port" toml:"port"`
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

func ParseArgs(args []string) (Config, error) {
	if len(args) < 1 {
		return Config{}, fmt.Errorf("mode is required")
	}

	cfg := Config{
		HttpServerPort: DefaultHTTPPort,
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
		sshPort    int
		connect    string
		target     string
		daemon     bool
		quiet      bool
		v          bool
		vv         bool
		vvv        bool
		verbose    bool
		configFile string
	)

	fs.IntVar(&port, "port", 0, "port")
	// Initializing sshPort to 0 helps in checking for CLI option checking later
	fs.IntVar(&sshPort, "ssh", 0, "SSH port")
	fs.StringVar(&connect, "connect", "", "remote URL")
	fs.StringVar(&target, "target", "", "remote URL")
	fs.BoolVar(&daemon, "daemon", false, "daemonize")
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
