// 接收小游戏自检（app/minigame/src/selftest.ts）发过来的日志和画布截图。
// 小游戏在开发者工具里跑起来后，会 POST 到这里。只用 Node 内置模块。
const http = require('http')
const fs = require('fs')
const path = require('path')

const OUT = process.env.SINK_OUT || '/tmp/impl-165-sink/out'
const PORT = Number(process.env.SINK_PORT || 18675)
fs.mkdirSync(OUT, { recursive: true })
const logPath = path.join(OUT, 'log.jsonl')
fs.writeFileSync(logPath, '')

// multipart/form-data 里抠出第一个文件字段的内容。wx.uploadFile 发的就是这个格式。
function firstFilePart(buf, boundary) {
  const b = Buffer.from('--' + boundary)
  let pos = buf.indexOf(b)
  while (pos !== -1) {
    const headEnd = buf.indexOf('\r\n\r\n', pos)
    if (headEnd === -1) return null
    const head = buf.slice(pos, headEnd).toString('utf8')
    const next = buf.indexOf(b, headEnd)
    if (next === -1) return null
    if (/filename=/.test(head)) {
      let body = buf.slice(headEnd + 4, next)
      if (body.slice(-2).toString() === '\r\n') body = body.slice(0, -2)
      return body
    }
    pos = next
  }
  return null
}

// query 里的 name 只当文件名用：直接拼路径的话 `?name=../../x` 能写到 OUT 外面（#208 四）。
// 只监听 127.0.0.1，风险低，但没理由留这个口子。
function safeName(raw) {
  const s = String(raw || '')
  if (!s || s.includes('\0')) return null
  const base = path.basename(s)
  if (!base || base === '.' || base === '..' || base.includes('/') || base.includes('\\')) return null
  return base
}

function handle(req, res, body) {
  const url = new URL(req.url, 'http://127.0.0.1')
  const raw = url.searchParams.get('name')
  if (raw) {
    // 截图
    const name = safeName(raw)
    if (!name) throw new Error('bad name: ' + JSON.stringify(raw))
    const ct = req.headers['content-type'] || ''
    const m = /boundary=(.+)$/.exec(ct)
    let img = null
    if (m) img = firstFilePart(body, m[1].replace(/^"|"$/g, ''))
    if (!img) img = body
    const file = path.join(OUT, name + '.png')
    fs.writeFileSync(file, img)
    console.log('SHOT ' + name + ' ' + img.length + 'B -> ' + file)
    return
  }
  // 日志：JSON 解析失败、或者根本不是对象（null / 数组 / 数字），都当「没解析出来」记下来，
  // 不能因为一条坏日志就把接收端整个搞崩（#208 四）。
  let j = null
  try { j = JSON.parse(body.toString('utf8')) } catch { j = null }
  if (!j || typeof j !== 'object' || Array.isArray(j)) j = { kind: 'unparsed', raw: body.toString('utf8').slice(0, 200) }
  fs.appendFileSync(logPath, JSON.stringify(j) + '\n')
  const d = j.data && typeof j.data === 'object' ? j.data : {}
  const kind = typeof j.kind === 'string' && j.kind ? j.kind : 'unknown'; // 缺 kind 也要兜住
  const line = kind === 'state' ? 'STATE ' + d.label + ' stack=' + JSON.stringify(d.stack) + ' sheets=' + d.sheets
    : kind === 'touchstart' ? 'TOUCH ' + d.x + ',' + d.y + ' hit=' + d.hit
    : kind === 'unparsed' ? 'UNPARSED ' + String(j.raw || '').slice(0, 200)
    : kind.toUpperCase() + ' ' + JSON.stringify(d).slice(0, 220);
  if (kind !== j.kind) console.log('（这条日志没有 kind，按 unknown 记）');
  console.log(line);
}

http
  .createServer((req, res) => {
    const chunks = []
    req.on('data', (c) => chunks.push(c))
    req.on('end', () => {
      try {
        handle(req, res, Buffer.concat(chunks))
        res.writeHead(200, { 'content-type': 'text/plain' })
        res.end('ok')
      } catch (e) {
        // 一条请求处理不了不该让接收端退出：回 400、打日志，继续收后面的
        console.error('BAD ' + (e && e.message ? e.message : e))
        res.writeHead(400, { 'content-type': 'text/plain' })
        res.end('bad request\n')
      }
    })
  })
  .listen(PORT, '127.0.0.1', () => console.log('sink on 127.0.0.1:' + PORT + ' -> ' + OUT))

