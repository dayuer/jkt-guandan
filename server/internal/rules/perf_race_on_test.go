//go:build race

package rules

// perfScale 是耗时上限的倍率：`-race` 给每条语句插桩，同一手牌实测慢 ~5 倍
// （领出最慢 138ms → 582ms），这里放宽 10 倍留余量。CI 跑的就是 `go test -race`。
const perfScale = 10
