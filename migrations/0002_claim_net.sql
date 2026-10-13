-- 认领码的两处加固（§5.2 修订、§10.3）。

-- 1) 认领码只能在签发时的同一个网络里兑换。签发时记下客户端 IP 归一后的网络前缀：
--    IPv4 取前 24 位，IPv6 取前 64 位。
ALTER TABLE claim_tokens ADD COLUMN issued_net TEXT;

-- 2) 未使用的码之间不允许重复。应用层签发时已经会查重，这里是数据库层的最后一道防线
--    （§11.2）：即使应用层有漏洞，也不可能插进两个同号的可用码。
--    谓词要和签发时的查重条件一致——都是「used_at IS NULL」，作废旧码时也写 used_at。
DROP INDEX claim_code_live;
CREATE UNIQUE INDEX claim_code_live ON claim_tokens(code) WHERE used_at IS NULL;
