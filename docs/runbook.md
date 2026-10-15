# 运维手册

适用于「随时掼掼蛋」的生产环境 https://guandan.synon.ai 。设计依据见 [server-design.md](server-design.md) §12、§13；生产拓扑以 #12 下 coord 的说明为准（和设计文档 §8「单容器自带 autocert」不同）。

## 0. 生产拓扑

```
浏览器 ──HTTPS──▶ nginx（宿主机，TLS、静态前端）
                   ├─ /                 → /var/www/guandan/current → <短 SHA>/   （符号链接，发布时原子切换）
                   └─ /api/ /ws /g/ /healthz /readyz → 127.0.0.1:18200 → 容器 guandan（:8080）
```

- **服务器**：新加坡那台（地址见 #12；本机执行脚本前设 `export SSH_HOST=root@<服务器>`），Debian 12，Docker 26，nginx 1.22。**同机跑着很多其他生产服务**：可用内存约 1.4 GB，磁盘已用 67%。
- **容器**：`guandan`，镜像 `guandan:<短 SHA>`，只映射到 `127.0.0.1:18200`（宿主机的 8080、8443 被别的服务占着，**绝不能映射到这两个端口**）。参数：`--read-only --cap-drop ALL --security-opt no-new-privileges --memory 256m`。数据卷 `guandan-data`。不设 `GUANDAN_DOMAIN`，容器内只走 HTTP；`GUANDAN_PUBLIC_URL=https://guandan.synon.ai`。
- **nginx 站点**：`/etc/nginx/sites-available/guandan.synon.ai` 加片段 `/etc/nginx/snippets/guandan-security.conf`，由 coord 维护。仓库里的 [deploy/nginx/](../deploy/nginx/) 照抄线上配置。改动先改模板，过 `scripts/check-nginx.sh`（本地 CI 的 `local-ci/nginx` 也跑），再同步到服务器并执行 `nginx -t`。站点里关掉了 `open_file_cache`，所以切换 `current` 后**不需要 reload nginx**。
  - **每个反向代理的 location 都要写全 `Host`、`X-Real-IP`、`X-Forwarded-For`、`X-Forwarded-Proto`**：location 里只要写了一条 `proxy_set_header`，就不再继承 server 级的设置。漏了 `X-Real-IP` 的话，客户端可以伪造 IP。
  - 证书由 certbot 续期，webroot 是 `/var/www/certbot`。
  - 检查线上文件：`ssh $SSH_HOST sh -s /etc/nginx/sites-available/guandan.synon.ai < scripts/check-nginx.sh`。
- **客户端 IP**：DNS 只做解析，不走 Cloudflare 代理。nginx 用 `$remote_addr` 覆盖设置 `X-Real-IP`，用 `$proxy_add_x_forwarded_for` 设置 `X-Forwarded-For`。应用默认不信任任何代理；env 文件里设了 `GUANDAN_TRUSTED_PROXIES=172.17.0.1/32`（容器看到的对端就是网桥网关），只有来自它的这两个头才被采用。启动日志里的 `trusted_proxies` 字段会显示生效值。
- **不要在服务器上改别的站点、别的服务，也不要改全局 nginx 配置。**

## 1. 上线前准备

### 1.1 环境变量文件

容器的配置写在服务器上的 `/etc/guandan/guandan.env`（文件 600、目录 700、属主 root，coord 已经建好），`scripts/deploy-remote.sh` 启动容器时会用 `--env-file` 读它。目前的内容：

```bash
GUANDAN_PUBLIC_URL=https://guandan.synon.ai
GUANDAN_TRUSTED_PROXIES=172.17.0.1/32      # 容器看到的对端是网桥网关；只信任它传来的 X-Real-IP
```

等仓库所有者提供 bucket 和 age 公钥之后，再加上备份相关的变量：

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
- **上线前检查项：在真实的 bucket 上验证一次。** S3 签名只用 AWS 官方样例和本地的假 S3 测过。仓库所有者提供 bucket 之后，配置好上面的变量，在服务器上执行：

  ```bash
  docker exec guandan /guandan backup now    # 应输出「异地备份：….db.age 已上传」
  docker exec guandan /guandan backup list   # 异地列表里能看到刚才那份
  ```

  再按第 4 节，用 `restore -i <私钥> latest` 在一个临时数据卷上恢复一次，确认能解密、能通过校验。

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

在本机（需要 Docker、git，并且能 `ssh $SSH_HOST`）执行：

