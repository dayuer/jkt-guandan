# 运维手册

适用于单台 VPS 上用 Docker 跑的「随时掼掼蛋」服务。设计依据见 [server-design.md](server-design.md) §12、§13。
下面的命令都在服务器上执行。容器名默认是 `guandan`，数据卷默认是 `guandan-data`。

## 1. 上线前准备

### 1.1 环境变量文件

把配置写进 `/etc/guandan/guandan.env`，`scripts/deploy.sh` 会用 `--env-file` 读它：

```bash
GUANDAN_DOMAIN=example.com,backup.example.com
GUANDAN_ADMIN_EMAIL=you@example.com
# 异地备份（上线前必须配置，§13.3）
GUANDAN_BACKUP_URL=https://<S3 endpoint>/<bucket>/<前缀>
GUANDAN_BACKUP_KEY=age1…
AWS_REGION=ap-southeast-3
AWS_ACCESS_KEY_ID=…
AWS_SECRET_ACCESS_KEY=…
```

- 这个文件里有凭据，权限设成 `chmod 600`，属主设成 root。
- `GUANDAN_BACKUP_URL` 使用 path-style 地址。AWS S3、Cloudflare R2、Backblaze B2、MinIO 都支持这种写法，例如 `https://s3.ap-southeast-3.amazonaws.com/guandan-backup/prod`。
- 也可以写成 `file:///某个目录`，把备份放到挂载进容器的网络盘上。
- 给 bucket 用的凭据只开放 `PutObject`、`GetObject`、`ListBucket`、`DeleteObject` 这四项权限，只作用于这个 bucket（最好限定到这个前缀）。
- 超过 30 天的远端备份，服务每小时会自己删除一次。bucket 上另外配一条 35 天过期的生命周期规则作为兜底。

### 1.2 生成备份密钥

```bash
docker run --rm ghcr.io/dayuer/jkt-guandan:<tag> backup keygen
```

- 输出里 `# age1…` 那一行是公钥，填进 `GUANDAN_BACKUP_KEY`。
- `AGE-SECRET-KEY-…` 那一行是私钥，**存进密码管理器，不要放在服务器上**。只有恢复时才用到它，用完就删。
- 数据库里存着 VAPID 私钥，所以备份一定要加密。私钥丢了，所有备份都解不开。

### 1.3 外部探活

用任意一个免费的探活服务，每 1–5 分钟请求一次 `https://<域名>/readyz`，连续失败就通知仓库所有者（§13.7）。`/readyz` 会在以下任一情况失败：

- 写事务提交不了；
- 某个后台任务的心跳超过 2 个周期（备份 2 小时、清理 20 分钟）；
- 数据目录剩余空间不到 500 MB；
- 最近一份本地快照超过 2 小时；
- 异地上传连续失败 3 次。

失败时，响应体里的 `problems` 会写明具体是哪一项。

## 2. 发布

```bash
scripts/deploy.sh <提交 SHA>
```

CI 在 `main` 上为每个提交构建镜像，推送到 `ghcr.io/dayuer/jkt-guandan:<SHA>`，同时更新 `:main` 标签。脚本依次执行：

1. 拉取镜像；
2. 在旧容器里做一次 `guandan backup now`，留一个最新的恢复点；
3. `docker stop -t 30` 停掉旧容器，触发优雅关闭，然后把它改名为 `guandan-prev`；
4. 用同样的参数启动新容器：`--read-only --cap-drop ALL`、日志大小限制、`--env-file`；
5. 每秒执行一次 `docker exec guandan /guandan healthcheck --ready`。60 秒内就绪就删掉 `guandan-prev`，发布完成；
6. 新容器退出、崩溃重启或 60 秒内没有就绪时：打印最近的日志，删掉新容器，把 `guandan-prev` 改回原名并启动。脚本退出码为 1。

在本机可以用 `make deploy-test` 验证回滚逻辑：它先发布一个好镜像，再发布一个起不来的镜像，确认能回滚。CI 每次都会跑这个测试。

