// 场景之间怎么跳（main.ts 实现）。单独一个文件，免得场景互相 import 成环。

export interface Nav {
  detail(code: string): void;
  create(): void;
  /** 开局成功：换成新局的详情，返回键回到 Tab 页 */
  created(code: string): void;
  tab(name: TabName): void;
  lesson(id: string): void;
  /** 牌桌计分（#165） */
  score(code: string): void;
  /** 「我的」→ 设置（#211） */
  settings(): void;
  devices(): void;
  privacy(): void;
  back(): void;
}

export type TabName = "games" | "learn" | "friends" | "me";
