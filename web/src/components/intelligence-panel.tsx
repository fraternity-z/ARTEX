"use client";

import * as React from "react";

import Link from "next/link";

import { CopyIcon, RefreshCwIcon } from "lucide-react";
import { toast } from "sonner";

import { CredentialContent, CredentialTrafficViewer } from "@/components/credential-content";
import { ToneDot } from "@/components/status-badge";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  type IntelligenceKind,
  type IntelligencePage,
  type IntelligenceScope,
  intelligenceApi,
  type TaskIntelligence,
} from "@/lib/intelligence";
import { type Tone, toneClasses } from "@/lib/status";
import { copyText } from "@/lib/utils";

const PAGE_SIZE = 25;
const KINDS: { value: IntelligenceKind; label: string; tone: Tone }[] = [
  { value: "credential", label: "凭据", tone: "violet" },
  { value: "lead", label: "重要线索", tone: "blue" },
  { value: "sensitive_data", label: "敏感数据", tone: "amber" },
];
const STATUS: Record<IntelligenceKind, Record<string, string>> = {
  credential: { unverified: "未验证", verified: "已验证", invalid: "已失效" },
  lead: { pending: "待跟进", in_progress: "跟进中", dismissed: "已排除", converted: "已转漏洞" },
  sensitive_data: { suspected: "疑似", confirmed: "已确认", dismissed: "已排除" },
};

function time(value: string) {
  return new Date(value).toLocaleString("zh-CN");
}

function preview(value: string, length: number) {
  const characters = Array.from(value);
  return characters.length > length ? `${characters.slice(0, length).join("")}…` : value;
}

function conversationHref(item: TaskIntelligence) {
  const query = new URLSearchParams({ id: String(item.task_id), tab: "sessions" });
  if (item.source_session) query.set("session", item.source_session);
  return `/function/tasks/detail?${query}`;
}

function KindBadge({ kind }: { kind: IntelligenceKind }) {
  const meta = KINDS.find((item) => item.value === kind);
  return <Badge className={toneClasses[meta?.tone ?? "neutral"]}>{meta?.label ?? kind}</Badge>;
}

function AssetLinks({ ids, onNavigate }: { ids: number[]; onNavigate: () => void }) {
  return ids.length ? (
    <span className="inline-flex flex-wrap gap-x-2 gap-y-1">
      {ids.map((id) => (
        <Link
          key={id}
          href={`/function/assets?asset=${id}`}
          onClick={onNavigate}
          className="underline underline-offset-2"
          aria-label={`查看资产 #${id}`}
        >
          #{id}
        </Link>
      ))}
    </span>
  ) : (
    <>未关联</>
  );
}

type IntelligencePanelProps = { onNavigate?: () => void } & (
  | { taskId: string; scope?: never; scopeId?: never }
  | { taskId?: never; scope: IntelligenceScope; scopeId: string }
);

export function IntelligencePanel(props: IntelligencePanelProps) {
  const scope = props.scope ?? "tasks";
  const scopeId = props.scopeId ?? props.taskId;
  return (
    <IntelligencePanelContent
      key={`${scope}:${scopeId}`}
      scope={scope}
      scopeId={scopeId}
      onNavigate={props.onNavigate}
    />
  );
}

