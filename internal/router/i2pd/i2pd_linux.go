//go:build linux && cgo

package i2pd

/*
#cgo CXXFLAGS: -std=c++20
#cgo LDFLAGS: ${SRCDIR}/../../../third_party/prebuilt/linux-amd64/libi2pdwrapper.a ${SRCDIR}/../../../third_party/prebuilt/linux-amd64/libi2pd.a -lssl -lcrypto -lz -lboost_system -lboost_filesystem -lboost_date_time -lboost_program_options -lboost_json -lpthread -latomic -lstdc++ -lm -lminiupnpc
#include <stdlib.h>
#include "capi.h"
#include "capi2.h"
*/
import "C"

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"i2p-proxy/internal/router"
)

// libi2pd's RouterContext/Transports/NetDb are process-wide singletons
// (see libi2pd/api.cpp) - only the first Router in this process actually
// initializes the underlying embedded router; later instances share it and
// only create their own local Destination.
var initOnce sync.Once
var initErr error

type Router struct {
	cfg router.Config

	mu      sync.Mutex
	dest    C.i2pp_dest_t
	started bool
}

func New(cfg router.Config) *Router {
	return &Router{cfg: cfg}
}

func (r *Router) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return nil
	}

	dataDir := r.cfg.DataDir
	if dataDir == "" {
		dataDir = "./i2pdata"
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("i2pd: creating data dir: %w", err)
	}
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return fmt.Errorf("i2pd: resolving data dir: %w", err)
	}
	if err := installCertificates(absDataDir); err != nil {
		return fmt.Errorf("i2pd: installing reseed certificates: %w", err)
	}

	initOnce.Do(func() { initErr = startEmbeddedRouter(absDataDir, r.cfg.Port, r.cfg.LogLevel) })
	if initErr != nil {
		return initErr
	}

	keysFile := C.CString(filepath.Join(absDataDir, "destination.keys"))
	defer C.free(unsafe.Pointer(keysFile))

	dest := C.I2PP_CreateDestination(keysFile, 1)
	if dest == nil {
		return fmt.Errorf("i2pd: failed to create local destination")
	}
	r.dest = dest
	r.started = true
	return nil
}

func startEmbeddedRouter(dataDir string, port int, logLevel string) error {
	appName := C.CString("i2p-proxy")
	defer C.free(unsafe.Pointer(appName))

	// argv[0] is consumed as the program name by boost::program_options'
	// command_line_parser (same convention as a normal C main(argc, argv))
	// and silently skipped - omitting a placeholder here previously caused
	// --datadir to be eaten as if it were argv[0], so it was never applied
	// and i2pd fell back to its own default (~/.<appname>).
	progNameArg := C.CString("i2p-proxy")
	defer C.free(unsafe.Pointer(progNameArg))
	datadirArg := C.CString("--datadir=" + dataDir)
	defer C.free(unsafe.Pointer(datadirArg))
	logArg := C.CString("--log=stdout")
	defer C.free(unsafe.Pointer(logArg))

	argv := []*C.char{progNameArg, datadirArg, logArg}
	if port > 0 {
		portArg := C.CString(fmt.Sprintf("--port=%d", port))
		defer C.free(unsafe.Pointer(portArg))
		argv = append(argv, portArg)
	}
	C.C_InitI2P(C.int(len(argv)), &argv[0], appName)

	// --loglevel is parsed into config by C_InitI2P but, like SetCertsDir,
	// is only ever applied to the logger from i2pd's own daemon main() (see
	// daemon/Daemon.cpp calling i2p::log::Logger().SetLogLevel(...)) - never
	// from the libi2pd/api.h InitI2P/StartI2P path our embedding uses. Without
	// this call i2pd's internal logger silently stays at its "warn" default
	// regardless of any --loglevel argv value.
	if logLevel != "" {
		levelArg := C.CString(logLevel)
		defer C.free(unsafe.Pointer(levelArg))
		C.I2PP_SetLogLevel(levelArg)
	}

	// i2pd's own daemon main() calls fs::SetCertsDir itself (see
	// daemon/Daemon.cpp); our embedding via libi2pd/api.h's InitI2P never
	// does, so without this, certsDir stays empty and reseed can't find the
	// trust roots installCertificates() already wrote to disk.
	certsDirArg := C.CString(filepath.Join(dataDir, "certificates"))
	defer C.free(unsafe.Pointer(certsDirArg))
	C.I2PP_SetCertsDir(certsDirArg)

	C.C_StartI2P()

	// Requests a port mapping from the LAN router via UPnP, if supported/
	// enabled there. Helps with inbound reachability on ordinary home/office
	// NAT; does nothing for carrier-grade NAT upstream of that router. Safe
	// to call even when built without USE_UPNP - logs a warning and no-ops.
	C.I2PP_StartUPnP()

	return nil
}

// Stop tears down this Router's local Destination. It deliberately does not
// call C_StopI2P/C_TerminateI2P - those would shut down the process-wide
// router that other Router instances in this process (see initOnce) may
// still be using. Full router teardown happens implicitly on process exit,
// which is an accepted limitation for now (see doc comment on i2pd_stub.go
// for the broader backend status).
func (r *Router) Stop(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started {
		return nil
	}
	C.I2PP_DestroyDestination(r.dest)
	r.dest = nil
	r.started = false
	return nil
}

func (r *Router) Ready() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started {
		return false
	}
	return C.I2PP_IsDestinationReady(r.dest) != 0
}

