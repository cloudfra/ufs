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

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/grpcfs"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestServeRoundTrip covers what a grpc:// -mount promises: the file system the
// command opened answers the FS service on a socket, so a caller in another
// process writes one file and gets it back by every route the service offers.
//
// The listener is a real one on the loopback interface with a port the operating
// system picked, because a test cannot ask a fixed address for a free port. The
// served file system is a memory:// one, the same one the flag takes, so what a
// test changes and checks is the served tree and nothing of the machine in
// between. The client is the generated one over a connection of its own, which
// is what an external implementer of the service is reached by: a caller that
// links this module uses the grpc:// driver instead, and the test after this one
// covers that route.
func TestServeRoundTrip(t *testing.T) {
	t.Parallel()

	ctx, stopServing := context.WithCancel(t.Context())
	fsys, err := ufs.New(ctx, "memory://")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("Close of the served file system: %v", err)
		}
	})

	lis := loopbackListener(ctx, t)
	tcp, ok := lis.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener on %v wants a TCP address to report its port, got %v", lis.Addr(), lis.Addr())
	}

	served := make(chan error, 1)
	go func() {
		served <- serve(ctx, fsys, lis)
	}()

	client := dialServed(t, tcp.Port)

	const name, content = "note.txt", "served over gRPC"
	upload(ctx, t, client, name, content)

	if got := download(ctx, t, client, name); got != content {
		t.Errorf("ReadFile of %q read %q, want the content the upload sent", name, got)
	}
	names := listing(ctx, t, client, ".")
	if !slices.Contains(names, name) {
		t.Errorf("ListFiles of the root listed %v, want %q among them", names, name)
	}

	// What ends the serving side here is the context, which is what SIGINT and
	// SIGTERM end it with in the command.
	stopServing()
	if err := <-served; err != nil {
		t.Errorf("serve stopped by a canceled context = %v, want nil", err)
	}

	// serve does not close what it was handed, and it returns only once no
	// handler is still running, so this read of the file the upload wrote is the
	// check that the file system outlives the serving side.
	if got := readFrom(t, fsys, name); got != content {
		t.Errorf("%q in the served file system = %q, want the content the upload put there", name, got)
	}
}

// TestServedAddressOpensAsAFileSystem covers the route a caller that links this
// module takes: the address a grpc:// -mount listens on is a file system to the
// grpc:// driver, so a caller that knows only that URI reaches the tree this
// process holds. That is what the flag is for — the same address the driver dials
// — with the driver standing in for a caller on another machine.
func TestServedAddressOpensAsAFileSystem(t *testing.T) {
	t.Parallel()

	ctx, stopServing := context.WithCancel(t.Context())
	fsys, err := ufs.New(ctx, "memory://")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("Close of the served file system: %v", err)
		}
	})

	lis := loopbackListener(ctx, t)

	served := make(chan error, 1)
	go func() {
		served <- serve(ctx, fsys, lis)
	}()

	remote, err := grpcfs.New(ctx, "grpc://"+lis.Addr().String())
	if err != nil {
		t.Fatalf("grpcfs.New of the served address %q: %v", lis.Addr(), err)
	}
	t.Cleanup(func() {
		if err := remote.Close(); err != nil {
			t.Errorf("Close of the file system the driver built: %v", err)
		}
	})

	// What the driver writes over the socket is in the file system the command
	// holds, which is what one file system behind a mount and behind a socket
	// means. Both ends are a types.WriteFS, so the helpers below reach either one.
	const written, writtenContent = "written-over-the-socket.txt", "written through the driver"
	if err := writeTo(t, remote, written, writtenContent); err != nil {
		t.Fatalf("writing %q through the driver: %v", written, err)
	}
	if got := readFrom(t, fsys, written); got != writtenContent {
		t.Errorf("%q in the served file system = %q, want what the driver wrote", written, got)
	}

	// And what the process wrote without the socket is what the driver reads back.
	const local, localContent = "written-in-the-process.txt", "written without the socket"
	if err := writeTo(t, fsys, local, localContent); err != nil {
		t.Fatalf("writing %q in the served file system: %v", local, err)
	}
	if got := readFrom(t, remote, local); got != localContent {
		t.Errorf("%q through the driver = %q, want what the process wrote", local, got)
	}

	stopServing()
	if err := <-served; err != nil {
		t.Errorf("serve stopped by a canceled context = %v, want nil", err)
	}
}

