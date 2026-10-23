> 移植自 yardmaster 使用说明，版本 dev，2026-10-23。
> 模板来源：dayuer/yardmaster 的 `docs/USAGE.md`；项目名与仓库地址已替换。

# 使用说明

> 安装后可直接用 `ym help` 查看总览，`ym help all` 查看全部主题；本文是更详细的版本。master 的流程顺序见 [WORKFLOW.md](WORKFLOW.md)。

## 1. 概念

| 概念 | 含义 |
|---|---|
| Project | 一个 git 仓库 + 一组 agent + 独立配额，如 `jkt-guandan` |
| Agent | `<project>/<name>`，如 `jkt-guandan/impl-a`，绑定一个角色 |
| Role | `master` / `auditor` / `worker`；每项目一个 master |
| Task | 一个 GitHub issue，写作 `owner/repo#N` |
| Event | 消息类型：`dispatch`、`claim`、`step`、`review` 等 |
| Inbox | 每个 agent 一个独立队列；消息只带指针和摘要 |
| State | `queued → delivered → read → acked`；超时转 `overdue`/`dead` |
| Ledger | GitHub issue/PR 评论，唯一事实来源 |

## 2. 前置条件

- 服务器：Docker、域名 `yard.synon.ai`、nginx + 证书。
- agent 机器：macOS 或 Linux，能访问 `https://yard.synon.ai`。
- 每个 agent 有模型凭据（API key 或订阅），Yardmaster 不接触。
- master 机器有 `gh` 和该项目 GitHub 写 token。
- worker 机器有 git 凭据，能推分支；协议约定 worker 不写 GitHub 评论、PR、label。
- auditor 机器有 `gh` 和一个能在 PR 上评论的 GitHub token，只用来贴自己的 `REVIEW` 票评论；这是协议约定，GitHub 侧当前并不限制（共用登录、评论写权限与 label 写权限同源，见 [SECURITY.md](SECURITY.md) / 上游）。

## 3. 安装

服务端用 Docker，见 DEPLOYMENT.md。

客户端：

```bash
# 从 release 下载，或从源码构建
go install github.com/dayuer/yardmaster/cmd/ym@latest   # ym 工具从 yardmaster 仓库安装
ym version
```

把 `ym` 放进 `PATH`。macOS 上 refresh token 存 Keychain，Linux 存 `~/.config/yardmaster/credentials`（600）。

## 4. 服务器初始化

在服务器上通过 `docker exec` 执行，不经过公网：

```bash
docker exec -it yardmaster /ym admin project add jkt-guandan \
  --repo dayuer/jkt-guandan --branch main --max-workers 3

docker exec -it yardmaster /ym admin agent enroll \
  --project jkt-guandan --name master --role master
# 输出一次 refresh token：ymr_xxx

docker exec -it yardmaster /ym admin agent enroll \
  --project jkt-guandan --name impl-a --role worker
```

每个 agent 的 token 只显示一次，立刻交给对应机器。

### 4.1 项目初始化（master 的第一个 issue）

项目建好后，由该项目的 master 执行一次初始化：建标签、开第一个 issue、写看板评论。

```bash
ym init --project yardmaster \
  --wave "M1 核心" \
  --title "M1 服务骨架" \
  --goal "单项目 HTTP API + CLI + SQLite，能完成 send/claim/ack" \
  --scope "cmd/,internal/" \
  --acceptance "1. /healthz 200; 2. send/claim/ack 可用; 3. worker 发 review 返回 403"
```

产出：

- 标签：`type:*`、`P0..P2`、`status:*`、`owner:*`、`needs-user`
- 第一个 issue：`[coord] 派工：...`，含目标、范围、验收标准、依赖、风险
- master 的 `DECISION` 评论

`ym init` 做四件事：建标准标签、开首个派工 issue（打 `yardmaster:init` 标签）、写 `DECISION` 评论、更新 `CLAUDE.md` 托管块。重复执行会先按 `yardmaster:init` 查找，已有就跳过建 issue；`--dry-run` 只打印不写。

### 4.2 协议如何送达 agent

三层，任何一层失效另外两层兜底：

1. **CLAUDE.md 托管块**：`ym init` 写入，只放入口和硬规则，不放全文。
2. **SessionStart hook**：调用 `scripts/protocol-context.sh`，注入约 15 行的角色卡；装了 `ym` 时委托 `ym protocol --inject`，从服务端取当前版本。
3. **服务端闸门**：角色、必填字段、密钥、收件箱越权都在服务端拒绝，不依赖 agent 是否读过协议。

