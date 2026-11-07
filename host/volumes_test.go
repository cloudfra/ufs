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

package host

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

var errFakeUnmount = errors.New("fake unmount failed")

// fakeMountServer stands in for a mounted volume in volumeSet tests.
type fakeMountServer struct {
	volume   *volume
	closed   *[]string
	closeErr error
}

func (f *fakeMountServer) Close() error {
	*f.closed = append(*f.closed, f.volume.mountPath)
	f.volume.release()
	return f.closeErr
}

func (f *fakeMountServer) Wait() {}

func TestVolumeSetClaimRejectsOverLimit(t *testing.T) {
	t.Parallel()
	set := &volumeSet{limit: 1}

	first, err := set.claim("/first")
	if err != nil {
		t.Fatalf("claim /first: %v", err)
	}

	_, err = set.claim("/second")
	if !errors.Is(err, ErrMountLimit) {
		t.Fatalf("claim /second returned %v, want an ErrMountLimit", err)
	}
	if !strings.Contains(err.Error(), `"/first"`) {
		t.Errorf("claim /second error %q does not name the held mount", err)
	}

	first.release()
	first.release()

	if _, err := set.claim("/second"); err != nil {
		t.Errorf("claim /second after releasing /first: %v", err)
	}
}

func TestVolumeSetReleaseAll(t *testing.T) {
	t.Parallel()
	set := &volumeSet{}
	var closed []string

	// A volume whose mount has not completed has no server to close.
	if _, err := set.claim("/pending"); err != nil {
		t.Fatalf("claim /pending: %v", err)
	}

	for _, mountPath := range []string{"/first", "/second"} {
		v, err := set.claim(mountPath)
		if err != nil {
			t.Fatalf("claim %s: %v", mountPath, err)
		}
		v.setServer(&fakeMountServer{volume: v, closed: &closed, closeErr: errFakeUnmount})
	}

	if err := set.releaseAll(); !errors.Is(err, errFakeUnmount) {
		t.Errorf("releaseAll returned %v, want an errFakeUnmount", err)
	}
	if want := []string{"/second", "/first"}; !slices.Equal(closed, want) {
		t.Errorf("closed volumes %v, want %v", closed, want)
	}

	set.mu.Lock()
	held := len(set.held)
	set.mu.Unlock()
	if held != 1 {
		t.Errorf("set holds %d volumes after releaseAll, want 1: only the unmounted volume", held)
	}
}
