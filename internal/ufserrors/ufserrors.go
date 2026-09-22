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

// Package ufserrors provides helper functions for combining and wrapping
// errors that are not tied to a specific file-system implementation.
package ufserrors

import "errors"

// Join returns nil if all errs are nil, returns the single non-nil
// error directly (without wrapping) if exactly one is non-nil, or [errors.Join]
// when multiple are non-nil. Avoids the wrapper overhead and identity change
// that errors.Join introduces for the single-error case.
func Join(errs ...error) error {
	var first error
	multiple := false
	for _, err := range errs {
		if err == nil {
			continue
		}
		if first == nil {
			first = err
			continue
		}
		multiple = true
		break
	}
	switch {
	case first == nil:
		return nil
	case !multiple:
		return first
	default:
		return errors.Join(errs...)
	}
}
