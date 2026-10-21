import { http } from "@/lib/api";

export type IntelligenceKind = "credential" | "lead" | "sensitive_data";
export type IntelligenceScope = "tasks" | "assets";

export interface CredentialAccount {
  label?: string;
  username?: string;
  password?: string;
  token?: string;
  cookie?: string;
  endpoint?: string;
  protocol?: string;
}

export interface CredentialTraffic {
  id: string;
  url: string;
  method: string;
  status: number;
  side: string;
  excerpt: string;
  accounts: number[];
}

export interface CredentialAnalysis {
  state: "queued" | "processing" | "ready" | "error";
  method?: "rules" | "agent";
  accounts: CredentialAccount[] | null;
  traffic: CredentialTraffic[] | null;
  traffic_state: string;
  traffic_note?: string;
  note?: string;
  error?: string;
  model?: string;
  input_tokens: number;
  output_tokens: number;
  processed_at?: string;
}

export interface CredentialTrafficDetail {
  reference: CredentialTraffic;
  req?: string;
  resp?: string;
  unavailable: boolean;
}

export interface TaskIntelligence {
  credentials?: CredentialAnalysis;
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
  credential_agent_enabled: boolean;
  credential_traffic_binding: boolean;
  items: TaskIntelligence[];
  total: number;
  counts: Record<IntelligenceKind, number>;
  enabled: boolean;
}

export interface IntelligenceSettings {
  task_intelligence_enabled: boolean;
  credential_agent_enabled: boolean;
  credential_traffic_binding: boolean;
}

export const intelligenceApi = {
  list: (scope: IntelligenceScope, scopeId: string, params: URLSearchParams, signal: AbortSignal) =>
    http<IntelligencePage>(`/${scope}/${encodeURIComponent(scopeId)}/intelligence?${params}`, {
      signal,
      cache: "no-store",
    }),
  updateStatus: (taskId: number, id: number, status: string) =>
    http<{ updated: boolean }>(`/tasks/${taskId}/intelligence/${id}`, {
      method: "PATCH",
      body: JSON.stringify({ status }),
    }),
  detail: (scope: IntelligenceScope, scopeId: string, id: number, signal?: AbortSignal) =>
    http<TaskIntelligence>(`/${scope}/${encodeURIComponent(scopeId)}/intelligence/${id}`, {
      signal,
      cache: "no-store",
    }),
  retryCredentials: (taskId: number, id: number) =>
    http<{ queued: boolean }>(`/tasks/${taskId}/intelligence/${id}/credentials/retry`, { method: "POST" }),
  credentialTraffic: (scope: IntelligenceScope, scopeId: string, id: number, trafficId: string) =>
    http<CredentialTrafficDetail>(
      `/${scope}/${encodeURIComponent(scopeId)}/intelligence/${id}/traffic/${encodeURIComponent(trafficId)}`,
      { cache: "no-store" },
    ),
  settings: () => http<IntelligenceSettings>("/settings"),
  setSettings: (patch: Partial<IntelligenceSettings>) =>
    http<IntelligenceSettings>("/settings", {
      method: "PUT",
      body: JSON.stringify(patch),
    }),
};