macOS 上另有一个坑：系统的「本地网络」隐私权限会挡住从 SSH 会话挂到后台的 postman（报 `EHOSTUNREACH` / `no route to host`）。postman 应与会话一起从本机终端启动（上游 yardmaster 的 scripts/agent-up.sh 负责这件事），不要在 SSH 里 nohup。

.claude/agents/master.md、`yard-auditor.md`、`worker.md` 放各角色的职责和禁止事项；spawn 子 agent 时用 `--agent` 加载。全文始终只在 `docs/COORDINATION.md`。

启动会话前设置角色，角色卡才知道该注入哪一版：

```bash
AGENT_ROLE=worker claude
AGENT_ROLE=master claude --name jkt-guandan-master
```

手工启动时 `--name` 也用 `<project>-<name>`：服务端名字在项目内唯一，不同项目可能有同名 agent，本地会话名需要带项目前缀。

### 4.3 需求拆解

需求先成 `type:requirement` issue，再由 master 拆成 `type:task`。

```bash
ym req new --title "M2：实时推送" --body "把收件箱接入 WS 推送" --priority P1
# requirement: #2 https://...
```

master 写 plan JSON（示例见 examples/plan.example.json），然后：

```bash
ym plan --epic 2 --file plan.json
```

产物：

- 每个 task 一个 `[coord] 派工：...` issue，带 `type:task`、`P*`、`status:accepted`、`owner:impl`
- 需求 issue 追加任务列表和 `<!-- yardmaster:planned -->`
- 需求 issue 从 `status:triage` 改为 `status:accepted`

没有拆解的需求不派工。重复拆解会被 `yardmaster:planned` 拦住；确需追加用 `--force`。

### 4.4 强制客户端版本（可选）

协议真的不兼容时，服务端可以硬拒旧客户端，而不是让它带着旧语义继续工作——
旧客户端"看起来完全正常"是过去最贵的故障来源。

```toml
# config.toml；留空 = 不拒任何客户端（闸门关闭）
required_client_commits = "abc1234"   # 也可以用环境变量 YM_REQUIRED_CLIENT_COMMITS
```

这是一份**允许清单，不是「最低版本」**：commit 无法排序，客户端跑在服务端前面（`ym upgrade` 之后）是正常状态，所以判定只有「在清单里 / 不在清单里」。

| 开关 | 作用 |
|---|---|
| `required_client_commits` | 逗号分隔的允许清单。留空 = 不拒任何客户端（只有配了清单才生效）。滚动升级期间把新旧两个都写上，升完删掉旧的 |
| 短 sha | 短写法和完整 sha 双向互认：清单项写 `abc1234`、客户端发完整 40 位，或反过来，都算同一个 commit |
| 校验 | 每一项必须是 7–40 位十六进制。拼错、大小写以外的字符、空项都会让**服务端启动失败**，而不是静默拒掉所有客户端 |
| `X-Ym-Client-Commit` | 客户端每个 HTTP 请求和 WebSocket 握手都带自己的构建 commit（来自 ldflags）。**不带这个头**（直接 `go build` 的开发构建）时一律放行 |
| 豁免路径 | `GET /healthz` 和 `GET /version` 永远不被拒，否则升级路径自锁死：被拒的客户端连"该升到哪个版本"都问不到 |

被拒时返回 **426 Upgrade Required**（不是 403——那会被当成凭据问题），body 里写明允许清单、本机 commit 和升级方式；`ym` 会把它打印成一句人话，不吐原始 JSON。

`GET /version` 不需要 token，返回服务端 commit、版本号和当前生效的 `required_client_commits`（空数组 = 闸门关闭），脚本可以直接用它判断。响应头里的 `X-Ym-Min-Client` 是单值的**提示**，权威清单以 `/version` 为准。

### 4.5 无仓库机器自我升级（--from-server）

没有 git 仓库的机器（agent 机）没有东西可编译：`ym upgrade` 会从服务端下载**服务端自己那一份**二进制。

```bash
ym upgrade --check                      # 只比较本机与服务端的 commit（不需要仓库）
ym upgrade --client-only --from-server  # 下载 ym/ym-gate/ym-postman 并原子替换
```

