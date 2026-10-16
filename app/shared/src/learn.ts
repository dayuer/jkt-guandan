/**
 * 学牌（#106，设计稿 E1 / E2）：静态的掼蛋规则，按「入门 / 进阶 / 高级」分三组，每组三课。
 *
 * #253 P6 起住在 app/shared：课本正文、分组、以及「下一课 / 某课」这些查询两端共用。
 * 这一层**不碰 window / wx**：学习进度存在哪由调用方给（`LearnStore`），
 * H5 的默认值（`window.localStorage`）在 `app/h5/src/learn.ts`，
 * 小游戏在 `app/minigame/src/lib/learn.ts`（走 shim.ts 垫的那层 wx 存储）。
 *
 * 规则以 2022《竞技掼蛋竞赛规则（试行）》（国家体育总局棋牌运动管理中心审定，2022-09-08）为准
 * （#205），出处写在对应正文里，条款号出自这份规则。牌桌计分（server/internal/scoring、
 * app/minigame/src/lib/scoring.ts）用同一套口径。
 * 地方打法和规则冲突时按规则来，所以正文里不再写「常见约定」。
 */

export type Level = "basic" | "advanced" | "expert";

export const LEVELS: { key: Level; label: string; title: string; hint: string }[] = [
  { key: "basic", label: "入门", title: "入门 · 先会打", hint: "规则、牌型、升级" },
  { key: "advanced", label: "进阶", title: "进阶 · 打明白", hint: "炸弹、进贡、接风" },
  { key: "expert", label: "高级", title: "高级 · 打得好", hint: "逢人配、配合、过 A" },
];

export type Suit = "♠" | "♥" | "♣" | "♦" | "🃏";

/** 牌桌示意里的一张牌。 */
export interface Card {
  rank: string;
  suit: Suit;
  /** 金边高亮（比如逢人配）。 */
  hot?: boolean;
}

export interface Section {
  title: string;
  body: string;
}

export interface Quiz {
  question: string;
  options: string[];
  /** 正确选项的下标。 */
  answer: number;
  /** 答完给的解释。 */
  explain: string;
}

export interface Lesson {
  id: string;
  level: Level;
  title: string;
  /** 列表和正文标题下的一句话。 */
  summary: string;
  minutes: number;
  hand?: Card[];
  handCaption?: string;
  sections: Section[];
  tip?: string;
  quiz: Quiz;
}

