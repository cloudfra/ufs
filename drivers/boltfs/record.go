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
	"fmt"
	"io/fs"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/cloudfra/ufs/proto"
)

// Field numbers of pb.BoltFileRecord and google.protobuf.Timestamp, used by
// decodeBoltRecord's hand-rolled parser.
const (
	recordModeField    protowire.Number = 1
	recordModTimeField protowire.Number = 2
	recordContentField protowire.Number = 3

	timestampSecondsField protowire.Number = 1
	timestampNanosField   protowire.Number = 2
)

// boltRecord is the decoded form of a pb.BoltFileRecord.
type boltRecord struct {
	mode    fs.FileMode
	modTime time.Time
	// content aliases the encoded record and is only valid for the lifetime
	// of the bolt transaction the record was read in; clone it to retain it.
	content []byte
}

// encodeBoltRecord serializes mode, modTime and content into the protobuf
// wire encoding of pb.BoltFileRecord, the value stored for each file's key
// (and each directory's selfKey) in the bolt database.
func encodeBoltRecord(mode fs.FileMode, modTime time.Time, content []byte) ([]byte, error) {
	data, err := proto.Marshal(&pb.BoltFileRecord{
		Mode:    uint32(mode),
		ModTime: timestamppb.New(modTime),
		Content: content,
	})
	if err != nil {
		return nil, fmt.Errorf("cannot marshal bolt record: %w", err)
	}
	return data, nil
}

// decodeBoltRecord parses the pb.BoltFileRecord wire encoding in data. It is
// equivalent to proto.Unmarshal, but the returned content aliases data rather
// than being copied, so that Stat and ReadDir, which only need its length, do
// not allocate a copy of every file they touch. Unknown fields are skipped
// for forward compatibility.
func decodeBoltRecord(data []byte) (boltRecord, error) {
	if data == nil {
		return boltRecord{}, fmt.Errorf("corrupt bolt record: missing: %w", fs.ErrInvalid)
	}
	var rec boltRecord
	var seconds, nanos int64
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return boltRecord{}, corruptRecord(n)
		}
		data = data[n:]
		switch {
		case num == recordModeField && typ == protowire.VarintType:
			v, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return boltRecord{}, corruptRecord(n)
			}
			rec.mode = fs.FileMode(uint32(v)) //nolint:gosec // G115: proto uint32 field is encoded as a varint
			data = data[n:]
		case num == recordModTimeField && typ == protowire.BytesType:
			v, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return boltRecord{}, corruptRecord(n)
			}
			var err error
			if seconds, nanos, err = decodeTimestamp(v); err != nil {
				return boltRecord{}, err
			}
			data = data[n:]
		case num == recordContentField && typ == protowire.BytesType:
			v, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return boltRecord{}, corruptRecord(n)
			}
			rec.content = v
			data = data[n:]
		default:
			n := protowire.ConsumeFieldValue(num, typ, data)
			if n < 0 {
				return boltRecord{}, corruptRecord(n)
			}
			data = data[n:]
		}
	}
	// Matches timestamppb.Timestamp.AsTime, including for an absent mod_time.
	rec.modTime = time.Unix(seconds, nanos).UTC()
	return rec, nil
}

// decodeTimestamp parses the google.protobuf.Timestamp wire encoding in data.
func decodeTimestamp(data []byte) (seconds, nanos int64, err error) {
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return 0, 0, corruptRecord(n)
		}
		data = data[n:]
		if typ == protowire.VarintType && (num == timestampSecondsField || num == timestampNanosField) {
			v, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return 0, 0, corruptRecord(n)
			}
			if num == timestampSecondsField {
				seconds = int64(v) //nolint:gosec // G115: proto int64 is encoded as a two's-complement varint
			} else {
				nanos = int64(int32(v)) //nolint:gosec // G115: proto int32 is encoded as a sign-extended varint
			}
			data = data[n:]
			continue
		}
		n = protowire.ConsumeFieldValue(num, typ, data)
		if n < 0 {
			return 0, 0, corruptRecord(n)
		}
		data = data[n:]
	}
	return seconds, nanos, nil
}

func corruptRecord(n int) error {
	return fmt.Errorf("corrupt bolt record: %w: %w", protowire.ParseError(n), fs.ErrInvalid)
}