### 2.1 回滚失败：旧版本拒绝新迁移

迁移只做向前兼容的修改，但旧版本的程序发现库里有它不认识的迁移时，会拒绝启动（防止降级把数据写坏）。所以，如果新版本已经执行了新迁移，之后才没能就绪，自动回滚时旧容器就起不来，日志里会出现「不认识的迁移」。这时按以下步骤处理：

```bash
docker rm -f guandan                       # 确保没有容器在用数据卷
# 用发布前那份快照恢复（deploy.sh 第 2 步做的，在本地快照目录里）
docker run --rm --env-file /etc/guandan/guandan.env -v guandan-data:/data \
  ghcr.io/dayuer/jkt-guandan:<旧 SHA> backup list
docker run --rm --env-file /etc/guandan/guandan.env -v guandan-data:/data \
  ghcr.io/dayuer/jkt-guandan:<旧 SHA> restore /data/backup/guandan-<发布前的时间>.db
scripts/deploy.sh <旧 SHA>
```

恢复的是本地快照，未加密，不需要私钥。从发布前备份到回滚之间写入的数据会丢失，通常只有几十秒。

## 3. 备份

服务启动后自动执行，不需要 cron：

- **本地快照**：每小时用 `VACUUM INTO` 生成一份 `/data/backup/guandan-<UTC 时间>.db`。快照用 `quick_check` 校验，并检查迁移是否都认识，两项都通过才算成功。本地保留最近 48 份，再加 7 个每日快照（取当天最新的一份）。启动时如果最近一份快照不到一小时，就等满一小时再做下一份。
- **异地备份**：每份快照用 age 公钥加密后上传，对象名是 `guandan-<UTC 时间>.db.age`，远端保留 30 天。上传失败时本地快照照常保留，下个小时重试；连续失败 3 次后 `/readyz` 报错。

常用命令：

```bash
docker exec guandan /guandan backup now     # 立刻备份一次（例如做危险操作之前）
docker exec guandan /guandan backup list    # 列出本地快照和异地备份
```

`/metrics` 里的 `guandan_backup_last_local_timestamp_seconds`、`guandan_backup_last_upload_timestamp_seconds` 和 `guandan_backup_upload_consecutive_failures` 可以用来画图或告警。

## 4. 恢复

能接受的数据损失最多 1 小时（§13.3）。

```bash
# 1. 停服务。restore 要拿数据目录锁，服务没停时会拒绝执行
docker stop guandan

# 2. 把私钥临时放到服务器上（容器里的用户是 65532，要能读到这个文件）
install -m 600 -o 65532 /dev/stdin /root/guandan-key.txt   # 粘贴 AGE-SECRET-KEY-… 后按 Ctrl-D

# 3. 恢复。参数可以是 latest（异地最新一份）、异地对象名或本地快照路径
docker run --rm --env-file /etc/guandan/guandan.env -v guandan-data:/data \
  -v /root/guandan-key.txt:/key.txt:ro \
  ghcr.io/dayuer/jkt-guandan:<SHA> restore -i /key.txt latest

# 4. 删掉私钥，启动服务
shred -u /root/guandan-key.txt
docker start guandan        # 或 scripts/deploy.sh <SHA>
docker exec guandan /guandan healthcheck --ready && echo ready
```

`restore` 的执行过程：

1. 把快照下载到数据目录里的临时文件；
2. 解密，再用 `quick_check` 和迁移检查校验，任一项不通过就停下，原库保持不动；
3. 把原来的 `guandan.db`（以及 `-wal`、`-shm`）改名为 `guandan.db.before-restore-<时间>`；
4. 把快照放到原位。

确认恢复后的数据没问题，再删除这些 `before-restore` 文件。

在一台全新的机器上恢复，步骤完全相同：先建好环境变量文件，数据卷是空的也没关系。

### 4.1 恢复演练

