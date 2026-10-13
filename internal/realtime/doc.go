// Package realtime 负责把变化实时推给页面（docs/server-design.md §5.5）。
//
// 现在是占位，实现在任务 #18。届时这里会有：
//   - 进程内的 Hub：map[topic]set[*conn]，一把互斥锁保护，Publish 只把消息投进
//     各连接的发送队列，不在锁里做网络写；
//   - 主题三类：game:{code}、feed:{area}、user:{id}；
//   - 每个连接两个 goroutine（读 sub/unsub、写队列 + 25 秒心跳），发送队列容量 32，
//     写满即断开；
//   - 握手地址 GET /ws，必须校验 Origin 是本站。
//
// REST 始终是事实来源：WebSocket 只说「哪里变了」，客户端断线重连后重新拉 REST。
// 事件只在写事务提交后发布——games / identity 通过 store.Tx.AfterCommit 登记回调，
// 回滚不发。
package realtime
