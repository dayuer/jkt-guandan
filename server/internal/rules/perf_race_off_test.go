//go:build !race

package rules

// perfScale 是耗时上限的倍率：不带 -race 时就是实测值本身（见 moves_test.go 的注释）。
const perfScale = 1
