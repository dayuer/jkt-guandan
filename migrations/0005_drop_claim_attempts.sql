-- breaking: 删除 claim_tokens.attempts。旧版本签发认领码时会 INSERT 这一列，回滚到旧版本后签发会失败。
-- 「每个码最多试 5 次」的计数器从来没有生效过：猜错的码匹配不到任何行，数不出来（#95）。
-- 真正起作用的是同网络绑定（issued_net）、每 IP 限速和全站失败预算。
ALTER TABLE claim_tokens DROP COLUMN attempts;
