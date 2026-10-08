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

package hashutil

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"strings"
	"testing"
)

var testPayload = bytes.Repeat([]byte("ufs hashutil test payload\n"), 1024)

func TestNewHash(t *testing.T) {
	t.Parallel()
	sum256 := sha256.Sum256(testPayload)
	sum384 := sha512.Sum384(testPayload)
	sum512 := sha512.Sum512(testPayload)

	testCases := []struct {
		algorithm string
		want      []byte
	}{
		{algorithm: "sha256", want: sum256[:]},
		{algorithm: "sha384", want: sum384[:]},
		{algorithm: "sha512", want: sum512[:]},
		{algorithm: "SHA256", want: sum256[:]},
		{algorithm: "Sha512", want: sum512[:]},
	}
	for _, tc := range testCases {
		t.Run(tc.algorithm, func(t *testing.T) {
			t.Parallel()
			h, err := NewHash(tc.algorithm)
			if err != nil {
				t.Fatalf("NewHash(%q) = %v", tc.algorithm, err)
			}
			if _, err := h.Write(testPayload); err != nil {
				t.Fatal(err)
			}
			if got := h.Sum(nil); !bytes.Equal(got, tc.want) {
				t.Errorf("NewHash(%q) digest = %x, want %x", tc.algorithm, got, tc.want)
			}
		})
	}
}

func TestNewHashErrors(t *testing.T) {
	t.Parallel()
	for _, algorithm := range []string{"", "md5", "sha1", "sha-256", " sha256", "sha256 ", "sha512/256"} {
		t.Run(algorithm, func(t *testing.T) {
			t.Parallel()
			h, err := NewHash(algorithm)
			if err == nil {
				t.Fatalf("NewHash(%q) = %v, want error", algorithm, h)
			}
			if h != nil {
				t.Errorf("NewHash(%q) = %v, want nil on error", algorithm, h)
			}
			if !strings.Contains(err.Error(), "unsupported checksum algorithm") {
				t.Errorf("error = %v, want mention of unsupported checksum algorithm", err)
			}
		})
	}
}

func TestParseChecksum(t *testing.T) {
	t.Parallel()
	sum256 := sha256.Sum256(testPayload)
	sum384 := sha512.Sum384(testPayload)
	sum512 := sha512.Sum512(testPayload)
	hex256 := hex.EncodeToString(sum256[:])

	testCases := []struct {
		name          string
		value         string
		wantAlgorithm string
		wantDigest    []byte
	}{
		{name: "bare digest", value: hex256, wantAlgorithm: "sha256", wantDigest: sum256[:]},
		{name: "bare uppercase digest", value: strings.ToUpper(hex256), wantAlgorithm: "sha256", wantDigest: sum256[:]},
		{name: "bare mixed case digest", value: strings.ToUpper(hex256[:32]) + hex256[32:], wantAlgorithm: "sha256", wantDigest: sum256[:]},
		{name: "sha256", value: "sha256:" + hex256, wantAlgorithm: "sha256", wantDigest: sum256[:]},
		{name: "sha384", value: "sha384:" + hex.EncodeToString(sum384[:]), wantAlgorithm: "sha384", wantDigest: sum384[:]},
		{name: "sha512", value: "sha512:" + hex.EncodeToString(sum512[:]), wantAlgorithm: "sha512", wantDigest: sum512[:]},
		{name: "uppercase algorithm", value: "SHA512:" + hex.EncodeToString(sum512[:]), wantAlgorithm: "sha512", wantDigest: sum512[:]},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checksum, err := ParseChecksum(tc.value)
			if err != nil {
				t.Fatalf("ParseChecksum(%q) = %v", tc.value, err)
			}
			if checksum.algorithm != tc.wantAlgorithm {
				t.Errorf("algorithm = %q, want %q", checksum.algorithm, tc.wantAlgorithm)
			}
			if !bytes.Equal(checksum.want, tc.wantDigest) {
				t.Errorf("expected digest = %x, want %x", checksum.want, tc.wantDigest)
			}
		})
	}
}

