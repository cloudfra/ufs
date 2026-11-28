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

package grpcfs_test

import (
	"io/fs"
	"sync/atomic"
	"testing"

	"github.com/cloudfra/ufs/drivers/grpcfs"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/proto"
	"google.golang.org/grpc/codes"
)

// stallFS holds its first Stat until release is closed, after reporting on
// entered that it has begun, so a test can keep one rpc in progress for as long
// as it needs without sleeping. Every later Stat answers at once. finished is
// set as the stalled Stat returns, which is still inside the rpc.
type stallFS struct {
	types.WriteFS
	stalled  atomic.Bool
	finished atomic.Bool
	entered  chan struct{}
	release  chan struct{}
}

func (s *stallFS) Stat(name string) (fs.FileInfo, error) {
	if s.stalled.CompareAndSwap(false, true) {
		s.entered <- struct{}{}
		<-s.release
		defer s.finished.Store(true)
	}
	return s.WriteFS.Stat(name)
}

// TestServerShutdownWaitsForRPC keeps a GetFile in progress while Shutdown is
// called and checks that Shutdown returns only after that rpc has, and that
// every rpc afterwards is refused with Unavailable without reaching the file
// system.
func TestServerShutdownWaitsForRPC(t *testing.T) {
	t.Parallel()
	inner := &stallFS{WriteFS: servedFS(t), entered: make(chan struct{}), release: make(chan struct{})}
	server := grpcfs.NewServer(inner)
	req := &proto.GetFileRequest{Name: "."}

	rpcDone := make(chan error)
	go func() {
		_, err := server.GetFile(t.Context(), req)
		rpcDone <- err
	}()
	<-inner.entered

	shutdownDone := make(chan bool)
	go func() {
		server.Shutdown()
		shutdownDone <- inner.finished.Load()
	}()

	// Until Shutdown begins a new rpc is answered; once it has, it is refused.
	for {
		_, err := server.GetFile(t.Context(), req)
		if err != nil {
			wantCode(t, err, codes.Unavailable)
			break
		}
	}

	close(inner.release)
	if err := <-rpcDone; err != nil {
		t.Errorf("GetFile in progress at Shutdown = %v, want nil", err)
	}
	if !<-shutdownDone {
		t.Error("Shutdown returned while an rpc was still in progress")
	}

	_, err := server.ListFiles(t.Context(), &proto.ListFilesRequest{Parent: "."})
	wantCode(t, err, codes.Unavailable)
}
