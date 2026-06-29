# Copyright 2026 Jeremy Edwards
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


GO = go
RM = rm
ZIP = zip
RAR = rar
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

test-root:
	CGO_ENABLED=1 go test -race -tags root ./...

bench:
	CGO_ENABLED=1 go test -bench=. -benchmem -benchtime=3s ./...

bench-root:
	CGO_ENABLED=1 go test -bench=. -benchmem -benchtime=3s -tags root ./...

bin/go/%: $(ASSETS)
	GOOS=$(firstword $(subst _, ,$(notdir $(abspath $(dir $@))))) GOARCH=$(word 2, $(subst _, ,$(notdir $(abspath $(dir $@))))) GOARM=$(subst v,,$(word 3, $(subst _, ,$(notdir $(abspath $(dir $@)))))) CGO_ENABLED=0 \
		$(GO) build -o $@ \
		cmd/$(basename $(notdir $@))/$(basename $(notdir $@)).go
	touch $@

all: build
build: $(ALL_BINARIES) $(ASSETS)

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
	rm -rf bin/
	rm -rf testing/testassets/

deps:
	$(GO) get -u ./...
	$(GO) mod tidy
	$(GO) mod download

.PHONY: all build presubmit lint test check test-100 test-deflake test-root bench bench-root clean deps testassets