export const LESSONS: Lesson[] = [
  {
    id: "overview",
    level: "basic",
    title: "规则速览",
    summary: "四人两队，比谁先出完",
    minutes: 6,
    hand: [
      { rank: "7", suit: "♥", hot: true },
      { rank: "7", suit: "♠" },
      { rank: "A", suit: "♠" },
      { rank: "K", suit: "♦" },
      { rank: "2", suit: "♣" },
    ],
    handCaption: "本局打 7：红桃 7 就是「逢人配」",
    sections: [
      {
        title: "四人两队",
        body: "两副牌共 108 张（含 4 张王），四人围坐，对家为搭档，每人 27 张。最先出完的叫头游，接着是二游、三游、末游（规则里依次叫上游、二游、三游、下游，第二条 10）。下面各课还按大家习惯的叫法。目标是让本队两人都尽量靠前。",
      },
      {
        title: "级牌与逢人配",
        body: "双方都从 2 开始打。本局打几，几就是级牌，大小仅次于大小王，排在 A 前面。红桃级牌叫「逢人配」，除了大小王以外，能当任意一张牌用。",
      },
      {
        title: "怎么出牌",
        body: "第一个出牌的人可以出任意牌型，后面的人按座位顺序轮流，要么出同一种牌型、同样张数、更大的牌压上，要么「不要」。其他三家都不要时，最后出牌的人重新起一手。炸弹可以压任何非炸弹的牌型。",
      },
      {
        title: "大小顺序",
        body: "单张从小到大：2、3、4……K、A，然后是级牌、小王、大王。打几级，那个点数就从原来的位置提到 A 之上。",
      },
    ],
    tip: "逢人配是全场最值钱的一张牌，开局别急着拆，先看它能帮你凑成什么。",
    quiz: {
      question: "本局打 7，下面哪张是逢人配？",
      options: ["黑桃 7", "红桃 7", "红桃 A", "小王"],
      answer: 1,
      explain: "逢人配是红桃级牌。打 7 时就是红桃 7，其他花色的 7 只是普通级牌。",
    },
  },
  {
    id: "combos",
    level: "basic",
    title: "牌型大全",
    summary: "能出什么、谁大谁小",
    minutes: 8,
    hand: [
      { rank: "3", suit: "♠" },
      { rank: "4", suit: "♥" },
      { rank: "5", suit: "♣" },
      { rank: "6", suit: "♦" },
      { rank: "7", suit: "♠" },
    ],
    handCaption: "顺子：正好 5 张连着的点数，花色不限",
    sections: [
      { title: "单张、对子、三张", body: "一张、两张同点、三张同点。跟牌时张数和牌型都要一样，比点数大小。王也有对子：两张大王、两张小王都是一对，一大一小不算（第三条 2）。" },
      {
        title: "三带二",
        body: "三张同点再带一对，比如 888+55。只比三张的大小，带的对子不管。注意是带「一对」，不能带两张单牌。",
      },
      {
        title: "顺子",
        body: "正好 5 张点数相连的牌，花色不限，比如 34567。A 可以当最大（10JQKA），也可以当 1（A2345），但不能绕圈（JQKA2 不行）。顺子里级牌按原来的点数算，王不能进顺子。",
      },
      { title: "三连对", body: "三个相连的对子，比如 556677，俗称「木板」。同样 A 可以在两头：AA2233、QQKKAA。" },
      { title: "钢板", body: "两个相连的三张，比如 555666。A 的用法同上。" },
      { title: "炸弹", body: "四张或更多同点的牌、五张同花色的顺子（同花顺），以及四张王。炸弹能压任何非炸弹的牌型，炸弹之间怎么比放在「进阶 · 炸弹大小」。" },
    ],
    tip: "出牌前先把手里的牌按牌型理一遍：顺子、木板、钢板一次能走五六张，比一张张出快得多。",
    quiz: {
      question: "下面哪一手是合法的顺子？",
      options: ["J Q K A 2", "A 2 3 4 5", "3 4 5 6", "10 J Q K 小王"],
      answer: 1,
      explain: "A 可以当 1 接在 2 前面，A2345 是最小的顺子。顺子必须正好 5 张、不能绕圈、不能带王。",
    },
  },
  {
    id: "levelup",
    level: "basic",
    title: "进贡与升级",
    summary: "一局打完怎么算",
    minutes: 5,
    sections: [
      {
        title: "谁升级",
        body: "一局打完，只有拿到头游的那一队升级。升几级看搭档的名次：搭档二游（双上）升 3 级，搭档三游升 2 级，搭档末游升 1 级。",
      },
      {
        title: "打谁的级",
        body: "两队各记各的级数。下一局打的是刚刚升级那一队的级：比如你们从 5 升到 8，下一局就打 8，红桃 8 是逢人配。",
      },
      {
        title: "进贡一句话",
        body: "从第二副牌开始（第一局不进贡），发完牌上一局的末游要把手里最大的一张牌（红桃级牌除外）交给头游，头游再还一张不超过 10 的牌。细节在「进阶 · 进贡与还贡」。",
      },
    ],
    quiz: {
      question: "你拿了头游，搭档是三游，你们队升几级？",
      options: ["1 级", "2 级", "3 级", "不升"],
      answer: 1,
      explain: "头游 + 三游升 2 级；头游 + 二游（双上）升 3 级；头游 + 末游升 1 级。",
    },
  },
  {
    id: "bombs",
    level: "advanced",
    title: "炸弹大小",
    summary: "炸弹之间谁压谁",
    minutes: 5,
    hand: [
      { rank: "9", suit: "♠" },
      { rank: "10", suit: "♠" },
      { rank: "J", suit: "♠" },
      { rank: "Q", suit: "♠" },
      { rank: "K", suit: "♠" },
    ],
    handCaption: "同花顺：比五张炸大，比六张炸小",
    sections: [
      {
        title: "从小到大",
        body: "四张炸 < 五张炸 < 同花顺 < 六张炸 < 七张炸 < 八张炸 < 九张炸 < 十张炸 < 四王（天王炸）。张数多的炸弹一定比张数少的大，同花顺卡在五张和六张之间。",
      },
      { title: "张数一样怎么比", body: "同样张数的炸弹比点数，级牌最大。两个同花顺比最大那张的点数。" },
      {
        title: "最多几张",
        body: "两副牌每个点数有 8 张，再加上 2 张逢人配，最大能凑出十张炸。四张王（两大两小）是全场最大的牌，什么都压得住。",
      },
    ],
    tip: "别急着用炸弹抢出牌权。留着它在对手快出完的时候拦一手，往往更值钱。",
    quiz: {
      question: "同花顺和五张炸弹谁大？",
      options: ["五张炸弹大", "同花顺大", "一样大，后出的赢", "看点数"],
      answer: 1,
      explain: "同花顺排在五张炸之上、六张炸之下。",
    },
  },
  {
    id: "tribute",
    level: "advanced",
    title: "进贡与还贡",
    summary: "输了要交牌，赢了要还",
    minutes: 6,
    sections: [
      {
        title: "单贡",
        body: "从第二副牌开始，第一局不进贡。末游把手里最大的一张牌交给头游（红桃级牌不算，大王最大）；头游还给末游一张牌点不超过 10 的牌，如果手里的牌全都大于 10，就还最小的一张（第十条二）。",
      },
      {
        title: "双下要双贡",
        body: "如果一队拿了头游和二游（对面「双下」），对面两人都要进贡：牌点大的那张给头游，小的给二游，两人各自还一张。两张贡牌牌点相同时，按顺时针方向进贡（第十条三）。",
      },
      {
        title: "抗贡",
        body: "要进贡的一方在进贡前抓到两张大王（双下时两人合起来算），就抗贡，这一局不再贡牌（第十条四）—— 不是可选项，抓到了就抗。",
      },
      {
        title: "谁先出",
        body: "单贡时由末游先出；双贡时由贡给头游的那位先出；抗贡时由上一局的头游先出（第十条二、三、四）。",
      },
    ],
    quiz: {
      question: "你上局末游，手里有大王、红桃级牌和 A，要贡哪张？",
      options: ["红桃级牌", "大王", "A", "随便一张"],
      answer: 1,
      explain: "进贡交最大的一张，但红桃级牌（逢人配）不算在内。大王最大，所以贡大王。",
    },
  },
  {
    id: "jiefeng",
    level: "advanced",
    title: "接风",
    summary: "出完之后，搭档接着出",
    minutes: 3,
    sections: [
      {
        title: "什么是接风",
        body: "一个人出完最后一手牌后，如果其他三家都不要，出牌权就交给他的对家，由对家重新起一手。这叫「接风」，规则里叫「借风出牌」，只在上游或二游出完时发生（第九条）。",
      },
      {
        title: "对手压了怎么办",
        body: "只要有人压上，接风就不成立，出牌权按正常规则走：谁最后出牌、其他人都不要，谁就起下一手。",
      },
    ],
    tip: "快出完的时候，最后一手尽量留一个对手接不住的牌型，帮搭档拿到接风。",
    quiz: {
      question: "你出完最后一手，下家和上家都不要，搭档也不要，下一手谁出？",
      options: ["你的下家", "你的搭档", "你的上家", "重新洗牌"],
      answer: 1,
      explain: "你出完之后没人压上，出牌权交给你的对家（搭档），这就是接风。",
    },
  },
  {
    id: "wildcard",
    level: "expert",
    title: "逢人配怎么用",
    summary: "一张牌，十种用法",
    minutes: 5,
    hand: [
      { rank: "5", suit: "♠" },
      { rank: "5", suit: "♣" },
      { rank: "5", suit: "♦" },
      { rank: "7", suit: "♥", hot: true },
    ],
    handCaption: "打 7 时：三张 5 + 红桃 7 = 四张 5 的炸弹",
    sections: [
      {
        title: "能当什么",
        body: "逢人配（红桃级牌）能当除大小王以外的任何一张牌：补顺子、补对子、补炸弹、补同花顺都可以。单出时它就是一张级牌。",
      },
      { title: "不能当什么", body: "不能当大王或小王，所以凑不出四王炸。" },
      {
        title: "什么时候用",
        body: "最常见的用法是把三张凑成四张炸，或者给差一张的同花顺补位。两张逢人配在一个人手里时，可以考虑留到最后再决定怎么配。",
      },
    ],
    quiz: {
      question: "打 7 时，红桃 7 不能当哪张牌？",
      options: ["黑桃 A", "梅花 3", "大王", "方块 7"],
      answer: 2,
      explain: "逢人配能代替除大小王以外的任何牌。",
    },
  },
  {
    id: "teamwork",
    level: "expert",
    title: "记牌与配合",
    summary: "两个人打一副牌",
    minutes: 6,
    sections: [
      {
        title: "先记大牌",
        body: "两副牌有 4 张王、8 张级牌（其中 2 张逢人配）、8 张 A。出过几张、谁出的，记住这些就能大致判断谁手里还有炸弹。",
      },
      {
        title: "让搭档先走",
        body: "搭档牌好时，你的任务是帮他拿出牌权，而不是自己抢头游。头游 + 二游能升 3 级，比你一个人跑掉强得多。",
      },
      {
        title: "顶牌",
        body: "对手快出完时，上家要用大牌顶住，不让他小牌溜走；必要时用炸弹拦。",
      },
    ],
    quiz: {
      question: "两副牌里一共有几张逢人配？",
      options: ["1 张", "2 张", "4 张", "8 张"],
      answer: 1,
      explain: "每副牌有一张红桃级牌，两副牌一共 2 张。",
    },
  },
  {
    id: "pass-a",
    level: "expert",
    title: "打 A 与一局的胜负",
    summary: "过了 A 才算赢",
    minutes: 4,
    sections: [
      {
        title: "从 2 打到 A",
        body: "级数依次是 2、3……10、J、Q、K、A。升级越过 A 时停在 A，下一局打 A。",
      },
      {
        title: "怎么算过 A",
        body: "必须是打本队的 A 那一局：本队拿到头游，而且搭档不是末游，才算「过 A」，一过就赢下整场（第二条 13、第十二条）。打对方的级时赢了只是升级，不算过 A；只拿到头游 + 末游也不算。",
      },
      {
        title: "打 A 失败",
        body: "A 级必打：本队停在 A，打到过为止（第二条 13）。没过几次只记着；下一局打谁的级照常看上一局谁是上游，轮到打本队的级时还打 A（本队上游、搭档下游那种没过，下一局确实还打 A；对方上游时下一局打对方升到的级）。",
      },
    ],
    tip: "打 A 之前先看清是打哪一队的 A：打本队的 A 要拿上游、而且搭档不是末游才算过；打对方的级时赢了只是照常升级。",
    quiz: {
      question: "打 A 这一局，你头游、搭档末游，算过 A 吗？",
      options: ["算", "不算", "看对手", "再打一局决定"],
      answer: 1,
      explain: "按竞赛规则，过 A 要打本队的 A、并且搭档不能是末游（第二条 13）。",
    },
  },
];

