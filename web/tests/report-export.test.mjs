import ts from "typescript";

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";

const source = readFileSync(
  new URL("../src/app/(main)/function/tasks/detail/_tabs/report-tab.tsx", import.meta.url),
  "utf8",
);
const { outputText } = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX },
});

function render({ report = "# 漏洞报告 🔍\n\n```http\nGET /测试\n```\n", loading = false, fail, copyOK = true } = {}) {
  const states = [report, loading, false];
  const errors = [];
  const successes = [];
  const copied = [];
  const timers = [];
  const revoked = [];
  let blob;
  let attached = false;
  let clicked = false;
  let removed = false;
  const anchor = {
    click() {
      if (fail === "click") throw new Error("blocked");
      clicked = true;
    },
    remove() {
      removed = true;
    },
  };
  const jsx = (type, props) => ({ type, props });
  const modules = {
    react: { useState: () => [states.shift(), () => undefined], useEffect: () => undefined },
    "react/jsx-runtime": { jsx, jsxs: jsx, Fragment: "fragment" },
    "lucide-react": {},
    sonner: { toast: { error: (message) => errors.push(message), success: (message) => successes.push(message) } },
    "@/components/markdown": { Markdown: "markdown" },
    "@/components/ui/button": { Button: "button" },
    "@/components/ui/card": { Card: "card", CardContent: "content", CardHeader: "header", CardTitle: "title" },
    "@/lib/api": {},
    "@/lib/utils": {
      copyText: async (text) => {
        copied.push(text);
        return copyOK;
      },
    },
  };
  const exports = {};
  runInNewContext(outputText, {
    exports,
    require: (name) => {
      assert.ok(Object.hasOwn(modules, name), `unexpected import: ${name}`);
      return modules[name];
    },
    Blob,
    URL: {
      createObjectURL(value) {
        if (fail === "url") throw new Error("unavailable");
        blob = value;
        return "blob:report";
      },
      revokeObjectURL: (url) => revoked.push(url),
    },
    document: {
      createElement: (tag) => {
        assert.equal(tag, "a");
        return anchor;
      },
      body: {
        appendChild: (element) => {
          assert.equal(element, anchor);
          attached = true;
        },
      },
    },
    setTimeout: (callback, delay) => timers.push({ callback, delay }),
  });
  const tree = exports.ReportTab({ taskId: "task-123" });
  return {
    tree,
    report,
    anchor,
    errors,
    successes,
    copied,
    timers,
    revoked,
    get blob() {
      return blob;
    },
    get attached() {
      return attached;
    },
    get clicked() {
      return clicked;
    },
    get removed() {
      return removed;
    },
  };
}

function button(node, label) {
  if (!node || typeof node !== "object") return;
  if (node.type === "button" && JSON.stringify(node.props.children).includes(label)) return node;
  return [node.props?.children]
    .flat(Infinity)
    .map((child) => button(child, label))
    .find(Boolean);
}

test("exports the complete UTF-8 Markdown with a task-specific filename and releases resources", async () => {
  const view = render();
  button(view.tree, "导出 Markdown").props.onClick();
  assert.equal(await view.blob.text(), view.report);
  assert.equal(view.blob.type, "text/markdown;charset=utf-8");
  assert.equal(view.anchor.download, "report-task-123.md");
  assert.equal(view.anchor.href, "blob:report");
  assert.ok(view.attached && view.clicked && view.removed);
  assert.deepEqual(view.errors, []);
  assert.deepEqual(view.revoked, []);
  assert.equal(view.timers[0].delay, 1000);
  view.timers[0].callback();
  assert.deepEqual(view.revoked, ["blob:report"]);
});

for (const state of [{ loading: true }, { report: "" }, { loading: true, report: "" }]) {
  test(`hides report actions for ${JSON.stringify(state)}`, () => {
    const view = render(state);
    assert.equal(button(view.tree, "导出 Markdown"), undefined);
    assert.equal(button(view.tree, "复制"), undefined);
    assert.equal(view.blob, undefined);
  });
}

for (const fail of ["url", "click"]) {
  test(`reports ${fail} failure and cleans up allocated resources`, () => {
    const view = render({ fail });
    assert.doesNotThrow(() => button(view.tree, "导出 Markdown").props.onClick());
    assert.deepEqual(view.errors, ["导出失败，请重试或复制 Markdown"]);
    assert.ok(view.removed);
    for (const timer of view.timers) timer.callback();
    assert.deepEqual(view.revoked, fail === "click" ? ["blob:report"] : []);
  });
}

for (const copyOK of [true, false]) {
  test(`preserves copying Markdown when the clipboard result is ${copyOK}`, async () => {
    const view = render({ copyOK });
    await button(view.tree, "复制").props.onClick();
    assert.deepEqual(view.copied, [view.report]);
    assert.deepEqual(view.successes, copyOK ? ["已复制 Markdown"] : []);
    assert.deepEqual(view.errors, copyOK ? [] : ["复制失败，请手动选择文本复制"]);
  });
}
