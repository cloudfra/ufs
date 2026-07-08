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

include proto.mk
include toolchain.mk

# Installed Tools
GO_WITH_PROXY = go
GO = GOPROXY=off go
GO_RACE=-race
DOCKER = docker
TAR = tar
SEVENZIP = 7z
GO_TEST_COUNT = 10

TEST_FILE_ASSETS = testing/testassets/files/index.html
TEST_FILE_ASSETS += testing/testassets/files/site.js
TEST_FILE_ASSETS += testing/testassets/files/weird\ \#1.txt
TEST_FILE_ASSETS += testing/testassets/files/weird\ \#.txt
TEST_FILE_ASSETS += testing/testassets/files/weird$$.txt
TEST_FILE_ASSETS += testing/testassets/files/assets/1.txt
TEST_FILE_ASSETS += testing/testassets/files/assets/2.txt
TEST_FILE_ASSETS += testing/testassets/files/assets/onetwothree/1.txt
TEST_FILE_ASSETS += testing/testassets/files/assets/onetwothree/2.txt
TEST_FILE_ASSETS += testing/testassets/files/assets/onetwothree/3.txt
TEST_FILE_ASSETS += testing/testassets/files/assets/four/4.txt
TEST_FILE_ASSETS += testing/testassets/files/assets/sixseven/6.txt
TEST_FILE_ASSETS += testing/testassets/files/assets/sixseven/7.txt
TEST_FILE_ASSETS += testing/testassets/files/assets/images/1.txt
TEST_FILE_ASSETS += testing/testassets/files/assets/images/2.txt
TEST_FILE_ASSETS += testing/testassets/files/assets/images/laptop.png
TEST_FILE_ASSETS += testing/testassets/files/assets/images/walking-duck.gif
TEST_FILE_ASSETS += testing/testassets/files/assets/images/eXample.TIFF
TEST_FILE_ASSETS += testing/testassets/files/assets/images/blue.ico
TEST_FILE_ASSETS += testing/testassets/files/assets/images/Laptop.jpg
TEST_FILE_ASSETS += testing/testassets/files/assets/images/earth.avi
TEST_FILE_ASSETS += testing/testassets/files/assets/images/earth.mp4
TEST_FILE_ASSETS += testing/testassets/files/assets/images/earth.webm


TEST_ARCHIVE_ASSETS = testing/testassets/archives/nodir-testassets.zip
TEST_ARCHIVE_ASSETS += testing/testassets/archives/single-testassets.zip
TEST_ARCHIVE_ASSETS += testing/testassets/archives/nested-testassets.zip
TEST_ARCHIVE_ASSETS += testing/testassets/archives/testassets.tar.gz
TEST_ARCHIVE_ASSETS += testing/testassets/archives/testassets.tar.bz2
TEST_ARCHIVE_ASSETS += testing/testassets/archives/testassets.tar.xz
TEST_ARCHIVE_ASSETS += testing/testassets/archives/testassets.tar.lz4
TEST_ARCHIVE_ASSETS += testing/testassets/archives/testassets.tar
TEST_ARCHIVE_ASSETS += testing/testassets/archives/testassets.7z

TEST_ASSETS = $(TEST_FILE_ASSETS) $(TEST_ARCHIVE_ASSETS)
ASSETS =

SHORT_SHA = $(shell git rev-parse --short=7 HEAD | tr -d [:punct:])
DIRTY_VERSION = v0.0.0-$(SHORT_SHA)
VERSION = $(shell git describe --tags || (echo $(DIRTY_VERSION) && exit 1))
BUILD_DATE = $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
TAG := $(VERSION)

export PATH := $(PWD)/build/toolchain/bin:$(PATH)
SOURCE_DIRS=$(shell go list ./... | grep -v '/vendor/')

REGISTRY = ghcr.io/cloudfra
PROTOS =
TEST_ASSETS = $(TEST_FILE_ASSETS) $(TEST_ARCHIVE_ASSETS)
ASSETS = $(PROTOS)
ALL_APPS = walk

GO_TEST_COUNT = 25

