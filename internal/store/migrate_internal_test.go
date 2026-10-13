package store

import (
	"strings"
	"testing"
)

// 两个迁移编号相同（比如两个分支各自加了 0002）时拒绝启动（coord 对 #34 的要求）。
func TestCheckUniqueNumbers(t *testing.T) {
	cases := []struct {
		names []string
		bad   string // 错误信息里应出现的内容，空表示应通过
	}{
		{[]string{"0001_init.sql", "0002_bans.sql", "0003_x.sql"}, ""},
		{[]string{"0001_init.sql", "0002_bans.sql", "0002_identity.sql"}, "编号重复"},
		{[]string{"0002_a.sql", "2_b.sql"}, "编号重复"}, // 前导零不同也算同号
		{[]string{"init.sql"}, "编号_说明"},
		{[]string{"0003.sql"}, "编号_说明"},
	}
	for _, c := range cases {
		err := checkUniqueNumbers(c.names)
		if c.bad == "" && err != nil {
			t.Errorf("%v 应通过: %v", c.names, err)
		}
		if c.bad != "" && (err == nil || !strings.Contains(err.Error(), c.bad)) {
			t.Errorf("%v 应报「%s」，实际 %v", c.names, c.bad, err)
		}
	}
}

// 仓库里实际的迁移文件必须满足这条规则。
func TestEmbeddedMigrationsHaveUniqueNumbers(t *testing.T) {
	if _, err := migrationNames(); err != nil {
		t.Fatal(err)
	}
}
