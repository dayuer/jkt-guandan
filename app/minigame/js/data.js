// 原型用的本地假数据，形状尽量贴近一期 GET /api/games。接真接口时换掉这里。

const AREAS = ['SCBD', 'PIK', 'Central Park', 'Kuningan', 'Senayan', 'Kelapa Gading'];
const KINDS = ['经典掼蛋', '不升级', '新手教学局'];
const BANNED = ['赌', '筹码', '输赢', 'judi', 'taruhan'];

let seq = 4;
const games = [
  { code: 'K7Q2', day: '今天', date: '10/14', week: '周三', time: '19:30', area: 'SCBD', kind: '经典掼蛋',
    place: 'Pacific Place · 鼎泰丰包间', note: '打到 A 为止，新手友好。停车 B3，到了在群里喊一声。',
    host: '老王', players: ['老王', 'Lily', '阿杰'], wait: 0 },
  { code: 'P3M8', day: '今天', date: '10/14', week: '周三', time: '20:00', area: 'PIK', kind: '不升级',
    place: 'PIK Avenue · 茶室二楼', note: '', host: '陈哥', players: ['陈哥', '小周'], wait: 0 },
  { code: 'C9X1', day: '明天', date: '10/15', week: '周四', time: '14:00', area: 'Central Park', kind: '新手教学局',
    place: 'Central Park Mall · 咖啡厅', note: '完全不会也可以来，带你从理牌开始。', host: 'Kevin', players: ['Kevin'], wait: 0 },
  { code: 'S2N5', day: '周六', date: '10/17', week: '周六', time: '19:00', area: 'Senayan', kind: '经典掼蛋',
    place: 'Plaza Senayan · 海底捞旁边的包间，名字很长用来测试省略号', note: '', host: '张总',
    players: ['张总', '小李', 'Andy', '王姐'], wait: 2 },
];

function need(g) { return 4 - g.players.length; }
function find(code) { return games.find(g => g.code === code); }

function create(form, me) {
  const code = 'N' + (seq++) + Math.random().toString(36).slice(2, 4).toUpperCase();
  const g = { code, day: form.day, date: form.date, week: form.week, time: form.time, area: form.area,
    kind: form.kind, place: form.place, note: form.note, host: me, players: [me], wait: 0, mine: true };
  games.unshift(g);
  return g;
}

function bannedWord(s) {
  const low = String(s).toLowerCase();
  return BANNED.find(w => low.includes(w));
}

module.exports = { AREAS, KINDS, games, need, find, create, bannedWord };
