import assert from 'node:assert/strict';
import test from 'node:test';
import { tokenize } from './search.mjs';

test('中文段落可按词搜索', () => {
  const words = tokenize('任务需要备份数据库，然后恢复数据。');
  for (const word of tokenize('任务 备份 数据库 恢复')) assert.ok(words.includes(word), word);
});

test('保留英文配置名称和数字，忽略空白与标点', () => {
  assert.deepEqual(tokenize('MCP ARTEX_PG_DSN 8787'), ['MCP', 'ARTEX_PG_DSN', '8787']);
  assert.deepEqual(tokenize('  ，。！？\n'), []);
  assert.deepEqual(tokenize(''), []);
});
