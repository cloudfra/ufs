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

// Package telemetryfs provides the "telemetry" file system decorator, which
// reports an OpenTelemetry span and a duration measurement for each operation
// on the file system it wraps. Importing the package registers the decorator.
//
// The decorator uses the global tracer and meter providers, see
// [go.opentelemetry.io/otel.SetTracerProvider] and
// [go.opentelemetry.io/otel.SetMeterProvider]. Until the application installs
// an SDK they discard everything.
//
// # Traces
//
// Each operation is a span named "ufs." followed by the operation, such as
// "ufs.readfile", with the attributes ufs.operation, ufs.system (the URI
// scheme of the wrapped file system) and ufs.path, which for a rename is the
// old path, with the new one in ufs.new_path, and for a batch create is the
// first file, with the number of files in ufs.files. A failed operation records
// its error and sets the span status.
//
// The decorator implements [types.BatchCreateFS] only when the file system it
// wraps does, so a batch is never reported as one on a file system that cannot
// write it.
//
// File system methods take no [context.Context], so a span cannot join the
// span that is current in its caller. Spans are children of the span in the
// context that created the file system, if it has one, and root spans
// otherwise.
//
// # Metrics
//
// The histogram ufs.operation.duration records how long each operation took,
// in seconds, with the attributes ufs.operation and ufs.system, and error.type
// when the operation failed: not_exist, exist, permission, invalid or other.
// The path is never a metric attribute.
//
// # Limitations
//
// Only operations on the file system are reported. Reads and writes on a file
// that Open or Create returned are not.
package telemetryfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"slices"
	"time"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/ops"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"gopkg.in/yaml.v3"
)

var (
	_ types.WriteFS      = (*telemetryFS)(nil)
	_ types.RenameFileFS = (*telemetryFS)(nil)
	_ types.Watcher      = (*watchTelemetryFS)(nil)

	_ types.BatchCreateFS = (*batchTelemetryFS)(nil)
)

const (
	optionName = "telemetry"

	// scopeName is the instrumentation scope of the spans and measurements.
	scopeName = "github.com/cloudfra/ufs/drivers/decorators/telemetryfs"

	durationMetricName = "ufs.operation.duration"

	operationKey = attribute.Key("ufs.operation")
	systemKey    = attribute.Key("ufs.system")
	pathKey      = attribute.Key("ufs.path")
	newPathKey   = attribute.Key("ufs.new_path")
	filesKey     = attribute.Key("ufs.files")
	errorTypeKey = attribute.Key("error.type")
)

// durationBuckets are the histogram boundaries in seconds. They start well
// below the SDK defaults because an operation on a local or in-memory file
// system takes microseconds.
var durationBuckets = []float64{
	0.00001, 0.0001, 0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// Options configures the telemetry decorator. In a mount spec it is written
// either as a bare bool, which turns traces and metrics on or off together, or
// as a mapping:
//
//	options:
//	  - telemetry: true
//
//	options:
//	  - telemetry:
//	      traces: true
//	      metrics: false
type Options struct {
	// Traces reports a span for each operation.
	Traces bool `yaml:"traces,omitempty"`

	// Metrics records the duration of each operation.
	Metrics bool `yaml:"metrics,omitempty"`
}

func (o Options) isZero() bool {
	return !o.Traces && !o.Metrics
}

// UnmarshalYAML accepts both the bare bool and the mapping form of [Options].
func (o *Options) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var enabled bool
		if err := node.Decode(&enabled); err != nil {
			return err
		}
		o.Traces, o.Metrics = enabled, enabled
		return nil
	}
	type plain Options
	return node.Decode((*plain)(o))
}

// MarshalYAML writes [Options] in its bare bool form when traces and metrics
// are both on, and as a mapping otherwise.
func (o Options) MarshalYAML() (any, error) {
	if o.Traces && o.Metrics {
		return true, nil
	}
	type plain Options
	return plain(o), nil
}