- 没有 `--repo` 且默认目录不是 git 检出时自动走 `--from-server`；显式给了 `--repo` 就坚持 git 路径，不是仓库就报错。
- `--url` 默认取 agent 凭据里的服务端地址（`YM_URL` 可覆盖），不再写死 127.0.0.1。
- 服务端只分发**与自己同平台**（`runtime.GOOS/GOARCH`）的 ym/ym-gate/ym-postman，其他平台返回 404 并提示用 `--repo` 自己编译。`/api/v1/dist/...` 需要有效 agent token；版本门对它放行，所以被 426 拒掉的客户端仍能取到新二进制。
- 三重校验：响应头 `X-Ym-Sha256` 必须等于下载字节的 sha256；`X-Ym-Commit` 必须等于服务端 `/healthz` 的 commit；下载的 `ym version` 自报的 commit 也必须是同一个。任一不符就丢弃临时文件、**不动现有二进制**。
- 服务端在另一台机器时，本机没有可管理的 server 进程：升级只替换客户端二进制，不会去探测或重启远端服务端。
- 边界：这是可信局域网内的便利路径。服务端若是明文 HTTP，sha256 与二进制走同一通道，防不了中间人同时替换两者；公网或不可信网络请在带仓库的机器上用 `--repo` 从源码构建。

## 5. 客户端登录

```bash
ym login --project jkt-guandan --agent master --token ymr_xxx
ym whoami
# jkt-guandan/master (master)  access expires in 59m
```

- `login` 用 refresh token 换一次 access token，并把 refresh 存进 Keychain。
- 之后所有命令自动换票，access token 到期前自动续。
- 换机器时重新 enroll 一个 agent，不复用 token。

## 6. 启动角色

### 6.1 master

master 是一个长期交互会话。注册本机 socket，让 postman 能叫醒它：

```json
{
  "hooks": {
    "SessionStart": [{
      "hooks": [{
        "type": "command",
        "command": "ym register-session --pid $PPID"
      }]
    }],
    "SessionEnd": [{
      "hooks": [{
        "type": "command",
        "command": "ym unregister-session"
      }]
    }]
  }
}
```

`ym register-session` 读取 `CLAUDE_CODE_MESSAGING_SOCKET`，写入
`$YM_CONFIG_DIR/sessions/<agent>.json`（600，按 agent 分文件；未设
`YM_CONFIG_DIR` 时是 `~/.config/yardmaster/`）。

在 master 机器上另开一个 postman：

```bash
ym-postman --agent master
```

postman 连上后先发 `hello`，把离线期间的 `queued` 邮件一次性标成 `delivered` 登记进执行队列；空闲且队列非空时只注入一条指针「队列 N 条；执行 ym inbox --next」，要详情的用 `ym claim`。

master 每 5 分钟跑一次巡检，看各 agent 的积压和超时：

```bash
ym supervise --json
ym agents
```

离线、未读超时、`overdue` 的处理见 §7.6。

### 6.2 worker

两种方式。

**手动**：

每个 agent 只认领自己的收件箱；认领不需要角色参数，身份来自 token。

```bash
ym inbox --state unread              # 先看有没有未读
ym claim --json                      # 认领即标记 read
ym read <msg_id>                     # 只看不认领时用
# 按返回的任务干活
ym send --event step --step 1/3 --files "server/x.py" --result "tests pass"
ym send --event done --branch impl/42 --commit abc123 --summary "完成"
ym sent --state unacked              # 看自己发出去的消息被读了没有
```

**runner**（每任务拉起一次 `claude -p`）：

```bash
ym-runner --project jkt-guandan --agent impl-a --max 1
```

runner 自己认领、拼 prompt、拉起模型、解析结果、发消息，不需要 MCP。

### 6.3 auditor

```bash
ym claim --json
# 审 diff、跑检查
ym send --event review --task dayuer/jkt-guandan#42 --sha <head> \
  --verdict approve \
  --checks "tests pass; lint clean" \
  --acceptance "1 满足; 2 满足; 3 未验证（待真机）"
```

### 6.4 三台机器按模型领取角色

角色在签发领取码时绑定，agent 不能自选；模型由各机器的环境决定。

| 机器 | 模型 | 角色 | agent |
|---|---|---|---|
| master 机 | Opus | master | `P/master` |
| 审计机 | GLM | auditor | `P/auditor-1` |
| worker 机 | DeepSeek | worker | `P/worker-1` |

1. owner 在服务器签发一次性码：

```bash
ym admin role issue --project P --name auditor-1 --role auditor --ttl 1h
ym admin role issue --project P --name worker-1 --role worker --ttl 1h
```

