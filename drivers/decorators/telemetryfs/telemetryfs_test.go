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
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"syscall"
	"testing"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/core"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/ops"
	ufsTesting "github.com/cloudfra/ufs/testing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

const (
	memFSName = "memory://test"
)

var both = Options{Traces: true, Metrics: true}

// recorder collects what a decorated file system reports.
type recorder struct {
	spans  *tracetest.SpanRecorder
	reader *sdkmetric.ManualReader
	tp     *sdktrace.TracerProvider
	mp     *sdkmetric.MeterProvider
}

func newRecorder() *recorder {
	spans := tracetest.NewSpanRecorder()
	reader := sdkmetric.NewManualReader()
	return &recorder{
		spans:  spans,
		reader: reader,
		tp:     sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)),
		mp:     sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
	}
}

// wrap decorates inner so that it reports to r.
func (r *recorder) wrap(ctx context.Context, t testing.TB, inner types.WriteFS, opts Options) types.WriteFS {
	t.Helper()
	fsys, err := newTelemetryFS(ctx, inner, opts, r.tp, r.mp)
	if err != nil {
		t.Fatalf("newTelemetryFS() = %v, want nil", err)
	}
	return fsys
}

// durations returns the number of recorded operations by their attributes,
// formatted as "operation/system/error.type".
func (r *recorder) durations(t *testing.T) map[string]uint64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := r.reader.Collect(t.Context(), &rm); err != nil {
		t.Fatalf("Collect() = %v, want nil", err)
	}
	got := map[string]uint64{}
	for _, sm := range rm.ScopeMetrics {
		if sm.Scope.Name != scopeName {
			t.Errorf("scope = %q, want %q", sm.Scope.Name, scopeName)
		}
		for _, m := range sm.Metrics {
			if m.Name != durationMetricName || m.Unit != "s" {
				t.Errorf("metric = %q (%q), want %q (s)", m.Name, m.Unit, durationMetricName)
			}
			hist, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("metric data is %T, want a float64 histogram", m.Data)
			}
			for _, dp := range hist.DataPoints {
				if dp.Sum < 0 {
					t.Errorf("duration sum = %v, want >= 0", dp.Sum)
				}
				got[attrKey(dp.Attributes.ToSlice())] += dp.Count
			}
		}
	}
	return got
}

func attrKey(attrs []attribute.KeyValue) string {
	set := attribute.NewSet(attrs...)
	op, _ := set.Value(operationKey)
	system, _ := set.Value(systemKey)
	errType, _ := set.Value(errorTypeKey)
	return fmt.Sprintf("%s/%s/%s", op.AsString(), system.AsString(), errType.AsString())
}

// errFS fails every operation with err.
type errFS struct {
	types.WriteFS
	err error
}

func (fsys *errFS) Stat(string) (fs.FileInfo, error) { return nil, fsys.err }

