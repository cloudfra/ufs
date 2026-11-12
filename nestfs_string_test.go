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

import "testing"

func TestNestFSString(t *testing.T) {
	fsys := makeNestFS(t.Context(), mustBaseFS(t, "null://"))
	for _, name := range []string{"d", "c"} {
		if err := fsys.addMount(name, makeNestFS(t.Context(), mustBaseFS(t, "memory://"+name))); err != nil {
			t.Fatalf("addMount(%q) = %v, want nil", name, err)
		}
	}

	got := fsys.String()
	want := `{"nestFS":{"/":{"nullFS":"null:?ro=true"},"/c":{"memFS":"memory://c"},"/d":{"memFS":"memory://d"}}}`
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
