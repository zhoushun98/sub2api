/**
 * otok: 渠道状态卡片「降智检测」（鹈鹕测试）接口。
 */
import { apiClient } from '@/api/client'

export type OtokPelicanRunStatus = 'queued' | 'running' | 'succeeded' | 'failed'

/** 一次模型请求，不含生成内容。 */
export interface OtokPelicanRunSummary {
  id: number
  model: string
  status: OtokPelicanRunStatus
  attempt: number
  duration_ms: number | null
  first_token_ms: number | null
  output_tokens: number | null
  reasoning_tokens: number | null
  has_html: boolean
  error_code: string
  error_message: string
  started_at: string | null
  finished_at: string | null
}

/** 一轮测试（弹窗左侧的一行）。 */
export interface OtokPelicanRound {
  id: number
  group_id: number
  trigger: 'schedule' | 'manual'
  started_at: string
  finished_at: string | null
  runs: OtokPelicanRunSummary[]
}

/** 带生成内容的单条记录。 */
export interface OtokPelicanRunDetail extends OtokPelicanRunSummary {
  round_id: number
  group_id: number
  reasoning_effort: string
  response_model: string
  input_tokens: number | null
  http_status: number | null
  output_text: string
  output_html: string
}

export interface OtokPelicanGroups {
  group_ids: number[]
  interval_minutes: number
  keep_rounds: number
  reasoning_effort: string
}

export interface OtokPelicanGroupConfig {
  group_id: number
  enabled: boolean
  models: string[]
}

export interface OtokPelicanConfig {
  enabled: boolean
  interval_minutes: number
  reasoning_effort: string
  prompt: string
  timeout_seconds: number
  concurrency: number
  keep_rounds: number
  default_models: string[]
  groups: OtokPelicanGroupConfig[]
}

export interface OtokPelicanGroupProbeStatus {
  group_id: number
  has_key: boolean
  last_started_at: string | null
  running: boolean
  last_error: string
}

export interface OtokPelicanProbeStatus {
  user_id: number
  user_email: string
  user_status: string
  balance: number
  base_url: string
  disabled: boolean
  error: string
  groups: OtokPelicanGroupProbeStatus[]
}

/** 弹窗用的数据源：用户端只能看自己可用的已启用分组，管理端可以看任意分组。 */
export interface OtokPelicanSource {
  listRounds(groupId: number): Promise<OtokPelicanRound[]>
  getRun(runId: number): Promise<OtokPelicanRunDetail>
}

export const otokPelicanAPI = {
  async getGroups() {
    return (await apiClient.get<OtokPelicanGroups>('/otok/pelican/groups')).data
  },
  async listRounds(groupId: number) {
    return (await apiClient.get<{ rounds: OtokPelicanRound[] }>(`/otok/pelican/groups/${groupId}/rounds`)).data.rounds
  },
  async getRun(runId: number) {
    return (await apiClient.get<OtokPelicanRunDetail>(`/otok/pelican/runs/${runId}`)).data
  },
} satisfies OtokPelicanSource & Record<string, unknown>

export const otokAdminPelicanAPI = {
  async getConfig() {
    return (await apiClient.get<OtokPelicanConfig>('/admin/otok/pelican/config')).data
  },
  async updateConfig(data: OtokPelicanConfig) {
    return (await apiClient.put<OtokPelicanConfig>('/admin/otok/pelican/config', data)).data
  },
  async getStatus() {
    return (await apiClient.get<OtokPelicanProbeStatus>('/admin/otok/pelican/status')).data
  },
  async runNow(groupId: number) {
    return (await apiClient.post<{ round_id: number }>('/admin/otok/pelican/run-now', { group_id: groupId })).data
  },
  async listRounds(groupId: number) {
    return (await apiClient.get<{ rounds: OtokPelicanRound[] }>(`/admin/otok/pelican/groups/${groupId}/rounds`)).data.rounds
  },
  async getRun(runId: number) {
    return (await apiClient.get<OtokPelicanRunDetail>(`/admin/otok/pelican/runs/${runId}`)).data
  },
} satisfies OtokPelicanSource & Record<string, unknown>
