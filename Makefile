export PATH := $(PATH):`go env GOPATH`/bin
export GO111MODULE=on
LDFLAGS := -s -w
NOWEB_TAG = $(shell [ ! -d web/frps/dist ] || [ ! -d web/frpc/dist ] && echo ',noweb')
FRP_COMPAT_BASELINE_COUNT ?= 8
FRP_COMPAT_FLOOR_VERSION ?= 0.61.0

.PHONY: web frps-web frpc-web frps frpc frps-exe frpc-exe frps-linux frpc-linux e2e-compatibility-smoke e2e-compatibility e2e-compatibility-floor

all: env fmt web build

build: frps frpc

build-exe: frps-exe frpc-exe

build-linux: frps-linux frpc-linux

env:
	@go version

web: frps-web frpc-web

frps-web:
	$(MAKE) -C web/frps build

frpc-web:
	$(MAKE) -C web/frpc build

fmt:
	go fmt ./...

fmt-more:
	gofumpt -l -w .

gci:
	gci write -s standard -s default -s "prefix(github.com/fatedier/frp/)" ./

vet:
	go vet -tags "$(NOWEB_TAG)" ./...

frps:
	go build -buildmode=c-shared -trimpath -ldflags "$(LDFLAGS)" -tags "frps$(NOWEB_TAG)" -o bin/frps.dll ./cmd/frps

frpc:
	go build -buildmode=c-shared -trimpath -ldflags "$(LDFLAGS)" -tags "frpc$(NOWEB_TAG)" -o bin/frpc.dll ./cmd/frpc

frps-exe:
	env CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -tags "frps$(NOWEB_TAG)" -o bin/frps ./cmd/frps

frpc-exe:
	env CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -tags "frpc$(NOWEB_TAG)" -o bin/frpc ./cmd/frpc

frps-linux:
	env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -tags "frps$(NOWEB_TAG)" -o bin/frps_linux ./cmd/frps

frpc-linux:
	env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -tags "frpc$(NOWEB_TAG)" -o bin/frpc_linux ./cmd/frpc

test: gotest

gotest:
	go test -tags "$(NOWEB_TAG)" -v --cover ./assets/...
	go test -tags "$(NOWEB_TAG)" -v --cover ./cmd/...
	go test -tags "$(NOWEB_TAG)" -v --cover ./client/...
	go test -tags "$(NOWEB_TAG)" -v --cover ./server/...
	go test -tags "$(NOWEB_TAG)" -v --cover ./pkg/...

e2e:
	./hack/run-e2e.sh

e2e-trace:
	DEBUG=true LOG_LEVEL=trace ./hack/run-e2e.sh

e2e-compatibility-smoke: build
	FRP_COMPAT_BASELINE_COUNT=1 ./hack/run-e2e-compatibility.sh

e2e-compatibility: build
	FRP_COMPAT_BASELINE_COUNT="$(FRP_COMPAT_BASELINE_COUNT)" ./hack/run-e2e-compatibility.sh

e2e-compatibility-floor: build
	FRP_COMPAT_BASELINE_VERSIONS="$(FRP_COMPAT_FLOOR_VERSION)" ./hack/run-e2e-compatibility.sh

e2e-compatibility-last-frpc:
	if [ ! -d "./lastversion" ]; then \
		TARGET_DIRNAME=lastversion ./hack/download.sh; \
	fi
	FRPC_PATH="`pwd`/lastversion/frpc" ./hack/run-e2e.sh
	rm -r ./lastversion

e2e-compatibility-last-frps:
	if [ ! -d "./lastversion" ]; then \
		TARGET_DIRNAME=lastversion ./hack/download.sh; \
	fi
	FRPS_PATH="`pwd`/lastversion/frps" ./hack/run-e2e.sh
	rm -r ./lastversion

alltest: vet gotest e2e

clean:
	rm -f ./bin/frpc
	rm -f ./bin/frpc.dll
	rm -f ./bin/frpc.h
	rm -f ./bin/frpc_linux
	rm -f ./bin/frps
	rm -f ./bin/frps.dll
	rm -f ./bin/frps.h
	rm -f ./bin/frps_linux
	rm -rf ./lastversion
	rm -rf ./.cache
	rm -rf ./.compat
