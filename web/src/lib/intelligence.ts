import { http } from "@/lib/api";

export type IntelligenceKind = "credential" | "lead" | "sensitive_data";

export interface TaskIntelligence {
  id: number;
  task_id: number;
  node_id?: number;
  kind: IntelligenceKind;
  title: string;
  content: string;
  evidence: string;
  confidence: "observed" | "inferred";
  status: string;
  asset_ids: number[];
  worker: string;
  created_at: string;
  updated_at: string;
  inherited: boolean;
  source_session?: string;
}

export interface IntelligencePage {
  items: TaskIntelligence[];
  total: number;
  counts: Record<IntelligenceKind, number>;
  enabled: boolean;
}

interface IntelligenceSettings {
  task_intelligence_enabled: boolean;
}

export const intelligenceApi = {
  list: (taskId: string, params: URLSearchParams, signal: AbortSignal) =>
    http<IntelligencePage>(`/tasks/${encodeURIComponent(taskId)}/intelligence?${params}`, {
      signal,
      cache: "no-store",
    }),
  updateStatus: (taskId: number, id: number, status: string) =>
    http<{ updated: boolean }>(`/tasks/${taskId}/intelligence/${id}`, {
      method: "PATCH",
      body: JSON.stringify({ status }),
    }),
  settings: () => http<IntelligenceSettings>("/settings"),
  setEnabled: (enabled: boolean) =>
    http<IntelligenceSettings>("/settings", {
      method: "PUT",
      body: JSON.stringify({ task_intelligence_enabled: enabled }),
    }),
};