function IntelligencePanelContent({
  scope,
  scopeId,
  onNavigate,
}: {
  scope: IntelligenceScope;
  scopeId: string;
  onNavigate?: () => void;
}) {
  const [kind, setKind] = React.useState<IntelligenceKind | "all">("all");
  const [status, setStatus] = React.useState("all");
  const [search, setSearch] = React.useState("");
  const [query, setQuery] = React.useState("");
  const [page, setPage] = React.useState(0);
  const [data, setData] = React.useState<IntelligencePage | null>(null);
  const [error, setError] = React.useState("");
  const [refreshVersion, setRefreshVersion] = React.useState(0);
  const [selected, setSelected] = React.useState<TaskIntelligence | null>(null);
  const [saving, setSaving] = React.useState(false);
  const [retrying, setRetrying] = React.useState(false);
  const [trafficTarget, setTrafficTarget] = React.useState<{ item: TaskIntelligence; id: string } | null>(null);

  React.useEffect(() => {
    const timer = setTimeout(() => {
      setQuery(search.trim());
      setPage(0);
    }, 300);
    return () => clearTimeout(timer);
  }, [search]);

  React.useEffect(() => {
    let alive = true;
    let busy = false;
    const controller = new AbortController();
    const load = async () => {
      if (busy) return;
      busy = true;
      try {
        const params = new URLSearchParams({ limit: String(PAGE_SIZE), offset: String(page * PAGE_SIZE) });
        if (kind !== "all") params.set("kind", kind);
        if (status !== "all") params.set("status", status);
        if (query) params.set("q", query);
        const result = await intelligenceApi.list(scope, scopeId, params, controller.signal);
        if (!alive) return;
        const lastPage = Math.max(0, Math.ceil(result.total / PAGE_SIZE) - 1);
        if (page > lastPage) {
          setPage(lastPage);
          return;
        }
        setData(result);
        setSelected((current) => (current ? (result.items.find((item) => item.id === current.id) ?? current) : null));
        setError("");
      } catch (reason) {
        if (alive) setError((reason as Error).message);
      } finally {
        busy = false;
      }
    };
    void refreshVersion;
    void load();
    const timer = setInterval(() => {
      if (!document.hidden) void load();
    }, 8000);
    return () => {
      alive = false;
      controller.abort();
      clearInterval(timer);
    };
  }, [scope, scopeId, kind, status, query, page, refreshVersion]);

  const close = () => setSelected(null);
  const navigate = () => {
    close();
    onNavigate?.();
  };
  const update = async (value: string) => {
    if (scope !== "tasks" || !selected || selected.inherited || saving || value === selected.status) return;
    const target = selected;
    setSaving(true);
    try {
      await intelligenceApi.updateStatus(target.task_id, target.id, value);
      setSelected((item) => (item?.id === target.id ? { ...item, status: value } : item));
      setRefreshVersion((version) => version + 1);
      toast.success("情报状态已更新");
    } catch (reason) {
      toast.error(`更新失败：${(reason as Error).message}`);
    } finally {
      setSaving(false);
    }
  };
  const retry = async () => {
    if (scope !== "tasks" || !selected || selected.inherited || retrying) return;
    const target = selected;
    setRetrying(true);
    try {
      await intelligenceApi.retryCredentials(target.task_id, target.id);
      setRefreshVersion((version) => version + 1);
      toast.success("已加入凭据处理队列");
    } catch (reason) {
      toast.error(`重新处理失败：${(reason as Error).message}`);
    } finally {
      setRetrying(false);
    }
  };
  const statusOptions =
    kind === "all"
      ? Object.fromEntries(Object.values(STATUS).flatMap((options) => Object.entries(options)))
      : STATUS[kind];
  let emptyMessage = "开启自动记录后，新增情报会在此显示；历史会话不会自动补提取。";
  if (data?.enabled) emptyMessage = "Agent 在后续执行中发现有价值的信息后，会在此显示。";
  if (query || kind !== "all" || status !== "all") emptyMessage = "尝试调整分类或筛选条件。";

  return (
    <div className="flex min-w-0 flex-col gap-4">
      {data && !data.enabled && scope === "tasks" ? (
        <Alert>
          <AlertTitle>自动记录已关闭</AlertTitle>
          <AlertDescription>
            已有情报仍可查看。前往{" "}
            <Link href="/system/settings" className="underline">
              系统设置
            </Link>{" "}
            开启后，Agent 将在执行中记录新增情报。
          </AlertDescription>
        </Alert>
      ) : null}
      <Card className="min-w-0">
        <CardHeader>
          <CardTitle>{scope === "tasks" ? "任务情报" : "资产关联情报"}</CardTitle>
          <CardDescription>
            {scope === "tasks" ? "集中查看本次获取与直接关联任务继承的情报。" : "按来源任务查看此资产关联的情报。"}
            直接展示凭据、敏感数据及来源证据，发现凭据不代表验证成功。
          </CardDescription>
        </CardHeader>
        <CardContent className="flex min-w-0 flex-col gap-4">
          <Tabs
            value={kind}
            onValueChange={(value) => {
              setKind(value as IntelligenceKind | "all");
              setStatus("all");
              setPage(0);
            }}
          >
            <div className="overflow-x-auto">
              <TabsList className="w-max">
                <TabsTrigger value="all">
                  全部 {data ? Object.values(data.counts).reduce((sum, count) => sum + count, 0) : ""}
                </TabsTrigger>
                {KINDS.map((item) => (
                  <TabsTrigger key={item.value} value={item.value}>
                    <ToneDot tone={item.tone} />
                    {item.label} {data?.counts[item.value] ?? 0}
                  </TabsTrigger>
                ))}
              </TabsList>
            </div>
          </Tabs>
          <div className="flex flex-wrap items-center gap-2">
            <Input
              aria-label="搜索情报标题或上报者"
              placeholder="搜索标题或上报者"
              value={search}
              maxLength={200}
              onChange={(event) => setSearch(event.target.value)}
              className="min-w-40 flex-1"
            />
            <Select
              value={status}
              onValueChange={(value) => {
                setStatus(value);
                setPage(0);
              }}
            >
              <SelectTrigger aria-label="筛选情报状态">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectItem value="all">全部状态</SelectItem>
                  {Object.entries(statusOptions).map(([value, label]) => (
                    <SelectItem key={value} value={value}>
                      {label}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
            <Button variant="outline" onClick={() => setRefreshVersion((version) => version + 1)}>
              <RefreshCwIcon data-icon="inline-start" />
              刷新
            </Button>
          </div>
          {error ? (
            <Alert variant="destructive">
              <AlertTitle>读取情报失败</AlertTitle>
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          ) : null}
          {!data && !error ? <Skeleton className="h-40 w-full" /> : null}
          {data?.items.length ? (
            <Table className="min-w-[68rem] table-fixed">
              <colgroup>
                <col className="w-[20%]" />
                <col className="w-[35%]" />
                <col className="w-[8%]" />
                <col className="w-[12%]" />
                <col className="w-[6%]" />
                <col className="w-[12%]" />
                <col className="w-[7%]" />
              </colgroup>
              <TableHeader>
                <TableRow>
                  <TableHead>类别 / 标题</TableHead>
                  <TableHead>内容 / 来源证据</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>来源</TableHead>
                  <TableHead>资产</TableHead>
                  <TableHead>发现时间</TableHead>
                  <TableHead>操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.items.map((item) => (
                  <TableRow key={item.id}>
                    <TableCell className="whitespace-normal align-top">
                      <div className="flex min-w-0 flex-col items-start gap-1">
                        <KindBadge kind={item.kind} />
                        <span className="max-w-full [overflow-wrap:anywhere]">{item.title}</span>
                      </div>
                    </TableCell>
                    <TableCell className="whitespace-normal align-top">
                      <div className="flex min-w-0 flex-col gap-2">
                        <CredentialContent
                          item={item}
                          enabled={data.credential_agent_enabled}
                          onDetails={() => setSelected(item)}
                          onTraffic={(id) => setTrafficTarget({ item, id })}
                        />
                        <div className="flex flex-col gap-1 text-muted-foreground text-xs">
                          <span>来源证据</span>
                          <p className="whitespace-pre-wrap [overflow-wrap:anywhere]">{preview(item.evidence, 220)}</p>
                        </div>
                      </div>
                    </TableCell>
                    <TableCell className="whitespace-normal align-top [overflow-wrap:anywhere]">
                      <div className="flex flex-col items-start gap-1">
                        <Badge variant="outline">{STATUS[item.kind][item.status] ?? item.status}</Badge>
                        <span className="text-muted-foreground text-xs">
                          {item.confidence === "observed" ? "直接观察" : "推测"}
                        </span>
                      </div>
                    </TableCell>
                    <TableCell className="whitespace-normal align-top [overflow-wrap:anywhere]">
                      <div className="flex flex-col gap-1">
                        <Link href={conversationHref(item)} onClick={navigate} className="underline">
                          任务 #{item.task_id}
                        </Link>
                        <span className="text-muted-foreground text-xs">
                          {item.worker || "Agent"}
                          {item.node_id ? ` · 节点 #${item.node_id}` : ""}
                        </span>
                        {item.inherited ? <Badge variant="outline">继承 · 只读</Badge> : null}
                      </div>
                    </TableCell>
                    <TableCell className="whitespace-normal align-top [overflow-wrap:anywhere]">
                      <AssetLinks ids={item.asset_ids} onNavigate={navigate} />
                    </TableCell>
                    <TableCell className="whitespace-normal align-top text-xs">{time(item.created_at)}</TableCell>
                    <TableCell className="whitespace-normal align-top">
                      <Button size="sm" variant="outline" onClick={() => setSelected(item)}>
                        详情
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          ) : null}
          {data && data.items.length === 0 ? (
            <Empty>
              <EmptyHeader>
                <EmptyTitle>暂无符合条件的情报</EmptyTitle>
                <EmptyDescription>{emptyMessage}</EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : null}
        </CardContent>
        <CardFooter className="flex flex-wrap items-center justify-between gap-2">
          <span className="text-muted-foreground text-sm">
            共 {data?.total ?? 0} 条 · 每页 {PAGE_SIZE} 条
          </span>
          <div className="flex items-center gap-2">
            <Button size="sm" variant="outline" disabled={page === 0} onClick={() => setPage((value) => value - 1)}>
              上一页
            </Button>
            <span className="text-muted-foreground text-xs">
              {page + 1} / {Math.max(1, Math.ceil((data?.total ?? 0) / PAGE_SIZE))}
            </span>
            <Button
              size="sm"
              variant="outline"
              disabled={!data || (page + 1) * PAGE_SIZE >= data.total}
              onClick={() => setPage((value) => value + 1)}
            >
              下一页
            </Button>
          </div>
        </CardFooter>
      </Card>
      <Dialog
        open={!!selected}
        onOpenChange={(open) => {
          if (!open) close();
        }}
      >
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle className="pr-6 [overflow-wrap:anywhere]">{selected?.title ?? "情报详情"}</DialogTitle>
            <DialogDescription>完整展示记录内容与实际来源证据。</DialogDescription>
          </DialogHeader>
          {selected ? (
            <div className="flex min-w-0 flex-col gap-4">
              <div className="flex flex-wrap items-center gap-2">
                <KindBadge kind={selected.kind} />
                <Badge variant="outline">{selected.confidence === "observed" ? "直接观察" : "推测"}</Badge>
                <span className="text-muted-foreground text-xs">
                  任务 #{selected.task_id}
                  {selected.node_id ? ` · 来源节点 #${selected.node_id}` : ""} · {selected.worker || "Agent"}
                </span>
              </div>
              {scope === "tasks" && !selected.inherited ? (
                <Select value={selected.status} disabled={saving} onValueChange={(value) => void update(value)}>
                  <SelectTrigger aria-label="更新情报状态">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      {Object.entries(STATUS[selected.kind]).map(([value, label]) => (
                        <SelectItem key={value} value={value}>
                          {label}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              ) : (
                <p className="text-muted-foreground text-sm">
                  {STATUS[selected.kind][selected.status]} · {selected.inherited ? "继承记录只读，" : ""}
                  请到来源任务修改状态
                </p>
              )}
              <div className="flex items-center justify-between gap-2">
                <span className="text-sm">内容</span>
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => {
                    void copyText(selected.content).then((copied) => {
                      if (copied) toast.success("已复制");
                      else toast.error("复制失败，请手动选择内容复制");
                    });
                  }}
                >
                  <CopyIcon data-icon="inline-start" />
                  复制
                </Button>
              </div>
              <CredentialContent
                key={selected.id}
                item={selected}
                detail
                enabled={data?.credential_agent_enabled}
                onTraffic={(id) => setTrafficTarget({ item: selected, id })}
              />
              {scope === "tasks" &&
              selected.kind === "credential" &&
              !selected.inherited &&
              data?.credential_agent_enabled ? (
                <Button
                  variant="outline"
                  size="sm"
                  disabled={
                    retrying || selected.credentials?.state === "queued" || selected.credentials?.state === "processing"
                  }
                  onClick={() => void retry()}
                >
                  重新处理凭据
                </Button>
              ) : null}
              <p className="text-sm">来源证据</p>
              <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-muted p-3 text-xs">
                {selected.evidence}
              </pre>
              <div className="flex flex-wrap gap-2 text-muted-foreground text-xs">
                <span>
                  关联资产：
                  <AssetLinks ids={selected.asset_ids} onNavigate={navigate} />
                </span>
                <span>发现于 {time(selected.created_at)}</span>
                <span>更新于 {time(selected.updated_at)}</span>
              </div>
              <Link href={conversationHref(selected)} onClick={navigate} className="text-sm underline">
                {selected.source_session ? "查看来源任务会话" : "查看来源任务"}
              </Link>
            </div>
          ) : null}
        </DialogContent>
      </Dialog>
      <CredentialTrafficViewer
        scope={scope}
        scopeId={scopeId}
        item={trafficTarget?.item ?? null}
        trafficId={trafficTarget?.id ?? null}
        onClose={() => setTrafficTarget(null)}
      />
    </div>
  );
}
