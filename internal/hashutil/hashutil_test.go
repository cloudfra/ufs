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
	"encoding/hex"
	"strings"
	"testing"
)

var testPayload = bytes.Repeat([]byte("ufs hashutil test payload\n"), 1024)

// Hex digests of testPayload.
const (
	testPayloadSHA256     = "612fbc92c582585320a998a34686ad0688ef92737031899138ee5440db0b2ecd"
	testPayloadSHA384     = "73941a78be51ae1ce97a448607826dd4671bc749ffbb9f8c2c1af212cf01d8bf36eecbfa6c458dfcea12208c19bd2cd0"
	testPayloadSHA512     = "29a9626b86a9f7befad1a4bd0b17ae74a10ab71934c8460087c91567693fc880574ae9bc1552ed300357de13e55b9e07e42a470d1d828c56632d252a65128abb"
	testPayloadSHA3Sum256 = "0f81054cf843437815d67168bb62dabcf5c210ba3a4e85f76b42be208a7baf26"
	testPayloadSHA3Sum384 = "6585611740ef8002f0fdfa0e8fddf301998d49359c871a4a1880d28d6891762beb813d3c38d20ae765b560f0cd0fdc15"
	testPayloadSHA3Sum512 = "3c006bc4ae2bbb6d78c19a2f595d4d95a2a143f38d611d46ef03a29729a641fe32b959af261ab81bf62895d120942783f236194fddd56d9a229bcf9b2ffd0f28"
)

func TestNew(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		algorithm string
		want      string
	}{
		{algorithm: "sha256", want: testPayloadSHA256},
		{algorithm: "sha384", want: testPayloadSHA384},
		{algorithm: "sha512", want: testPayloadSHA512},
		{algorithm: "sha3-256", want: testPayloadSHA3Sum256},
		{algorithm: "sha3-384", want: testPayloadSHA3Sum384},
		{algorithm: "sha3-512", want: testPayloadSHA3Sum512},
		{algorithm: "SHA256", want: testPayloadSHA256},
		{algorithm: "Sha512", want: testPayloadSHA512},
		{algorithm: "SHA3-256", want: testPayloadSHA3Sum256},
	}
	for _, tc := range testCases {
		t.Run(tc.algorithm, func(t *testing.T) {
			t.Parallel()
			h, err := New(tc.algorithm)
			if err != nil {
				t.Fatalf("New(%q) = %v", tc.algorithm, err)
			}
			if _, err := h.Write(testPayload); err != nil {
				t.Fatal(err)
			}
			if got := hex.EncodeToString(h.Sum(nil)); got != tc.want {
				t.Errorf("New(%q) digest = %s, want %s", tc.algorithm, got, tc.want)
			}
		})
	}
}

func TestNewErrors(t *testing.T) {
	t.Parallel()
	for _, algorithm := range []string{"", "md5", "sha1", "sha-256", " sha256", "sha256 ", "sha512/256", "sha3", "sha3_256", "sha3-224"} {
		t.Run(algorithm, func(t *testing.T) {
			t.Parallel()
			h, err := New(algorithm)
			if err == nil {
				t.Fatalf("New(%q) = %v, want error", algorithm, h)
			}
			if h != nil {
				t.Errorf("New(%q) = %v, want nil on error", algorithm, h)
			}
			if !strings.Contains(err.Error(), "unsupported checksum algorithm") {
				t.Errorf("error = %v, want mention of unsupported checksum algorithm", err)
			}
		})
	}
}

func TestParseChecksum(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name          string
		value         string
		wantAlgorithm string
		wantDigest    string
	}{
		{name: "bare digest", value: testPayloadSHA256, wantAlgorithm: "sha256", wantDigest: testPayloadSHA256},
		{name: "bare uppercase digest", value: strings.ToUpper(testPayloadSHA256), wantAlgorithm: "sha256", wantDigest: testPayloadSHA256},
		{name: "bare mixed case digest", value: strings.ToUpper(testPayloadSHA256[:32]) + testPayloadSHA256[32:], wantAlgorithm: "sha256", wantDigest: testPayloadSHA256},
		{name: "sha256", value: "sha256:" + testPayloadSHA256, wantAlgorithm: "sha256", wantDigest: testPayloadSHA256},
		{name: "sha384", value: "sha384:" + testPayloadSHA384, wantAlgorithm: "sha384", wantDigest: testPayloadSHA384},
		{name: "sha512", value: "sha512:" + testPayloadSHA512, wantAlgorithm: "sha512", wantDigest: testPayloadSHA512},
		{name: "sha3-256", value: "sha3-256:" + testPayloadSHA3Sum256, wantAlgorithm: "sha3-256", wantDigest: testPayloadSHA3Sum256},
		{name: "sha3-384", value: "sha3-384:" + testPayloadSHA3Sum384, wantAlgorithm: "sha3-384", wantDigest: testPayloadSHA3Sum384},
		{name: "sha3-512", value: "sha3-512:" + testPayloadSHA3Sum512, wantAlgorithm: "sha3-512", wantDigest: testPayloadSHA3Sum512},
		{name: "uppercase algorithm", value: "SHA512:" + testPayloadSHA512, wantAlgorithm: "sha512", wantDigest: testPayloadSHA512},
		{name: "uppercase sha3 algorithm and digest", value: "SHA3-256:" + strings.ToUpper(testPayloadSHA3Sum256), wantAlgorithm: "sha3-256", wantDigest: testPayloadSHA3Sum256},
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
			if got := hex.EncodeToString(checksum.want); got != tc.wantDigest {
				t.Errorf("expected digest = %s, want %s", got, tc.wantDigest)
			}
		})
	}
}

func TestParseChecksumErrors(t *testing.T) {
	t.Parallel()
	hex256 := strings.Repeat("ab", len(testPayloadSHA256)/2)
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
		{name: "sha256 digest labeled sha3-512", value: "sha3-512:" + hex256, wantErr: "invalid sha3-512 checksum"},
		{name: "sha3 without size", value: "sha3:" + hex256, wantErr: `unsupported checksum algorithm "sha3"`},
		{name: "odd length digest", value: "sha256:" + hex256[:63], wantErr: "invalid sha256 checksum"},
		{name: "non hex digest", value: "sha256:" + strings.Repeat("zz", len(testPayloadSHA256)/2), wantErr: "invalid sha256 checksum"},
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
	for _, value := range []string{
		testPayloadSHA256,
		"sha256:" + testPayloadSHA256,
		"sha384:" + testPayloadSHA384,
		"sha512:" + testPayloadSHA512,
		"sha3-256:" + testPayloadSHA3Sum256,
		"sha3-384:" + testPayloadSHA3Sum384,
		"sha3-512:" + testPayloadSHA3Sum512,
	} {
		t.Run(value[:8], func(t *testing.T) {
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