func (r *Router) LocalDestination() (router.Destination, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started {
		return "", router.ErrNotReady
	}
	buf := make([]byte, 64)
	n := C.I2PP_GetBase32Address(r.dest, (*C.char)(unsafe.Pointer(&buf[0])), C.int(len(buf)))
	if n < 0 {
		return "", fmt.Errorf("i2pd: base32 address buffer too small")
	}
	return router.Destination(string(buf[:n]) + ".b32.i2p"), nil
}

func (r *Router) Listen(ctx context.Context) (router.Listener, error) {
	r.mu.Lock()
	dest, started := r.dest, r.started
	r.mu.Unlock()
	if !started {
		return nil, router.ErrNotReady
	}
	localDest, err := r.LocalDestination()
	if err != nil {
		return nil, err
	}
	return &listener{dest: dest, localDest: localDest}, nil
}

// Dial opens a stream to dest on I2P streaming port 0 - this backend's
// application-level target (host/port for the thing the client actually
// wants) travels inside the stream payload via internal/protocol, not as
// the I2P streaming port, matching how the loopback backend and proxysrv/
// proxycli are wired.
func (r *Router) Dial(ctx context.Context, dest router.Destination) (router.Conn, error) {
	r.mu.Lock()
	d, started := r.dest, r.started
	r.mu.Unlock()
	if !started {
		return nil, router.ErrNotReady
	}

	timeoutMs := 30000
	if deadline, ok := ctx.Deadline(); ok {
		if remain := time.Until(deadline); remain > 0 {
			timeoutMs = int(remain / time.Millisecond)
		}
	}

	cAddr := C.CString(string(dest))
	defer C.free(unsafe.Pointer(cAddr))

	s := C.I2PP_Dial(d, cAddr, 0, C.int(timeoutMs))
	if s == nil {
		return nil, fmt.Errorf("i2pd: dial %s: failed or timed out", dest)
	}
	return &conn{stream: s, remote: dest}, nil
}

type listener struct {
	dest      C.i2pp_dest_t
	localDest router.Destination
}

func (l *listener) Accept() (router.Conn, error) {
	s := C.I2PP_Accept(l.dest)
	if s == nil {
		return nil, fmt.Errorf("i2pd: listener closed")
	}
	c := &conn{stream: s}
	buf := make([]byte, 64)
	if n := C.I2PP_StreamRemoteAddress(s, (*C.char)(unsafe.Pointer(&buf[0])), C.int(len(buf))); n > 0 {
		c.remote = router.Destination(string(buf[:n]) + ".b32.i2p")
	}
	return c, nil
}

func (l *listener) Close() error                         { return nil } // torn down via Router.Stop -> I2PP_DestroyDestination, which unblocks Accept
func (l *listener) LocalDestination() router.Destination { return l.localDest }

type conn struct {
	stream        C.i2pp_stream_t
	remote        router.Destination
	deadlineMu    sync.Mutex
	readDeadline  time.Time
	writeDeadline time.Time
}

func (c *conn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.deadlineMu.Lock()
	deadline := c.readDeadline
	c.deadlineMu.Unlock()
	timeoutMs := 0
	if !deadline.IsZero() {
		remain := time.Until(deadline)
		if remain <= 0 {
			return 0, os.ErrDeadlineExceeded
		}
		timeoutMs = int((remain + time.Millisecond - 1) / time.Millisecond)
	}
	n := C.I2PP_StreamRecv(c.stream, (*C.uint8_t)(unsafe.Pointer(&p[0])), C.size_t(len(p)), C.int(timeoutMs))
	if n < 0 {
		if n == -2 {
			return 0, os.ErrDeadlineExceeded
		}
		return 0, fmt.Errorf("i2pd: stream read failed")
	}
	if n == 0 {
		return 0, io.EOF
	}
	return int(n), nil
}

func (c *conn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	n := C.I2PP_StreamSend(c.stream, (*C.uint8_t)(unsafe.Pointer(&p[0])), C.size_t(len(p)))
	if n < 0 {
		return 0, fmt.Errorf("i2pd: stream write failed")
	}
	return int(n), nil
}

func (c *conn) Close() error {
	C.I2PP_StreamClose(c.stream)
	return nil
}

func (c *conn) RemoteDestination() router.Destination { return c.remote }

// Read deadlines use the C API's per-call timeout. Write deadlines are stored
// for compatibility, but the native send API has no timeout parameter.
/* The C stream API takes a timeout per-call rather than a persistent
// deadline; a real deadline would need tracking a target time here and
// passing the remaining budget into each I2PP_StreamRecv call, mirroring
// what Dial does with ctx. Left as a follow-up - proxysrv/proxycli only use
// SetDeadline around the initial protocol handshake today, and that window
// is short enough that blocking-until-first-byte is an acceptable gap for
// now. */
func (c *conn) SetDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	c.readDeadline, c.writeDeadline = t, t
	c.deadlineMu.Unlock()
	return nil
}
func (c *conn) SetReadDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	c.readDeadline = t
	c.deadlineMu.Unlock()
	return nil
}
func (c *conn) SetWriteDeadline(t time.Time) error {
	c.deadlineMu.Lock()
	c.writeDeadline = t
	c.deadlineMu.Unlock()
	return nil
}

var _ router.Router = (*Router)(nil)
var _ router.Conn = (*conn)(nil)
var _ router.Listener = (*listener)(nil)