```bash
SSH_HOST=root@<服务器> scripts/deploy.sh            # 发布当前 HEAD；也可以写成 scripts/deploy.sh <git 引用>
```

**本机部分**（`scripts/deploy.sh`）：

1. 用 `git worktree` 取出这个提交的干净副本，未提交的改动不会被发布出去；
2. 按服务器的 CPU 架构交叉编译镜像 `guandan:<短 SHA>`，构建号写进 `X-App-Build`；
3. 用 `docker build --target web` 取出前端产物 `app/h5/dist`，取完立刻删掉构建镜像；
4. 把镜像送到服务器（服务器上已有同名镜像就跳过），两种方式见 2.4，默认是 `docker save | gzip | ssh docker load`；
5. 把静态文件打包传到 `/var/www/guandan/<短 SHA>/`，先解压到临时目录再改名，不会出现解到一半的目录。

**服务器部分**（`scripts/deploy-remote.sh`，通过 ssh 执行）：

0. 预检：镜像和静态目录都在；`current` 要么不存在，要么是符号链接。**如果 `current` 是一个真目录就停下**，因为 `ln -sfn` 会把链接建到目录里面去。任何东西都还没动；
1. `docker stop -t 30` 优雅停掉旧容器，改名为 `guandan-prev`；
2. **用新镜像做一次冷备份**：`docker run --rm … <新镜像> backup now --local --if-exists --no-migrate`。在停服之后做，快照就是旧版本最后的状态；`backup` 从不迁移数据库，所以快照是迁移前的；这一步也不依赖旧版本有没有 `backup` 子命令。**备份失败就中止发布**，旧容器原样拉起来，因为回滚靠的就是这份快照。做备份用的镜像要带 `org.jkt-guandan.ops` 标签（#34 起的镜像都有）：新镜像没有时（比如手动回滚到 #34 之前的版本）改用正在跑的旧版本的镜像；两个都没有就拒绝发布，除非显式设 `ALLOW_NO_BACKUP=1`（比如首次部署到空数据卷）。更早的二进制不认识子命令，会直接起一个服务、永远不返回，所以不能拿它们来备份；
3. 用上面第 0 节的参数启动新容器，最多等 60 秒 `/readyz`。新容器退出或崩溃重启时立刻判为失败；
4. 用 `rename(2)` 原子地把 `current` 切到新版本；
5. 经 nginx 确认 `https://guandan.synon.ai/readyz` 和首页都正常；
6. 成功：删掉 `guandan-prev`，只保留最近 5 个版本的静态目录和镜像。当前版本和上一个版本无论如何都保留。

**第 3–5 步任何一步失败**：删掉新容器，把 `guandan-prev` 改回原名并启动，`current` 指回原来的版本。脚本退出码为 1，并打印新容器最近的日志。**如果旧容器起不来**（新版本已经执行了旧版本不认识的迁移），脚本会停下旧容器，用新镜像把第 2 步的快照 `restore` 回去，再启动旧容器；从发布前到回滚之间的写入会丢失，日志里会写明。

可以用环境变量覆盖的项：`SSH_HOST`、`WEB_ROOT`、`IMAGE_SOURCE`、`PORT`、`MEMORY`、`PUBLIC_URL`、`ENV_FILE`、`SITE_URL`、`READY_TIMEOUT`、`KEEP`。

`make deploy-test` 会在本机用 Docker 验证服务器部分（不需要 nginx 和 ssh），覆盖以下场景：首次发布、升级并清理旧版本、新版本起不来、`current` 是真目录、切换后站点检查失败、新版本跑完一条兼容迁移后才失败、**从线上版本（0e45b18，遇到任何不认识的迁移都拒绝启动）升级、新版本迁移后失败，靠冷备份自动恢复**。改到后端、`Dockerfile`、`deploy/` 或部署脚本时，本地 CI 的 `local-ci/docker` 会跑这个测试（COORDINATION.md §4.2）。

### 2.1 手动回滚到更早的版本

服务器上保留着最近几个版本的镜像和静态目录，直接用服务器部分的脚本切回去：

```bash
ssh $SSH_HOST 'ls -t /var/www/guandan; docker image ls guandan'
ssh $SSH_HOST 'env SHA=<旧短 SHA> sh -s' < scripts/deploy-remote.sh
```

### 2.2 迁移和回滚

迁移默认必须向前兼容（§12.1：只加表、加列）。旧版本的程序遇到库里多出来、自己不认识的兼容迁移时，会记一条 warn 日志，然后照常启动。所以**普通发布的自动回滚始终有效**：即使新版本跑完迁移之后才失败，旧版本也能起来。`make deploy-test` 的场景 6 验证的就是这一点。

