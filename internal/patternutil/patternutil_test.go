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
//
// Neither the name of the copyright holder nor the names of its contributors
// may be used to endorse or promote products derived from this software
// without specific prior written permission.

package patternutil

import (
	"errors"
	"path"
	"strings"
	"testing"
)

func TestMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		patterns []string
		path     string
		want     bool
	}{
		{
			name:     "a pattern matching the whole name",
			patterns: []string{"*.key"},
			path:     "server.key",
			want:     true,
		},
		{
			name:     "the same pattern matching one component of a deeper name",
			patterns: []string{"*.key"},
			path:     "deploy/prod/server.key",
			want:     true,
		},
		{
			name:     "a directory name matching everything below it",
			patterns: []string{".ssh"},
			path:     ".ssh/id_ed25519",
			want:     true,
		},
		{
			name:     "the same directory name several levels down",
			patterns: []string{"secrets"},
			path:     "checkout/deploy/secrets/api-token",
			want:     true,
		},
		{
			name:     "a pattern holding a slash matching the whole path",
			patterns: []string{"keys/*.key"},
			path:     "keys/api.key",
			want:     true,
		},
		{
			name:     "the same slashed pattern not matching a different parent",
			patterns: []string{"keys/*.key"},
			path:     "deploy/keys/api.key",
			want:     false,
		},
		{
			name:     "a pattern holding no slash matching no whole path",
			patterns: []string{"server.key"},
			path:     "deploy/keys/server.key",
			want:     true,
		},
		{
			name:     "a pattern holding no slash not matching across a slash",
			patterns: []string{"keys/server.key"},
			path:     "deploy/keys/server.key",
			want:     false,
		},
		{
			name:     "a backslash quoting a star for a literal match",
			patterns: []string{`server\*key`},
			path:     "server*key",
			want:     true,
		},
		{
			name:     "an exact name not matching a name merely starting with it",
			patterns: []string{".env"},
			path:     ".env.local",
			want:     false,
		},
		{
			name:     "a starred suffix matching the rest of a dotted name",
			patterns: []string{".env.*"},
			path:     ".env.local",
			want:     true,
		},
		{
			name:     "a star matching every single component",
			patterns: []string{"*"},
			path:     "dir/file",
			want:     true,
		},
		{
			name:     "no pattern matching an unprotected name",
			patterns: []string{"*.key", "*.pem"},
			path:     "notes.txt",
			want:     false,
		},
		{
			name:     "an empty list matching nothing",
			patterns: nil,
			path:     "server.key",
			want:     false,
		},
		{
			name:     "a malformed pattern matching nothing",
			patterns: []string{"["},
			path:     "[",
			want:     false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := Match(test.patterns, test.path); got != test.want {
				t.Errorf("Match(%q, %q) = %v, want %v", test.patterns, test.path, got, test.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	if err := Validate(nil); err != nil {
		t.Errorf("Validate(nil) = %v, want nil", err)
	}
	if err := Validate([]string{"*.key", ".env.*", "keys/*.key", "secrets"}); err != nil {
		t.Errorf("Validate of four valid patterns = %v, want nil", err)
	}

	err := Validate([]string{"*.key", "["})
	if err == nil {
		t.Fatalf("Validate(%q) = nil, want an error for the malformed pattern", []string{"*.key", "["})
	}
	if !errors.Is(err, path.ErrBadPattern) {
		t.Errorf("Validate of %q = %v, want an error wrapping %v", "["+" etc", err, path.ErrBadPattern)
	}
	if !strings.Contains(err.Error(), `"["`) {
		t.Errorf("Validate of a malformed pattern = %q, want it to name the pattern", err)
	}
}
