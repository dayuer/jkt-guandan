# #59 分享面板对齐 C1 的验证截图

本地 `guandan seed` 的 SEED01，内置浏览器把视口设成 375 宽，再点「⇪ 分享」。海报接口在 #54 里，还没合并，所以截图里是加载失败的占位图，尺寸和真图一样。

| 文件 | 说明 |
|---|---|
| `375x667-share.jpg` | iPhone SE 高度：整个面板放得下，不用滚 |
| `375x560-share-top.jpg` | 更矮的屏：面板顶到 24px 余量，内部可以滚（#61 的 max-height / overflow 保留） |
| `375x560-share-bottom.jpg` | 同上，滚到底：「取消」整行可见、可点 |

量测：375×560 下 `.sheet.share` 的 clientHeight 536、scrollHeight 591、`overflow-y: auto`；滚到底后「取消」的底边在 527.5，在视口内。
