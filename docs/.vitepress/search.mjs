// 中文按词索引；不依赖在线搜索服务，也不上传读者的搜索内容。
export function tokenize(text) {
  return Array.from(
    new Intl.Segmenter('zh-CN', { granularity: 'word' }).segment(text),
  ).filter(part => part.isWordLike).map(part => part.segment);
}
