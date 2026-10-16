# 「随时掼掼蛋」常用命令，对应 docs/server-design.md §8、§9、§13.5。
GO ?= go
NPM ?= npm

.PHONY: help web-install web-build build vet go-test race-test vuln web-test shared-install shared-check test e2e run seed docker deploy-test restore-drill clean

help:
	@echo "make build    构建二进制（会先构建前端，再用 embed 打进去）"
	@echo "make run      开发模式跑起来：GUANDAN_DEV=1，监听 :8080，数据写在 ./.data"
	@echo "make test     go vet + 后端测试（-race）+ 前端测试 + 共享层 typecheck"
	@echo "make e2e      浏览器端到端冒烟（Playwright + 无头 Chromium，先构建二进制）"
	@echo "make vuln     govulncheck"
	@echo "make seed     往 ./.data 写入设计稿里的示例数据"
	@echo "make docker   构建 Docker 镜像"
	@echo "make deploy-test    用 Docker 验证 scripts/deploy.sh 的自动回滚"
	@echo "make restore-drill  用 Docker 演练备份恢复（docs/runbook.md）"

web-install:
	cd app/h5 && $(NPM) ci

web-build: web-install
	cd app/h5 && $(NPM) run build

BUILD ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)

build: web-build
	CGO_ENABLED=0 $(GO) build -trimpath \
		-ldflags="-s -w -X github.com/dayuer/jkt-guandan/server/internal/buildinfo.Build=$(BUILD)" \
		-o bin/guandan ./server/cmd/guandan

vet:
	$(GO) vet ./...

# race 检测需要 CGO；发布构建仍然关掉 CGO（§9）。
race-test:
	$(GO) test -race ./...

vuln:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...

web-test: web-install
	cd app/h5 && $(NPM) test

# 共享层守门要一份**带 JS API** 的 typescript 编译器：app/h5 装的是 typescript 7（native 版，
# 没有 createSourceFile），所以用小游戏那份（devDependency `typescript ^5.6`）。
# 这里只在**缺依赖时**装 —— 每次 make test 都 npm ci 一遍要 40 多秒，而它平时都在。
shared-install:
	@test -d app/minigame/node_modules/typescript || (cd app/minigame && $(NPM) ci)

# 两端共用的那层（app/shared）的两道门（#253）：规矩与理由见 app/shared/README.md。
#   1) 单独 typecheck —— 它有自己的 tsconfig，而且**不带 DOM lib**：共享层里一旦冒出
#      window / document / localStorage 这类浏览器 API，这里当场编译不过，不用靠人记得；
#      tsc 装在 app/h5 的 node_modules 里，所以从那儿跑，--no-install 免得悄悄去下载。
#   2) 文件之间只许 `import type`（值 import 会让两端在运行期互相牵连，tsc 拦不住），
#      由 scripts/check-shared-imports.mjs 用编译器 API 扫；它自己的自测（那些该红/不该红的写法）
#      跟在后面一起跑 —— 这道门要是哑了，比没有更坏。
# 默认值（窗口地址、本地存储、时间写法）都归各端的壳。
shared-check: web-install shared-install
	cd app/h5 && npx --no-install tsc -p ../shared --noEmit
	node scripts/check-shared-imports.mjs
	node --test scripts/check-shared-imports.test.mjs

# 先构建前端再跑后端测试，这样 httpapi 的用例能真正验证内嵌进去的页面。
test: vet web-build race-test web-test shared-check

# 浏览器端到端冒烟（#72）。第一次跑之前装一次浏览器：cd app/h5 && npx playwright install chromium
# 端口每次运行单独分配（#228），本机几个会话同时跑也不会撞；要固定端口就自己设 E2E_PORT。
e2e: build
	@port=$${E2E_PORT:-$$(scripts/e2e-port.sh)} || exit 1; \
	echo "e2e 服务端端口：$$port"; \
	cd app/h5 && E2E_PORT=$$port $(NPM) run e2e

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
	rm -rf bin .data app/h5/dist/*