// TestParseMount covers the one flag that decides which output runs: a path on the
// host is a mount, a grpc:// address is a socket, and anything else that carries a
// scheme is a caller putting a file system where an address belongs.
func TestParseMount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		value       string
		wantAddress string
		wantPath    string
		wantRefuse  string
	}{
		{value: "/mnt/data", wantPath: "/mnt/data"},
		{value: `C:\mnt\data`, wantPath: `C:\mnt\data`},
		{value: "./scratch", wantPath: "./scratch"},
		{value: "grpc://0.0.0.0:9000", wantAddress: "0.0.0.0:9000"},
		{value: "grpc://:9000", wantAddress: ":9000"},
		{value: "grpc://localhost:9000", wantAddress: "localhost:9000"},
		{value: "grpc://127.0.0.1:9000/", wantAddress: "127.0.0.1:9000"},
		{value: "grpcs://host:9000", wantRefuse: "certificate"},
		{value: "grpc://", wantRefuse: "address to listen on"},
		{value: "grpc://host:9000/subtree", wantRefuse: "more than an address"},
		{value: "grpc://host:9000?cache=null%3A", wantRefuse: "more than an address"},
		{value: "gs://my-bucket", wantRefuse: "-uri"},
		{value: "https://example.com/assets.zip", wantRefuse: "-uri"},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			t.Parallel()
			got, err := parseMount(tt.value)
			if tt.wantRefuse != "" {
				if err == nil {
					t.Fatalf("parseMount(%q) = %+v, want it refused", tt.value, got)
				}
				if !strings.Contains(err.Error(), tt.wantRefuse) {
					t.Errorf("parseMount(%q) = %v, want an error saying %q", tt.value, err, tt.wantRefuse)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMount(%q) = %v, want it accepted", tt.value, err)
			}
			if got.address != tt.wantAddress {
				t.Errorf("parseMount(%q) address = %q, want %q", tt.value, got.address, tt.wantAddress)
			}
			if got.mountPath != tt.wantPath {
				t.Errorf("parseMount(%q) mountPath = %q, want %q", tt.value, got.mountPath, tt.wantPath)
			}
			if got.address != "" && got.mountPath != "" {
				t.Errorf("parseMount(%q) = %+v, want one of the two filled, not both", tt.value, got)
			}
		})
	}
}

// TestRunRefusesMountURIWithNoAddress covers the guard in front of the flag layer:
// a -mount value naming a file system is refused before a file system is opened,
// mounted or bound to a port.
func TestRunRefusesMountURIWithNoAddress(t *testing.T) {
	t.Parallel()
	err := run("memory://", "gs://my-bucket", "")
	if err == nil {
		t.Fatal(`run(-mount "gs://my-bucket") = nil, want an error for a file system URI where an address or a path belongs`)
	}
	if !strings.Contains(err.Error(), "-mount") {
		t.Errorf("run(-mount \"gs://my-bucket\") = %v, want an error naming the flag it rejects", err)
	}
}

// TestDebugEndpoints covers -debug: the address it names answers the process's own
// endpoints and nothing else. The mux is built by hand rather than taken from the
// default one, so the check that an ordinary path is not answered is part of what
// makes the flag safe to point at a reachable port.
func TestDebugEndpoints(t *testing.T) {
	t.Parallel()

	ctx, stopServing := context.WithCancel(t.Context())
	lis := loopbackListener(ctx, t)
	tcp, ok := lis.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener on %v wants a TCP address to report its port, got %v", lis.Addr(), lis.Addr())
	}

	stopped := make(chan error, 1)
	go func() {
		stopped <- serveDebug(ctx, lis)
	}()

	base := fmt.Sprintf("http://127.0.0.1:%d", tcp.Port)
	status, body := getFrom(ctx, t, base+"/debug/vars")
	if status != http.StatusOK {
		t.Fatalf("GET /debug/vars = %d, want 200", status)
	}
	var vars map[string]any
	if err := json.Unmarshal(body, &vars); err != nil {
		t.Fatalf("GET /debug/vars answered %q, want the expvar document: %v", body, err)
	}
	if _, reported := vars["cmdline"]; !reported {
		t.Errorf("GET /debug/vars reported %v, want the expvar counters including cmdline", slices.Sorted(maps.Keys(vars)))
	}

	status, body = getFrom(ctx, t, base+"/debug/pprof/")
	if status != http.StatusOK {
		t.Fatalf("GET /debug/pprof/ = %d, want 200", status)
	}
	if !bytes.Contains(body, []byte("Types of profiles available")) {
		t.Errorf("GET /debug/pprof/ answered %q, want the profiler index", body)
	}

	if status, _ = getFrom(ctx, t, base+"/"); status != http.StatusNotFound {
		t.Errorf("GET / = %d, want 404 from a mux that answers only the debug endpoints", status)
	}

	if status, _ = getFrom(ctx, t, base+"/debug/pprof/goroutine"); status != http.StatusOK {
		t.Errorf("GET /debug/pprof/goroutine = %d, want 200 from an index that reaches the profiles", status)
	}

	stopServing()
	if err := <-stopped; err != nil {
		t.Errorf("serveDebug stopped by a canceled context = %v, want nil", err)
	}
}

