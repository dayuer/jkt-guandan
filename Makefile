# 「随时掼掼蛋」常用命令，对应 docs/server-design.md §8、§9、§13.5。
GO ?= go
NPM ?= npm

.PHONY: help web-install web-build build vet go-test race-test vuln web-test test e2e run seed docker deploy-test restore-drill clean

help:
	@echo "make build    构建二进制（会先构建前端，再用 embed 打进去）"
	@echo "make run      开发模式跑起来：GUANDAN_DEV=1，监听 :8080，数据写在 ./.data"
	@echo "make test     go vet + 后端测试（-race）+ 前端测试"
	@echo "make e2e      浏览器端到端冒烟（Playwright + 无头 Chromium，先构建二进制）"
	@echo "make vuln     govulncheck"
	@echo "make seed     往 ./.data 写入设计稿里的示例数据"
	@echo "make docker   构建 Docker 镜像"
	@echo "make deploy-test    用 Docker 验证 scripts/deploy.sh 的自动回滚"
	@echo "make restore-drill  用 Docker 演练备份恢复（docs/runbook.md）"

web-install:
	cd web && $(NPM) ci

web-build: web-install
	cd web && $(NPM) run build

BUILD ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)

build: web-build
	CGO_ENABLED=0 $(GO) build -trimpath \
		-ldflags="-s -w -X github.com/dayuer/jkt-guandan/internal/buildinfo.Build=$(BUILD)" \
		-o bin/guandan ./cmd/guandan

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

# 浏览器端到端冒烟（#72）。第一次跑之前装一次浏览器：cd web && npx playwright install chromium
e2e: build
	cd web && $(NPM) run e2e

run: build
	GUANDAN_DEV=1 GUANDAN_DATA=./.data ./bin/guandan

seed: build
	GUANDAN_DEV=1 GUANDAN_DATA=./.data ./bin/guandan seed

docker:
	docker build --build-arg BUILD=$(BUILD) -t guandan:local .

deploy-test: docker
	scripts/test-deploy.sh guandan:local

restore-drill: docker
	scripts/restore-drill.sh guandan:local

clean:
	rm -rf bin .data web/dist/*
