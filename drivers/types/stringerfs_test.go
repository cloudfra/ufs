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

package types

import "testing"

func TestDescriptionLeaf(t *testing.T) {
	t.Parallel()
	got := Description(map[string]any{"memFS": "memory://test"})
	want := `{"memFS":"memory://test"}`
	if got != want {
		t.Errorf("Description() = %q, want %q", got, want)
	}
}

func TestDescriptionSortsKeys(t *testing.T) {
	t.Parallel()
	parts := map[string]any{
		"/d": "memory://d",
		"/c": "memory://c",
		"/":  Nested(`{"nullFS":"null://"}`),
	}
	first := Description(map[string]any{"nestFS": parts})
	want := `{"nestFS":{"/":{"nullFS":"null://"},"/c":"memory://c","/d":"memory://d"}}`
	if first != want {
		t.Errorf("Description() = %q, want %q", first, want)
	}
	// A map's iteration order varies, so a description must not depend on it.
	for range 8 {
		if got := Description(map[string]any{"nestFS": parts}); got != first {
			t.Errorf("Description() = %q, want the same description %q", got, first)
		}
	}
}

func TestDescriptionKeepsURISeparators(t *testing.T) {
	t.Parallel()
	got := Description(map[string]any{"gitFS": "git:///repo?ref=main&ro=true"})
	want := `{"gitFS":"git:///repo?ref=main&ro=true"}`
	if got != want {
		t.Errorf("Description() = %q, want %q", got, want)
	}
}

func TestNestedDescription(t *testing.T) {
	t.Parallel()
	inner := Description(map[string]any{"memFS": "memory://test"})
	got := Description(map[string]any{"telemetryFS": Nested(inner)})
	want := `{"telemetryFS":{"memFS":"memory://test"}}`
	if got != want {
		t.Errorf("Description() of a nested description = %q, want %q", got, want)
	}
}

func TestNestedNotJSON(t *testing.T) {
	t.Parallel()
	got := Description(map[string]any{"readWrapFS": Nested("fstest.MapFS")})
	want := `{"readWrapFS":"fstest.MapFS"}`
	if got != want {
		t.Errorf("Description() of a description that is not JSON = %q, want %q", got, want)
	}
}