func TestTelemetryReportsEachOperation(t *testing.T) {
	t.Parallel()
	r := newRecorder()
	inner := core.MakeMemFS(memFSName)
	fsys := r.wrap(t.Context(), t, inner, both)
	defer ufsTesting.ValidateClose(t, fsys)()

	if err := fsys.MkdirAll("dir", fs.ModePerm); err != nil {
		t.Fatalf("MkdirAll(dir) = %v, want nil", err)
	}
	f, err := fsys.Create("dir/a.txt")
	if err != nil {
		t.Fatalf("Create(dir/a.txt) = %v, want nil", err)
	}
	if _, err := f.WriteString("hello"); err != nil {
		t.Fatalf("WriteString() = %v, want nil", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	rf, err := fsys.Open("dir/a.txt")
	if err != nil {
		t.Fatalf("Open(dir/a.txt) = %v, want nil", err)
	}
	// The decorator must not hide what the file of the wrapped file system
	// can do.
	if _, ok := rf.(io.ReaderAt); !ok {
		t.Errorf("Open(dir/a.txt) = %T, want an io.ReaderAt", rf)
	}
	if err := rf.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if _, err := fsys.Stat("dir/a.txt"); err != nil {
		t.Errorf("Stat(dir/a.txt) = %v, want nil", err)
	}
	if _, err := fsys.Lstat("dir/a.txt"); err != nil {
		t.Errorf("Lstat(dir/a.txt) = %v, want nil", err)
	}
	if _, err := fsys.ReadDir("dir"); err != nil {
		t.Errorf("ReadDir(dir) = %v, want nil", err)
	}
	if data, err := fsys.ReadFile("dir/a.txt"); err != nil || string(data) != "hello" {
		t.Errorf("ReadFile(dir/a.txt) = %q, %v, want hello, nil", data, err)
	}
	// Whether memFS supports links is not what is tested here, only that the
	// call is reported with its outcome.
	_, readLinkErr := fsys.ReadLink("dir/a.txt")
	if err := ops.Rename(fsys, "dir/a.txt", "dir/b.txt"); err != nil {
		t.Errorf("Rename(dir/a.txt, dir/b.txt) = %v, want nil", err)
	}
	if err := fsys.Remove("dir/b.txt"); err != nil {
		t.Errorf("Remove(dir/b.txt) = %v, want nil", err)
	}
	if err := fsys.RemoveAll("dir"); err != nil {
		t.Errorf("RemoveAll(dir) = %v, want nil", err)
	}

	wantOps := []string{"mkdir", "create", "open", "stat", "lstat", "readdir", "readfile", "readlink", "rename", "remove", "removeall"}
	wantPaths := []string{"dir", "dir/a.txt", "dir/a.txt", "dir/a.txt", "dir/a.txt", "dir", "dir/a.txt", "dir/a.txt", "dir/a.txt", "dir/b.txt", "dir"}

	spans := r.spans.Ended()
	if len(spans) != len(wantOps) {
		t.Fatalf("got %d spans, want %d", len(spans), len(wantOps))
	}
	for i, span := range spans {
		if got, want := span.Name(), "ufs."+wantOps[i]; got != want {
			t.Errorf("span %d name = %q, want %q", i, got, want)
		}
		if got := span.InstrumentationScope().Name; got != scopeName {
			t.Errorf("span %d scope = %q, want %q", i, got, scopeName)
		}
		attrs := attribute.NewSet(span.Attributes()...)
		if got, _ := attrs.Value(operationKey); got.AsString() != wantOps[i] {
			t.Errorf("span %d %s = %q, want %q", i, operationKey, got.AsString(), wantOps[i])
		}
		if got, _ := attrs.Value(systemKey); got.AsString() != "memory" {
			t.Errorf("span %d %s = %q, want memory", i, systemKey, got.AsString())
		}
		if got, _ := attrs.Value(pathKey); got.AsString() != wantPaths[i] {
			t.Errorf("span %d %s = %q, want %q", i, pathKey, got.AsString(), wantPaths[i])
		}
		wantNewPath := ""
		if wantOps[i] == "rename" {
			wantNewPath = "dir/b.txt"
		}
		if got, _ := attrs.Value(newPathKey); got.AsString() != wantNewPath {
			t.Errorf("span %d %s = %q, want %q", i, newPathKey, got.AsString(), wantNewPath)
		}
		wantCode := codes.Unset
		if wantOps[i] == "readlink" && readLinkErr != nil {
			wantCode = codes.Error
		}
		if got := span.Status().Code; got != wantCode {
			t.Errorf("span %d (%s) status = %v, want %v", i, span.Name(), got, wantCode)
		}
	}

	got := r.durations(t)
	if len(got) != len(wantOps) {
		t.Errorf("got %d duration series %v, want %d", len(got), got, len(wantOps))
	}
	for _, op := range wantOps {
		key := op + "/memory/"
		if op == "readlink" && readLinkErr != nil {
			key += errorClassNames[classify(readLinkErr)]
		}
		if got[key] != 1 {
			t.Errorf("duration count for %q = %d, want 1 (all: %v)", key, got[key], got)
		}
	}
}

// TestTelemetryReportsOpenFile checks that OpenFile is forwarded and reported
// as an open, with an error status when the wrapped file system cannot honor
// the flags.
func TestTelemetryReportsOpenFile(t *testing.T) {
	t.Parallel()
	r := newRecorder()
	fsys := r.wrap(t.Context(), t, core.MakeMemFS(memFSName), both)
	defer ufsTesting.ValidateClose(t, fsys)()
	ofs, ok := fsys.(types.OpenFileFS)
	if !ok {
		t.Fatalf("%T does not implement types.OpenFileFS", fsys)
	}

	w, err := ofs.OpenFile("a.txt", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("OpenFile(a.txt, write) = %v, want nil", err)
	}
	ufsTesting.Must(t, w.Close())
	rf, err := ofs.OpenFile("a.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile(a.txt, read) = %v, want nil", err)
	}
	ufsTesting.Must(t, rf.Close())
	// memFS replaces files but cannot append to one.
	if _, err := ofs.OpenFile("a.txt", os.O_WRONLY|os.O_APPEND, 0); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("OpenFile(a.txt, append) = %v, want errors.ErrUnsupported", err)
	}

	spans := r.spans.Ended()
	wantCodes := []codes.Code{codes.Unset, codes.Unset, codes.Error}
	if len(spans) != len(wantCodes) {
		t.Fatalf("got %d spans, want %d", len(spans), len(wantCodes))
	}
	for i, span := range spans {
		if got := span.Name(); got != "ufs.open" {
			t.Errorf("span %d name = %q, want ufs.open", i, got)
		}
		attrs := attribute.NewSet(span.Attributes()...)
		if got, _ := attrs.Value(pathKey); got.AsString() != "a.txt" {
			t.Errorf("span %d %s = %q, want a.txt", i, pathKey, got.AsString())
		}
		if got := span.Status().Code; got != wantCodes[i] {
			t.Errorf("span %d status = %v, want %v", i, got, wantCodes[i])
		}
	}
}

