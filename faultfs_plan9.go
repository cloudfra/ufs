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

package ufs

import "syscall"

// faultErrors is the Plan 9 version of the realistic errors in
// faultfs_notplan9.go. Plan 9's syscall package has no ENOSPC, EDQUOT or
// ECONNRESET, so ETIMEDOUT stands in for a failed remote connection.
var faultErrors = []error{
	syscall.EIO,
	syscall.EACCES,
	syscall.ETIMEDOUT,
}
