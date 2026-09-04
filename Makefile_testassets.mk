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
TEST_FILE_ASSETS += testing/testassets/files/assets/deep/x/y/1.txt
TEST_FILE_ASSETS += testing/testassets/files/assets/deep/x/y/2.txt
TEST_FILE_ASSETS += testing/testassets/files/assets/deep/x/3.txt
TEST_FILE_ASSETS += testing/testassets/files/assets/deep/z/4.txt
TEST_FILE_ASSETS += testing/testassets/files/assets/deep/5.txt


TEST_ARCHIVE_ASSETS = testing/testassets/archives/nodir-testassets.zip
TEST_ARCHIVE_ASSETS += testing/testassets/archives/nodir-deep-testassets.zip
TEST_ARCHIVE_ASSETS += testing/testassets/archives/single-testassets.zip
TEST_ARCHIVE_ASSETS += testing/testassets/archives/nested-testassets.zip
TEST_ARCHIVE_ASSETS += testing/testassets/archives/testassets.tar.gz
TEST_ARCHIVE_ASSETS += testing/testassets/archives/testassets.tar.bz2
TEST_ARCHIVE_ASSETS += testing/testassets/archives/testassets.tar.xz
TEST_ARCHIVE_ASSETS += testing/testassets/archives/testassets.tar.lz4
TEST_ARCHIVE_ASSETS += testing/testassets/archives/testassets.tar
TEST_ARCHIVE_ASSETS += testing/testassets/archives/testassets.7z

TEST_ASSETS = $(TEST_FILE_ASSETS) $(TEST_ARCHIVE_ASSETS)

testing/testassets/archives/nodir-testassets.zip: $(TEST_FILE_ASSETS)
	mkdir -p $(dir $@)
	cd testing/testassets/files/assets/; $(ZIP) -qr9 ../../archives/nodir-testassets.zip onetwothree/1.txt onetwothree/2.txt onetwothree/3.txt 1.txt 2.txt sixseven/6.txt sixseven/7.txt

testing/testassets/archives/nodir-deep-testassets.zip: $(TEST_FILE_ASSETS)
	mkdir -p $(dir $@)
	cd testing/testassets/files/assets/; $(ZIP) -qr9 ../../archives/nodir-deep-testassets.zip deep/x/y/1.txt deep/x/y/2.txt deep/x/3.txt deep/z/4.txt deep/5.txt onetwothree/1.txt onetwothree/2.txt

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

testing/testassets/files/assets/deep/x/y/1.txt:
	mkdir -p $(dir $@)
	echo -n $@ > $@

testing/testassets/files/assets/deep/x/y/2.txt:
	mkdir -p $(dir $@)
	echo -n $@ > $@

testing/testassets/files/assets/deep/x/3.txt:
	mkdir -p $(dir $@)
	echo -n $@ > $@

testing/testassets/files/assets/deep/z/4.txt:
	mkdir -p $(dir $@)
	echo -n $@ > $@

testing/testassets/files/assets/deep/5.txt:
	mkdir -p $(dir $@)
	echo -n $@ > $@

testassets: $(TEST_ASSETS)
archiveassets: $(TEST_ARCHIVE_ASSETS)

.PHONY: testassets archiveassets