2. 每台机器设置自己的模型环境（示例；模型名和 base URL 以厂商当前文档为准）：

```bash
# master
export ANTHROPIC_MODEL=opus

# auditor（GLM 的 Anthropic 兼容端点）
export ANTHROPIC_BASE_URL=<GLM endpoint>
export ANTHROPIC_AUTH_TOKEN=<GLM key>
export ANTHROPIC_MODEL=<GLM model>

# worker（DeepSeek 的 Anthropic 兼容端点）
export ANTHROPIC_BASE_URL=https://api.deepseek.com/anthropic
export ANTHROPIC_AUTH_TOKEN=<DeepSeek key>
export ANTHROPIC_MODEL=<DeepSeek model>
```

3. 每台机器首次启动：

```bash
read -rs YM_ENROLL_CODE && export YM_ENROLL_CODE   # 粘贴领取码后回车，不回显
scripts/agent-up.sh
```

之后直接 scripts/agent-up.sh。脚本会：

- 用 `ym whoami` 推导角色，设置 `AGENT_ROLE`；
- 启动 `ym-postman`；
- 启动 `claude --name <project-name>`（完整 agent 的 `/` 换成 `-`，如 `yardmaster-worker-1`），多余参数原样传给 claude（如 `--model opus`）。

4. 领取码单次、有期限；重复使用返回 401。角色在签发时定死，agent 不能改。

服务端的 agent 名字只在项目内唯一：两个项目都可以有 `worker-1`。本地会话名、postman 日志（`/tmp/ym-postman-<project-name>.log`）和配置根必须按项目区分，脚本用 `<project>-<name>`。

#### 同一台机器跑多个 agent

凭据、会话记录和租约文件都在配置根下，默认 `~/.config/yardmaster/`。同一台机器上再加一个 agent 时，给它一个独立的根即可，不必改 HOME：

```bash
YM_CONFIG_DIR=$HOME/.yardmaster/worker-1 scripts/agent-up.sh
YM_CONFIG_DIR=$HOME/.yardmaster/worker-2 scripts/agent-up.sh
```

`ym` 与 `ym-postman` 都认这个变量（空值视为未设置，相对路径转成绝对路径）。

不要用「给每个 agent 一个假 HOME」来绕：`HOME` 同时决定 `~/.claude`，换了它 `SessionStart` hook 就不再加载，`ym register-session` 永不执行；`GOPATH` 也跟着走，假 HOME 会堆积只读的模块缓存。

### 6.5 同一台机器跑多个 agent（过渡方案，有坑）

§6.4 假设一台机器一个 agent。同机多 agent 时 HOME 只能有一份凭据
（internal/client/client.go 的 `CredPath()` 把路径写死为
~/.config/yardmaster/credentials.json），所以第二个 agent 的 `ym enroll`
会覆盖第一个。`YM_CONFIG_DIR` 落地前（见 上游）只能靠给每个 agent 一个独立
HOME 来绕，而**这个绕法会同时破坏自动注册**。

#### 必须四步，少一步 agent 就是个死信箱

```bash
# 1) master 在服务器本机签码（角色在此刻绑定，agent 不能自选）
ym admin role issue --project P --name worker-1 --role worker --ttl 2h

# 2) 在该 agent 自己的会话里换凭据（HOME 独立，一份 offer 一个目录）
mkdir -p /tmp/ym-home-P-worker-1
export HOME=/tmp/ym-home-P-worker-1
read -rs YM_ENROLL_CODE && export YM_ENROLL_CODE   # 粘贴后回车，不回显
ym enroll --url <server> --model deepseek
# 领取码不要进命令行参数（ps），也不要内联赋值（整行进 shell 历史）；
# 另一种写法是 --code-file <path>，或 --code-file - 从 stdin 读

# 3) 注册会话 socket —— 必须在该 agent 自己的 claude 会话里跑
HOME=/tmp/ym-home-P-worker-1 ym register-session --agent P/worker-1 --role worker

# 4) 起 postman（推送 + 心跳）
HOME=/tmp/ym-home-P-worker-1 ym-postman --agent P/worker-1 \
  > /tmp/ym-postman-P-worker-1.log 2>&1 &
```

**第 3 步为什么必须手工做**：§6.1 的 `SessionStart` hook 本来会自动调用
`ym register-session`，但 hook 配置住在 `~/.claude/`。改了 HOME 之后那是一个
空目录，**所有 hook 都不加载**，于是注册永远不会发生。这一条是 HOME 隔离最
反直觉的副作用。

