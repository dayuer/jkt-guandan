// Package realtime 负责把变化实时推给页面（docs/server-design.md §5.5、§11.5、§12.3）。
//
//   - Hub：map[topic]set[*conn]，一把互斥锁保护。Publish 只把消息投进各连接的发送队列，
//     不在锁里做网络写；队列（容量 32）满了就断开那个连接，慢客户端拖不慢别人；
//   - 主题三类：game:{code}、feed:{area} 公开；user:{id} 只能订阅自己的；每个连接最多 20 个；
//   - 每个连接两个 goroutine：读（sub/unsub，每秒最多 10 条，单条最大 4 KB）和写（发送队列、
//     每 25 秒一次 ping，10 秒内没有 pong 就断开）；
//   - GET /ws 握手时校验 Origin 是本站；同一 IP 最多 20 条连接、每秒最多 5 条新连接；
//   - 关闭时给所有连接发 1012（服务重启），客户端随后重连；
//   - 身份失效（设备被退出、切换身份、被封禁）时以 4001 断开：同一进程里的操作在提交后主动
//     KickDevice / KickUser，其余靠每次心跳时的 Revalidate 复查兜底（#43）。
//
// REST 始终是事实来源：WebSocket 只说「哪里变了」，客户端断线重连后重新拉 REST。
// 事件只在写事务提交后发布——games / identity 通过 store.Tx.AfterCommit 登记回调，回滚不发。
package realtime
