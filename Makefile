# Copyright 2026 Cloudfra
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

REGISTRY = ghcr.io/cloudfra
TEST_ASSETS =
ASSETS = $(PROTOS)
GO_PACKAGE = github.com/cloudfra/ufs
ALL_APPS = ufsmount walk
PRODUCTION=1
PLAN9_PLATFORMS:=

PROTOS = proto/ufs.pb.go
PROTOS += proto/ufs_grpc.pb.go
PROTOS += proto/ufs.pb.gw.go
PROTOS += proto/ufs.swagger.json
PROTOS += proto/writelog.pb.go

ZIP = zip
RAR = rar
TAR = tar
SEVENZIP = 7z

include Makefile_build.mk

# Runs the deflake command over the host package alone and verbosely, so a
# mount that wedges names the test it stopped in: go test streams a single
# package's output as it is produced, while a suite buffers each package's
# output until that package finishes. Its own timeout is below the CI step's,
# so go test reports the stuck test and dumps the stack of every goroutine
# before the runner cancels the step and takes the log with it.
HOST_TEST_TIMEOUT = 5m

test-host: testassets
	CGO_ENABLED=1 $(GO) test -shuffle=on -tags testing $(GO_RACE) ./host/... -cover -count $(GO_TEST_COUNT) -test.short -timeout $(HOST_TEST_TIMEOUT) -v

.PHONY: test-host