**第 3 步为什么必须在该会话内跑**：`--pid` 默认取 `PPID`，`--socket` 默认取
`$CLAUDE_CODE_MESSAGING_SOCKET`。只有在那个 claude 会话里这两个默认值才指向
正确的进程与 socket；在别的 shell 里跑会写入一个不属于它的 socket。

**顺序不能颠倒**：postman 启动时会做一次 catch-up，把离线邮件登记进队列并按队列模型最多注入一条指针，此时若还没有会话记录，
日志会出现 `catch-up skipped: no live session registered`，必须重启 postman
才会再补一次。

#### 验证四项，缺一项就是没通

```bash
ls  /tmp/ym-home-P-worker-1/.config/yardmaster/sessions/   # 1. 有 json
cat /tmp/ym-home-P-worker-1/.config/yardmaster/sessions/*.json  # 2. pid 活着、socket 是该会话的
grep -i "no session registered" /tmp/ym-postman-P-worker-1.log  # 3. 应为空
ym supervise                                               # 4. online=true，消息从 queued 转 delivered
```

#### 已知坑

- **`register-session` 挡不住错误注册**。它只做 `os.Stat(socket)` 检查文件
  存在，而 socket 文件在进程死后仍然留着。注册到一个死 pid 会**成功**，之后
  postman 会把消息登记为 `delivered`，指针投进那个死 socket 时注入失败——消息
  不会丢（`ym inbox` 的 `unread` 含 `queued`+`delivered`，轮询仍能读到），但
  **推送静默失效**。注入失败不会被记为已注入，下一个 tick 会重试；会话重新注册
  后 `pid:socket` 变化，会按新的空闲期再注入一次。
- **`GOPATH` 也跟着 HOME 走**。该 agent 跑 `go build` 会把模块缓存下载到这个
  假 HOME 里（实测单个 agent 259 MB），且缓存文件只读，`rm -rf` 会报
  Permission denied，需先 `chmod -R u+w`。
- **干活不依赖 postman**。`ym inbox` 轮询直接读 `queued`+`delivered`，所以
  即使第 3、4 步没做，有人去那个会话里敲 `ym inbox` 它照样能工作；postman
  的价值是「master 能主动叫醒它」和 `online`/`last_seen` 可见性。

`YM_CONFIG_DIR`落地后，上述全部改为 `YM_CONFIG_DIR=<dir> ym ...`，
不再借道 HOME，第 3 步的 hook 问题随之消失。

## 7. 一个任务的完整流程

```
master  dispatch → worker claim → worker step... → worker done
        → master 开 PR、post 评论
        → master review_request → auditor review
        → master post、ym-gate merge、gh pr merge
        → auditor verified
```

### 7.1 派工

```bash
gh issue create --repo dayuer/jkt-guandan --title "修复 X" --label type:task
ym send --event dispatch --task dayuer/jkt-guandan#42 --to impl-a \
  --goal "修复 X" \
  --scope "server/" --out-of-scope "configs/" \
  --acceptance "1. 测试通过; 2. 不改变现有接口"
```

### 7.2 认领与执行

```bash
ym claim --json
ym send --event claim --task dayuer/jkt-guandan#42 \
  --files "server/" --current-impl "见 server/x.py" --risks "无" --steps 3
```

每步：

```bash
ym send --event step --task dayuer/jkt-guandan#42 --step 1/3 \
  --files "server/x.py" --commands "pytest tests/" \
  --result "12 passed" --unverified "未在 m4 上跑"
```

推送分支后：

```bash
ym send --event done --task dayuer/jkt-guandan#42 \
  --branch impl/42 --commit $(git rev-parse HEAD) --summary "完成，测试通过"
```

### 7.3 master 落账、派审

```bash
ym claim --json                        # 取到发给自己 master 的消息
ym post <msg_id>                       # 渲染成 GitHub 评论，幂等
ym ack <msg_id>

gh pr create --repo dayuer/jkt-guandan --base main --head impl/42 --fill
ym send --event review_request --task dayuer/jkt-guandan#42 --to auditor-1 \
  --sha $(git rev-parse impl/42) --acceptance "1. 测试通过; 2. 不改变接口"
```

### 7.4 审计与合并

```bash
ym claim --json
ym send --event review --task dayuer/jkt-guandan#42 --sha <head> \
  --verdict approve --checks "..." --acceptance "1 满足; 2 满足"
```