func init() {
	ufs.RegisterDecorator(ufs.NewDecorator(optionName, wrap))
}

func wrap(ctx context.Context, inner types.WriteFS, opts Options) (types.WriteFS, error) {
	if opts.isZero() {
		return inner, nil
	}
	return newTelemetryFS(ctx, inner, opts, otel.GetTracerProvider(), otel.GetMeterProvider())
}

// operation identifies a file system operation. It indexes the attributes
// that are built once per file system.
type operation uint8

const (
	opOpen operation = iota
	opStat
	opLstat
	opReadDir
	opReadFile
	opReadLink
	opCreate
	opMkdirAll
	opRemove
	opRemoveAll
	opRename
	opBatchCreate
	numOperations
)

// operationNames uses the op names that the path errors of this module use.
var operationNames = [numOperations]string{
	opOpen:        "open",
	opStat:        "stat",
	opLstat:       "lstat",
	opReadDir:     "readdir",
	opReadFile:    "readfile",
	opReadLink:    "readlink",
	opCreate:      "create",
	opMkdirAll:    "mkdir",
	opRemove:      "remove",
	opRemoveAll:   "removeall",
	opRename:      "rename",
	opBatchCreate: "batchcreate",
}

// errorClass groups errors into the few values reported as error.type.
type errorClass uint8

const (
	errNone errorClass = iota
	errNotExist
	errExist
	errPermission
	errInvalid
	errOther
	numErrorClasses
)

var errorClassNames = [numErrorClasses]string{
	errNotExist:   "not_exist",
	errExist:      "exist",
	errPermission: "permission",
	errInvalid:    "invalid",
	errOther:      "other",
}

func classify(err error) errorClass {
	switch {
	case err == nil:
		return errNone
	case errors.Is(err, fs.ErrNotExist):
		return errNotExist
	case errors.Is(err, fs.ErrExist):
		return errExist
	case errors.Is(err, fs.ErrPermission):
		return errPermission
	case errors.Is(err, fs.ErrInvalid):
		return errInvalid
	default:
		return errOther
	}
}

type telemetryFS struct {
	inner types.WriteFS
	cfg   Options
	// ctx carries the span that the operation spans are children of. It is
	// never canceled.
	ctx context.Context //nolint:containedctx // File system methods take no context.

	// tracer is nil when traces are off.
	tracer trace.Tracer
	// duration is nil when metrics are off.
	duration metric.Float64Histogram

	// The options below are built once so that reporting an operation does
	// not build an attribute set.
	spanNames   [numOperations]string
	spanOptions [numOperations][]trace.SpanStartOption
	recordAttrs [numOperations][numErrorClasses][]metric.RecordOption
}

// watchTelemetryFS is the decorator for a file system that implements
// [types.Watcher]. It is a separate type so that the decorator only claims to
// watch when the file system it wraps does.
type watchTelemetryFS struct {
	*telemetryFS
	watcher types.Watcher
}

// Watch forwards to the wrapped file system. It is not reported.
func (fsys *watchTelemetryFS) Watch(ctx context.Context, name string, hook types.NotifyHook) (io.Closer, error) {
	return fsys.watcher.Watch(ctx, name, hook)
}

// batchTelemetryFS decorates a file system that implements
// [types.BatchCreateFS], so the decorator claims batch support only when the
// file system it wraps has it.
type batchTelemetryFS struct {
	*telemetryFS
	batcher types.BatchCreateFS
}

// BatchCreate forwards to the wrapped file system and is reported.
func (fsys *batchTelemetryFS) BatchCreate(files []types.BatchFile) error {
	return fsys.batchCreate(fsys.batcher, files)
}

