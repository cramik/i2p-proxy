# Architecture

```
cmd/i2p-proxy/main.go      single binary, mode: server|client|both
internal/router/router.go  backend-agnostic Router interface (Start/Stop/Dial/Listen)
internal/router/loopback/  dev/test backend, no real I2P, used to validate the stack
internal/router/i2pd/      real embedded backend (libi2pd via cgo), see below
internal/protocol/         client<->server wire framing (i2p-dest/domain/ip/hosted)
internal/acl/              outproxy allow-ports + SSRF denylist + per-source rate limit
internal/config/           YAML config load/validate
internal/proxysrv/         server: accepts I2P streams, relays to .i2p or clearnet exit
internal/proxycli/         client: expose (fixed port-forward), SOCKS5, HTTP CONNECT
config/config.example.yaml
third_party/i2pd/          vendored i2pd source (git submodule) + capi2.{h,cpp} additions
third_party/docker/        reproducible Docker builds (C++ lib, Go cgo binary)
third_party/prebuilt/      compiled static libs for linux-amd64 and windows-amd64 (not tracked, see below)
```

## Embedded I2P backend

i2pd (the C++ I2P router, `github.com/PurpleI2P/i2pd`) ships a thin C API
(`libi2pd_wrapper/capi.h`) for embedding it in-process, the same seam i2pd's
own Android app uses to link `libi2pd` directly into the app process instead
of running a separate daemon. That API only covers init/start/stop, so this
project adds `capi2.h`/`capi2.cpp` on top of it, exposing the
destination/stream primitives (`i2p::api::CreateLocalDestination`/
`CreateStream`/`AcceptStream` from `libi2pd/api.h`) as a flat C ABI:

- `I2PP_CreateDestination`/`I2PP_DestroyDestination`: persistent keys, loaded
  from disk or generated on first run
- `I2PP_GetBase32Address`: the destination's public `.b32.i2p` address
- `I2PP_IsDestinationReady`: tunnel-build readiness
- `I2PP_Dial`/`I2PP_Accept`: open/accept streams
- `I2PP_StreamSend`/`I2PP_StreamRecv`/`I2PP_StreamClose`: stream I/O
- `I2PP_StreamRemoteAddress`: base32 address of an accepted stream's peer
- `I2PP_SetCertsDir`, `I2PP_SetLogLevel`, `I2PP_StartUPnP`/`I2PP_StopUPnP`:
  setup i2pd's own daemon `main()` normally handles, which the API-only
  embedding path skips

`internal/router/i2pd/i2pd_linux.go` (`//go:build linux && cgo`) and
`i2pd_windows.go` (`//go:build windows && cgo`) implement `router.Router` by
calling this C API via cgo, linking the prebuilt static libs from
`third_party/prebuilt/<os-arch>/`. `i2pd_stub.go` covers every other
build target and returns an error at startup.

`libi2pd`'s `RouterContext`/`Transports`/`NetDb` are process-wide singletons
(see `libi2pd/api.cpp`): only the first `Router` created in a process
actually initializes the embedded router; later instances share it and only
create their own local destination.

The i2pd submodule also carries one upstream patch: `RunnableClientDestination`
(`libi2pd/Destination.h`) now derives from `RunnableServiceWithWork` instead
of `RunnableService`. Without a permanent work guard, a destination with
sparse, bursty activity (exactly this project's usage: create one
destination, wait for tunnels, publish its LeaseSet) can have its
`io_context` stop permanently between timer chains, since
`RunnableService::Run()`'s loop never calls `io_context::restart()` once
`run()` returns out of work, per boost::asio's documented contract. That
silently breaks LeaseSet publication. `RouterContext`'s own service already
used `RunnableServiceWithWork` for this reason; `RunnableClientDestination`
just wasn't.

## Known limitations

- **Write deadlines are advisory.** Read deadlines are enforced
  (`conn.SetDeadline`/`SetReadDeadline`/`SetWriteDeadline` in the i2pd
  backends), but `proxysrv`/`proxycli` only rely on deadlines around the
  initial protocol handshake today. A full fix threads a deadline into each
  `I2PP_StreamRecv` timeout argument.
- **No graceful full-router shutdown.** `Router.Stop()` only tears down that
  instance's destination (`I2PP_DestroyDestination`); it deliberately does
  not call `C_StopI2P`/`C_TerminateI2P`, since those teardown process-wide
  singletons other `Router` instances in the same process may still be
  using. Full teardown currently happens implicitly on process exit.
- **Reachability depends on a stable external UDP mapping.** SSU2 (i2pd's
  UDP transport) needs a stable external port for inbound reachability and
  reliable LeaseSet-publish confirmation. Operators behind NAT that doesn't
  preserve a stable mapping (some cloud/container network stacks, some
  symmetric-NAT home routers) should set `router.port` to a fixed value and
  forward it explicitly, since i2pd's UPnP support only reaches a real
  Internet Gateway Device on the LAN, not one nested behind another layer of
  virtualized networking (e.g. Docker Desktop's WSL2-backed network on
  Windows).

## Build reproduction

The prebuilt static libraries under `third_party/prebuilt/<os-arch>/` are
not tracked in git (see `.gitignore`); build them from the i2pd submodule:

```sh
# 1. Build libi2pd + wrapper (produces third_party/prebuilt/linux-amd64/*.a)
docker build -f third_party/docker/linux-amd64/Dockerfile -t i2p-proxy-libi2pd-smoketest third_party
cid=$(docker create i2p-proxy-libi2pd-smoketest)
docker cp "$cid:/src/i2pd/libi2pd.a" third_party/prebuilt/linux-amd64/libi2pd.a
docker cp "$cid:/src/i2pd/libi2pdwrapper.a" third_party/prebuilt/linux-amd64/libi2pdwrapper.a
docker rm "$cid"

# 2. Build the Go binary against those libs
docker build -f third_party/docker/linux-amd64/go-build.Dockerfile -t i2p-proxy-go-i2pd-linux .
```

There is no equivalent Docker build for Windows yet; the windows/amd64
backend was built locally against a MinGW toolchain (Boost, OpenSSL, zlib,
miniupnpc) via i2pd's own `Makefile.mingw`.