master：

```bash
ym post <review_msg_id>
ym-gate merge 42                       # 校验 head 上 standard→final 两段 approve、检查、P0、owner 批准
ym-gate audit-master                   # 机器复核 master 的派工/票单/合并留痕/现场一致性（只报事实）
ym-gate audit-master --local-agents worker-1,auditor-2  # 声明本机 agent：0 个 postman 才报 problem
gh pr merge 42 --merge --match-head-commit <40 位 sha>
```

### 7.5 合并后验证

```bash
ym send --event verified --task dayuer/jkt-guandan#42 --sha <merge_sha>
```

master `ym post` 并关闭 issue。

### 7.6 状态、回执与巡检

一条消息的状态：

```
queued → delivered → read → acked
                    └── 超时 ──> overdue → dead
```

- 收件人 `claim` 或 `read` 时变成 `read`；`ack` 后变成 `acked`。
- 发送者会收到 `read` / `acked` / `overdue` / `dead` / `cancelled` 的回执，也可以主动查。postman 对所有回执只记日志、不叫醒会话；唤醒只来自队列判定：空闲且队列非空时注入一条指针「队列 N 条；执行 ym inbox --next」。master 另外收到 `overdue` 元数据推送。

```bash
ym receipt <msg_id>
ym sent --state unacked
```

- 发送者只能看自己发出的消息和状态；收件人的正文只有收件人能读。
- master 巡检只看元数据：

```bash
ym supervise --json
```

| 情况 | master 动作 |
|---|---|
| agent 离线且有 `queued` | `ym cancel` 后改派 |
| `delivered_unread` 超过 15 分钟 | 已由服务端发 `reminder`；超过 30 分钟改派 |
| `overdue > 0` | 取消旧消息，改派给另一个 agent，新消息 `parent_id` 指向旧的 |
| `in_progress` 达到上限 | 暂停派新活 |
| 空闲 agent | 优先接新派工 |

改派后在 issue 上写 `DECISION`；涉及范围变化先 `ASK:`。

## 8. CLI 参考

| 命令 | 作用 |
|---|---|
| `ym login` | 存 refresh token，换 access token |
| `ym whoami` | 显示当前身份和角色 |
| `ym send` | 发消息（协议闸门在服务端） |
| `ym claim` | 从自己的收件箱认领，标记 `read` |
| `ym read` | 标记已读但不认领 |
| `ym ack` | 确认处理完 |
| `ym inbox` | 列出自己的收件箱（可按状态过滤） |
| `ym sent` | 列出自己的出站消息及状态 |
| `ym receipt` | 查询或等待某条消息的回执 |
| `ym supervise` | master 专用：各 agent 积压与超时元数据 |
| `ym agents` | 本项目的 agent 与在线状态 |
| `ym cancel` | 撤回未确认消息 |
| `ym heartbeat` | 更新在线状态（postman/runner 自动调用） |
| `ym verify` | 校验消息与评论的对应关系 |
| `ym help` | 内置帮助；`ym help all` 查看全部主题 |
| `ym enroll` | 用一次性码领取角色并保存凭据 |
| `ym init` | 建标签、首个 issue、CLAUDE.md 托管块 |
| `ym req new` | 需求落成 `type:requirement` issue |
| `ym plan` | 把需求拆成 `type:task` 子 issue |
| `ym post` | master 专用：把消息渲染成 GitHub 评论 |
| `ym register-session` | 注册本机会话 socket（hook 调用） |
| `ym mcp` | 【未实现】本地 stdio MCP server；见 §9 |
| `ym serve` | 启动服务端 |
| `ym admin` | 建项目、发/轮换/吊销 token、备份（仅服务器本机） |

所有命令支持 `--json`；错误返回稳定的 `code`。

## 9. MCP

> **【未实现】** 当前构建里没有 `ym mcp` 子命令，下面的配置照抄会直接报
> `unknown command`。这一节保留的是设计意图，不是可执行的用法；在实现合入之前，
> 用 CLI（§8）或 `ym-postman` 叫醒会话。

交互式 Claude 会话可以挂本地 MCP：

```bash
claude mcp add --scope user yardmaster -- ym mcp --project jkt-guandan --agent master
```

等价配置：

```json
{
  "mcpServers": {
    "yardmaster": {
      "command": "ym",
      "args": ["mcp", "--project", "jkt-guandan", "--agent", "master"]
    }
  }
}
```

