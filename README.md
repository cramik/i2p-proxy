# i2p-proxy

A single Go binary that embeds a real I2P router in-process (no external
`i2pd`/`i2p` daemon) and runs two roles, either separately or together:

- **server**: publishes an I2P destination (an eepsite-style `.b32.i2p`
  address), accepts inbound I2P streams, and relays them either to other
  `.i2p` destinations or, if the operator opts in, out to clearnet
  (outproxy/exit mode, same exposure profile as a Tor exit relay).
- **client**: runs the same embedded router locally and exposes plain local
  ports that tunnel to a remote server's destination, either a fixed
  port-forward to one of the server's `hosted_services`, or a dynamic
  SOCKS5/HTTP-CONNECT proxy for exit-style anonymized browsing.

## Status

| Platform | Backend | Status |
|---|---|---|
| linux/amd64 | embedded i2pd (cgo) | Verified end-to-end over the real I2P network: reseed, transport sessions, LeaseSet publish, and a successful cross-peer stream dial. |
| windows/amd64 | embedded i2pd (cgo) | Verified end-to-end between two local processes (server exposing a local HTTP service, client tunneling to it). |
| any other platform | stub | Not implemented; returns an error at startup. |

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the embedding design and
known limitations.

## Build

```sh
go build -o i2p-proxy ./cmd/i2p-proxy
```

The embedded i2pd backend requires cgo and the prebuilt static libraries
under `third_party/prebuilt/<os-arch>/`, built from the vendored i2pd
submodule. See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#build-reproduction)
for how those are produced. Without them (or with `CGO_ENABLED=0`), the
binary still builds and runs against the `loopback` backend, useful for
developing and testing the rest of the stack without a real I2P router.

```sh
git clone --recurse-submodules https://github.com/cramik/i2p-proxy.git
```

## Run

```sh
cp config/config.example.yaml config.yaml
# edit config.yaml for your role (server/client/both)
./i2p-proxy -config config.yaml
```

See [config/config.example.yaml](config/config.example.yaml) for all
options, including outproxy/exit mode, hosted services, rate limiting, and
the client's expose/SOCKS5/HTTP-CONNECT modes.

## Security notes

- Outproxy/exit mode is off by default. Enabling it relays clearnet traffic
  on behalf of I2P clients, exposing the operator's IP as the egress point,
  the same exposure profile as running a Tor exit relay or VPN exit node.
  Understand the legal and abuse implications for your jurisdiction first.
- The outproxy path enforces an allow-list of destination ports and a
  deny-list of RFC1918/loopback/link-local networks (SSRF protection) in
  addition to per-source connection rate limiting.
- Destination logging is off by default (`logging.log_destinations: false`).

## License

See [LICENSE](LICENSE). The vendored i2pd submodule under `third_party/i2pd`
carries its own BSD-3-Clause license.
