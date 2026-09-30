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

package simplecachefs

import (
	"container/list"
	"strings"
	"time"

	"github.com/cloudfra/ufs/internal/pathutil"
)

// entry is one file in a tier's index.
type entry struct {
	name    string
	size    int64
	modTime time.Time
	// writers counts the open handles writing the file (hot tier only). A file
	// with writers is never dropped.
	writers int

	elem *list.Element
}

// tier indexes the files of one tier in FIFO order: oldest modification time
// first. It is not safe for concurrent use; cacheFS.mu guards it.
type tier struct {
	entries map[string]*entry
	order   *list.List
	bytes   int64
}

func newTier() *tier {
	return &tier{entries: map[string]*entry{}, order: list.New()}
}

// get returns name's entry, or nil.
func (t *tier) get(name string) *entry {
	return t.entries[name]
}

// live reports whether e is still indexed.
func (t *tier) live(e *entry) bool {
	return e != nil && t.entries[e.name] == e
}

// put records name with size and modTime and places it in modTime order,
// creating the entry if needed. A new modTime is normally the latest, so the
// search for its place starts at the back.
func (t *tier) put(name string, size int64, modTime time.Time) *entry {
	e, ok := t.entries[name]
	if ok {
		t.order.Remove(e.elem)
		t.bytes -= e.size
	} else {
		e = &entry{name: name}
		t.entries[name] = e
	}
	e.size = size
	e.modTime = modTime
	t.bytes += size
	mark := t.order.Back()
	for mark != nil && mark.Value.(*entry).modTime.After(modTime) {
		mark = mark.Prev()
	}
	if mark == nil {
		e.elem = t.order.PushFront(e)
	} else {
		e.elem = t.order.InsertAfter(e, mark)
	}
	return e
}

// resize changes a live entry's size.
func (t *tier) resize(e *entry, size int64) {
	if !t.live(e) {
		return
	}
	t.bytes += size - e.size
	e.size = size
}

// remove drops name from the index.
func (t *tier) remove(name string) {
	e, ok := t.entries[name]
	if !ok {
		return
	}
	t.order.Remove(e.elem)
	t.bytes -= e.size
	delete(t.entries, name)
}

// removeAll drops name and every entry below it.
func (t *tier) removeAll(name string) {
	for key := range t.entries {
		if covers(name, key) {
			t.remove(key)
		}
	}
}

// ascend calls fn on each entry from the front of the FIFO, oldest first,
// until fn returns false. fn must not modify the index.
func (t *tier) ascend(fn func(e *entry) bool) {
	for el := t.order.Front(); el != nil; el = el.Next() {
		if !fn(el.Value.(*entry)) {
			return
		}
	}
}

// covers reports whether removing dir removes name: name is dir or below it.
func covers(dir, name string) bool {
	if dir == pathutil.CwdPath || dir == name {
		return true
	}
	return strings.HasPrefix(name, dir+pathutil.UnixSeparator)
}