// noRenameFS hides the Rename of the file system it wraps.
type noRenameFS struct {
	types.WriteFS
}

// TestTelemetryRenameFallback checks that a rename on a file system without
// Rename is done by copying, reported as a single rename.
func TestTelemetryRenameFallback(t *testing.T) {
	t.Parallel()
	r := newRecorder()
	inner := core.MakeMemFS(memFSName)
	fsys := r.wrap(t.Context(), t, noRenameFS{inner}, both)
	defer ufsTesting.ValidateClose(t, fsys)()
	f, err := inner.Create("a.txt")
	if err != nil {
		t.Fatalf("Create(a.txt) = %v, want nil", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}

	if err := ops.Rename(fsys, "a.txt", "b.txt"); err != nil {
		t.Fatalf("Rename(a.txt, b.txt) = %v, want nil", err)
	}
	if _, err := inner.Stat("b.txt"); err != nil {
		t.Errorf("Stat(b.txt) = %v, want nil", err)
	}
	if err := ops.Rename(fsys, "a.txt", "c.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Rename(a.txt, c.txt) = %v, want %v", err, fs.ErrNotExist)
	}

	spans := r.spans.Ended()
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2", len(spans))
	}
	for i, span := range spans {
		if got := span.Name(); got != "ufs.rename" {
			t.Errorf("span %d name = %q, want ufs.rename", i, got)
		}
	}
	if got := spans[1].Status().Code; got != codes.Error {
		t.Errorf("failed rename status = %v, want %v", got, codes.Error)
	}
	got := r.durations(t)
	for _, key := range []string{"rename/memory/", "rename/memory/not_exist"} {
		if got[key] != 1 {
			t.Errorf("duration count for %q = %d, want 1 (all: %v)", key, got[key], got)
		}
	}
}

func TestTelemetryReportsErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err  error
		want string
	}{
		{err: &fs.PathError{Op: "stat", Path: "a", Err: fs.ErrNotExist}, want: "not_exist"},
		{err: &fs.PathError{Op: "stat", Path: "a", Err: fs.ErrExist}, want: "exist"},
		{err: &fs.PathError{Op: "stat", Path: "a", Err: fs.ErrPermission}, want: "permission"},
		{err: &fs.PathError{Op: "stat", Path: "a", Err: fs.ErrInvalid}, want: "invalid"},
		{err: &fs.PathError{Op: "stat", Path: "a", Err: syscall.EIO}, want: "other"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			r := newRecorder()
			fsys := r.wrap(t.Context(), t, &errFS{WriteFS: core.MakeMemFS(memFSName), err: tc.err}, both)
			defer ufsTesting.ValidateClose(t, fsys)()

			// The decorator returns the error of the wrapped file system as is.
			if _, err := fsys.Stat("a"); err != tc.err { //nolint:errorlint // Identity is the point.
				t.Fatalf("Stat(a) = %v, want %v", err, tc.err)
			}

			spans := r.spans.Ended()
			if len(spans) != 1 {
				t.Fatalf("got %d spans, want 1", len(spans))
			}
			if got := spans[0].Status(); got.Code != codes.Error || got.Description != tc.err.Error() {
				t.Errorf("span status = %+v, want Error %q", got, tc.err)
			}
			if got := len(spans[0].Events()); got != 1 {
				t.Errorf("span has %d events, want the recorded error", got)
			}

			if got, key := r.durations(t), "stat/memory/"+tc.want; got[key] != 1 || len(got) != 1 {
				t.Errorf("durations = %v, want only %q = 1", got, key)
			}
		})
	}
}

func TestTelemetryOnlyTracesOrMetrics(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		opts        Options
		wantSpans   int
		wantMetrics int
	}{
		{name: "traces", opts: Options{Traces: true}, wantSpans: 1},
		{name: "metrics", opts: Options{Metrics: true}, wantMetrics: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRecorder()
			fsys := r.wrap(t.Context(), t, core.MakeMemFS(memFSName), tc.opts)
			defer ufsTesting.ValidateClose(t, fsys)()

			if _, err := fsys.Stat("."); err != nil {
				t.Fatalf("Stat(.) = %v, want nil", err)
			}
			if got := len(r.spans.Ended()); got != tc.wantSpans {
				t.Errorf("got %d spans, want %d", got, tc.wantSpans)
			}
			if got := len(r.durations(t)); got != tc.wantMetrics {
				t.Errorf("got %d duration series, want %d", got, tc.wantMetrics)
			}
		})
	}
}

// TestTelemetrySpanParent verifies that operation spans are children of the
// span in the context the file system was created with, and that they outlive
// that context.
func TestTelemetrySpanParent(t *testing.T) {
	t.Parallel()
	r := newRecorder()
	ctx, cancel := context.WithCancel(t.Context())
	ctx, parent := r.tp.Tracer("test").Start(ctx, "parent")
	fsys := r.wrap(ctx, t, core.MakeMemFS(memFSName), both)
	defer ufsTesting.ValidateClose(t, fsys)()
	parent.End()
	cancel()

	if _, err := fsys.Stat("."); err != nil {
		t.Fatalf("Stat(.) = %v, want nil", err)
	}
	spans := r.spans.Ended()
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2", len(spans))
	}
	if got, want := spans[1].Parent().SpanID(), parent.SpanContext().SpanID(); got != want {
		t.Errorf("span parent = %v, want %v", got, want)
	}
	if got := r.durations(t)["stat/memory/"]; got != 1 {
		t.Errorf("duration count = %d, want 1", got)
	}
}

func TestTelemetryWithoutSystem(t *testing.T) {
	t.Parallel()
	r := newRecorder()
	fsys := r.wrap(t.Context(), t, &uriFS{WriteFS: core.MakeMemFS(memFSName)}, both)
	defer ufsTesting.ValidateClose(t, fsys)()

	if _, err := fsys.Stat("."); err != nil {
		t.Fatalf("Stat(.) = %v, want nil", err)
	}
	for _, attr := range r.spans.Ended()[0].Attributes() {
		if attr.Key == systemKey {
			t.Errorf("span has %s = %q, want no attribute", systemKey, attr.Value.AsString())
		}
	}
	if got := r.durations(t)["stat//"]; got != 1 {
		t.Errorf("duration count = %d, want 1", got)
	}
}

func TestTelemetryCloseIsNotReported(t *testing.T) {
	t.Parallel()
	r := newRecorder()
	fsys := r.wrap(t.Context(), t, core.MakeMemFS(memFSName), both)
	if err := fsys.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if got := len(r.spans.Ended()); got != 0 {
		t.Errorf("got %d spans, want 0", got)
	}
	if got := r.durations(t); len(got) != 0 {
		t.Errorf("durations = %v, want none", got)
	}
}