func TestParseChecksumErrors(t *testing.T) {
	t.Parallel()
	hex256 := strings.Repeat("ab", sha256.Size)
	testCases := []struct {
		name    string
		value   string
		wantErr string
	}{
		{name: "empty", value: "", wantErr: "invalid sha256 checksum"},
		{name: "algorithm without digest", value: "sha512:", wantErr: "invalid sha512 checksum"},
		{name: "short digest", value: "sha256:" + hex256[:62], wantErr: "got 62 hex characters, want 64"},
		{name: "long digest", value: "sha256:" + hex256 + "ab", wantErr: "got 66 hex characters, want 64"},
		{name: "sha256 digest labeled sha384", value: "sha384:" + hex256, wantErr: "got 64 hex characters, want 96"},
		{name: "sha256 digest labeled sha512", value: "sha512:" + hex256, wantErr: "got 64 hex characters, want 128"},
		{name: "odd length digest", value: "sha256:" + hex256[:63], wantErr: "invalid sha256 checksum"},
		{name: "non hex digest", value: "sha256:" + strings.Repeat("zz", sha256.Size), wantErr: "invalid sha256 checksum"},
		{name: "unsupported algorithm", value: "md5:d41d8cd98f00b204e9800998ecf8427e", wantErr: `unsupported checksum algorithm "md5"`},
		{name: "empty algorithm", value: ":" + hex256, wantErr: `unsupported checksum algorithm ""`},
		{name: "padded algorithm", value: " sha256:" + hex256, wantErr: "unsupported checksum algorithm"},
		{name: "second colon", value: "sha256:sha256:" + hex256, wantErr: "invalid sha256 checksum"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checksum, err := ParseChecksum(tc.value)
			if err == nil {
				t.Fatalf("ParseChecksum(%q) = %+v, want error containing %q", tc.value, checksum, tc.wantErr)
			}
			if checksum != nil {
				t.Errorf("ParseChecksum(%q) = %+v, want nil on error", tc.value, checksum)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want mention of %q", err, tc.wantErr)
			}
		})
	}
}

func TestChecksumVerify(t *testing.T) {
	t.Parallel()
	sum256 := sha256.Sum256(testPayload)
	sum384 := sha512.Sum384(testPayload)
	sum512 := sha512.Sum512(testPayload)

	for _, value := range []string{
		hex.EncodeToString(sum256[:]),
		"sha256:" + hex.EncodeToString(sum256[:]),
		"sha384:" + hex.EncodeToString(sum384[:]),
		"sha512:" + hex.EncodeToString(sum512[:]),
	} {
		t.Run(value[:7], func(t *testing.T) {
			t.Parallel()
			checksum, err := ParseChecksum(value)
			if err != nil {
				t.Fatalf("ParseChecksum(%q) = %v", value, err)
			}
			if err := checksum.Verify(); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
				t.Errorf("Verify() before any data = %v, want checksum mismatch", err)
			}

			// Written in two parts to check that Write accumulates.
			half := len(testPayload) / 2
			for _, part := range [][]byte{testPayload[:half], testPayload[half:]} {
				if n, err := checksum.Write(part); n != len(part) || err != nil {
					t.Fatalf("Write() = (%d, %v), want (%d, nil)", n, err, len(part))
				}
			}
			if err := checksum.Verify(); err != nil {
				t.Errorf("Verify() = %v", err)
			}
			if err := checksum.Verify(); err != nil {
				t.Errorf("second Verify() = %v", err)
			}

			if _, err := checksum.Write([]byte("extra")); err != nil {
				t.Fatal(err)
			}
			err = checksum.Verify()
			if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
				t.Fatalf("Verify() after extra data = %v, want checksum mismatch", err)
			}
			if !strings.Contains(err.Error(), "expected "+checksum.algorithm+":"+hex.EncodeToString(checksum.want)) {
				t.Errorf("error = %v, want the expected digest with its algorithm", err)
			}
		})
	}
}
