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

package core

import (
	"context"
	"strings"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/drivers/uri"
)

const (
	grpcFSPrefix = "grpc:"
)

var (
	grpcDeviceInfo    = types.NewDeviceInfo("grpc", "grpc", 1, false)
	grpcDeviceInfoMap = types.NewDeviceMap(grpcDeviceInfo)
)

func init() {
	ufs.Register(ufs.Driver{
		Name:       "grpc",
		CreateFunc: newGrpcFS,
		MatchFunc:  isGrpcFSUri,
		Priority:   1,
		Standard:   true,
		ReadWrite:  true,
	})
}

func isGrpcFSUri(name string) bool {
	return strings.HasPrefix(name, grpcFSPrefix)
}

func newGrpcFS(ctx context.Context, name string) (types.WriteFS, error) {
	serving, err := uri.IsServingAddressString(name)
	if err != nil {
		return nil, err
	}
	if serving {
		return makeGrpcServerFS(ctx, name)
	}
	return makeGrpcClientFS(ctx, name)
}

// grpc implementation needs a full reboot