func TestTelemetryString(t *testing.T) {
	t.Parallel()
	inner := core.MakeMemFS(memFSName)
	fsys := newRecorder().wrap(t.Context(), t, inner, both)
	defer ufsTesting.ValidateClose(t, fsys)()
	if got, want := fsys.String(), fmt.Sprintf("telemetryFS(%s)", inner); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestWrap(t *testing.T) {
	t.Parallel()
	inner := core.MakeMemFS(memFSName)
	defer ufsTesting.ValidateClose(t, inner)()

	fsys, err := wrap(t.Context(), inner, Options{})
	if err != nil {
		t.Fatalf("wrap() = %v, want nil", err)
	}
	if fsys != inner {
		t.Errorf("wrap with zero Options = %s, want the inner file system", fsys)
	}

	// With no SDK installed the global providers discard everything, and the
	// decorator still has to work.
	fsys, err = wrap(t.Context(), inner, both)
	if err != nil {
		t.Fatalf("wrap() = %v, want nil", err)
	}
	if fsys == inner {
		t.Error("wrap with non-zero Options should decorate the file system")
	}
	if _, err := fsys.Stat("."); err != nil {
		t.Errorf("Stat(.) = %v, want nil", err)
	}
	if _, err := fsys.Stat("missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(missing) = %v, want %v", err, fs.ErrNotExist)
	}
}

func TestOptionsDecode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		raw  any
		want Options
	}{
		{name: "true", raw: true, want: both},
		{name: "false", raw: false, want: Options{}},
		{name: "mapping", raw: map[string]any{"traces": true, "metrics": false}, want: Options{Traces: true}},
		{name: "metrics only", raw: map[string]any{"metrics": true}, want: Options{Metrics: true}},
		{name: "empty mapping", raw: map[string]any{}, want: Options{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ufs.DecodeOptions[Options](tc.raw)
			if err != nil {
				t.Fatalf("DecodeOptions() = %v, want nil", err)
			}
			if got != tc.want {
				t.Errorf("DecodeOptions() = %+v, want %+v", got, tc.want)
			}
		})
	}

	if _, err := ufs.DecodeOptions[Options]("sometimes"); err == nil {
		t.Error("DecodeOptions() with an invalid bool = nil error, want error")
	}
	if _, err := ufs.DecodeOptions[Options](map[string]any{"traces": "sometimes"}); err == nil {
		t.Error("DecodeOptions() with invalid traces = nil error, want error")
	}
}

// uriFS overrides the URI reported by the file system it embeds.
type uriFS struct {
	types.WriteFS
	u   *url.URL
	err error
}

func (fsys *uriFS) URI() (*url.URL, error) {
	return fsys.u, fsys.err
}

