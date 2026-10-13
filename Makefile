# 「随时掼掼蛋」常用命令，对应 docs/server-design.md §8、§9、§13.5。
GO ?= go
NPM ?= npm

.PHONY: help web-install web-build build vet go-test race-test vuln web-test test run docker clean

help:
	@echo "make build    构建二进制（会先构建前端，再用 embed 打进去）"
	@echo "make run      开发模式跑起来：GUANDAN_DEV=1，监听 :8080，数据写在 ./.data"
	@echo "make test     go vet + 后端测试（-race）+ 前端测试"
	@echo "make vuln     govulncheck"
	@echo "make docker   构建 Docker 镜像"

web-install:
	cd web && $(NPM) ci

web-build: web-install
	cd web && $(NPM) run build

build: web-build
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o bin/guandan ./cmd/guandan

vet:
	$(GO) vet ./...

# race 检测需要 CGO；发布构建仍然关掉 CGO（§9）。
race-test:
	$(GO) test -race ./...

vuln:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...

web-test: web-install
	cd web && $(NPM) test

# 先构建前端再跑后端测试，这样 httpapi 的用例能真正验证内嵌进去的页面。
test: vet web-build race-test web-test

run: build
	GUANDAN_DEV=1 GUANDAN_DATA=./.data ./bin/guandan

docker:
	docker build -t guandan:local .

clean:
	rm -rf bin .data web/dist/*