LINUX_PLATFORMS = linux/386 linux/amd64 linux/arm/v5 linux/arm/v6 linux/arm/v7 linux/arm64 linux/loong64 linux/s390x linux/ppc64 linux/ppc64le linux/riscv64 linux/mips64le linux/mips linux/mipsle linux/mips64
ANDROID_PLATFORMS = android/arm64 # android/386 android/amd64 android/arm android/arm/v5 android/arm/v6 android/arm/v7
WINDOWS_PLATFORMS = windows/386 windows/amd64 windows/arm64 # windows/arm/v5 windows/arm/v6 windows/arm/v7
MAIN_PLATFORMS = windows/amd64 linux/amd64 linux/arm64
IOS_PLATFORMS = # ios/amd64 ios/arm64
DARWIN_PLATFORMS = darwin/amd64 darwin/arm64
DRAGONFLY_PLATFORMS = dragonfly/amd64
FREEBSD_PLATFORMS = freebsd/386 freebsd/amd64 freebsd/arm/v5 freebsd/arm/v6 freebsd/arm/v7 freebsd/arm64
NETBSD_PLATFORMS = netbsd/amd64 netbsd/arm64 netbsd/386 netbsd/arm/v5 netbsd/arm/v6 netbsd/arm/v7
OPENBSD_PLATFORMS = openbsd/386 openbsd/amd64 openbsd/arm/v5 openbsd/arm/v6 openbsd/arm/v7 openbsd/arm64 # openbsd/mips64
PLAN9_PLATFORMS = plan9/386 plan9/amd64 plan9/arm/v5 plan9/arm/v6 plan9/arm/v7
SOLARIS_PLATFORMS = solaris/amd64
NICHE_PLATFORMS = js/wasm illumos/amd64 aix/ppc64 $(ANDROID_PLATFORMS) $(DARWIN_PLATFORMS) $(IOS_PLATFORMS) $(DRAGONFLY_PLATFORMS) $(FREEBSD_PLATFORMS) $(NETBSD_PLATFORMS) $(OPENBSD_PLATFORMS) $(PLAN9_PLATFORMS) $(SOLARIS_PLATFORMS)
ALL_PLATFORMS = $(LINUX_PLATFORMS) $(WINDOWS_PLATFORMS) $(NICHE_PLATFORMS)

MAIN_BINARIES = $(foreach app,$(ALL_APPS),$(foreach platform,$(MAIN_PLATFORMS),build/bin/$(platform)/$(app)$(if $(findstring windows,$(platform)),.exe,)))
WINDOWS_BINARIES = $(foreach app,$(ALL_APPS),$(foreach platform,$(WINDOWS_PLATFORMS),build/bin/$(platform)/$(app)$(if $(findstring windows,$(platform)),.exe,)))
ALL_BINARIES = $(foreach app,$(ALL_APPS),$(foreach platform,$(ALL_PLATFORMS),build/bin/$(platform)/$(app)$(if $(findstring windows,$(platform)),.exe,)))
CODESIGN_CERT ?= build/certs/codesign.crt
CODESIGN_KEY ?= build/certs/codesign.key

RELEASE_BINARIES = $(foreach app,$(ALL_APPS),$(foreach platform,$(ALL_PLATFORMS),build/release/$(app)-$(subst /,_,$(platform))$(if $(findstring windows,$(platform)),.exe,)))

WINDOWS_VERSIONS = 1709 1803 1809 1903 1909 2004 20H2 ltsc2022 ltsc2025
BUILDX_BUILDER = buildx-builder
# --provenance/--sbom=false: buildx defaults to emitting an image index (manifest
# list) wrapping a single-platform build to carry attestations. `docker manifest
# create`/`annotate` can't reference into a nested index, which breaks the
# per-platform tags merged by the `images` target below.
DOCKER_EXTRA_FLAGS = --builder $(BUILDX_BUILDER) --provenance=false --sbom=false
# Both Dockerfiles declare BUILD_DATE/VCS_REF/BUILD_VERSION build-args for
# their OCI/label-schema LABELs; without these, every image ships with empty
# version/created/vcs-ref labels.
DOCKER_LABEL_ARGS = --build-arg BUILD_DATE=$(BUILD_DATE) --build-arg VCS_REF=$(SHORT_SHA) --build-arg BUILD_VERSION=$(VERSION)

TOOLCHAIN = $(COMMON_TOOLCHAIN) $(PROTOC_TOOLCHAIN)
tools: $(TOOLCHAIN)