func TestTelemetryURI(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		opts Options
		want string
	}{
		{name: "both", opts: both, want: "[{telemetry: true}]"},
		{name: "traces", opts: Options{Traces: true}, want: "[{telemetry: {traces: true}}]"},
		{name: "metrics", opts: Options{Metrics: true}, want: "[{telemetry: {metrics: true}}]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			inner := &uriFS{WriteFS: core.MakeMemFS(memFSName), u: &url.URL{Scheme: "memory", Host: "test"}}
			fsys := newRecorder().wrap(t.Context(), t, inner, tc.opts)
			defer ufsTesting.ValidateClose(t, fsys)()
			u, err := fsys.URI()
			if err != nil {
				t.Fatalf("URI() = %v, want nil", err)
			}
			if got := u.Query().Get("options"); got != tc.want {
				t.Errorf("URI() options = %q, want %q", got, tc.want)
			}

			// New applies the decorator again from the URI.
			reopened, err := ufs.New(t.Context(), u.String())
			if err != nil {
				t.Fatalf("New(%q) = %v, want nil", u, err)
			}
			defer ufsTesting.ValidateClose(t, reopened)()
			ru, err := reopened.URI()
			if err != nil {
				t.Fatalf("URI() = %v, want nil", err)
			}
			if got := ru.Query().Get("options"); got != tc.want {
				t.Errorf("reopened URI() options = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("inner has no URI", func(t *testing.T) {
		t.Parallel()
		fsys := newRecorder().wrap(t.Context(), t, &uriFS{WriteFS: core.MakeMemFS(memFSName)}, both)
		defer ufsTesting.ValidateClose(t, fsys)()
		if u, err := fsys.URI(); u != nil || err != nil {
			t.Errorf("URI() = %v, %v, want nil, nil", u, err)
		}
	})

	t.Run("inner URI fails", func(t *testing.T) {
		t.Parallel()
		wantErr := errors.New("no uri")
		fsys := newRecorder().wrap(t.Context(), t, &uriFS{WriteFS: core.MakeMemFS(memFSName), err: wantErr}, both)
		defer ufsTesting.ValidateClose(t, fsys)()
		if _, err := fsys.URI(); !errors.Is(err, wantErr) {
			t.Errorf("URI() = %v, want %v", err, wantErr)
		}
	})
}

// archiveDirFS reports every name as an archive directory.
type archiveDirFS struct {
	types.WriteFS
}

func (archiveDirFS) IsMountedArchiveDir(string) bool { return true }

// TestTelemetryForwardsIsMountedArchiveDir verifies that the decorator answers
// for the file system it wraps, and reports false when that file system has no
// archive directories.
func TestTelemetryForwardsIsMountedArchiveDir(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		inner types.WriteFS
		want  bool
	}{
		{name: "forwards", inner: archiveDirFS{core.MakeMemFS(memFSName)}, want: true},
		{name: "no_archive_dirs", inner: struct{ types.WriteFS }{core.MakeMemFS(memFSName)}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := newRecorder().wrap(t.Context(), t, tc.inner, both)
			defer ufsTesting.ValidateClose(t, fsys)()
			got := fsys.(types.MountedArchiveDirFS).IsMountedArchiveDir("data.zip.d")
			if got != tc.want {
				t.Errorf("IsMountedArchiveDir() = %t, want %t", got, tc.want)
			}
		})
	}
}

// watchFS records the Watch calls it receives.
type watchFS struct {
	types.WriteFS
	watched []string
}

func (fsys *watchFS) Watch(_ context.Context, name string, _ types.NotifyHook) (io.Closer, error) {
	fsys.watched = append(fsys.watched, name)
	return io.NopCloser(nil), nil
}

// TestTelemetryForwardsWatch verifies that the decorator watches through to
// the file system it wraps, and does not claim to watch when that file system
// cannot.
func TestTelemetryForwardsWatch(t *testing.T) {
	t.Parallel()

	inner := &watchFS{WriteFS: core.MakeMemFS(memFSName)}
	fsys := newRecorder().wrap(t.Context(), t, inner, both)
	defer ufsTesting.ValidateClose(t, fsys)()
	w, ok := fsys.(types.Watcher)
	if !ok {
		t.Fatalf("%T is not a types.Watcher, want it to forward Watch", fsys)
	}
	if _, err := w.Watch(t.Context(), "dir", func(types.NotifyOp, string) {}); err != nil {
		t.Fatalf("Watch(dir) = %v, want nil", err)
	}
	if len(inner.watched) != 1 || inner.watched[0] != "dir" {
		t.Errorf("inner watched %v, want [dir]", inner.watched)
	}

	plain := newRecorder().wrap(t.Context(), t, struct{ types.WriteFS }{core.MakeMemFS(memFSName)}, both)
	defer ufsTesting.ValidateClose(t, plain)()
	if _, ok := plain.(types.Watcher); ok {
		t.Errorf("%T is a types.Watcher, want none when the inner file system is not", plain)
	}
}

// TestTelemetryFSDriver runs the shared file system tests through the
// decorator, which must not change how the file system behaves.
func TestTelemetryFSDriver(t *testing.T) {
	ufsdriversTesting.WriteFS(t, func(t *testing.T) types.WriteFS {
		return newRecorder().wrap(t.Context(), t, core.MakeMemFS(memFSName), both)
	})
}

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