// batchCreate reports a batch on batcher as one operation, attributed to the
// path of its first file and its number of files. The decorator checks no
// names, so the wrapped file system's error comes back unchanged.
func (fsys *telemetryFS) batchCreate(batcher types.BatchCreateFS, files []types.BatchFile) error {
	span, start := fsys.begin(opBatchCreate)
	err := batcher.BatchCreate(files)
	var name string
	if len(files) > 0 {
		name = files[0].Name
	}
	if span != nil && span.IsRecording() {
		span.SetAttributes(filesKey.Int(len(files)))
	}
	fsys.end(opBatchCreate, name, span, start, err)
	return err
}

// newTelemetryFS wraps inner as a [types.WriteFS] that reports its operations to
// tracerProvider and meterProvider, as selected by cfg. Close always delegates
// to inner without being reported.
func newTelemetryFS(ctx context.Context, inner types.WriteFS, cfg Options, tracerProvider trace.TracerProvider, meterProvider metric.MeterProvider) (types.WriteFS, error) {
	fsys := &telemetryFS{
		inner: inner,
		cfg:   cfg,
		ctx:   context.WithoutCancel(ctx),
	}

	system := systemOf(inner)
	for op, name := range operationNames {
		attrs := make([]attribute.KeyValue, 0, 3)
		attrs = append(attrs, operationKey.String(name))
		if system != "" {
			attrs = append(attrs, systemKey.String(system))
		}
		fsys.spanNames[op] = "ufs." + name
		fsys.spanOptions[op] = []trace.SpanStartOption{trace.WithAttributes(attrs...)}
		for class, className := range errorClassNames {
			classAttrs := attrs
			if className != "" {
				classAttrs = slices.Concat(attrs, []attribute.KeyValue{errorTypeKey.String(className)})
			}
			fsys.recordAttrs[op][class] = []metric.RecordOption{metric.WithAttributeSet(attribute.NewSet(classAttrs...))}
		}
	}

	if cfg.Traces {
		fsys.tracer = tracerProvider.Tracer(scopeName)
	}
	if cfg.Metrics {
		duration, err := meterProvider.Meter(scopeName).Float64Histogram(
			durationMetricName,
			metric.WithUnit("s"),
			metric.WithDescription("Duration of file system operations."),
			metric.WithExplicitBucketBoundaries(durationBuckets...),
		)
		if err != nil {
			return nil, fmt.Errorf("cannot create telemetryFS, %w", err)
		}
		fsys.duration = duration
	}

	if w, ok := inner.(types.Watcher); ok {
		return &watchTelemetryFS{telemetryFS: fsys, watcher: w}, nil
	}
	if b, ok := inner.(types.BatchCreateFS); ok {
		return &batchTelemetryFS{telemetryFS: fsys, batcher: b}, nil
	}
	return fsys, nil
}

// systemOf returns the URI scheme of fsys, or "" when it has no URI.
func systemOf(fsys types.WriteFS) string {
	u, err := fsys.URI()
	if err != nil || u == nil {
		return ""
	}
	return u.Scheme
}

func (fsys *telemetryFS) DeviceInfo() types.DeviceMap {
	return fsys.inner.DeviceInfo()
}

// IsMountedArchiveDir forwards to the wrapped file system, see
// [types.MountedArchiveDirFS].
func (fsys *telemetryFS) IsMountedArchiveDir(name string) bool {
	m, ok := fsys.inner.(types.MountedArchiveDirFS)
	return ok && m.IsMountedArchiveDir(name)
}

func (fsys *telemetryFS) URI() (*url.URL, error) {
	u, err := fsys.inner.URI()
	if err != nil {
		return nil, err
	}
	return ufs.AppendURIOption(u, optionName, fsys.cfg)
}

func (fsys *telemetryFS) String() string {
	return fmt.Sprintf("telemetryFS(%s)", fsys.inner)
}

// begin starts reporting an operation. The results are passed to end once the
// wrapped file system returns.
func (fsys *telemetryFS) begin(op operation) (trace.Span, time.Time) {
	var span trace.Span
	if fsys.tracer != nil {
		_, span = fsys.tracer.Start(fsys.ctx, fsys.spanNames[op], fsys.spanOptions[op]...)
	}
	var start time.Time
	if fsys.duration != nil {
		start = time.Now()
	}
	return span, start
}