all: no-sudo $(ALL_BINARIES)
assets: $(ASSETS)
protos: $(PROTOS)
windows-binaries: $(WINDOWS_BINARIES)

build/packages/%-binaries.zip: $(ALL_BINARIES)
	mkdir -p $(dir $@)
	(cd build/bin/$*/; zip -qr9 $(REPOSITORY_ROOT)/$@ *)
	touch $(REPOSITORY_ROOT)/$@

release-binaries: $(RELEASE_BINARIES)

build/packages/release.tar.gz: $(ALL_BINARIES)
	mkdir -p $(dir $@)
	cd build/bin/; tar -cvf - * | gzip -9 - > $(REPOSITORY_ROOT)/$@
	touch $(REPOSITORY_ROOT)/$@

ifeq ($(CODESIGN_CERT)|$(CODESIGN_KEY),build/certs/codesign.crt|build/certs/codesign.key)
build/certs/codesign.crt build/certs/codesign.key &: $(CERTTOOL)
	mkdir -p $(dir $(CODESIGN_CERT))
	$(TOOLCHAIN_BIN)/certtool$(EXE) --code-sign --target=linux --public-certificate=$(CODESIGN_CERT) --private-key=$(CODESIGN_KEY)
endif

build/bin/%: $(ASSETS)
	GOOS=$(word 3, $(subst /, ,$(dir $@))) GOARCH=$(word 4, $(subst /, ,$(dir $@))) GOARM=$(subst v,,$(word 5, $(subst /, ,$(dir $@)))) CGO_ENABLED=0 $(GO) build -ldflags="-X 'github.com/cloudfra/template-go/internal.version=$(VERSION)' -X 'github.com/cloudfra/template-go/internal.buildstamp=$(BUILD_DATE)'" -o $@ cmd/$(basename $(notdir $@))/$(basename $(notdir $@)).go
	touch $@

run: cmd/example/example.go
	$(GO) run cmd/example/example.go

lint: lint-go lint-terraform lint-docker lint-yaml lint-shell lint-vuln

lint-terraform: build/toolchain/bin/terraform$(EXE) build/toolchain/bin/tflint$(EXE) build/toolchain/bin/trivy$(EXE)
	(cd install/terraform; $(REPOSITORY_ROOT)/build/toolchain/bin/terraform$(EXE) fmt .)
	$(REPOSITORY_ROOT)/build/toolchain/bin/tflint$(EXE) --init --chdir install/terraform
	$(REPOSITORY_ROOT)/build/toolchain/bin/tflint$(EXE) --chdir install/terraform
	# tflint covers style/correctness, not security misconfigurations (overly
	# permissive IAM, public storage buckets, missing encryption, etc.) -
	# trivy config (successor to the now-maintenance-mode tfsec, folded into
	# trivy) covers that instead, reusing the same tool already pinned for
	# image scanning rather than adding a second, redundant IaC scanner.
	$(REPOSITORY_ROOT)/build/toolchain/bin/trivy$(EXE) config --severity HIGH,CRITICAL --exit-code 1 install/terraform

lint-go: build/toolchain/bin/golangci-lint$(EXE) build/toolchain/bin/gofumpt$(EXE) build/toolchain/bin/revive$(EXE)
	$(GO) fmt ./...
	-build/toolchain/bin/gofumpt$(EXE) -l -w .
	build/toolchain/bin/golangci-lint$(EXE) run ./...
	build/toolchain/bin/revive$(EXE) -set_exit_status -exclude=build/... ./...
	$(GO) mod verify

lint-docker: build/toolchain/bin/hadolint$(EXE)
	$(FIND) cmd -iname 'Dockerfile*' -exec build/toolchain/bin/hadolint$(EXE) {} +

# -shellcheck points explicitly at our pinned binary instead of relying on it
# merely being resolvable via PATH, so `run:` script blocks embedded in
# workflow YAML get checked by shellcheck too (there are no standalone .sh
# scripts in this repo today - see lint-shell).
#
# -ignore silences actionlint v1.7.12's unknown-permission-scope check for
# "code-quality": that scope is real (see the Upload Coverage Report step in
# deploy.yaml) but newer than actionlint's built-in scope list. Drop this
# once actionlint recognizes it upstream.
lint-yaml: build/toolchain/bin/actionlint$(EXE) build/toolchain/bin/shellcheck$(EXE)
	build/toolchain/bin/actionlint$(EXE) -shellcheck=$(REPOSITORY_ROOT)/build/toolchain/bin/shellcheck$(EXE) -ignore 'unknown permission scope "code-quality"'

