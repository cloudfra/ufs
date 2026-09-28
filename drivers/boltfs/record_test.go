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

//go:build !wasm

package boltfs

import (
	"bytes"
	"errors"
	"io/fs"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/cloudfra/ufs/proto"
)

// TestDecodeBoltRecordMatchesProto verifies the hand-rolled decoder agrees
// with proto.Unmarshal for every shape of record the driver writes, plus
// records other writers could produce (absent fields, pre-epoch times).
func TestDecodeBoltRecordMatchesProto(t *testing.T) {
	testCases := []struct {
		name string
		rec  *pb.BoltFileRecord
	}{
		{name: "empty", rec: &pb.BoltFileRecord{}},
		{name: "file", rec: &pb.BoltFileRecord{Mode: uint32(fs.ModePerm), ModTime: timestamppb.New(time.Date(2026, 9, 24, 1, 2, 3, 456789, time.UTC)), Content: []byte("hello world")}},
		{name: "dir", rec: &pb.BoltFileRecord{Mode: uint32(fs.ModeDir | 0o755), ModTime: timestamppb.New(time.Unix(1, 0))}},
		{name: "pre_epoch", rec: &pb.BoltFileRecord{Mode: 0o600, ModTime: timestamppb.New(time.Date(1900, 1, 1, 0, 0, 0, 999999999, time.UTC))}},
		{name: "no_mod_time", rec: &pb.BoltFileRecord{Mode: 0o644, Content: []byte{0, 1, 2}}},
		{name: "large_content", rec: &pb.BoltFileRecord{Content: bytes.Repeat([]byte("x"), 1<<16)}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := proto.Marshal(tc.rec)
			if err != nil {
				t.Fatal(err)
			}
			got, err := decodeBoltRecord(data)
			if err != nil {
				t.Fatalf("decodeBoltRecord() = %v, want nil", err)
			}
			var want pb.BoltFileRecord
			if err := proto.Unmarshal(data, &want); err != nil {
				t.Fatal(err)
			}
			if got.mode != fs.FileMode(want.GetMode()) {
				t.Errorf("mode = %v, want %v", got.mode, fs.FileMode(want.GetMode()))
			}
			if !got.modTime.Equal(want.GetModTime().AsTime()) {
				t.Errorf("modTime = %v, want %v", got.modTime, want.GetModTime().AsTime())
			}
			if !bytes.Equal(got.content, want.GetContent()) {
				t.Errorf("content length = %d, want %d", len(got.content), len(want.GetContent()))
			}
		})
	}
}

func TestEncodeDecodeBoltRecordRoundTrip(t *testing.T) {
	modTime := time.Date(2026, 9, 24, 12, 0, 0, 123, time.UTC)
	data, err := encodeBoltRecord(fs.ModeDir|0o750, modTime, []byte("content"))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := decodeBoltRecord(data)
	if err != nil {
		t.Fatal(err)
	}
	if rec.mode != fs.ModeDir|0o750 {
		t.Errorf("mode = %v, want %v", rec.mode, fs.ModeDir|0o750)
	}
	if !rec.modTime.Equal(modTime) {
		t.Errorf("modTime = %v, want %v", rec.modTime, modTime)
	}
	if string(rec.content) != "content" {
		t.Errorf("content = %q, want %q", rec.content, "content")
	}
}

func TestDecodeBoltRecordSkipsUnknownFields(t *testing.T) {
	data, err := encodeBoltRecord(0o644, time.Unix(100, 0), []byte("abc"))
	if err != nil {
		t.Fatal(err)
	}
	// Append fields a future version might add, of every wire type.
	data = protowire.AppendTag(data, 99, protowire.VarintType)
	data = protowire.AppendVarint(data, 7)
	data = protowire.AppendTag(data, 100, protowire.BytesType)
	data = protowire.AppendBytes(data, []byte("future"))
	data = protowire.AppendTag(data, 101, protowire.Fixed64Type)
	data = protowire.AppendFixed64(data, 1)
	data = protowire.AppendTag(data, 102, protowire.Fixed32Type)
	data = protowire.AppendFixed32(data, 1)

	rec, err := decodeBoltRecord(data)
	if err != nil {
		t.Fatalf("decodeBoltRecord() = %v, want nil", err)
	}
	if rec.mode != 0o644 || string(rec.content) != "abc" || !rec.modTime.Equal(time.Unix(100, 0)) {
		t.Errorf("decodeBoltRecord() = %+v, want mode 0644, content abc, modTime 100", rec)
	}
}

func TestDecodeBoltRecordCorrupt(t *testing.T) {
	valid, err := encodeBoltRecord(0o644, time.Unix(100, 0), []byte("abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	testCases := []struct {
		name string
		data []byte
	}{
		{name: "nil", data: nil},
		{name: "truncated", data: valid[:len(valid)-2]},
		{name: "bad_tag", data: []byte{0x80}},
		{name: "bad_timestamp", data: protowire.AppendBytes(protowire.AppendTag(nil, recordModTimeField, protowire.BytesType), []byte{0x08, 0x80})},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeBoltRecord(tc.data); !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("decodeBoltRecord() = %v, want fs.ErrInvalid", err)
			}
		})
	}
}

func BenchmarkDecodeBoltRecord(b *testing.B) {
	data, err := encodeBoltRecord(fs.ModePerm, time.Now(), bytes.Repeat([]byte("x"), 1<<20))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := decodeBoltRecord(data); err != nil {
			b.Fatal(err)
		}
	}
}
