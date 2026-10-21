"use client";

import * as React from "react";

import Link from "next/link";

import { NotebookTabsIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { type IntelligenceSettings, intelligenceApi } from "@/lib/intelligence";

export function IntelligenceCard() {
  const [enabled, setEnabled] = React.useState(false);
  const [credentialAgent, setCredentialAgent] = React.useState(false);
  const [credentialBinding, setCredentialBinding] = React.useState(false);
  const [loaded, setLoaded] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [error, setError] = React.useState("");

  const apply = React.useCallback((settings: IntelligenceSettings) => {
    setEnabled(!!settings.task_intelligence_enabled);
    setCredentialAgent(!!settings.credential_agent_enabled);
    setCredentialBinding(!!settings.credential_traffic_binding);
  }, []);

  const load = React.useCallback(async () => {
    try {
      const settings = await intelligenceApi.settings();
      apply(settings);
      setLoaded(true);
      setError("");
    } catch (reason) {
      setError(`读取失败：${(reason as Error).message}`);
    }
  }, [apply]);

  React.useEffect(() => {
    void load();
  }, [load]);

  const save = async (patch: Partial<IntelligenceSettings>) => {
    setSaving(true);
    setError("");
    try {
      const settings = await intelligenceApi.setSettings(patch);
      apply(settings);
      toast.success(
        patch.task_intelligence_enabled === undefined
          ? "凭据处理设置已保存"
          : settings.task_intelligence_enabled
            ? "已开启任务情报自动记录"
            : "已关闭任务情报自动记录",
      );
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
          自动记录任务中获取的凭据、重要线索和敏感数据，并保留实际来源证据。三个开关默认关闭。
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <div className="flex items-start justify-between gap-4">
          <div className="min-w-0 space-y-1">
            <Label htmlFor="task-intelligence-enabled" className="text-sm">
              Agent 自动记录{enabled ? " · 已开启" : " · 已关闭"}
            </Label>
            <p id="intelligence-record-description" className="text-xs text-muted-foreground">
              任务执行时记录新情报，会增加任务 Agent 的提示与上报 Token。仅负责记录，不启动凭据整理。 关闭后同时停用下方
              Agent 与流量绑定，已有情报和整理结果仍可查看；历史会话不会自动补提取。
            </p>
          </div>
          <Switch
            id="task-intelligence-enabled"
            aria-describedby="intelligence-record-description"
            checked={enabled}
            disabled={!loaded || saving}
            onCheckedChange={(value) => void save({ task_intelligence_enabled: value })}
          />
        </div>
        <div className="flex items-start justify-between gap-4 border-t pt-3">
          <div className="min-w-0 space-y-1">
            <Label htmlFor="credential-agent-enabled" className="text-sm">
              凭据 Agent
            </Label>
            <p id="credential-agent-description" className="text-xs text-muted-foreground">
              独立整理已有和新增凭据，拆分并高亮账号、密码、Token 和 Cookie，不改变原始上报或验证状态。
              复杂文本会使用额外模型 Token；可解析的 JSON 无需模型。需先开启自动记录。 模型和提示词可在{" "}
              <Link href="/system/agents/detail?key=credential_processor" className="underline">
                Agent → 凭据整理
              </Link>{" "}
              中配置。
            </p>
          </div>
          <Switch
            id="credential-agent-enabled"
            aria-describedby="credential-agent-description"
            checked={credentialAgent}
            disabled={!loaded || saving || !enabled}
            onCheckedChange={(value) => void save({ credential_agent_enabled: value })}
          />
        </div>
        <div className="flex items-start justify-between gap-4 border-t pt-3">
          <div className="min-w-0 space-y-1">
            <Label htmlFor="credential-traffic-binding" className="text-sm">
              绑定流量
            </Label>
            <p id="credential-traffic-description" className="text-xs text-muted-foreground">
              需先开启凭据 Agent。核对已抓取流量中的资产与凭据，关联真实请求或响应并保存匹配片段；
              不主动登录，也不把匹配流量视为验证成功。关闭 Agent 时同时停用，已有绑定保留。
            </p>
          </div>
          <Switch
            id="credential-traffic-binding"
            aria-describedby="credential-traffic-description"
            checked={credentialBinding}
            disabled={!loaded || saving || !enabled || !credentialAgent}
            onCheckedChange={(value) => void save({ credential_traffic_binding: value })}
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
