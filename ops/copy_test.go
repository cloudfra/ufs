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

package ops_test

import (
	"errors"
	"io"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"github.com/cloudfra/ufs/ops"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

func TestCopy(t *testing.T) {
	src, err := newMemFS("memory://src")
	if err != nil {
		t.Fatal(err)
	}
	dst, err := newMemFS("memory://dst")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := src.Close(); err != nil {
			t.Errorf("failed to close src FS: %v", err)
		}
		if err := dst.Close(); err != nil {
			t.Errorf("failed to close dst FS: %v", err)
		}
	}()

	f, err := src.Create("hello.txt")
	if err != nil {
		t.Errorf("failed to create file in srcFS: %v", err)
	}
	if n, err := f.WriteString("hello world"); err != nil {
		t.Fatalf("WriteString() = %v", err)
	} else if n != len("hello world") {
		t.Fatalf("WriteString() = %d, want %d", n, len("hello world"))
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}

	if err := ops.Copy(src, "hello.txt", dst, "copy.txt"); err != nil {
		t.Fatalf("Copy() = %v, want nil", err)
	}

	rf, err := dst.Open("copy.txt")
	if err != nil {
		t.Fatalf("Open(copy.txt) = %v, want nil", err)
	}
	defer func() {
		if err := rf.Close(); err != nil {
			t.Errorf("failed to close rf: %v", err)
		}
	}()
	data, err := io.ReadAll(rf)
	if err != nil {
		t.Fatalf("ReadAll() = %v, want nil", err)
	}
	if string(data) != "hello world" {
		t.Errorf("copy content = %q, want %q", data, "hello world")
	}
}

func TestCopyOpenError(t *testing.T) {
	src, err := newAngryFS("angry://")
	if err != nil {
		t.Fatal(err)
	}
	dst, err := newMemFS("memory://dst")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := dst.Close(); err != nil {
			t.Errorf("failed to close dst FS: %v", err)
		}
	}()

	if err := ops.Copy(src, "file.txt", dst, "file.txt"); err == nil {
		t.Error("Copy with angry src succeeded, want error")
	}
}

// closeErrorFS is a types.WriteFS whose files accept every write and then fail to
// Close, like a backend that only persists buffered data on Close.
type closeErrorFS struct {
	types.WriteFS
	err error
}

func (fsys *closeErrorFS) Create(name string) (types.File, error) {
	f, err := fsys.WriteFS.Create(name)
	if err != nil {
		return nil, err
	}
	return &closeErrorFile{File: f, err: fsys.err}, nil
}

type closeErrorFile struct {
	types.File
	err error
}

func (f *closeErrorFile) Close() error {
	return ufserrors.Join(f.File.Close(), f.err)
}

func TestCopyDestinationCloseError(t *testing.T) {
	src, err := newMemFS("memory://src")
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, src)()
	f, err := src.Create("file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("data"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	mem, err := newMemFS("memory://dst")
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, mem)()
	wantErr := errors.New("flush failed")
	dst := &closeErrorFS{WriteFS: mem, err: wantErr}

	if err := ops.Copy(src, "file.txt", dst, "file.txt"); !errors.Is(err, wantErr) {
		t.Errorf("Copy() = %v, want %v", err, wantErr)
	}
}

func TestCopyCreateError(t *testing.T) {
	src, err := newMemFS("memory://src")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := src.Close(); err != nil {
			t.Errorf("failed to close src FS: %v", err)
		}
	}()
	f, err := src.Create("file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := f.WriteString("data"); err != nil {
		t.Fatal(err)
	} else if n != len("data") {
		t.Fatalf("WriteString() = %d, want %d", n, len("data"))
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	dst, err := newAngryFS("angry://")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := dst.Close(); err == nil {
			t.Error("want angryFS to fail Close(), got nil error")
		}
	}()
	if err := ops.Copy(src, "file.txt", dst, "file.txt"); err == nil {
		t.Error("Copy with angry dst succeeded, want error")
	}
}
