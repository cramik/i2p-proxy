// Package config loads the YAML configuration described in the project
// design doc: router backend selection, server (eepsite/outproxy) settings,
// and client (expose / socks5 / http) settings.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Mode string

const (
	ModeServer Mode = "server"
	ModeClient Mode = "client"
	ModeBoth   Mode = "both"
)

type Config struct {
	Mode    Mode          `yaml:"mode"`
	Router  RouterConfig  `yaml:"router"`
	Server  ServerConfig  `yaml:"server"`
	Client  ClientConfig  `yaml:"client"`
	Logging LoggingConfig `yaml:"logging"`
}

type RouterConfig struct {
	Backend string `yaml:"backend"` // "i2pd" | "loopback"
	DataDir string `yaml:"data_dir"`
	// Port pins the NTCP2/SSU2 listen port instead of letting the backend
	// pick one automatically. See router.Config.Port doc comment.
	Port    int `yaml:"port"`
	Tunnels struct {
		InboundLength    int `yaml:"inbound_length"`
		OutboundLength   int `yaml:"outbound_length"`
		InboundQuantity  int `yaml:"inbound_quantity"`
		OutboundQuantity int `yaml:"outbound_quantity"`
	} `yaml:"tunnels"`
}

type ServerConfig struct {
	Outproxy struct {
		Enabled      bool     `yaml:"enabled"`
		AllowPorts   []int    `yaml:"allow_ports"`
		DenyNetworks []string `yaml:"deny_networks"`
	} `yaml:"outproxy"`
	RelayToI2P bool `yaml:"relay_to_i2p"`
	// HostedServices maps a virtual port a client's "expose" entry can ask
	// for (protocol.AddrHosted) to a local address the server operator
	// explicitly wants reachable, e.g. their own eepsite web server. This is
	// separate from the outproxy path and its SSRF denylist on purpose.
	HostedServices []HostedService `yaml:"hosted_services"`
	RateLimit      struct {
		PerSourceConns         int `yaml:"per_source_conns"`
		PerSourceBandwidthKbps int `yaml:"per_source_bandwidth_kbps"`
	} `yaml:"rate_limit"`
}

type HostedService struct {
	Port  int    `yaml:"port"`  // virtual port clients request
	Local string `yaml:"local"` // actual address to dial, e.g. "127.0.0.1:8080"
}

type ExposeEntry struct {
	Local      string `yaml:"local"`
	RemotePort int    `yaml:"remote_port"`
}

type ClientConfig struct {
	UpstreamDest string        `yaml:"upstream_dest"`
	Expose       []ExposeEntry `yaml:"expose"`
	SOCKS5Listen string        `yaml:"socks5_listen"`
	HTTPListen   string        `yaml:"http_listen"`
}

type LoggingConfig struct {
	Level           string `yaml:"level"`
	LogDestinations bool   `yaml:"log_destinations"`
}

func Default() Config {
	var c Config
	c.Mode = ModeBoth
	c.Router.Backend = "loopback"
	c.Router.DataDir = "./i2pdata"
	c.Router.Tunnels.InboundLength = 3
	c.Router.Tunnels.OutboundLength = 3
	c.Router.Tunnels.InboundQuantity = 2
	c.Router.Tunnels.OutboundQuantity = 2
	c.Server.Outproxy.Enabled = false
	c.Server.Outproxy.AllowPorts = []int{80, 443}
	c.Server.RelayToI2P = true
	c.Server.RateLimit.PerSourceConns = 20
	c.Server.RateLimit.PerSourceBandwidthKbps = 512
	c.Client.SOCKS5Listen = "127.0.0.1:4444"
	c.Client.HTTPListen = "127.0.0.1:4445"
	c.Logging.Level = "info"
	c.Logging.LogDestinations = false
	return c
}

func Load(path string) (Config, error) {
	c := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return c, fmt.Errorf("config: reading %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("config: parsing %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}

func (c Config) Validate() error {
	switch c.Mode {
	case ModeServer, ModeClient, ModeBoth:
	default:
		return fmt.Errorf("config: invalid mode %q (want server|client|both)", c.Mode)
	}
	if c.Mode == ModeClient || c.Mode == ModeBoth {
		if c.Client.UpstreamDest == "" && len(c.Client.Expose) == 0 &&
			c.Client.SOCKS5Listen == "" && c.Client.HTTPListen == "" {
			return fmt.Errorf("config: client mode requires upstream_dest with at least one of expose/socks5_listen/http_listen")
		}
	}
	return nil
}