export function lessonsOf(level: Level): Lesson[] {
  return LESSONS.filter((l) => l.level === level);
}

export function lessonById(id: string): Lesson | undefined {
  return LESSONS.find((l) => l.id === id);
}

/** 下一课（同一组读完了接下一组）；最后一课返回 undefined。 */
export function nextLesson(id: string): Lesson | undefined {
  const i = LESSONS.findIndex((l) => l.id === id);
  return i >= 0 ? LESSONS[i + 1] : undefined;
}

// ── 学习进度：只存在本机（不是凭证，也不需要同步） ───────────────────────────

const DONE_KEY = "gd_learn_done";

/**
 * 我们用到的那两个方法。H5 的 `localStorage` 和小游戏垫在 `window.localStorage` 上的
 * 那层 wx 存储都满足；这里不用 DOM 的 `Storage` 类型，共享层就不必依赖 DOM 的 lib。
 */
export interface LearnStore {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

export function doneLessons(storage: Pick<LearnStore, "getItem"> | null): Set<string> {
  try {
    const raw = storage?.getItem(DONE_KEY);
    const list = raw ? (JSON.parse(raw) as unknown) : [];
    return new Set(Array.isArray(list) ? list.filter((x): x is string => typeof x === "string") : []);
  } catch {
    return new Set();
  }
}

export function markDone(id: string, storage: LearnStore | null): void {
  const done = doneLessons(storage);
  done.add(id);
  try {
    storage?.setItem(DONE_KEY, JSON.stringify([...done]));
  } catch {
    // 存不下（无痕模式）就算了，进度只是个提示。
  }
}
