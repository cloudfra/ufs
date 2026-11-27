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
// answer only whether they worked, which is why it sits here. The three verbs
// name the operation they are doing rather than sharing one name, which is why the
// helper takes the op as an argument; none of them is a wrapper around another,
// and none of them takes the lifetime lock — the section is the helper's.

import (
	"io/fs"

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
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
// the error it returns. The closed check and the rpc are one read-lock section;
// the path validation stays outside it.
//
// Remove, RemoveAll and [grpcFS.MkdirAll] are thin verbs over it and take no lock
// of their own — a read lock held across a call into a sibling that takes the lock
// again deadlocks the pair against a Close waiting between them. What each passes
// as rpc is therefore a call on the connection and never a call back into this
// file system, which is what lets the section hold the lock across it.
func (fsys *grpcFS) call(op, name string, rpc func() error) error {
	if err := pathutil.Validate(op, name); err != nil {
		return err
	}
	fsys.mu.RLock()
	if fsys.closed.Load() {
		fsys.mu.RUnlock()
		return ufserrors.NewPathError(op, name, fs.ErrClosed)
	}
	err := rpc()
	fsys.mu.RUnlock()
	if err != nil {
		return pathError(op, name, err)
	}
	return nil
}
