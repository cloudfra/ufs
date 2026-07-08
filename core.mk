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

EXE =
FIND = find

ifeq ($(OS),Windows_NT)
	HOST_OS = windows
	HOST_PLATFORM = windows_amd64
	HOST_ARCH = amd64
	# Give priority to /usr/bin because it conflicts with C:\Windows\system32 within Msys32 environment.
	FIND = /usr/bin/find.exe
	EXE = .exe
	SED_REPLACE = sed -i
else
	UNAME_S := $(shell uname -s)
	UNAME_ARCH := $(shell uname -m)
	ifeq ($(UNAME_S),Linux)
		HOST_OS = linux
		SED_REPLACE = sed -i
		ifeq ($(UNAME_ARCH),arm)
			HOST_PLATFORM = linux_arm
			HOST_ARCH = arm
		else
			HOST_PLATFORM = linux_amd64
			HOST_ARCH = amd64
		endif
	endif
	ifeq ($(UNAME_S),Darwin)
		HOST_OS = darwin
		HOST_PLATFORM = darwin_amd64
		SED_REPLACE = sed -i ''
	endif
endif

# Directory containing this makefile (i.e. the repository root), regardless
# of where `make` is invoked from.
REPOSITORY_ROOT := $(patsubst %/,%,$(dir $(abspath $(lastword $(MAKEFILE_LIST)))))
BUILD_DIR = $(REPOSITORY_ROOT)/build
ARCHIVES_DIR = $(BUILD_DIR)/archives
TOOLCHAIN_DIR = $(BUILD_DIR)/toolchain
TOOLCHAIN_BIN = $(TOOLCHAIN_DIR)/bin
THIRDPARTY_DIR = $(REPOSITORY_ROOT)/third_party

TOOLCHAIN_GO = go
TOOLCHAIN_GO_INSTALL = GOPATH=$(TOOLCHAIN_DIR) $(TOOLCHAIN_GO) install
CURL = curl --retry 5 --retry-connrefused

no-sudo:
ifndef ALLOW_BUILD_WITH_SUDO
ifeq ($(shell whoami),root)
	@echo "ERROR: Running Makefile as root (or sudo)"
	@echo "Please follow the instructions at https://docs.docker.com/install/linux/linux-postinstall/ if you are trying to sudo run the Makefile because of the 'Cannot connect to the Docker daemon' error."
	@echo "NOTE: sudo/root do not have the authentication token to talk to any GCP service via gcloud."
	exit 1
endif
endif

# A literal "," inside a $(if ...) argument is parsed as another argument
# separator, not part of the text - COMMA hides it behind a nested
# variable reference so it survives into objcopy's --set-section-flags.
COMMA := ,
