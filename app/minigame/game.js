// 小游戏入口：#83 选型原型，原生 Canvas + 自写 UI，三屏（列表、详情、开局）。
// 数据是本地假数据（js/data.js），不连服务端。

const App = require('./js/app');
const Home = require('./js/scenes/home');
const Detail = require('./js/scenes/detail');
const Create = require('./js/scenes/create');

const app = new App();

const open = {
  detail: code => app.push(new Detail(app, code, open)),
  create: () => app.push(new Create(app, open)),
  // 开局成功：回到列表再进详情，返回键回到列表
  created: code => {
    app.stack = [app.stack[0]];
    app.push(new Detail(app, code, open));
    app.toast('开好了，点「分享」发到群里');
  },
};

app.replaceRoot(new Home(app, open));

// 从分享卡片进来：query 带牌局短码 g（冷启动和热启动都要处理）
function enter(query) {
  const code = query && query.g;
  if (code && require('./js/data').find(code)) {
    app.stack = [app.stack[0]];
    open.detail(code);
  }
}
enter(wx.getLaunchOptionsSync().query);
wx.onShow(res => enter(res.query));

wx.showShareMenu({ menus: ['shareAppMessage', 'shareTimeline'] });
wx.onShareAppMessage(() => ({ title: '随时掼掼蛋 · 找人凑一桌，就差你一个' }));