MCP 只暴露五个工具：`inbox_send`、`inbox_claim`、`inbox_ack`、`inbox_list`、`verify`。管理动作和 `ym post` 不通过 MCP。

MCP 不负责推送；叫醒仍由 `ym-postman` 完成。

## 10. 多项目

**master 是按项目的，不是本机唯一的**：租约一个项目一把（`master_leases.project_id`
是主键），每个项目至多一个活跃 master 由数据库的部分唯一索引 `ux_one_master`
强制（所以 `ym admin agent set-role --role master` 撞上已有 master 会 409，要
`--handover`）。agent id 形如 `<project>/<name>`，一个 `ym serve` 托管多个项目，
各有独立的 master、auditor、worker、租约、收件箱与记忆命名空间。

真正本机唯一的资源是另一组：服务端进程与两个端口（7435 public / 7436 admin）、
那一个 SQLite 库、`~/.local/bin` 的二进制、~/.claude/settings.json 的模型网关、
CPU 与磁盘。

### 10.1 把一个新项目接进来（手工路径）

`ym help project` 是同一份正文的入口；这里给用户手册版本。一条命令接入已经可用：

```bash
ym project init https://github.com/owner/repo      # 需要本机 gh 已登录、YM_ADMIN_TOKEN 可用
```

它按顺序做：预检（`gh` 可读仓库 / `--checkout` 可用，不存在则克隆进去 / 需要注册项目时管理员接口可达）→ 注册项目 → 登记 master → 能力边界探测 → 播种 `_rules/` → 协作脚手架 → 开发准备提案（只写文件，不建 issue）。预检失败时不会执行任何不可回滚动作。在服务端主机上以管理员运行时，登记 master 自动完成：凭据写进 `<配置根>/projects/<project>/`，不打印 token；远端或非管理员运行时先 `YM_CONFIG_DIR=<配置根>/projects/<project> ym login --project P --agent master` 再重跑，已登录的操作者不会被要求再登记。断点状态 `init-state.json` 与凭据 `credentials.json` 同在该目录；把 `YM_CONFIG_DIR` 指到这个目录本身等价于默认配置根，重跑不会从头注册。协作脚手架会建标签与看板 issue，生成 `docs/COORDINATION.md`、`docs/WORKFLOW.md`、`docs/SECURITY.md`、`docs/USAGE.md`（移植自 yardmaster，带来源与版本）、`CLAUDE.md` 协作协议块、`.yardmaster/autopilot.toml`（`enabled=false`）与 `scripts/install-hooks.sh` + `scripts/hooks/commit-msg`，并提交推送：空仓库推到默认分支，非空仓库提交到 `yardmaster/bootstrap` 并开 PR。新项目自成一体：引用但本项目没有的源码/CI/示例改成纯文字，文档里不出现指回 yardmaster 的链接；提交前跑死链检查（相对链接与反引号路径必须存在，且禁止上游 URL），不过就拒绝提交。文件一致则跳过、不同则报差异不覆盖，第二次跑零提交。任一阶段失败会回滚该阶段之前可回滚的动作并打印回滚清单；重跑跳过已完成的阶段。下面是手工路径，用于不需要整条链、或想逐步确认的场景。

```bash
# 1) 注册项目（服务器本机，走 admin 端口）
ym admin project add --id P --repo owner/repo
ym admin agent list --project P          # 不报 404 就算成

# 2) 给每个角色签一次性码（角色此刻绑定，agent 不能自选）
ym admin role issue --project P --name master --role master --ttl 1h

# 3) 每个 agent 一个独立配置根，再换取凭据
export YM_CONFIG_DIR=~/.ym-homes/P-<agent>
read -rs YM_ENROLL_CODE && export YM_ENROLL_CODE    # 粘贴后回车，不回显
scripts/agent-up.sh
ym whoami                                 # 应打出 P/<agent> 与期望角色

# 4) 每个 agent 一个 git worktree，开在那个项目自己的仓库里
git rev-parse --show-toplevel             # 应指向该项目检出

# 5) 启动一律走 scripts/agent-up.sh（上面第 3 步就是首次启动），不要手写

# 6) 播种项目规则，再派第一张工单（只有 master 能写）
ym memory write --path _rules/conventions.md --tier procedural --pin --body "..."
ym memory list --pinned                   # 看得到，且会进新 agent 的 SessionStart 摘要
```