只有标记了 breaking 的迁移会让旧版本拒绝启动（见 2.3），#34 之前的版本则会拒绝**任何**不认识的迁移（见 2.5）。这两种情况下回滚时旧容器起不来，`deploy-remote.sh` 会自动用发布前的冷备份恢复（见上面「第 3–5 步任何一步失败」）。

脚本自己的恢复也失败时，在服务器上手动执行（`backup` 和 `restore` 都不会迁移数据库，用新旧哪个镜像都可以）：

```bash
docker rm -f guandan                       # 确保没有容器在用数据卷
docker run --rm -v guandan-data:/data -e GUANDAN_PUBLIC_URL=https://guandan.synon.ai \
  guandan:<旧短 SHA> backup list            # 找到发布前那份（发布日志里也打印了）
docker run --rm -v guandan-data:/data -e GUANDAN_PUBLIC_URL=https://guandan.synon.ai \
  guandan:<新短 SHA> restore /data/backup/guandan-<发布前的时间>.db
```

然后在本机按 2.1 发布旧版本。从发布前备份到回滚之间写入的数据会丢失。

### 2.3 breaking 迁移：发布前人工确认

会破坏旧版本的迁移（删列、改列的含义、加更严格的约束），文件开头必须写一行 `-- breaking: <原因>`。`migrate` 会在 `schema_migrations` 里给它记上 `breaking=1`。规则：

- **编号由 coord 分配**，在 PR 描述里写「申请 000N」；编号重复时程序拒绝启动。
- 加 breaking 迁移的 PR 要把文件名加进 `server/internal/store/migrate_internal_test.go` 的 `confirmedBreaking`，否则测试失败。这样做是为了让作者和复审方必须在 PR 里明确确认。
- **发布前**：先确认这个版本不需要回滚，或者已经接受「回滚要从发布前快照恢复，会丢失这段时间的数据」；选一个低峰时段；发布后盯一会儿日志。

### 2.4 镜像怎么送到服务器

| 方式 | 用法 | 前提 |
|---|---|---|
| **ssh（默认）** | `scripts/deploy.sh` | 能 ssh 到服务器。不需要任何镜像仓库凭据；压缩后约 6 MB |
| **GHCR** | `IMAGE_SOURCE=ghcr scripts/deploy.sh` | 本机执行过 `docker login ghcr.io`（token 要有 `write:packages`）；服务器能拉到镜像：要么仓库所有者在 GitHub 的 Packages 设置里把 `jkt-guandan` 设为公开，要么在服务器上执行 `docker login ghcr.io`（token 只给 `read:packages`） |

两种方式下，服务器上的镜像名都是 `guandan:<短 SHA>`；GHCR 方式拉下来之后会重新打成这个名字，并删掉 GHCR 的标签。

### 2.5 这一次的过渡发布：从 0e45b18 升级到 #34

线上现在跑的是 0e45b18（#35）。它和 #34 之前的所有版本一样：遇到**任何**不认识的迁移都拒绝启动，也没有 `backup` 子命令。#34 带来了新迁移 `0003_bans.sql`，所以这一次发布如果在新版本跑完迁移之后失败，旧版本就起不来。按下面的步骤做：

1. 照常执行 `SSH_HOST=… scripts/deploy.sh`。脚本会在停掉旧容器之后用**新镜像**做冷备份（第 2 步），所以不依赖旧版本；冷备份失败就中止，旧版本原样恢复。
2. 如果新版本没有就绪，脚本回滚时会发现旧版本起不来，然后自动用冷备份恢复，再拉起旧版本。日志最后一行应是「已从发布前快照恢复，旧版本恢复服务」。这条路径已经在 `make deploy-test` 的场景 7 里用 0e45b18 的镜像实际验证过。
3. 发布日志里会打印冷备份的路径（`发布前冷备份：/data/backup/guandan-….db`）。**把这一行记下来**，脚本自己的恢复也失败时按 2.2 手动恢复。
4. 这次发布成功之后，线上就有了「兼容迁移只记 warn」的规则，以后的普通发布回滚不再需要恢复数据。

注意：0e45b18 的镜像没有 `org.jkt-guandan.ops` 标签。这次发布用的是新镜像做备份，不受影响；但如果以后要**手动回滚到 0e45b18 或更早**，数据卷上又已经有 `0003_bans.sql`，那个旧版本本来就起不来，应该按 2.2 先恢复发布前的快照，再回滚。

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