// TestRunOutputsStopsEveryOutput covers the pairing rule without a mount: the
// output that ends first is what the process reports, the others are brought down
// rather than left running, and runOutputs has waited for all of them before it
// returns, which is what puts the close of the file system behind them.
func TestRunOutputsStopsEveryOutput(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	stopped := errors.New("the socket stopped answering")
	broughtDown := make(chan struct{})

	err := runOutputs(ctx, []func(context.Context) error{
		func(context.Context) error { return stopped },
		func(ctx context.Context) error {
			<-ctx.Done()
			close(broughtDown)
			return nil
		},
	})
	if !errors.Is(err, stopped) {
		t.Errorf("runOutputs = %v, want the error the first output ended with", err)
	}

	select {
	case <-broughtDown:
	case <-time.After(10 * time.Second):
		t.Fatal("runOutputs returned without bringing down the output that was still running")
	}
}

// loopbackListener opens a socket on the loopback interface at a port the
// operating system picked, which is what lets a test reach an address it does not
// have to reserve.
func loopbackListener(ctx context.Context, tb testing.TB) net.Listener {
	tb.Helper()
	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if err := lis.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			tb.Errorf("Close listener: %v", err)
		}
	})
	return lis
}

// writeTo writes name in the file system it is handed, which is either end of the
// pairing: the one this process holds, or the one reached over a socket.
func writeTo(tb testing.TB, fsys types.WriteFS, name, content string) error {
	tb.Helper()
	f, err := fsys.Create(name)
	if err != nil {
		return err
	}
	if _, writeErr := io.WriteString(f, content); writeErr != nil {
		return errors.Join(writeErr, f.Close())
	}
	return f.Close()
}

// readFrom reads name from the file system it is handed, which is either end of
// the pairing, for the checks about what one end can see of the other.
func readFrom(tb testing.TB, fsys types.WriteFS, name string) string {
	tb.Helper()
	f, err := fsys.Open(name)
	if err != nil {
		tb.Fatalf("Open(%q): %v", name, err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			tb.Errorf("Close(%q): %v", name, err)
		}
	}()
	content, err := io.ReadAll(f)
	if err != nil {
		tb.Fatalf("ReadAll(%q): %v", name, err)
	}
	return string(content)
}

// dialServed returns the generated client for the FS service on the port the
// listener reported, over a connection of its own, which is the path a caller
// outside the process takes.
func dialServed(tb testing.TB, port int) proto.FSClient {
	tb.Helper()
	conn, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", port),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		tb.Fatalf("grpc.NewClient: %v", err)
	}
	tb.Cleanup(func() {
		if err := conn.Close(); err != nil {
			tb.Errorf("Close connection: %v", err)
		}
	})
	return proto.NewFSClient(conn)
}

// upload sends a whole file as the client-streaming WriteFile rpc documents it,
// one frame carrying the name, the mode and the content.
func upload(ctx context.Context, tb testing.TB, client proto.FSClient, name, content string) {
	tb.Helper()
	stream, err := client.WriteFile(ctx)
	if err != nil {
		tb.Fatalf("WriteFile of %q: %v", name, err)
	}
	if err := stream.Send(&proto.WriteFileRequest{Name: name, Mode: uint32(0o644), Data: []byte(content)}); err != nil {
		tb.Fatalf("WriteFile frame of %q: %v", name, err)
	}
	if _, err := stream.CloseAndRecv(); err != nil {
		tb.Fatalf("WriteFile answer for %q: %v", name, err)
	}
}

// download reads a file back through the server-streaming ReadFile rpc, joining
// the frames the handler sends.
func download(ctx context.Context, tb testing.TB, client proto.FSClient, name string) string {
	tb.Helper()
	stream, err := client.ReadFile(ctx, &proto.ReadFileRequest{Name: name})
	if err != nil {
		tb.Fatalf("ReadFile of %q: %v", name, err)
	}
	var out []byte
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return string(out)
		}
		if err != nil {
			tb.Fatalf("ReadFile frame of %q: %v", name, err)
		}
		out = append(out, frame.GetData()...)
	}
}

// listing names every child of parent that the ListFiles rpc reports.
func listing(ctx context.Context, tb testing.TB, client proto.FSClient, parent string) []string {
	tb.Helper()
	resp, err := client.ListFiles(ctx, &proto.ListFilesRequest{Parent: parent})
	if err != nil {
		tb.Fatalf("ListFiles of %q: %v", parent, err)
	}
	names := make([]string, 0, len(resp.GetFiles()))
	for _, f := range resp.GetFiles() {
		names = append(names, f.GetName())
	}
	return names
}

// getFrom asks the debug server for path and reports what it answered, body and
// all, with the connection given back either way.
func getFrom(ctx context.Context, tb testing.TB, url string) (int, []byte) {
	tb.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		tb.Fatalf("request for %q: %v", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		tb.Fatalf("GET %q: %v", url, err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			tb.Errorf("Close body of %q: %v", url, err)
		}
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		tb.Fatalf("body of %q: %v", url, err)
	}
	return resp.StatusCode, body
}
