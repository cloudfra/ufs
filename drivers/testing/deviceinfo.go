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

package testing

import (
	"io/fs"
	"path"
	"sort"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// deviceInfoRootKey is the key [types.DeviceInfoGet.DeviceInfo] documents as
// holding the device that backs the root of the FS. It is also the key nestFS
// reads to decide whether a mount holds a file it can let go of, so a map
// without it describes nothing about the file system that implements it.
const deviceInfoRootKey = "."

// deviceInfoGet runs the [types.DeviceInfoGet] conformance suite.
//
// The suite does not branch on Args.ReadOnly or Args.Bucket: which device backs
// a file system is not changed by the file system being read-only or by it
// having no explicit directories.
func deviceInfoGet(t *testing.T, args Args, createFSFunc func(t *testing.T) types.SimpleFS) {
	t.Run("DeviceInfoGet", func(t *testing.T) {
		requireType[types.DeviceInfoGet](t, args)
		t.Parallel()
		createDeviceInfoFSFunc := requireFSTypeFunc[types.DeviceInfoGetFS](t, createFSFunc)

		deviceInfoRoot(t, createDeviceInfoFSFunc)
		deviceInfoEntries(t, createDeviceInfoFSFunc)
		deviceInfoStable(t, createDeviceInfoFSFunc)
	})
}

// deviceInfoRoot checks that the map holds the documented root entry and that
// the entry describes a device rather than the zero [types.DeviceInfo].
func deviceInfoRoot(t *testing.T, createFSFunc func(t *testing.T) types.DeviceInfoGetFS) {
	t.Run("DeviceInfoRoot", func(t *testing.T) {
		t.Parallel()
		fsys := newDeviceInfoFS(t, createFSFunc)
		dm := fsys.DeviceInfo()

		if dm == nil {
			t.Fatalf("DeviceInfo() = nil, want a [types.DeviceMap] holding the key %q: a nil map reports the zero device for every path", deviceInfoRootKey)
		}
		root, ok := dm[deviceInfoRootKey]
		if !ok {
			t.Fatalf("DeviceInfo() has no %q entry, it has %v: nestFS reads this key to decide whether a mount holds a file, so a map without it describes nothing", deviceInfoRootKey, deviceInfoKeys(dm))
		}
		assertDeviceInfo(t, root, "DeviceInfo()["+deviceInfoRootKey+"]")
	})
}

// deviceInfoEntries checks every entry of the map: each key is a valid
// device-relative path, each entry describes a real device, and an entry below
// the root names a different device from the one backing its parent.
func deviceInfoEntries(t *testing.T, createFSFunc func(t *testing.T) types.DeviceInfoGetFS) {
	t.Run("DeviceInfoEntries", func(t *testing.T) {
		t.Parallel()
		fsys := newDeviceInfoFS(t, createFSFunc)
		dm := fsys.DeviceInfo()

		for _, key := range deviceInfoKeys(dm) {
			t.Run(key, func(t *testing.T) {
				t.Parallel()
				if !fs.ValidPath(key) {
					t.Errorf("DeviceInfo() has the key %q, want a valid device-relative path: [types.DeviceMap] keys are matched path element by path element, so a key that is not cleaned never matches the path it names", key)
					return
				}
				info := dm[key]
				assertDeviceInfo(t, info, "DeviceInfo()["+key+"]")

				if key == deviceInfoRootKey {
					// The root has no parent to compare it to.
					return
				}
				// [types.DeviceInfoGet.DeviceInfo] says the map is minimal and
				// names a path only where the backing device changes, so an
				// entry that repeats the device backing its nearest listed
				// ancestor says nothing. An entry may still name a device that
				// appears higher in the chain, which is why the comparison is
				// with the device backing the parent path rather than with
				// every entry above it.
				t.Run("EntryNotRedundant", func(t *testing.T) {
					parent := path.Dir(key)
					if backing := dm.Backing(parent); backing.Name() == info.Name() {
						t.Errorf("DeviceInfo()[%q] = %s repeats the device %s that already backs %q: the map is minimal and names only the paths where the backing device changes", key, info, backing, parent)
					}
				})
			})
		}
	})
}

// deviceInfoStable checks that asking twice reports the same devices, so that a
// caller cannot walk a file system with a different answer per call.
//
// Whether the returned map may be mutated by its caller, and whether two calls
// return the same map, are not checked: some drivers hand out a map they share
// with the package, which is a contract question for the owner rather than a
// fact this suite can settle.
func deviceInfoStable(t *testing.T, createFSFunc func(t *testing.T) types.DeviceInfoGetFS) {
	t.Run("DeviceInfoStable", func(t *testing.T) {
		t.Parallel()
		fsys := newDeviceInfoFS(t, createFSFunc)
		first := fsys.DeviceInfo()
		second := fsys.DeviceInfo()

		if diff := diffDeviceMaps(first, second); diff != "" {
			t.Errorf("DeviceInfo() a second time differs from the first (-first +second):\n%s", diff)
		}
	})
}

// assertDeviceInfo checks that info describes a device: it has a name, a type,
// and asks for at least one thread.
func assertDeviceInfo(t *testing.T, info types.DeviceInfo, what string) {
	t.Helper()
	if info == (types.DeviceInfo{}) {
		t.Errorf("%s = %s, want a [types.DeviceInfo] describing a device", what, info)
		return
	}
	if info.Name() == "" {
		t.Errorf("%s has an empty Name(), want the device name as the OS or the file system names it", what)
	}
	if info.DeviceType() == "" {
		t.Errorf("%s has an empty DeviceType(), want the kind of device that backs the file system: nestFS switches on this to decide whether a mount holds a file", what)
	}
	if info.ThreadCount() < 1 {
		t.Errorf("%s ThreadCount() = %d, want at least 1: [types.DeviceInfo] documents a recommended thread count, and 0 asks callers to do no work at all", what, info.ThreadCount())
	}
}

// newDeviceInfoFS returns a file system from createFS and closes it when the
// test ends.
func newDeviceInfoFS[T types.DeviceInfoGetFS](t *testing.T, createFS func(*testing.T) T) T {
	t.Helper()
	fsys := createFS(t)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	return fsys
}

// deviceInfoKeys returns the keys of dm sorted, so that a report lists them the
// same way every run.
func deviceInfoKeys(dm types.DeviceMap) []string {
	keys := make([]string, 0, len(dm))
	for key := range dm {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// diffDeviceMaps reports how two maps of device entries differ. [types.DeviceInfo]
// holds unexported fields, so it is compared directly rather than through go-cmp.
func diffDeviceMaps(first, second types.DeviceMap) string {
	diff := ""
	for _, key := range deviceInfoKeys(first) {
		info, ok := second[key]
		if !ok {
			diff += "- " + key + " " + first[key].String() + "\n"
			continue
		}
		if info != first[key] {
			diff += "! " + key + " " + first[key].String() + " -> " + info.String() + "\n"
		}
	}
	for _, key := range deviceInfoKeys(second) {
		if _, ok := first[key]; !ok {
			diff += "+ " + key + " " + second[key].String() + "\n"
		}
	}
	return diff
}