上线前至少演练一次，以后每次修改备份相关代码后再演练一次。演练脚本在本机用 Docker 模拟两台机器：一台旧机器、一个「异地」目录、一台新机器（全新的空数据卷）。CI 每次都会跑这个脚本。

```bash
make restore-drill        # 等价于 docker build 之后执行 scripts/restore-drill.sh guandan:local
```

**演练记录**：2026-10-13，镜像构建自 `cfc0bf8` 加上 #20 的改动，macOS + OrbStack。全过程在 PR 里，这里只列结论：

| 步骤 | 结果 |
|---|---|
| `backup keygen` | 生成 age 密钥对 |
| 旧机器启动，`seed --force` | 12 个用户、5 个牌局 |
| `backup now` | 本地快照 188 KB，加密对象 `guandan-20261013T164341Z.db.age` 上传到「异地」，文件头是 `age-encryption.org/v1` |
| 删掉旧机器 | — |
| 新机器的空数据卷上执行 `restore -i /key.txt latest` | 取到最新的一份，迁移 `0001_init.sql、0002_bans.sql`，校验通过 |
| 新机器启动 | `/readyz` 200；`admin stats` 显示 12 个用户、5 个牌局，与旧机器一致；`X-App-Build` 和 `/metrics` 正常 |
| 服务运行时执行 restore | 被拒绝：「数据目录 /data 已被另一个实例占用；恢复前先停服务」 |

## 5. 管理命令（§13.6）

都通过 `docker exec guandan /guandan …` 执行，和正在运行的服务共用同一个数据库：

```bash
guandan admin ban <用户或设备 id>          # 封禁，之后的请求一律返回 403（拦截由 #3 的身份中间件实现）
guandan admin game cancel <短码>           # 强制取消牌局，未发出的提醒一并放弃
guandan admin user delete --yes <用户 id>  # 处理 §14 的删除请求，见下文
guandan admin stats [--since 7d]           # 新用户、新牌局、埋点计数
guandan seed [--force]                     # 写入设计稿里的示例数据，只用于本地和 PR 验证
```

`admin user delete` 会：

- 把称呼改成「已注销牌友」，清空 WhatsApp 号码；
- 删除这个用户的设备、推送订阅、认领令牌、星标关系、邀请和候补；
- 把埋点里的用户 id 清空。

座位记录保留，所以历史局的人数不受影响。

## 6. 监控和日志

- **`/metrics`** 默认只监听容器内的 `127.0.0.1:9090`，外部访问不到。要从宿主机抓取，在环境变量文件里加 `GUANDAN_METRICS_ADDR=:9090`，再给 deploy.sh 设 `EXTRA_ARGS="-p 127.0.0.1:9090:9090"`，只映射到宿主机的回环地址。内容包括：
  - 请求数（按 2xx/4xx/5xx 分类）和请求耗时；
  - 写锁等待时间；
  - outbox 积压量和放弃数；
  - 备份时间；
  - 构建号。

  WebSocket 连接数由 #18 补上。
- **日志**：JSON 格式，输出到 stdout，用 `docker logs guandan` 查看；Docker 只保留 5 个 10 MB 的日志文件。每个请求都有一行日志，包含 `request_id`、`status`、`ms`、`write_wait_ms`。写锁等待超过 100ms、出现 panic、备份失败时，都会记 warn 或 error。
- **构建号**：每个响应都带 `X-App-Build: <提交 SHA>` 头，用来确认线上跑的是哪个版本。

## 7. 清理任务（§13.1）

清理任务每 10 分钟执行一次，不需要手动操作：

- 开局 6 小时后，`open` 或 `full` 的局改为 `done`；`open` 且只剩局头一人的局改为 `cancelled`；
- 删除过期的认领令牌（超过 1 天）和幂等键（超过 24 小时）；
- 删除已发送或已放弃超过 30 天的 outbox 记录；
- 删除 180 天没有出现过的设备，以及它们的推送订阅；
- 删除一年前的埋点（§14）；
- 每天执行一次 `PRAGMA optimize`。
