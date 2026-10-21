"use client";

import * as React from "react";

import { NotebookTabsIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { intelligenceApi } from "@/lib/intelligence";

export function IntelligenceCard() {
  const [enabled, setEnabled] = React.useState(false);
  const [loaded, setLoaded] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [error, setError] = React.useState("");

  const load = React.useCallback(async () => {
    try {
      const settings = await intelligenceApi.settings();
      setEnabled(!!settings.task_intelligence_enabled);
      setLoaded(true);
      setError("");
    } catch (reason) {
      setError(`读取失败：${(reason as Error).message}`);
    }
  }, []);

  React.useEffect(() => {
    void load();
  }, [load]);

  const toggle = async (value: boolean) => {
    setSaving(true);
    setError("");
    try {
      const settings = await intelligenceApi.setEnabled(value);
      setEnabled(!!settings.task_intelligence_enabled);
      toast.success(settings.task_intelligence_enabled ? "已开启任务情报自动记录" : "已关闭任务情报自动记录");
    } catch (reason) {
      setError(`保存失败：${(reason as Error).message}`);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Card className="mb-4 break-inside-avoid md:mb-6">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <NotebookTabsIcon className="size-4" />
          任务情报
        </CardTitle>
        <CardDescription id="task-intelligence-description">
          默认关闭。开启后，Agent 在执行任务时记录新获取的凭据、重要线索和敏感数据，并保留实际来源证据。
          额外的工具调用会增加 Token 消耗。关闭只停止新增记录，已有情报仍可查看；历史会话不会自动补提取。
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <div className="flex items-center justify-between gap-4">
          <Label htmlFor="task-intelligence-enabled" className="font-normal text-muted-foreground text-sm">
            Agent 自动记录{enabled ? " · 已开启" : " · 已关闭"}
          </Label>
          <Switch
            id="task-intelligence-enabled"
            aria-describedby="task-intelligence-description"
            checked={enabled}
            disabled={!loaded || saving}
            onCheckedChange={(value) => void toggle(value)}
          />
        </div>
        {error ? (
          <p role="alert" className="text-destructive text-sm">
            {error}
          </p>
        ) : null}
        {!loaded && error ? (
          <Button variant="outline" size="sm" onClick={() => void load()}>
            重新加载
          </Button>
        ) : null}
      </CardContent>
    </Card>
  );
}