# No standalone shell scripts exist in this repo yet (embedded workflow
# scripts are covered by lint-yaml's actionlint+shellcheck integration
# instead), but this keeps shellcheck wired to something real for whenever a
# script shows up under cmd/, install/, etc.
lint-shell: build/toolchain/bin/shellcheck$(EXE)
	@scripts="$$($(FIND) . -name '*.sh' -not -path './third_party/*' -not -path './build/*')"; \
	if [ -n "$$scripts" ]; then build/toolchain/bin/shellcheck$(EXE) $$scripts; fi

# Unlike the rest of the lint/test path (which runs under $(GO)'s GOPROXY=off
# to stay offline), govulncheck needs live network access to query vuln.go.dev
# - do not route this through $(GO).
lint-vuln: build/toolchain/bin/govulncheck$(EXE)
	build/toolchain/bin/govulncheck$(EXE) ./...

bench: $(TEST_ASSETS)
	$(GO) test -bench=. -benchmem -tags testing ${SOURCE_DIRS}

benchmark.html: $(TEST_ASSETS) build/toolchain/bin/vizb$(EXE)
	$(GO) test -json -bench=. -benchmem -tags testing ${SOURCE_DIRS} | build/toolchain/bin/vizb$(EXE) -o benchmark.html

test: $(TEST_ASSETS)
	$(GO) test -shuffle=on -tags testing ${SOURCE_DIRS}

tf-test: build/toolchain/bin/terraform$(EXE) $(TEST_ASSETS)
	# -backend=false: main.tftest.hcl mocks the providers and never touches
	# real state, so there's no need to configure the (real, per-environment)
	# GCS backend just to run tests.
	(cd install/terraform/; $(REPOSITORY_ROOT)/build/toolchain/bin/terraform$(EXE) init -backend=false)
	(cd install/terraform/; $(REPOSITORY_ROOT)/build/toolchain/bin/terraform$(EXE) test)

test-deflake: $(TEST_ASSETS)
	CGO_ENABLED=1 $(GO) test -shuffle=on -tags testing $(GO_RACE) ${SOURCE_DIRS} -cover -count $(GO_TEST_COUNT) -test.short

coverage.txt: $(ASSETS)
	for sfile in ${SOURCE_DIRS} ; do \
		go test -race "$$sfile" -coverprofile=package.coverage -covermode=atomic; \
		if [ -f package.coverage ]; then \
			cat package.coverage >> coverage.txt; \
			$(RM) package.coverage; \
		fi; \
	done; \
	sed -i '2,$${/mode: /d;}' $@

coverage.xml: coverage.txt build/toolchain/bin/gocover-cobertura$(EXE)
	$(REPOSITORY_ROOT)/build/toolchain/bin/gocover-cobertura$(EXE) < $< > $@

upgrade-deps:
	$(GO_WITH_PROXY) get -u ./...
	$(GO_WITH_PROXY) mod tidy

deps:
	$(GO_WITH_PROXY) mod download

testassets: $(TEST_ASSETS)
archiveassets: $(TEST_ARCHIVE_ASSETS)
presubmit: lint check

testing/testassets/archives/nodir-testassets.zip: $(TEST_FILE_ASSETS)
	mkdir -p $(dir $@)
	cd testing/testassets/files/assets/; $(ZIP) -qr9 ../../archives/nodir-testassets.zip onetwothree/1.txt onetwothree/2.txt onetwothree/3.txt 1.txt 2.txt sixseven/6.txt sixseven/7.txt

testing/testassets/archives/single-testassets.zip: $(TEST_FILE_ASSETS)
	mkdir -p $(dir $@)
	cd testing/testassets/files/; $(ZIP) -qr9 ../archives/single-testassets.zip .

testing/testassets/archives/nested-testassets.zip: $(TEST_FILE_ASSETS) testing/testassets/archives/single-testassets.zip testing/testassets/archives/testassets.7z
	mkdir -p $(dir $@)
	cd testing/testassets/files/; $(ZIP) -qr9 ../archives/nested-testassets.zip .; $(ZIP) -qr9j ../archives/nested-testassets.zip ../archives/single-testassets.zip ../archives/testassets.7z

