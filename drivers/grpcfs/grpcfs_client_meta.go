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

package grpcfs

// URI, String, DeviceInfo and IsSame are the [types.URIGet], [fmt.Stringer],
// [types.DeviceInfoGet] and [types.ComparableFS] halves of [types.ReadFS]: what
// a client can answer about itself without asking the server.

import (
	"io/fs"
	"net/url"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/drivers/uri"
)

// URI reports the address this file system reaches, which opens it back through
// [ufs.New] as long as the driver is registered.
func (fsys *grpcFS) URI() (*url.URL, error) {
	return &url.URL{Scheme: fsys.scheme, Host: fsys.address}, nil
}

// String describes what this file system is made of: the address it reaches,
// which is the whole identity of a file system that holds no data of its own.
func (fsys *grpcFS) String() string {
	return types.Description(map[string]any{"grpcFS": uri.OrDefault(fsys, fsys.address)})
}

// DeviceInfo reports the connection as the device behind every path.
//
// What the served file system sits on is deliberately not asked for: the
// remote flag of types.DeviceInfo says that a file system reports remoteness to
// itself, and to a client of this service everything is already remote, which is
// why the flag is not on the wire at all. What the client knows is the address,
// and a network address is the answer.
func (fsys *grpcFS) DeviceInfo() types.DeviceMap {
	return types.NewDeviceMap(types.NewDeviceInfo(fsys.address, "network", 1, true))
}

// IsSame reports whether other reaches the same address, which is the only
// comparison a client can make: the service leaves IsSame off the wire because
// it is not a question about locations, and what a client can compare is where
// it is looking. Two clients of one server do hold one set of data — a write
// through either is seen by the other — and two clients of one address cannot be
// told apart from here.
func (fsys *grpcFS) IsSame(other fs.FS) bool {
	otherGRPC, ok := types.InnermostFS(other).(*grpcFS)
	return ok && fsys.address == otherGRPC.address && fsys.scheme == otherGRPC.scheme
}
