// Copyright 2026 Cloudfra
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package main is the ufsmount application for exposing a virtual file system,
// either as a directory on the host operating system or as the FS service of
// proto/ufs.proto on a socket. -mount decides which one it is: a path on the
// host is a mount, and a grpc:// address is the socket to serve on.
package main

import (
	"context"
	"errors"
	"expvar"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cloudfra/ufs"
	_ "github.com/cloudfra/ufs/drivers/all"
	"github.com/cloudfra/ufs/drivers/grpcfs"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/host"
	"github.com/cloudfra/ufs/proto"
	"google.golang.org/grpc"
)

const (
	// debugReadHeaderTimeout bounds a client that opens the debug port and sends
	// nothing, which otherwise holds a connection open forever.
	debugReadHeaderTimeout = 10 * time.Second
	// debugShutdownTimeout bounds how long the debug server may take to stop. Its
	// handlers answer from memory and hold nothing open, so a request still
	// running when it expires is dropped rather than waited for.
	debugShutdownTimeout = 5 * time.Second
)

var (
	uriFlag     = flag.String("uri", "", "URI of the virtual file system to expose (e.g. memory://, gs://bucket).")
	mountFlag   = flag.String("mount", "", "Path on the host to mount the file system at, or a grpc://host:port address to serve it on over gRPC instead.")
	debugFlag   = flag.String("debug", "", "host:port address to run the debug HTTP server on (expvar and the Go profiler), e.g. 127.0.0.1:6060.")
	verboseFlag = flag.Bool("verbose", false, "Enable debug logging for ProjFS/FUSE callbacks.")
)

func main() {
	flag.Parse()
	if *verboseFlag {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}
	if *uriFlag == "" || *mountFlag == "" {
		flag.Usage()
		os.Exit(1)
	}
	if err := run(*uriFlag, *mountFlag, *debugFlag); err != nil {
		slog.Error("fatal error", "error", err)
		os.Exit(1)
	}
}

// run opens uri once and hands that one file system to the output -mount asked
// for: a mount on the host for a path, the FS service of proto/ufs.proto for a
// grpc:// address. -debug adds the process's own endpoints beside whichever one
// that was.
func run(uri, mountValue, debugAddress string) error {
	what, err := parseMount(mountValue)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	slog.Info("opening file system", "uri", uri)
	fsys, err := ufs.New(ctx, uri)
	if err != nil {
		return err
	}
	// The file system outlives every output: a handler answering the FS service
	// holds the handle it opened here until it unwinds, so all the outputs are
	// stopped and waited for before this close runs.
	defer func() {
		if err := fsys.Close(); err != nil {
			slog.Warn("cannot close file system", "fs", fsys, "error", err)
		}
	}()

	// The sockets are opened before anything runs, so an address that cannot be
	// bound is reported before a mount exists rather than beside half of what was
	// asked for.
	var served, debug net.Listener
	if what.address != "" {
		served, err = listen(ctx, what.address)
		if err != nil {
			return fmt.Errorf("cannot listen on -mount address %q: %w", what.address, err)
		}
	}
	if debugAddress != "" {
		debug, err = listen(ctx, debugAddress)
		if err != nil {
			closeListener(served)
			return fmt.Errorf("cannot listen on -debug address %q: %w", debugAddress, err)
		}
	}

	var outputs []func(context.Context) error
	if served != nil {
		outputs = append(outputs, func(ctx context.Context) error { return serve(ctx, fsys, served) })
	} else {
		outputs = append(outputs, func(ctx context.Context) error { return mountFS(ctx, fsys, uri, what.mountPath) })
	}
	if debug != nil {
		outputs = append(outputs, func(ctx context.Context) error { return serveDebug(ctx, debug) })
	}
	return runOutputs(ctx, outputs)
}

// listen opens the socket an output is served on.
func listen(ctx context.Context, address string) (net.Listener, error) {
	var lc net.ListenConfig
	return lc.Listen(ctx, "tcp", address)
}

// closeListener gives back a socket that was opened but never served on. Once an
// output is running, the server it was handed closes it.
func closeListener(lis net.Listener) {
	if lis == nil {
		return
	}
	if err := lis.Close(); err != nil {
		slog.Warn("cannot close listener", "address", lis.Addr(), "error", err)
	}
}

// mountFS mounts fsys at mountPath and returns once the mount is gone, whether
// that came from a signal, an unmount run outside the process, or an error.
func mountFS(ctx context.Context, fsys types.WriteFS, uri, mountPath string) error {
	slog.Info("mounting file system", "uri", uri, "mountPath", mountPath)
	server, err := host.Mount(ctx, fsys, mountPath)
	if err != nil {
		return fmt.Errorf("cannot mount %q at %q: %w", uri, mountPath, err)
	}
	defer func() {
		if err := server.Close(); err != nil {
			slog.Warn("cannot unmount", "mountPath", mountPath, "error", err)
		}
	}()

	slog.Info("mounted file system", "uri", uri, "mountPath", mountPath)
	server.Wait()
	slog.Info("unmounted file system", "mountPath", mountPath)
	return nil
}

