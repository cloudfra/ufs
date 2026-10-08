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

// Package hashutil creates hashes by algorithm name and verifies data against
// an expected checksum.
package hashutil

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"strings"
)

// defaultAlgorithm is the algorithm of a checksum that does not name one.
const defaultAlgorithm = "sha256"

// NewHash returns a new hash for the named algorithm: sha256, sha384 or
// sha512. The name is case-insensitive. An error is returned for any other
// algorithm.
func NewHash(algorithm string) (hash.Hash, error) {
	switch strings.ToLower(algorithm) {
	case "sha256":
		return sha256.New(), nil
	case "sha384":
		return sha512.New384(), nil
	case "sha512":
		return sha512.New(), nil
	default:
		return nil, fmt.Errorf("unsupported checksum algorithm %q, want sha256, sha384 or sha512", algorithm)
	}
}

// Checksum verifies data against an expected digest. Write the data to it,
// then call Verify. Create one with ParseChecksum.
type Checksum struct {
	algorithm string
	hash      hash.Hash
	want      []byte
}

// ParseChecksum parses value as "<algorithm>:<hex digest>", where algorithm is
// one accepted by NewHash. A value without an algorithm is a sha256 digest.
// Case is ignored. An error is returned if the algorithm is unsupported or the
// digest is not hex of the algorithm's length.
func ParseChecksum(value string) (*Checksum, error) {
	algorithm, digest, ok := strings.Cut(value, ":")
	if !ok {
		algorithm, digest = defaultAlgorithm, value
	}
	algorithm = strings.ToLower(algorithm)

	h, err := NewHash(algorithm)
	if err != nil {
		return nil, err
	}
	want, err := hex.DecodeString(digest)
	if err != nil {
		return nil, fmt.Errorf("invalid %s checksum %q: %w", algorithm, digest, err)
	}
	if len(want) != h.Size() {
		return nil, fmt.Errorf("invalid %s checksum %q: got %d hex characters, want %d", algorithm, digest, len(digest), hex.EncodedLen(h.Size()))
	}
	return &Checksum{algorithm: algorithm, hash: h, want: want}, nil
}

// Write adds p to the data being verified. It never returns an error.
func (c *Checksum) Write(p []byte) (int, error) {
	return c.hash.Write(p)
}

// Verify reports whether the digest of the data written so far matches the
// expected digest, returning an error describing the mismatch if not.
func (c *Checksum) Verify() error {
	if got := c.hash.Sum(nil); !bytes.Equal(got, c.want) {
		return fmt.Errorf("checksum mismatch: expected %s:%x, got %s:%x", c.algorithm, c.want, c.algorithm, got)
	}
	return nil
}
