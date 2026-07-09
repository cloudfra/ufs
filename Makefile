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

include Makefile_testassets.mk

GO = go
RM = rm
ZIP = zip
RAR = rar
TAR = tar
SEVENZIP = 7z
GO_TEST_COUNT = 10

ASSETS =

# https://go.dev/doc/install/source#environment
LINUX_PLATFORMS = linux_386 linux_amd64 linux_arm_v5 linux_arm_v6 linux_arm_v7 linux_arm64 linux_loong64 linux_s390x linux_ppc64 linux_ppc64le linux_riscv64 linux_mips64le linux_mips linux_mipsle linux_mips64
ANDROID_PLATFORMS = android_arm64 # android_386 android_amd64 android_arm android_arm_v5 android_arm_v6 android_arm_v7
WINDOWS_PLATFORMS = windows_386 windows_amd64 windows_arm64 # windows_arm_v5 windows_arm_v6 windows_arm_v7
MAIN_PLATFORMS = windows_amd64 linux_amd64 linux_arm64
IOS_PLATFORMS = #ios_amd64 ios_arm64
DARWIN_PLATFORMS = darwin_amd64 darwin_arm64
DRAGONFLY_PLATFORMS = dragonfly_amd64
FREEBSD_PLATFORMS = freebsd_386 freebsd_amd64 freebsd_arm_v5 freebsd_arm_v6 freebsd_arm_v7 freebsd_arm64
NETBSD_PLATFORMS = netbsd_amd64 netbsd_arm64 # netbsd_386 netbsd_arm_v5 netbsd_arm_v6 netbsd_arm_v7
OPENBSD_PLATFORMS = openbsd_386 openbsd_amd64 openbsd_arm_v5 openbsd_arm_v6 openbsd_arm_v7 openbsd_arm64 # openbsd_mips64
PLAN9_PLATFORMS = # plan9_386 plan9_amd64 plan9_arm_v5 plan9_arm_v6 plan9_arm_v7
NICHE_PLATFORMS = js_wasm illumos_amd64 aix_ppc64 $(ANDROID_PLATFORMS) $(DARWIN_PLATFORMS) $(IOS_PLATFORMS) $(DRAGONFLY_PLATFORMS) $(FREEBSD_PLATFORMS) $(NETBSD_PLATFORMS) $(OPENBSD_PLATFORMS) $(PLAN9_PLATFORMS) # solaris_amd64
ALL_PLATFORMS = $(LINUX_PLATFORMS) $(WINDOWS_PLATFORMS) $(NICHE_PLATFORMS)
ALL_APPS = walk

ALL_BINARIES = $(foreach app,$(ALL_APPS),$(foreach platform,$(ALL_PLATFORMS),bin/go/$(platform)/$(app)$(if $(findstring windows_,$(platform)),.exe,)))

presubmit: lint check

lint:
	go fmt ./...
	go vet ./...

test: check

check: $(TEST_ASSETS)
	CGO_ENABLED=0 go test ./...
	CGO_ENABLED=1 go test -race ./...

test-deflake:
	CGO_ENABLED=1 go test -race -count $(GO_TEST_COUNT) ./...

bin/go/%: $(ASSETS)
	GOOS=$(firstword $(subst _, ,$(notdir $(abspath $(dir $@))))) GOARCH=$(word 2, $(subst _, ,$(notdir $(abspath $(dir $@))))) GOARM=$(subst v,,$(word 3, $(subst _, ,$(notdir $(abspath $(dir $@)))))) CGO_ENABLED=0 \
		$(GO) build -o $@ \
		cmd/$(basename $(notdir $@))/$(basename $(notdir $@)).go
	touch $@

all: build
build: $(ALL_BINARIES) $(ASSETS)

presubmit: lint check

system-info:
	@echo "Number of Processors"
	@echo "$(shell nproc)"
	@echo ""
	@echo "Kernel Version"
	@uname -a
	@echo ""
	@echo "Storage Metrics"
	@df -h

clean:
	rm -rf bin/
	rm -rf testing/testassets/

deps:
	$(GO) get -u ./...
	$(GO) mod tidy
	$(GO) mod download

.PHONY: all build presubmit lint test check test-100 clean deps