上线前至少演练一次，以后每次修改备份相关代码后再演练一次。演练脚本在本机用 Docker 模拟两台机器：一台旧机器、一个「异地」目录、一台新机器（全新的空数据卷）。改到后端、`Dockerfile`、`deploy/` 或部署脚本时，本地 CI 的 `local-ci/docker` 会跑这个脚本。

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
| 新机器的空数据卷上执行 `restore -i /key.txt latest` | 取到最新的一份，迁移 `0001_init.sql、0003_bans.sql`，校验通过 |
| 新机器启动 | `/readyz` 200；`admin stats` 显示 12 个用户、5 个牌局，与旧机器一致；`X-App-Build` 和 `/metrics` 正常 |
| 服务运行时执行 restore | 被拒绝：「数据目录 /data 已被另一个实例占用；恢复前先停服务」 |

## 5. 管理命令（§13.6）

都通过 `docker exec guandan /guandan …` 执行，和正在运行的服务共用同一个数据库：

```bash
guandan admin ban <用户或设备 id>          # 封禁，之后的请求一律返回 403（由身份中间件 `identity` 的 gate 拦截，返回 `403 banned`）
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

**封小游戏用户**：小游戏用户删掉小游戏再进，`wx.login` 认回的还是同一个人。封了他的小游戏设备之后，重新登录会被拒（`403 banned`），但他名下其他已经登录的设备还能用，而且被封的设备 180 天后会被清理任务删掉，拦截随之失效。要彻底封，用 `admin ban <用户 id>`（server-design §13.6）。

## 6. 监控和日志

- **`/metrics`** 只监听容器内的 `127.0.0.1:9090`，外部访问不到，nginx 也不转发。查看方式是 `docker exec guandan /guandan metrics`。内容包括：
  - 请求数（按 2xx/4xx/5xx 分类）和请求耗时；
  - 写锁等待时间；
  - outbox 积压量和放弃数；
  - 备份时间；
  - 构建号；
  - **微信内容安全检测**：`guandan_wx_sec_check_total{outcome}`（`pass` / `risky` / `review` / `error` / `timeout` / `no_openid`）和 `guandan_wx_sec_check_duration_seconds`。`error` / `timeout` 明显升高说明微信那边在抖 —— 这时检测是**放行**的（§14 的失败策略），所以只会看到日志里的 errcode 变多，不会拦住用户；
  - **微信 access_token**：`guandan_wx_token_total{kind}`，`kind` 是 `ok`（普通取）/ `force`（真的强刷）/ `force_cooled`（想强刷但离上次不到 30 秒，退成普通模式）/ `error`（没取到）。

  WebSocket 连接数由 #18 补上。

### 强刷额度用完了怎么办

腾讯给的强制刷新额度是**每天 20 次、且两次之间要隔 30 秒**（《获取稳定版接口调用凭据》）。额度用完，`access_token` 就只能等缓存的自然过期才会换 —— 内容安全检测和订阅消息**都会跟着受影响**。

- **怎么看用了多少**：`increase(guandan_wx_token_total{kind="force"}[1d])`。注意这是**进程启动以来**的累计计数器推出来的，重启会丢 —— 所以它只是**下限**，不是「还剩几次」。真要精确，得连着看不重启的一段。
- **现象**：日志里 `consumer` / `code` 两个字段会标出是谁触发的（`consumer` 是「内容安全」或「订阅消息」，`code` 是微信错误码）。`kind="force"` 一直涨，说明有东西在反复把 token 弄脏；`force_cooled` 同时涨，说明我们已经在压着刷了。
- **处理**：
  1. 先看是不是**同一个 AppID 被别处共用**（`force_refresh` 会让上一次的 token 立刻失效，两边互相顶）。仓库里的约定是内容安全和订阅消息**共用同一个缓存**（§5.3 / #159），别在别的地方再建一个。
  2. 看 `code`：`40001` / `40014` / `42001` 才是 token 的问题，别的码不该触发强刷 —— 出现了就是分类逻辑的 bug。
  3. 额度真耗尽的当天**没有补救手段**，只能等自然日翻页；期间内容安全检测会走失败放行（不影响开局），订阅消息会按 §5.3 的退避重试。**不要**去重启服务「刷一下」—— 计数器会清零，等于把唯一的证据丢掉。
  4. 第二天换 `access_token` 前先确认根因：如果是共用 AppID 的外部系统在刷，先找它，否则明天还会再来一遍。
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
