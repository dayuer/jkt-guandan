//go:build race

package rules

// perfScale 是耗时上限的倍率：`-race` 给每条语句插桩，同一手牌实测慢 ~5 倍
// （领出最慢 138ms → 582ms），这里放宽 10 倍留余量。CI 跑的就是 `go test -race`。
const perfScale = 10

// simDeals 是随机对局（sim_test.go）跑几副牌：`-race` 下同一副慢好几倍，而本机常有几个
// 会话同时跑 CI，所以只跑 150 副（不带 -race 是 3000 副）。要跑满就不带 -race 跑本机的
// `go test ./internal/rules/ -run TestSimManyDeals`。
const simDeals = 150