testing/testassets/archives/testassets.tar.gz: testing/testassets/archives/testassets.tar
	gzip -9 -k -f $<
	touch $@

testing/testassets/archives/testassets.tar.bz2: testing/testassets/archives/testassets.tar
	bzip2 -9 -k -f $<
	touch $@

testing/testassets/archives/testassets.tar.xz: testing/testassets/archives/testassets.tar
	xz -9 -k -f $<
	touch $@

testing/testassets/archives/testassets.tar.lz4: testing/testassets/archives/testassets.tar
	lz4 -9 -f $< $@
	touch $@

testing/testassets/archives/testassets.tar:
	mkdir -p $(dir $@)
	cd testing/testassets/files/; $(TAR) cf ../archives/testassets.tar *

testing/testassets/archives/testassets.7z:
	mkdir -p $(dir $@)
	cd testing/testassets/files/; $(SEVENZIP) a ../archives/testassets.7z *

testing/testassets/files/index.html:
	mkdir -p $(dir $@)
	echo -n $@ > $@

testing/testassets/files/site.js:
	mkdir -p $(dir $@)
	echo -n $@ > $@

testing/testassets/files/weird\ \#1.txt:
	mkdir -p testing/testassets/files/
	echo -n '$@' > '$@'

testing/testassets/files/weird\ \#.txt:
	mkdir -p $(dir $@)
	echo -n '$@' > '$@'

testing/testassets/files/weird$$.txt:
	mkdir -p $(dir $@)
	echo -n $@ > $@

testing/testassets/files/assets/1.txt:
	mkdir -p $(dir $@)
	echo -n $@ > $@

testing/testassets/files/assets/2.txt:
	mkdir -p $(dir $@)
	echo -n $@ > $@

testing/testassets/files/assets/onetwothree/1.txt:
	mkdir -p $(dir $@)
	echo -n $@ > $@

testing/testassets/files/assets/onetwothree/2.txt:
	mkdir -p $(dir $@)
	echo -n $@ > $@

testing/testassets/files/assets/onetwothree/3.txt:
	mkdir -p $(dir $@)
	echo -n $@ > $@

testing/testassets/files/assets/four/4.txt:
	mkdir -p $(dir $@)
	echo -n $@ > $@

testing/testassets/files/assets/sixseven/6.txt:
	mkdir -p $(dir $@)
	echo -n $@ > $@

testing/testassets/files/assets/sixseven/7.txt:
	mkdir -p $(dir $@)
	echo -n $@ > $@

testing/testassets/files/assets/images/1.txt:
	mkdir -p $(dir $@)
	echo -n $@ > $@

testing/testassets/files/assets/images/2.txt:
	mkdir -p $(dir $@)
	echo -n $@ > $@

# https://picsum.photos/

testing/testassets/files/assets/images/laptop.png:
	mkdir -p $(dir $@)
	curl -L -o $@ "https://file-examples.com/storage/fe5938b8dd69855f49886e9/2017/10/file_example_PNG_500kB.png"

testing/testassets/files/assets/images/Laptop.jpg:
	mkdir -p $(dir $@)
	curl -L -o $@ "https://file-examples.com/storage/fe5938b8dd69855f49886e9/2017/10/file_example_JPG_100kB.jpg"

testing/testassets/files/assets/images/walking-duck.gif:
	mkdir -p $(dir $@)
	curl -L -o $@ "https://www.examplefile.com/images/downloaded.gif"

testing/testassets/files/assets/images/eXample.TIFF:
	mkdir -p $(dir $@)
	curl -L -o $@ "https://file-examples.com/storage/fe5938b8dd69855f49886e9/2017/10/file_example_TIFF_1MB.tiff"

testing/testassets/files/assets/images/blue.ico:
	mkdir -p $(dir $@)
	curl -L -o $@ "https://file-examples.com/storage/fe5938b8dd69855f49886e9/2017/10/file_example_favicon.ico"

testing/testassets/files/assets/images/earth.mp4:
	mkdir -p $(dir $@)
	curl -L -o $@ "https://file-examples.com/storage/fe5938b8dd69855f49886e9/2017/04/file_example_MP4_480_1_5MG.mp4"

