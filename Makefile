# 「随时掼掼蛋」常用命令，对应 docs/server-design.md §8、§9。
GO ?= go
NPM ?= npm

.PHONY: help web-install web-build build vet go-test web-test test run docker clean

help:
	@echo "make build    构建二进制（会先构建前端再用 embed 打进去）"
	@echo "make run      本地起来看看，监听 :8080，数据写在 ./.data"
	@echo "make test     go vet + 后端测试 + 前端测试"
	@echo "make docker   构建 Docker 镜像"

web-install:
	cd web && $(NPM) ci

web-build: web-install
	cd web && $(NPM) run build

build: web-build
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o bin/guandan ./cmd/guandan

vet:
	$(GO) vet ./...

go-test:
	$(GO) test ./...

web-test: web-install
	cd web && $(NPM) test

# 先构建前端，再跑后端测试——这样 httpapi 的用例能真正验证内嵌进去的页面。
test: vet web-build go-test web-test

run: build
	GUANDAN_DATA=./.data ./bin/guandan

docker:
	docker build -t guandan:local .

clean:
	rm -rf bin .data web/dist/*