- 启动一律走 scripts/agent-up.sh，不要每个项目手写一份。
- `ym-gate` 不按项目配：`--repo` 默认从 cwd 用 `gh repo view` 解析
  （cmd/ym-gate/main.go:174、internal/gate/gate.go），每个项目的 master 在
  自己的 checkout 里跑即可。
- 机器冷启动见 `ym help bootstrap`，角色领取见 `ym help enroll`。

#### 四条静默失败的坑

它们的共同点是**失败看起来像成功**：

| 坑 | 现象 |
|---|---|
| `--name` 带项目前缀 | `--project P --name P/auditor-1` 造出 `P/P/auditor-1`，enroll 与上线全部成功、毫无报错（上游 后两个入口都 422，旧服务端仍静默造幽灵）；`ym whoami` 显示 `P/P/auditor-1` |
| worktree 里写 `settings.local.json` 的 `env` 块 | 顶掉全局模型网关：postman 连上、`ym supervise` 报 `ONLINE=true`、派工单进了 `QUEUED`，但 `claude` 根本没起来 |
| 启动脚本不清继承的 `CLAUDE_CODE_*` | 新会话把父会话的 messaging socket 当自己的，派给新 agent 的工单被注入到父会话；唯一线索是 `Transcript saving is off — inherited CLAUDE_CODE_CHILD_SESSION marker` |
| 领取码经 argv | `--code` 的值出现在进程命令行里，同机可读、还进 shell 历史；内联赋值（把值写在命令前）整行也会进 shell 历史。用 `read -rs` 读进 `YM_ENROLL_CODE` 再单独跑命令，或 `--code-file`。验收：enroll 期间 `ps` 里不应出现 `ymc_` |

**队列空不等于做完**：`ym supervise` 的 `QUEUED=0` 既可能是 agent 已处理完，也可能是
单子根本没插进去。要分清只能看 `ym sent` 的 state，不能看队列深度。

#### 当前限制

- **模型网关是全局的**：两个项目要用不同模型，今天没有按项目的旋钮
  （`ANTHROPIC_BASE_URL` / `ANTHROPIC_MODEL` 是机器级环境变量）。
- **一个 SQLite 库装所有项目**：备份工具（上游，尚未实现）设计成整库 `VACUUM INTO`，
  没法单独备份或恢复一个项目；当前用 `sqlite3 .backup` 整库备份。

### 10.2 其他约定

- 每个项目一个 master，各自独立上下文。
- 一个 agent 只属于一个项目；跨项目再 enroll 一个。
- 项目之间数据、队列、文件面锁隔离。
- owner 可以用 `ym admin` 查看跨项目状态。

## 11. 故障排查

| 现象 | 原因 | 处理 |
|---|---|---|
| `401 unauthorized` | access token 过期或 refresh 被吊销 | `ym login` 重新换票；检查是否被 revoke |
| `403 forbidden` | 角色不匹配或 project 与 token 不符 | `ym whoami` 确认身份 |
| `422 missing field` | 事件的必填字段缺失 | 按 `ym send --help` 补齐 |
| master 不被叫醒 | postman 没跑、socket 注册失效 | 检查 ~/.yardmaster/sessions.json、`ym-postman` 日志 |
| postman 收到但没注入 | pid 已退出或 socket 不可连 | 重新登录 master 会话，触发 SessionStart |
| GitHub 没有评论 | master 没执行 `ym post`，或 token 无权限 | 手动 `ym post <msg_id>`；检查 `gh auth status` |
| 消息卡在 queued | master 离线、租约被占用 | `ym list --state queued`；等租约过期或 `ym reclaim` |
| 合并被拒 | head 上两段票不齐（standard → final）或任一段 changes、检查未过、P0 未关 | 重新审计最新 head；`ym-gate merge` 会给出票型与原因 |

## 12. FAQ

**需要 AI API key 吗？**
不需要。Yardmaster 不跑模型；AI key 只在 agent 机器上。

**需要给服务端 GitHub token 吗？**
不需要。评论由 master 机器的 `ym post` 写。

**agent 可以用不同账号和模型吗？**
可以。每个 agent 有独立 token；模型由该机器上的 `claude` 配置决定。

**总线挂了怎么办？**
GitHub 仍是账本。agent 退回 issue 评论继续推进；CLI 会在本地排队，恢复后重试。

**能不能不用 MCP？**
可以。CLI + `ym-runner` 覆盖全部场景；MCP 只是交互式会话的便利层。

**一个项目能有多个 master 吗？**
不能。服务端用唯一索引强制每项目一个有效 master。
