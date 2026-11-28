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

// Remove and RemoveAll are the [types.RemoveFS] and [types.RemoveAllFS]
// halves of [types.WriteFS]. The call helper is shared by the three rpcs that
// answer only whether they worked, which is why it sits here.

import (
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/proto"
)

// Remove deletes the file or empty directory at name.
func (fsys *grpcFS) Remove(name string) error {
	return fsys.call("remove", name, func() error {
		_, err := fsys.client.Remove(fsys.ctx, &proto.RemoveRequest{Name: name})
		return err
	})
}

// RemoveAll deletes name and everything below it.
func (fsys *grpcFS) RemoveAll(name string) error {
	return fsys.call("removeall", name, func() error {
		_, err := fsys.client.RemoveAll(fsys.ctx, &proto.RemoveAllRequest{Name: name})
		return err
	})
}

// call runs one rpc that answers only whether it worked, naming op and name in
// the error it returns.
func (fsys *grpcFS) call(op, name string, rpc func() error) error {
	if err := fsys.closedErr(op, name); err != nil {
		return err
	}
	if err := pathutil.Validate(op, name); err != nil {
		return err
	}
	if err := rpc(); err != nil {
		return pathError(op, name, err)
	}
	return nil
}
