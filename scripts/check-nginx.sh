#!/bin/sh
# 检查 guandan 的 nginx 站点配置（docs/runbook.md §0）。仓库里的模板和服务器上的线上文件都能查：
#   scripts/check-nginx.sh deploy/nginx/guandan.synon.ai.conf
#   ssh root@43.156.128.95 sh -s /etc/nginx/sites-available/guandan.synon.ai < scripts/check-nginx.sh
#
# 规则：
#   1. 每个含 proxy_pass 的 location 都要自己写全 Host、X-Real-IP、X-Forwarded-For、X-Forwarded-Proto。
#      nginx 的 location 里只要写了一条 proxy_set_header，就不再继承 server 级的；漏了 X-Real-IP，
#      客户端自己带的值会原样传给应用，按 IP 限流就能被伪造（#35）。
#      X-Real-IP 必须是 $remote_addr，X-Forwarded-For 必须是 $proxy_add_x_forwarded_for。
#   2. ACME 的 webroot 是 /var/www/certbot（和 certbot 的续期配置一致，否则续期会悄悄失败）。
#   3. 有 options-ssl-nginx.conf 和 ssl_dhparam；有 open_file_cache off（切换 current 立即生效）。
set -eu
FILE=${1:?"用法：scripts/check-nginx.sh <站点配置文件>"}
[ -r "$FILE" ] || { echo "读不到 $FILE" >&2; exit 2; }

awk '
function fail(msg) { printf "check-nginx: %s\n", msg; bad = 1 }
function check_location(name, body,    h, n, hdrs) {
  if (body !~ /proxy_pass[ \t]/) return
  n = split("Host X-Real-IP X-Forwarded-For X-Forwarded-Proto", hdrs, " ")
  for (h = 1; h <= n; h++)
    if (body !~ ("proxy_set_header[ \t]+" hdrs[h] "[ \t]"))
      fail("「" name "」反向代理了，但没有写 proxy_set_header " hdrs[h])
  if (body ~ /proxy_set_header[ \t]+X-Real-IP[ \t]/ && body !~ /proxy_set_header[ \t]+X-Real-IP[ \t]+\$remote_addr;/)
    fail("「" name "」的 X-Real-IP 必须是 $remote_addr")
  if (body ~ /proxy_set_header[ \t]+X-Forwarded-For[ \t]/ && body !~ /proxy_set_header[ \t]+X-Forwarded-For[ \t]+\$proxy_add_x_forwarded_for;/)
    fail("「" name "」的 X-Forwarded-For 必须是 $proxy_add_x_forwarded_for")
  locations++
}
{
  line = $0
  sub(/#.*/, "", line)                       # 去掉注释
  all = all "\n" line
  if (!inloc && line ~ /^[ \t]*location[ \t]/) {
    inloc = 1; depth = 0; body = ""
    name = line; sub(/^[ \t]*/, "", name); sub(/[ \t]*\{.*/, "", name)
  }
  if (inloc) {
    body = body "\n" line
    tmp = line; opens = gsub(/\{/, "", tmp)
    tmp = line; closes = gsub(/\}/, "", tmp)
    depth += opens - closes
    if (depth <= 0 && (opens > 0 || closes > 0)) { check_location(name, body); inloc = 0 }
  }
}
END {
  if (all !~ /acme-challenge\/[ \t]*\{[ \t]*root[ \t]+\/var\/www\/certbot;/) fail("ACME 的 webroot 应为 /var/www/certbot")
  if (all ~ /\/var\/www\/letsencrypt/) fail("不要用 /var/www/letsencrypt，certbot 的续期配置用的是 /var/www/certbot")
  if (all !~ /include[ \t]+\/etc\/letsencrypt\/options-ssl-nginx\.conf;/) fail("缺少 include /etc/letsencrypt/options-ssl-nginx.conf;")
  if (all !~ /ssl_dhparam[ \t]+\/etc\/letsencrypt\/ssl-dhparams\.pem;/) fail("缺少 ssl_dhparam /etc/letsencrypt/ssl-dhparams.pem;")
  if (all !~ /open_file_cache[ \t]+off;/) fail("缺少 open_file_cache off;")
  if (locations == 0) fail("一个反向代理的 location 都没有找到")
  if (bad) exit 1
  printf "check-nginx: 通过（%d 个反向代理的 location）\n", locations
}
' "$FILE"
