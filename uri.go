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

package ufs

import (
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/drivers/uri"
)

// URIOrDefault returns fsys's canonical URI, or value if fsys reports none. A
// file system that owns its data reports a URI even when no URI can name it, as
// [types.URIGet] requires, so value is used when fsys fails or is a wrapper of
// a file system that reported none.
//
// It is [uri.OrDefault], kept here because it is the name the base package has
// always exported to its drivers.
func URIOrDefault(fsys types.URIGet, value string) string {
	return uri.OrDefault(fsys, value)
}

// splitURIOptions removes the options query parameter from name and returns the
// decorators it lists. A name without the parameter is returned unchanged. Every
// pair of the parameter goes, the way [uri.RemoveParameter] removes a repeated
// parameter, and the decorators come from the first value written. The other
// parameters keep their order and escaping, which is why the name is reduced
// pair by pair instead of through url.Values.
//
// It is [uri.RemoveOptions], which holds the options parameter and its records.
func splitURIOptions(name string) (string, []MountOption, error) {
	return uri.RemoveOptions(name)
}
