//go:build !race

package rules

// perfScale 是耗时上限的倍率：不带 -race 时就是实测值本身（见 moves_test.go 的注释）。
const perfScale = 1

// simDeals 是随机对局（sim_test.go）跑几副牌：不带 -race 跑满。要复现某副就用它自己的种子
// （种子里带副号），想跑快一点用 `-short`（200 副）。
const simDeals = 3000