testing/testassets/files/assets/images/earth.webm:
	mkdir -p $(dir $@)
	curl -L -o $@ "https://file-examples.com/storage/fe5938b8dd69855f49886e9/2020/03/file_example_WEBM_480_900KB.webm"

testing/testassets/files/assets/images/earth.avi:
	mkdir -p $(dir $@)
	curl -L -o $@ "https://file-examples.com/storage/fe5938b8dd69855f49886e9/2018/04/file_example_AVI_480_750kB.avi"

testing/testassets/files/assets/images/example.svg:
	mkdir -p $(dir $@)
	curl -L -o $@ "https://file-examples.com/wp-content/storage/2020/03/file_example_SVG_20kB.svg"

clean:
	rm -f coverage.txt
	-chmod -R +w build/
	rm -rf build/
	rm -rf output/
	rm -rf testing/testassets/

presubmit: no-sudo tools lint all test-deflake

system-info:
	@echo "Number of Processors"
	@echo "$(shell nproc)"
	@echo ""
	@echo "Kernel Version"
	@uname -a
	@echo ""
	@echo "Storage Metrics"
	@df -h

ensure-builder:
	-$(DOCKER) buildx create --name $(BUILDX_BUILDER)

ALL_DOCKER_IMAGES = $(foreach app,$(ALL_APPS),docker-image-$(app))
docker-images: no-sudo $(ALL_DOCKER_IMAGES)
docker-image-%: build/bin/linux/amd64/% ensure-builder
	$(DOCKER) buildx build $(DOCKER_EXTRA_FLAGS) --platform linux/amd64 --build-arg BINARY_PATH=$< $(DOCKER_LABEL_ARGS) --build-arg BINARY_NAME=$* -f cmd/$*/Dockerfile -t $(REGISTRY)/$*:$(TAG) . $(DOCKER_PUSH)

ALL_SCAN_IMAGES = $(foreach app,$(ALL_APPS),scan-image-$(app))
scan-images: $(ALL_SCAN_IMAGES)

# docker-image-%/linux-images/windows-images use plain `buildx build`, which
# never loads the result into the local Docker daemon for ordinary (non-tag)
# runs - so there's nothing there for trivy to scan. This target does its own
# single-arch --load build purely so trivy has a local image to scan,
# independent of the release push pipeline.
scan-image-%: build/bin/linux/amd64/% build/toolchain/bin/trivy$(EXE) ensure-builder
	$(DOCKER) buildx build $(DOCKER_EXTRA_FLAGS) --platform linux/amd64 --build-arg BINARY_PATH=$< $(DOCKER_LABEL_ARGS) --build-arg BINARY_NAME=$* -f cmd/$*/Dockerfile -t $(REGISTRY)/$*:$(TAG)-scan --load .
	build/toolchain/bin/trivy$(EXE) image --severity HIGH,CRITICAL --exit-code 1 $(REGISTRY)/$*:$(TAG)-scan

ALL_IMAGES = $(foreach app,$(ALL_APPS),$(REGISTRY)/$(app))
# https://github.com/docker-library/official-images#architectures-other-than-amd64
images: no-sudo linux-images windows-images
	for image in $(ALL_IMAGES) ; do \
		$(DOCKER) manifest rm $$image:$(TAG) 2>/dev/null || true ; \
		$(DOCKER) manifest create $$image:$(TAG) $(foreach winver,$(WINDOWS_VERSIONS),$${image}:$(TAG)-windows_amd64-$(winver)) $(foreach platform,$(LINUX_PLATFORMS),$${image}:$(TAG)-$(subst /,_,$(platform))) ; \
		for winver in $(WINDOWS_VERSIONS) ; do \
			windows_version=`$(DOCKER) manifest inspect mcr.microsoft.com/windows/nanoserver:$${winver} | jq -r '.manifests[0].platform["os.version"]'`; \
			$(DOCKER) manifest annotate --os-version $${windows_version} $${image}:$(TAG) $${image}:$(TAG)-windows_amd64-$${winver} ; \
		done ; \
		$(DOCKER) manifest push $$image:$(TAG) ; \
	done

.SECONDEXPANSION:

ALL_LINUX_IMAGES = $(foreach app,$(ALL_APPS),$(foreach platform,$(LINUX_PLATFORMS),linux-image-$(app)-$(subst /,_,$(platform))))
linux-images: $(ALL_LINUX_IMAGES)

