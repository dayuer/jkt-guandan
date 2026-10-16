// Vite / vitest 的 `?raw` 导入：把文件原样当字符串读进来（rule-sync.test.ts 用它读 Go 源码）。
// 本包不装 @types/node，读别的语言的源文件就靠它。
declare module "*?raw" {
  const content: string;
  export default content;
}
