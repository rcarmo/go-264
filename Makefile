SHELL := /bin/bash
.DEFAULT_GOAL := help
# Export only supplied overrides before resolution; quoting values into shell code is unsafe.
ifneq ($(origin PROJECT_TMP_ROOT),undefined)
export PROJECT_TMP_ROOT
endif
ifneq ($(origin PROJECT_TMP_BASE),undefined)
export PROJECT_TMP_BASE
endif
ROOT_INFO := $(shell PROJECT=go-264 bash scripts/project-tmp.sh paths 2>&1)
ifeq ($(filter PROJECT_TMP_ROOT=%,$(ROOT_INFO)),)
$(error go-264 scratch resolution failed: $(ROOT_INFO))
endif
override PROJECT_TMP_ROOT := $(patsubst PROJECT_TMP_ROOT=%,%,$(filter PROJECT_TMP_ROOT=%,$(ROOT_INFO)))
CACHE_ROOT := $(PROJECT_TMP_ROOT)/cache
BUILD_ROOT := $(PROJECT_TMP_ROOT)/build
RUN_ROOT := $(PROJECT_TMP_ROOT)/runs
TEST_ROOT := $(PROJECT_TMP_ROOT)/tests
LOG_ROOT := $(PROJECT_TMP_ROOT)/logs
# CI retains tests/ and logs/ as artifacts; never delete them with scratch.
GO264_EVIDENCE_ROOT ?= $(if $(filter true TRUE 1,$(CI)),$(PROJECT_TMP_ROOT),$(if $(wildcard /workspace/reports),/workspace/reports/go-264,$(PROJECT_TMP_ROOT)))
GO264_FIXTURE_ROOT ?= $(if $(filter $(PROJECT_TMP_ROOT),$(GO264_EVIDENCE_ROOT)),$(TEST_ROOT)/fixtures,$(GO264_EVIDENCE_ROOT)/fixtures)
GO264_CONFORMANCE_ROOT ?= $(GO264_FIXTURE_ROOT)/h264-conformance
FFMPEG_SRC ?= $(CACHE_ROOT)/ffmpeg/ffmpeg-7.1.3
GO264_FFMPEG_BIN ?= $(BUILD_ROOT)/ffmpeg-7.1.3/ffmpeg
GO264_BBB_FIXTURE ?= $(GO264_FIXTURE_ROOT)/bbb_annexb.h264
RUN_ID := $(shell date -u +%Y%m%dT%H%M%SZ)-$(shell echo $$$$)
export PROJECT_TMP_ROOT CACHE_ROOT BUILD_ROOT RUN_ROOT TEST_ROOT LOG_ROOT GO264_EVIDENCE_ROOT
export GO264_FIXTURE_ROOT GO264_CONFORMANCE_ROOT FFMPEG_SRC GO264_FFMPEG_BIN GO264_BBB_FIXTURE
export GO264_RUN_ROOT := $(RUN_ROOT)
export GO264_BUILD_ROOT := $(BUILD_ROOT)
export GO264_CACHE_ROOT := $(CACHE_ROOT)
export GO264_TEST_ROOT := $(TEST_ROOT)
export GO264_LOG_ROOT := $(LOG_ROOT)
export GOCACHE := $(CACHE_ROOT)/go-build
export GOMODCACHE := $(CACHE_ROOT)/go-mod
export GOPATH := $(CACHE_ROOT)/go-path
export XDG_CACHE_HOME := $(CACHE_ROOT)/xdg
export TMPDIR := $(RUN_ROOT)/make/$(RUN_ID)/temp
export TMP := $(TMPDIR)
export TEMP := $(TMPDIR)
export GOTMPDIR := $(RUN_ROOT)/make/$(RUN_ID)/go
export PYTHONPYCACHEPREFIX := $(CACHE_ROOT)/python/pycache
export PIP_CACHE_DIR := $(CACHE_ROOT)/python/pip
export UV_CACHE_DIR := $(CACHE_ROOT)/uv
PROFILE_TEST := ./scripts/test-profile.sh

.PHONY: help tmp-init test test-race test-purego test-focused profile-benchmark vet arm64-build fixture-status fixture-status-strict fixture-status-test phase4-bootstrap phase4-test
help:
	@printf '%s\n' 'tmp-init: initialise project-owned cache/build/runs layout (never deletes)' 'test: full profiled Go suite' 'test-race: profiled race suite' 'test-purego: profiled purego suite' 'test-focused PKG=./decode RUN=TestName: profiled focused tests' 'profile-benchmark PKG=./decode BENCH=BenchmarkName: representative profiles' 'vet: go vet ./...' 'arm64-build: Linux ARM64 cross-build' 'fixture-status[-strict], fixture-status-test, phase4-bootstrap, phase4-test: external fixture gates'
tmp-init:
	@PROJECT=go-264 bash scripts/project-tmp.sh init >/dev/null
	@for p in $(GOCACHE) $(GOMODCACHE) $(GOPATH) $(XDG_CACHE_HOME) $(PYTHONPYCACHEPREFIX) $(PIP_CACHE_DIR) $(UV_CACHE_DIR) $(TMPDIR) $(GOTMPDIR) $(BUILD_ROOT) $(TEST_ROOT) $(LOG_ROOT); do \
	  test ! -L "$$p" && { test ! -e "$$p" || { test -d "$$p" && test -O "$$p"; }; } || { echo "Refusing unsafe path $$p" >&2; exit 1; }; \
	  mkdir -p "$$p"; done
test: tmp-init
	@$(PROFILE_TEST) ./...
test-race: tmp-init
	@$(PROFILE_TEST) -race ./decode ./frame ./filter ./pred ./transform
test-purego: tmp-init
	@CGO_ENABLED=0 $(PROFILE_TEST) -tags purego ./...
test-focused: tmp-init
	@test -n "$(PKG)" && test -n "$(RUN)" || { echo 'Set PKG=./package RUN=TestName' >&2; exit 2; }
	@$(PROFILE_TEST) -run '$(RUN)' '$(PKG)'
profile-benchmark: tmp-init
	@test -n "$(PKG)" && test -n "$(BENCH)" || { echo 'Set PKG=./package BENCH=BenchmarkName' >&2; exit 2; }
	@$(PROFILE_TEST) -run '^$$' -bench '$(BENCH)' -benchtime '$(or $(BENCHTIME),5x)' '$(PKG)'
vet: tmp-init
	@go vet ./...
arm64-build: tmp-init
	@GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...
fixture-status:
	@./scripts/fixture_gate_status.sh
fixture-status-strict:
	@./scripts/fixture_gate_status.sh --strict
fixture-status-test: tmp-init
	@./scripts/fixture_gate_status_test.sh
phase4-bootstrap: tmp-init
	@./scripts/bootstrap_phase4_fixtures.sh
phase4-test: tmp-init
	@GO264_PHASE4_REGRESSION=1 $(PROFILE_TEST) -run 'TestFFmpegReferenceParityPhase4|TestOfficialReferenceSyntax' ./cmd/decode264 ./decode
