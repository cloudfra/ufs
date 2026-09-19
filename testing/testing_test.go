// Copyright 2026 Jeremy Edwards
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
	"testing"
)

func TestTestAssetsFS(t *testing.T) {
	fsys := TestAssetsFS()
	want := "testing/testassets/files/index.html"
	data, err := fsys.ReadFile("testassets/files/index.html")
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if got != want {
		t.Errorf("got: %q, want: %q", got, want)
	}
}
