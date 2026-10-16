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

http
  .createServer((req, res) => {
    const chunks = []
    req.on('data', (c) => chunks.push(c))
    req.on('end', () => {
      const body = Buffer.concat(chunks)
      const url = new URL(req.url, 'http://127.0.0.1')
      const name = url.searchParams.get('name')
      if (name) {
        // 截图
        const ct = req.headers['content-type'] || ''
        const m = /boundary=(.+)$/.exec(ct)
        let img = null
        if (m) img = firstFilePart(body, m[1].replace(/^"|"$/g, ''))
        if (!img) img = body
        const file = path.join(OUT, name + '.png')
        fs.writeFileSync(file, img)
        console.log('SHOT ' + name + ' ' + img.length + 'B -> ' + file)
      } else {
        let j = null
        try { j = JSON.parse(body.toString('utf8')) } catch { j = { kind: 'unparsed', raw: body.toString('utf8').slice(0, 200) } }
        fs.appendFileSync(logPath, JSON.stringify(j) + '\n')
        const d = j.data || {}
        if (j.kind === 'state') console.log('STATE ' + d.label + ' stack=' + JSON.stringify(d.stack) + ' sheets=' + d.sheets)
        else if (j.kind === 'touchstart') console.log('TOUCH ' + d.x + ',' + d.y + ' hit=' + d.hit)
        else console.log(j.kind.toUpperCase() + ' ' + JSON.stringify(d).slice(0, 220))
      }
      res.writeHead(200, { 'content-type': 'text/plain' })
      res.end('ok')
    })
  })
  .listen(PORT, '127.0.0.1', () => console.log('sink on 127.0.0.1:' + PORT + ' -> ' + OUT))
