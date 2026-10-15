// #189 验证：局头转交给别人之后，**接手的那个人换了设备（本机快照没了）**也要看得到 K2「你现在是局头」。
// 顺带验证 #185 复审意见 3：接手的局头也要看得到上一位局头改过的「改了安排」横幅。
// 本机设备身份是「阿杰」（dev:ajie）。真服务端 + 真小游戏 bundle，热区点按，逐步截图。
module.exports = async (t) => {
  const { tapId, shot, pump, expect, has, waitFor, log, store } = t;
  const BASE = 'http://127.0.0.1:18776';
  const now = () => Math.floor(Date.now() / 1000);
  const nav = () => global.__nav;
  const top = () => global.__app.top;

  const raw = async (method, p, body, token, headers = {}) => {
    const r = await fetch(BASE + p, {
      method,
      headers: { 'content-type': 'application/json', ...(token ? { authorization: 'Bearer ' + token } : {}), ...headers },
      body: body ? JSON.stringify(body) : undefined,
    });
    return { status: r.status, body: await r.json().catch(() => null) };
  };
  const login = async (code, nickname) => (await raw('POST', '/api/wx/login', { code: 'dev:' + code, nickname })).body.device_token;
  const T = {
    laowang: await login('laowang', '老王'), lily: await login('lily', 'Lily'),
    ajie: await login('ajie', '阿杰'), zhou: await login('xiaozhou', '小周'),
    zhang: await login('zhang', '老张'),
  };

  // 「换设备」：把这一台上的状态快照清掉（微信缓存被清 / 换了手机），身份 token 留着。
  const clearLocal = () => {
    let n = 0;
    for (const k of [...store.keys()]) {
      if (/^gd_game_(seen|fallback_seen):/.test(k)) { store.delete(k); n++; }
    }
    log.push(`换设备：清掉 ${n} 个本机快照键`);
  };

  const mkGame = async (host, joiners, inSec = 4 * 3600) => {
    const r = await raw('POST', '/api/games', { starts_at: now() + inSec, area: 'SCBD', venue: 'Pacific Place · 鼎泰丰包间', kind: 'classic' }, T[host]);
    const code = r.body.game.short_code;
    for (const j of joiners) await raw('POST', `/api/games/${code}/seat`, null, T[j]);
    return code;
  };
  const openDetail = async (code) => {
    nav().detail(code);
    await pump(1400);
    await waitFor(() => top() && top().detail, 6000);
    await pump(400);
  };
  const leave = async (code, who) => {
    const r = await raw('DELETE', `/api/games/${code}/seat`, null, T[who]);
    log.push(`${who} 退出 → ${r.status}`);
    return r.status;
  };

  await pump(1500);

  // ── 场景 A：局头退出 → 阿杰接手 → 换设备 → K2「你现在是局头」 ────────────────
  // 阿杰是老王之外最早坐下的，所以老王退出时会转交给他。
  const A = await mkGame('laowang', ['ajie', 'lily', 'zhou']);
  log.push('A game ' + A);

  await openDetail(A);
  const a0 = (await raw('GET', `/api/games/${A}`, null, T.ajie)).body;
  expect(a0.me.role === 'seat' && !a0.me.became_host_at, 'A：这会儿阿杰只是普通座位，没有 became_host_at');
  expect(!has('notice:host'), 'A：还没转交 → 没有「你现在是局头」横幅');
  await shot('S0-before-transfer');

  // 老王（局头）退出 → 自动转交给阿杰
  expect((await leave(A, 'laowang')) === 200, 'A：局头退出成功');
  const a1 = (await raw('GET', `/api/games/${A}`, null, T.ajie)).body;
  expect(a1.host.nickname === '阿杰', 'A：服务端已经把这局交给阿杰');
  expect(!!a1.me.became_host_at, 'A：服务端给了 me.became_host_at');

  clearLocal(); // 换设备

  await top().load();
  await pump(900);
  expect(has('notice:host'), 'A：**无快照** + 我接手当上局头 → 出 K2 横幅');
  await shot('S1-took-over-fallback');

  // 自己开的局不该给自己弹（这里用同一条兜底规则的负向对照）
  await tapId('notice:host:x');
  await pump(600);
  expect(!has('notice:host'), 'A：关掉之后不再出现');

  await openDetail(A);
  await pump(1000);
  expect(!has('notice:host'), 'A：再进来不再出现（快照已写下）');

  // ── 场景 B：补位 → 又接手当上局头，且上一位局头改过时间 → 两条横幅一起出 ──────
  const B = await mkGame('laowang', ['lily', 'zhou', 'zhang']); // 4 个座位坐满
  log.push('B game ' + B);

  // 这几步之间都要隔一秒多：时间戳都是秒级的，落在同一秒就分不出先后 ——
  // 第一版没隔，「排候补」和「改时间」同秒，服务端其实是对的，却报成 FAIL。
  const tick = () => new Promise((r) => setTimeout(r, 1300));

  await tick();
  await raw('POST', `/api/games/${B}/seat`, null, T.ajie);      // 阿杰排候补

  // 局头在阿杰排候补之后改了时间（阿杰还没看过）
  await tick();
  const bg = (await raw('GET', `/api/games/${B}`, null, T.laowang)).body;
  const rs = await raw('PATCH', `/api/games/${B}`, { starts_at: now() + 7 * 3600 }, T.laowang, { 'If-Match': String(bg.game.version) });
  expect(rs.status === 200, `B：局头改时间成功（实际 ${rs.status}）`);

  await tick();
  await leave(B, 'lily');                                        // → 阿杰补位
  const b1 = (await raw('GET', `/api/games/${B}`, null, T.ajie)).body;
  expect(b1.me.role === 'seat' && !!b1.me.promoted_at, 'B：阿杰补上了座位');
  expect(!b1.me.became_host_at, 'B：这会儿还不是局头');

  // 其余座位空出来，最后老王退出 → 转交给阿杰
  await leave(B, 'zhou');
  await leave(B, 'zhang');
  expect((await leave(B, 'laowang')) === 200, 'B：老王退出成功');
  const b2 = (await raw('GET', `/api/games/${B}`, null, T.ajie)).body;
  expect(b2.host.nickname === '阿杰', 'B：服务端把这局交给了阿杰');
  expect(b2.me.joined_at < b2.me.promoted_at, 'B：joined_at 从排候补算，早于补位');
  expect(b2.game.rescheduled_at > b2.me.joined_at, 'B：改时间发生在我加入之后');
  expect(b2.me.became_host_at > b2.game.rescheduled_at, 'B：我接手**晚于**那次改时间 —— 不是我改的');

  clearLocal(); // 换设备
  await openDetail(B);
  await pump(1200);

  // 换设备后先是「轮到你了」弹层（#160 的兜底），关掉它才能看到下面的横幅。
  expect(has('sb:0'), 'B：换设备后先弹「有人退出，你补上了」（#160 的兜底）');
  await shot('S2-promoted-sheet');
  await tapId('sb:0');
  await pump(900);
  expect(!has('sb:0'), 'B：关掉弹层');

  expect(has('notice:host'), 'B：换设备后照样出「你现在是局头」');
  expect(has('notice:rescheduled'), 'B：**接手**的局头也出「改了安排」横幅（#185 复审意见 3）');
  await shot('S3-promoted-then-host-both');

  await tapId('notice:host:x');
  await pump(700);
  expect(!has('notice:host'), 'B：关掉局头那条');
  expect(has('notice:rescheduled'), 'B：还有一条没关，改时间横幅还在');
  await tapId('notice:rescheduled:x');
  await pump(700);
  expect(!has('notice:rescheduled'), 'B：两条都关掉');
  await shot('S4-both-dismissed');

  await openDetail(B);
  await pump(1000);
  expect(!has('notice:host') && !has('notice:rescheduled'), 'B：再进来两条都不再出现');
};
