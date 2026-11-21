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

package telemetryfs

import (
	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/types"
	"testing"

	"github.com/cloudfra/ufs/drivers/core"
	ufsTesting "github.com/cloudfra/ufs/testing"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func BenchmarkTelemetryStat(b *testing.B) {
	inner := core.MakeMemFS(memFSName)
	defer ufsTesting.ValidateClose(b, inner)()

	b.Run("inner", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := inner.Stat("."); err != nil {
				b.Fatal(err)
			}
		}
	})

	// No SDK is installed, the cost every user of the decorator pays.
	for _, bc := range []struct {
		name string
		opts Options
	}{
		{name: "noop", opts: both},
		{name: "noop_traces", opts: Options{Traces: true}},
		{name: "noop_metrics", opts: Options{Metrics: true}},
	} {
		b.Run(bc.name, func(b *testing.B) {
			fsys, err := wrap(b.Context(), inner, bc.opts)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := fsys.Stat("."); err != nil {
					b.Fatal(err)
				}
			}
		})
	}

	b.Run("sdk", func(b *testing.B) {
		r := &recorder{
			tp: sdktrace.NewTracerProvider(),
			mp: sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewManualReader())),
		}
		fsys := r.wrap(b.Context(), b, inner, both)
		b.ReportAllocs()
		for b.Loop() {
			if _, err := fsys.Stat("."); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkTelemetryBatchCreate measures the batch wrapper in the shape of
// BenchmarkTelemetryStat: the file system alone, the decorator with no SDK
// installed, and the decorator with one. The last case is the same three files
// through a file system that cannot write batches, which the decorator reports
// one operation per file.
func BenchmarkTelemetryBatchCreate(b *testing.B) {
	files := []types.BatchFile{
		{Name: "bench/a.txt", Data: []byte("a")},
		{Name: "bench/b.txt", Data: []byte("b")},
		{Name: "bench/c.txt", Data: []byte("c")},
	}
	inner := core.MakeMemFS(memFSName)
	defer ufsTesting.ValidateClose(b, inner)()

	b.Run("inner", func(b *testing.B) {
		batched := &batchFS{WriteFS: inner}
		b.ReportAllocs()
		for b.Loop() {
			if err := batched.BatchCreate(files); err != nil {
				b.Fatal(err)
			}
		}
	})

	// No SDK is installed, the cost every user of the decorator pays.
	for _, bc := range []struct {
		name string
		opts Options
	}{
		{name: "noop", opts: both},
		{name: "noop_traces", opts: Options{Traces: true}},
		{name: "noop_metrics", opts: Options{Metrics: true}},
	} {
		b.Run(bc.name, func(b *testing.B) {
			fsys, err := wrap(b.Context(), &batchFS{WriteFS: inner}, bc.opts)
			if err != nil {
				b.Fatal(err)
			}
			batched, ok := fsys.(types.BatchCreateFS)
			if !ok {
				b.Fatalf("%T does not write batches, want a decorator of a batch writer", fsys)
			}
			b.ReportAllocs()
			for b.Loop() {
				if err := batched.BatchCreate(files); err != nil {
					b.Fatal(err)
				}
			}
		})
	}

	b.Run("sdk", func(b *testing.B) {
		r := &recorder{
			tp: sdktrace.NewTracerProvider(),
			mp: sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewManualReader())),
		}
		fsys := r.wrap(b.Context(), b, &batchFS{WriteFS: inner}, both)
		batched, ok := fsys.(types.BatchCreateFS)
		if !ok {
			b.Fatalf("%T does not write batches, want a decorator of a batch writer", fsys)
		}
		b.ReportAllocs()
		for b.Loop() {
			if err := batched.BatchCreate(files); err != nil {
				b.Fatal(err)
			}
		}
	})

	// The decorator over a file system that cannot write batches, where each
	// file is a reported operation of its own.
	b.Run("fallback", func(b *testing.B) {
		fsys, err := wrap(b.Context(), struct{ types.WriteFS }{inner}, both)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		for b.Loop() {
			if err := ufs.BatchCreate(fsys, files...); err != nil {
				b.Fatal(err)
			}
		}
	})
}
