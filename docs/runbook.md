# 运维手册

适用于「随时掼掼蛋」的生产环境 https://guandan.synon.ai 。设计依据见 [server-design.md](server-design.md) §12、§13；生产拓扑以 #12 下 coord 的说明为准（和设计文档 §8「单容器自带 autocert」不同）。

## 0. 生产拓扑

```
浏览器 ──HTTPS──▶ nginx（宿主机，TLS、静态前端）
                   ├─ /                 → /var/www/guandan/current → <短 SHA>/   （符号链接，发布时原子切换）
                   └─ /api/ /ws /g/ /healthz /readyz → 127.0.0.1:18200 → 容器 guandan（:8080）
```

- **服务器**：新加坡 `root@43.156.128.95`，Debian 12，Docker 26，nginx 1.22。**同机跑着很多其他生产服务**：可用内存约 1.4 GB，磁盘已用 67%。
- **容器**：`guandan`，镜像 `guandan:<短 SHA>`，只映射到 `127.0.0.1:18200`（宿主机的 8080、8443 被别的服务占着，**绝不能映射到这两个端口**）。参数：`--read-only --cap-drop ALL --security-opt no-new-privileges --memory 256m`。数据卷 `guandan-data`。不设 `GUANDAN_DOMAIN`，容器内只走 HTTP；`GUANDAN_PUBLIC_URL=https://guandan.synon.ai`。
- **nginx 站点**：`/etc/nginx/sites-available/guandan.synon.ai` 加片段 `/etc/nginx/snippets/guandan-security.conf`，由 coord 维护。仓库里的模板是 [deploy/nginx/](../deploy/nginx/)，两边要保持一致。站点里关掉了 `open_file_cache`，所以切换 `current` 后**不需要 reload nginx**。
- **客户端 IP**：DNS 只做解析，不走 Cloudflare 代理。nginx 用 `$remote_addr` 覆盖设置 `X-Real-IP`，用 `$proxy_add_x_forwarded_for` 设置 `X-Forwarded-For`。应用只在对端属于 `GUANDAN_TRUSTED_PROXIES`（默认 127.0.0.1 和 docker 网桥）时才看这两个头。
- **不要在服务器上改别的站点、别的服务，也不要改全局 nginx 配置。**

## 1. 上线前准备

### 1.1 环境变量文件

备份相关的配置写进服务器上的 `/etc/guandan/guandan.env`（`chmod 600`，属主 root）。`scripts/deploy-remote.sh` 启动容器时会用 `--env-file` 读它；`GUANDAN_PUBLIC_URL` 由脚本直接传入，不用写在这里。

```bash
# 异地备份（上线前必须配置，§13.3）
GUANDAN_BACKUP_URL=https://<S3 endpoint>/<bucket>/<前缀>
GUANDAN_BACKUP_KEY=age1…
AWS_REGION=ap-southeast-1
AWS_ACCESS_KEY_ID=…
AWS_SECRET_ACCESS_KEY=…
```

- `GUANDAN_BACKUP_URL` 用 path-style 地址，AWS S3、Cloudflare R2、Backblaze B2、MinIO 都支持，例如 `https://s3.ap-southeast-1.amazonaws.com/guandan-backup/prod`。也可以写 `file:///某个目录`，把备份放到挂进容器的网络盘上。
- bucket 凭据只给这一个 bucket（最好限定到这个前缀）的 `PutObject`、`GetObject`、`ListBucket`、`DeleteObject` 四项权限。
- 远端超过 30 天的备份，服务每小时会自己删除。bucket 上另外配一条 35 天过期的生命周期规则兜底。

### 1.2 生成备份密钥

在任意一台装了 Docker 的机器上执行：

```bash
docker run --rm guandan:<短 SHA> backup keygen
```

- 输出里 `# age1…` 那一行是公钥，填进 `GUANDAN_BACKUP_KEY`。
- `AGE-SECRET-KEY-…` 那一行是私钥，**存进密码管理器，不要放在服务器上**。只有恢复时才用到它，用完就删。
- 数据库里存着 VAPID 私钥，所以备份一定要加密。私钥丢了，所有备份都解不开。

### 1.3 外部探活

用任意一个免费的探活服务，每 1–5 分钟请求一次 `https://guandan.synon.ai/readyz`，连续失败就通知仓库所有者（§13.7）。`/readyz` 会在以下任一情况失败：

- 写事务提交不了；
- 某个后台任务的心跳超过 2 个周期（备份 2 小时、清理 20 分钟）；
- 数据目录剩余空间不到 500 MB；
- 最近一份本地快照超过 2 小时；
- 异地上传连续失败 3 次。

失败时，响应体里的 `problems` 会写明具体是哪一项。

## 2. 发布

在本机（需要 Docker、git，并且能 `ssh root@43.156.128.95`）执行：

```bash
scripts/deploy.sh            # 发布当前 HEAD；也可以写成 scripts/deploy.sh <git 引用>
```

**本机部分**（`scripts/deploy.sh`）：

1. 用 `git worktree` 取出这个提交的干净副本，未提交的改动不会被发布出去；
2. 按服务器的 CPU 架构交叉编译镜像 `guandan:<短 SHA>`，构建号写进 `X-App-Build`；
3. 用 `docker build --target web` 取出前端产物 `web/dist`，取完立刻删掉构建镜像；
4. 用 `docker save | gzip | ssh docker load` 把镜像传上去。镜像只有十几 MB，服务器不用登录镜像仓库，也不用在服务器上构建（构建过程要占 350 MB，那台机器磁盘紧张）。服务器上已经有同名镜像就跳过；
5. 把静态文件打包传到 `/var/www/guandan/<短 SHA>/`，先解压到临时目录再改名，不会出现解到一半的目录。