# Stems here are "<app>-<platform>" with platform underscore-joined (e.g.
# "example-linux_arm_v7"): GNU Make strips everything before the last "/"
# when matching a slash-free pattern, so a literal "/" in the platform
# portion would never match this rule. $(call platform,...)/$(call appname,...)
# (defined near the bottom of this file) split the stem back apart.
linux-image-%: build/bin/$$(subst _,/,$$(call platform,$$*))/$$(call appname,$$*) ensure-builder
	$(DOCKER) buildx build $(DOCKER_EXTRA_FLAGS) --platform $(subst _,/,$(call platform,$*)) --build-arg BINARY_PATH=$< $(DOCKER_LABEL_ARGS) --build-arg BINARY_NAME=$(call appname,$*) -f cmd/$(call appname,$*)/Dockerfile -t $(REGISTRY)/$(call appname,$*):$(TAG)-$(call platform,$*) . $(DOCKER_PUSH)

ALL_WINDOWS_IMAGES = $(foreach app,$(ALL_APPS),$(foreach winver,$(WINDOWS_VERSIONS),windows-image-$(app)-$(winver)))
windows-images: $(ALL_WINDOWS_IMAGES)

# Stems here are "<app>-<winver>" (e.g. "example-ltsc2022"); reuse the same
# platform/appname split even though the trailing token is a Windows version,
# not an OS/arch pair.
windows-image-%: build/bin/windows/amd64/$$(call appname,$$*).exe ensure-builder
	$(DOCKER) buildx build $(DOCKER_EXTRA_FLAGS) --platform windows/amd64 --build-arg BINARY_PATH=$< $(DOCKER_LABEL_ARGS) --build-arg BINARY_NAME=$(call appname,$*) -f cmd/$(call appname,$*)/Dockerfile.windows --build-arg WINDOWS_VERSION=$(call platform,$*) -t $(REGISTRY)/$(call appname,$*):$(TAG)-windows_amd64-$(call platform,$*) . $(DOCKER_PUSH)

.PHONY: all tools assets protos windows-binaries run lint lint-go lint-terraform lint-docker lint-yaml lint-shell lint-vuln bench test tf-test test-deflake ensure-builder docker-images scan-images images linux-images windows-images upgrade-deps deps clean presubmit system-info release-binaries no-sudo
.SECONDEXPANSION:

# "appname-linux_arm_v5" -> "linux_arm_v5"
platform = $(lastword $(subst -, ,$(basename $(1))))
# strip "-<platform>" to recover app name (hyphen-safe)
appname  = $(patsubst %-$(call platform,$(1)),%,$(basename $(1)))
# source path: build/bin/linux/arm/v5/appname (no extension on sources)
rel2bin  = build/bin/$(subst _,/,$(call platform,$(1)))/$(call appname,$(1))$(if $(findstring windows,$(platform)),.exe,)

# Debian/Ubuntu's binutils package ships objcopy built with only the x86 BFD
# backends (elf64-x86-64, elf32-i386) - it cannot parse ARM/MIPS/PPC/RISC-V/
# LoongArch/s390x ELF at all, regardless of how valid those bytes are. So
# only these two Linux architectures can actually be embed-signed with it;
# every other Linux platform falls through to the plain-copy case below,
# same as darwin/bsd/other platforms with no apt-installable signing tool.
LINUX_OBJCOPY_SIGNABLE_PLATFORMS = linux_386 linux_amd64

build/release/%: $$(call rel2bin,$$*) $(CODESIGN_CERT) $(CODESIGN_KEY)
	@mkdir -p $(@D)
	cp $< $@
	touch $@
	$(if $(findstring windows,$(call platform,$*)),osslsigncode sign -certs $(CODESIGN_CERT) -key $(CODESIGN_KEY) -in $@ -out $@.signed && mv $@.signed $@ && chmod +x $@,)
	$(if $(filter $(LINUX_OBJCOPY_SIGNABLE_PLATFORMS),$(call platform,$*)),openssl cms -sign -binary -in $@ -signer $(CODESIGN_CERT) -inkey $(CODESIGN_KEY) -outform DER -out $@.sig && objcopy --add-section .cloudfra_signature=$@.sig --set-section-flags .cloudfra_signature=noload$(COMMA)readonly $@ && rm -f $@.sig,)
