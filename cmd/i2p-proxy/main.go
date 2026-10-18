// Command i2p-proxy is the single binary for both roles described in the
// project design: an eepsite-hosted proxy server (I2P relay + optional
// clearnet outproxy/exit), and a local client that exposes that remote
// service on plain local ports (fixed port-forward, SOCKS5, HTTP CONNECT).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"i2p-proxy/internal/acl"
	"i2p-proxy/internal/config"
	"i2p-proxy/internal/proxycli"
	"i2p-proxy/internal/proxysrv"
	"i2p-proxy/internal/router"
	"i2p-proxy/internal/router/i2pd"
	"i2p-proxy/internal/router/loopback"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "i2p-proxy:", err)
		os.Exit(1)
	}
}

func run() error {
	cfgPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel(cfg.Logging.Level),
	}))

	rt, err := newRouter(cfg)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Info("starting embedded I2P router", "backend", cfg.Router.Backend)
	if err := rt.Start(ctx); err != nil {
		return fmt.Errorf("router start: %w", err)
	}
	defer rt.Stop(context.Background())

	dest, err := rt.LocalDestination()
	if err != nil {
		return fmt.Errorf("router: local destination: %w", err)
	}
	log.Info("router ready", "destination", redact(string(dest), cfg.Logging.LogDestinations))

	var wg sync.WaitGroup
	errCh := make(chan error, 4)

	if cfg.Mode == config.ModeServer || cfg.Mode == config.ModeBoth {
		srv := buildServer(rt, cfg, log)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := srv.Run(ctx); err != nil {
				errCh <- fmt.Errorf("proxysrv: %w", err)
			}
		}()
	}

	if cfg.Mode == config.ModeClient || cfg.Mode == config.ModeBoth {
		if err := startClient(ctx, rt, cfg, log, &wg, errCh); err != nil {
			return err
		}
	}

	wg.Wait()
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

func newRouter(cfg config.Config) (router.Router, error) {
	switch cfg.Router.Backend {
	case "loopback":
		return loopback.New(), nil
	case "i2pd":
		return i2pd.New(router.Config{
			DataDir:          cfg.Router.DataDir,
			Port:             cfg.Router.Port,
			LogLevel:         cfg.Logging.Level,
			InboundLength:    cfg.Router.Tunnels.InboundLength,
			OutboundLength:   cfg.Router.Tunnels.OutboundLength,
			InboundQuantity:  cfg.Router.Tunnels.InboundQuantity,
			OutboundQuantity: cfg.Router.Tunnels.OutboundQuantity,
		}), nil
	default:
		return nil, fmt.Errorf("unknown router backend %q", cfg.Router.Backend)
	}
}

func buildServer(rt router.Router, cfg config.Config, log *slog.Logger) *proxysrv.Server {
	policy := acl.Policy{
		OutproxyEnabled: cfg.Server.Outproxy.Enabled,
		RelayToI2P:      cfg.Server.RelayToI2P,
		AllowPorts:      cfg.Server.Outproxy.AllowPorts,
		DenyNetworks:    acl.DefaultDenyNetworks(),
	}
	hosted := make(map[int]string, len(cfg.Server.HostedServices))
	for _, hs := range cfg.Server.HostedServices {
		hosted[hs.Port] = hs.Local
	}
	opts := proxysrv.Options{
		Policy:          policy,
		RateLimiter:     acl.NewRateLimiter(cfg.Server.RateLimit.PerSourceConns),
		HostedServices:  hosted,
		LogDestinations: cfg.Logging.LogDestinations,
	}
	if cfg.Server.Outproxy.Enabled {
		log.Warn("outproxy/exit mode is ENABLED - this node will relay clearnet traffic on behalf of clients, exposing this operator's IP as the egress point (same exposure profile as a Tor exit relay/VPN exit node)")
	}
	return proxysrv.New(rt, opts, log)
}

func startClient(ctx context.Context, rt router.Router, cfg config.Config, log *slog.Logger, wg *sync.WaitGroup, errCh chan<- error) error {
	if cfg.Client.UpstreamDest == "" {
		return fmt.Errorf("client mode requires client.upstream_dest")
	}
	upstream := cfg.Client.UpstreamDest
	if upstream == "SELF" {
		// Convenience for local smoke-testing "both" mode against the
		// loopback backend, where the destination is generated at runtime.
		dest, err := rt.LocalDestination()
		if err != nil {
			return fmt.Errorf("resolving SELF upstream: %w", err)
		}
		upstream = string(dest)
	}
	cli := proxycli.New(rt, router.Destination(upstream), log)

	for _, e := range cfg.Client.Expose {
		e := e
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := cli.Expose(ctx, e.Local, e.RemotePort); err != nil {
				errCh <- fmt.Errorf("proxycli expose %s: %w", e.Local, err)
			}
		}()
	}
	if cfg.Client.SOCKS5Listen != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := cli.ListenSOCKS5(ctx, cfg.Client.SOCKS5Listen); err != nil {
				errCh <- fmt.Errorf("proxycli socks5: %w", err)
			}
		}()
	}
	if cfg.Client.HTTPListen != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := cli.ListenHTTP(ctx, cfg.Client.HTTPListen); err != nil {
				errCh <- fmt.Errorf("proxycli http: %w", err)
			}
		}()
	}
	return nil
}

func logLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func redact(s string, log bool) string {
	if log {
		return s
	}
	return "<redacted>"
}