// serve answers the FS service of proto/ufs.proto from fsys on lis, and returns
// only once no handler is still running. What ends it is the listener going
// away, the server being stopped, or ctx ending — which is what a SIGINT or
// SIGTERM does here.
func serve(ctx context.Context, fsys types.WriteFS, lis net.Listener) error {
	// The options are not decoration: the handlers assume the message ceiling
	// they set, and grpc's own default is smaller than the single-frame upload
	// the WriteFile rpc documents, so a server built without them refuses an
	// upload at a number nothing in this command chose.
	grpcServer := grpc.NewServer(grpcfs.ServerOptions()...)
	fsServer := grpcfs.NewServer(fsys)
	proto.RegisterFSServer(grpcServer, fsServer)
	// Whichever way serving ends, no handler is still using fsys when this
	// returns and the caller closes it: Shutdown waits for the ones in progress,
	// which a failed Serve leaves behind as surely as Stop would.
	defer fsServer.Shutdown()

	address := lis.Addr().String()
	slog.Info("serving file system", "address", address)
	defer slog.Info("served file system", "address", address)

	stopped := make(chan error, 1)
	go func() {
		stopped <- grpcServer.Serve(lis)
	}()

	select {
	case err := <-stopped:
		return err
	case <-ctx.Done():
	}
	// Shutdown first: it ends the Watch streams, which run until their client
	// cancels them and would keep GracefulStop waiting forever, refuses new
	// rpcs, and waits for the rest. GracefulStop rather than Stop then: a
	// ReadFile or ListFiles handler holds the handle it opened on fsys until it
	// unwinds, and Stop returns without waiting for that, while the close of
	// fsys follows this function.
	fsServer.Shutdown()
	grpcServer.GracefulStop()
	if err := <-stopped; !errors.Is(err, grpc.ErrServerStopped) {
		return err
	}
	return nil
}

// serveDebug answers the process's own endpoints on lis: the expvar counters and
// the Go profiler. They say nothing about the file system and reach none of it,
// so this output never hands fsys to anyone and never keeps the process alive on
// its own — what ends it is ctx, the same as the others.
func serveDebug(ctx context.Context, lis net.Listener) error {
	// A mux of its own rather than the default one: the default carries whatever
	// an imported package decided to register, and this address may be reachable
	// from outside the process.
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.Handle("/debug/vars", expvar.Handler())

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: debugReadHeaderTimeout,
	}

	address := lis.Addr().String()
	slog.Info("serving debug endpoints", "address", address)
	defer slog.Info("served debug endpoints", "address", address)

	stopped := make(chan error, 1)
	go func() {
		// ErrServerClosed is Shutdown's own answer, not a failure to report.
		if err := server.Serve(lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			stopped <- err
			return
		}
		stopped <- nil
	}()

	select {
	case err := <-stopped:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.WithoutCancel(ctx), debugShutdownTimeout)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("cannot stop the debug server on %q: %w", address, err)
	}
	return <-stopped
}

// runOutputs keeps every output this run asked for alive and brings them all
// down when any one of them ends: a mount removed outside the process leaves
// nothing for a socket to be good for, and a socket that stopped answering is
// not worth a mount on its own. The output that ended first decides what the
// process reports; the others were stopped on purpose, so what they come back
// with is the shutdown rather than a second failure.
func runOutputs(ctx context.Context, outputs []func(context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ended := make(chan error, len(outputs))
	var wg sync.WaitGroup
	for _, output := range outputs {
		wg.Go(func() {
			ended <- output(ctx)
		})
	}

	err := <-ended
	cancel()
	wg.Wait()
	for len(ended) > 0 {
		if other := <-ended; other != nil {
			slog.Debug("output ended during shutdown", "error", other)
		}
	}
	return err
}

// exposure is what -mount asked for: a directory on the host, or the address the
// FS service is answered on. One flag carries both because a mount and a socket
// are the same file system seen from two sides, and the process has to know
// which side it is being asked for.
type exposure struct {
	mountPath string
	address   string // empty unless the value named a gRPC address
}

// parseMount decides which output a -mount value asks for. A value carrying a
// scheme is a URI, and the only URI this flag answers is grpc:// — the address
// to serve the FS service of proto/ufs.proto on, which is the address the
// grpc:// driver of drivers/grpcfs dials back. Any other scheme is a mistake
// about which flag takes a URI: -uri names the file system, -mount says where it
// appears. Everything without a scheme is a path on the host.
func parseMount(value string) (exposure, error) {
	scheme, _, hasScheme := strings.Cut(value, "://")
	if !hasScheme {
		return exposure{mountPath: value}, nil
	}
	switch scheme {
	case "grpc":
		u, err := url.Parse(value)
		if err != nil {
			return exposure{}, fmt.Errorf("-mount %q: %w", value, err)
		}
		if u.Host == "" {
			return exposure{}, fmt.Errorf("-mount %q wants an address to listen on, written grpc://host:port (e.g. grpc://0.0.0.0:9000)", value)
		}
		// Nothing is left for a path or a query to select: the service answers a
		// whole file system, a subtree is what a mount point is for, and -uri
		// already carries the query syntax.
		if (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
			return exposure{}, fmt.Errorf("-mount %q names more than an address: the FS service answers the whole file system, so a grpc:// address carries no path and no query", value)
		}
		return exposure{address: u.Host}, nil
	case "grpcs":
		// Refused rather than served in plaintext: grpcs:// is a promise about the
		// transport, and no flag gives this process a certificate to keep it with.
		return exposure{}, fmt.Errorf("-mount %q asks for TLS, which grpcs:// means, and no flag gives this process a certificate to serve with", value)
	default:
		return exposure{}, fmt.Errorf("-mount %q is a URI, and the only URI -mount answers is the grpc:// address to serve on: the file system's own URI belongs in -uri", value)
	}
}