**服务器部分**（`scripts/deploy-remote.sh`，通过 ssh 执行）：

0. 预检：镜像和静态目录都在；`current` 要么不存在，要么是符号链接。**如果 `current` 是一个真目录就停下**，因为 `ln -sfn` 会把链接建到目录里面去。任何东西都还没动；
1. 在旧容器里做一次 `guandan backup now`，留一个最新的恢复点；
2. `docker stop -t 30` 优雅停掉旧容器，改名为 `guandan-prev`；
3. 用上面第 0 节的参数启动新容器，最多等 60 秒 `/readyz`。新容器退出或崩溃重启时立刻判为失败；
4. 用 `rename(2)` 原子地把 `current` 切到新版本；
5. 经 nginx 确认 `https://guandan.synon.ai/readyz` 和首页都正常；
6. 成功：删掉 `guandan-prev`，只保留最近 5 个版本的静态目录和镜像。当前版本和上一个版本无论如何都保留。

**第 3–5 步任何一步失败**：删掉新容器，把 `guandan-prev` 改回原名并启动，`current` 指回原来的版本。脚本退出码为 1，并打印新容器最近的日志。

可以用环境变量覆盖的项：`SSH_HOST`、`WEB_ROOT`、`PORT`、`MEMORY`、`PUBLIC_URL`、`SITE_URL`、`READY_TIMEOUT`、`KEEP`。

`make deploy-test` 会在本机用 Docker 验证服务器部分（不需要 nginx 和 ssh），覆盖以下场景：首次发布、升级并清理旧版本、新版本起不来、`current` 是真目录、切换后站点检查失败。CI 每次都会跑这个测试。

### 2.1 手动回滚到更早的版本

服务器上保留着最近几个版本的镜像和静态目录，直接用服务器部分的脚本切回去：

```bash
ssh root@43.156.128.95 'ls -t /var/www/guandan; docker image ls guandan'
ssh root@43.156.128.95 'env SHA=<旧短 SHA> sh -s' < scripts/deploy-remote.sh
```

### 2.2 回滚失败：旧版本拒绝新迁移

迁移只做向前兼容的修改，但旧版本的程序发现库里有它不认识的迁移时，会拒绝启动（防止降级把数据写坏）。所以，如果新版本已经执行了新迁移，之后才没能就绪，回滚时旧容器就起不来，日志里会出现「不认识的迁移」。这时在服务器上执行：

```bash
docker rm -f guandan                       # 确保没有容器在用数据卷
# 用发布前那份快照恢复（第 1 步做的，在本地快照目录里，未加密）
docker run --rm -v guandan-data:/data -e GUANDAN_PUBLIC_URL=https://guandan.synon.ai \
  guandan:<旧短 SHA> backup list
docker run --rm -v guandan-data:/data -e GUANDAN_PUBLIC_URL=https://guandan.synon.ai \
  guandan:<旧短 SHA> restore /data/backup/guandan-<发布前的时间>.db
env SHA=<旧短 SHA> sh deploy-remote.sh     # 或者在本机按 2.1 执行
```

从发布前备份到回滚之间写入的数据会丢失，通常只有几十秒。

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
docker run --rm --env-file /etc/guandan/guandan.env -e GUANDAN_PUBLIC_URL=https://guandan.synon.ai \
  -v guandan-data:/data -v /root/guandan-key.txt:/key.txt:ro \
  guandan:<当前短 SHA> restore -i /key.txt latest

# 4. 删掉私钥，启动服务
shred -u /root/guandan-key.txt
docker start guandan
docker exec guandan /guandan healthcheck --ready && echo ready
```

`restore` 的执行过程：

1. 把快照下载到数据目录里的临时文件；
2. 解密，再用 `quick_check` 和迁移检查校验，任一项不通过就停下，原库保持不动；
3. 把原来的 `guandan.db`（以及 `-wal`、`-shm`）改名为 `guandan.db.before-restore-<时间>`；
4. 把快照放到原位。

确认恢复后的数据没问题，再删除这些 `before-restore` 文件。

换一台全新的机器：先按第 0 节装好 nginx 站点、按 1.1 建好环境变量文件，再执行 `scripts/deploy.sh`（会建出一个空库），然后按上面的步骤恢复。数据卷是空的也没关系。

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

- **`/metrics`** 只监听容器内的 `127.0.0.1:9090`，外部访问不到，nginx 也不转发。查看方式是 `docker exec guandan /guandan metrics`。内容包括：
  - 请求数（按 2xx/4xx/5xx 分类）和请求耗时；
  - 写锁等待时间；
  - outbox 积压量和放弃数；
  - 备份时间；
  - 构建号。

  WebSocket 连接数由 #18 补上。
- **日志**：JSON 格式，输出到 stdout，用 `docker logs guandan` 查看；Docker 只保留 5 个 10 MB 的日志文件。每个请求都有一行日志，包含 `request_id`、`ip`（经可信代理解析后的客户端 IP）、`status`、`ms`、`write_wait_ms`。写锁等待超过 100ms、出现 panic、备份失败时，都会记 warn 或 error。
- **构建号**：每个响应都带 `X-App-Build: <提交 SHA>` 头，用来确认线上跑的是哪个版本。

## 7. 清理任务（§13.1）

清理任务每 10 分钟执行一次，不需要手动操作：

- 开局 6 小时后，`open` 或 `full` 的局改为 `done`；`open` 且只剩局头一人的局改为 `cancelled`；
- 删除过期的认领令牌（超过 1 天）和幂等键（超过 24 小时）；
- 删除已发送或已放弃超过 30 天的 outbox 记录；
- 删除 180 天没有出现过的设备，以及它们的推送订阅；
- 删除一年前的埋点（§14）；
- 每天执行一次 `PRAGMA optimize`。