func (fsys *telemetryFS) end(op operation, name string, span trace.Span, start time.Time, err error) {
	fsys.endRename(op, name, "", span, start, err)
}

// endRename is end for an operation with a second path, newName, which is
// reported as ufs.new_path unless it is empty.
func (fsys *telemetryFS) endRename(op operation, name, newName string, span trace.Span, start time.Time, err error) {
	if fsys.duration != nil {
		fsys.duration.Record(fsys.ctx, time.Since(start).Seconds(), fsys.recordAttrs[op][classify(err)]...)
	}
	if span == nil {
		return
	}
	// The path is set here rather than at the start so that a span that is
	// not sampled does not pay for the attribute.
	if span.IsRecording() {
		span.SetAttributes(pathKey.String(name))
		if newName != "" {
			span.SetAttributes(newPathKey.String(newName))
		}
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
	}
	span.End()
}

func (fsys *telemetryFS) Open(name string) (fs.File, error) {
	span, start := fsys.begin(opOpen)
	f, err := fsys.inner.Open(name)
	fsys.end(opOpen, name, span, start, err)
	return f, err
}

func (fsys *telemetryFS) Close() error {
	return fsys.inner.Close()
}

func (fsys *telemetryFS) Stat(name string) (fs.FileInfo, error) {
	span, start := fsys.begin(opStat)
	info, err := fsys.inner.Stat(name)
	fsys.end(opStat, name, span, start, err)
	return info, err
}

func (fsys *telemetryFS) Lstat(name string) (fs.FileInfo, error) {
	span, start := fsys.begin(opLstat)
	info, err := fsys.inner.Lstat(name)
	fsys.end(opLstat, name, span, start, err)
	return info, err
}

func (fsys *telemetryFS) ReadDir(name string) ([]fs.DirEntry, error) {
	span, start := fsys.begin(opReadDir)
	entries, err := fsys.inner.ReadDir(name)
	fsys.end(opReadDir, name, span, start, err)
	return entries, err
}

func (fsys *telemetryFS) ReadFile(name string) ([]byte, error) {
	span, start := fsys.begin(opReadFile)
	data, err := fsys.inner.ReadFile(name)
	fsys.end(opReadFile, name, span, start, err)
	return data, err
}

func (fsys *telemetryFS) ReadLink(name string) (string, error) {
	span, start := fsys.begin(opReadLink)
	target, err := fsys.inner.ReadLink(name)
	fsys.end(opReadLink, name, span, start, err)
	return target, err
}

func (fsys *telemetryFS) Create(name string) (types.File, error) {
	span, start := fsys.begin(opCreate)
	f, err := fsys.inner.Create(name)
	fsys.end(opCreate, name, span, start, err)
	return f, err
}

func (fsys *telemetryFS) MkdirAll(name string, perm fs.FileMode) error {
	span, start := fsys.begin(opMkdirAll)
	err := fsys.inner.MkdirAll(name, perm)
	fsys.end(opMkdirAll, name, span, start, err)
	return err
}

func (fsys *telemetryFS) Remove(name string) error {
	span, start := fsys.begin(opRemove)
	err := fsys.inner.Remove(name)
	fsys.end(opRemove, name, span, start, err)
	return err
}

func (fsys *telemetryFS) RemoveAll(name string) error {
	span, start := fsys.begin(opRemoveAll)
	err := fsys.inner.RemoveAll(name)
	fsys.end(opRemoveAll, name, span, start, err)
	return err
}

// Rename renames with [ops.Rename], which copies and removes when the wrapped
// file system has no Rename of its own. The copy is reported as one rename.
func (fsys *telemetryFS) Rename(oldPath, newPath string) error {
	span, start := fsys.begin(opRename)
	err := ops.Rename(fsys.inner, oldPath, newPath)
	fsys.endRename(opRename, oldPath, newPath, span, start, err)
	return err
}
