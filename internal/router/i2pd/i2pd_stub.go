//go:build !((linux || windows) && cgo)

// Package i2pd is the production Router backend: i2pd's C++ router
// (libi2pd) statically linked into this binary via cgo, driven in-process -
// no external i2pd daemon process, no exec.Command.
//
// This mirrors the pattern i2pd's own Android app uses (embedding libi2pd
// directly in the app process): the same libi2pd_wrapper/capi.h C API
// i2pd ships for that purpose, extended here (capi2.h) with the
// destination/stream primitives (i2p::api::CreateLocalDestination /
// CreateStream / AcceptStream) needed to implement router.Router.
//
// STATUS: implemented and proven for linux/amd64 (i2pd_linux.go) and
// windows/amd64 (i2pd_windows.go). This stub file covers every other
// GOOS/GOARCH - producing static libs for a new target means running
// i2pd's own build for that platform (see third_party/docker/linux-amd64
// for the Linux Docker-based build, or the MSYS2/MinGW toolchain used for
// Windows) then adding an i2pd_<goos>.go analogous to the existing ones.
package i2pd

import (
	"context"
	"fmt"

	"i2p-proxy/internal/router"
)

type Router struct {
	cfg router.Config
}

func New(cfg router.Config) *Router {
	return &Router{cfg: cfg}
}

var errNotImplemented = fmt.Errorf("i2pd backend: not built for this platform yet - see package doc comment in internal/router/i2pd/i2pd_stub.go")

func (r *Router) Start(ctx context.Context) error                     { return errNotImplemented }
func (r *Router) Stop(ctx context.Context) error                      { return errNotImplemented }
func (r *Router) Ready() bool                                         { return false }
func (r *Router) LocalDestination() (router.Destination, error)       { return "", errNotImplemented }
func (r *Router) Listen(ctx context.Context) (router.Listener, error) { return nil, errNotImplemented }
func (r *Router) Dial(ctx context.Context, dest router.Destination) (router.Conn, error) {
	return nil, errNotImplemented
}

var _ router.Router = (*Router)(nil)
