/**
 * otok: 「降智检测」接口（自定义条目）。
 */
import { apiClient } from '@/api/client'

export type OtokQualityRunStatus = 'queued' | 'running' | 'succeeded' | 'failed'
export type OtokQualityProtocol = 'responses' | 'chat' | 'messages'

/** 一次模型请求，不含输出内容。error_message 等字段只有管理端接口返回。 */
export interface OtokQualityRunSummary {
  id: number
  model: string
  status: OtokQualityRunStatus
  attempt: number
  duration_ms: number | null
  first_token_ms: number | null
  output_tokens: number | null
  reasoning_tokens: number | null
  has_html: boolean
  error_code: string
  started_at: string | null
  finished_at: string | null
  error_message?: string
  http_status?: number | null
  response_model?: string
  protocol?: OtokQualityProtocol | string
}

/** 一轮测试；prompt 是这一轮发出时的提示词快照，只有管理端接口返回。 */
export interface OtokQualityRound {
  id: number
  item_id: number
  trigger: 'schedule' | 'manual'
  prompt?: string
  started_at: string
  finished_at: string | null
  runs: OtokQualityRunSummary[]
}

/** 带输出内容的单条记录。 */
export interface OtokQualityRunDetail extends OtokQualityRunSummary {
  round_id: number
  item_id: number
  input_tokens: number | null
  output_text: string
  output_html: string
  /** 有 HTML 时才有：独立预览地址（带 2 小时有效的签名）。 */
  preview_url?: string
}

export interface OtokQualityPublicItem {
  id: number
  name: string
  models: string[]
}

export interface OtokQualityItems {
  items: OtokQualityPublicItem[]
  interval_minutes: number
  keep_rounds: number
}

/** 管理端读到的条目：不含 key 明文和密文，只给打码值。 */
export interface OtokQualityAdminItem {
  id: number
  name: string
  enabled: boolean
  url: string
  protocol: OtokQualityProtocol | ''
  has_api_key: boolean
  api_key_masked: string
  api_key_decrypt_failed: boolean
  models: string[]
  reasoning_effort: string
  prompt: string
  extra_body: string
}

export interface OtokQualityConfig {
  version: number
  invalid: boolean
  enabled: boolean
  interval_minutes: number
  timeout_seconds: number
  concurrency: number
  keep_rounds: number
  items: OtokQualityAdminItem[]
}

/** 保存时提交的条目：id 为 0 表示新增；api_key 留空表示沿用已存的 key。 */
export interface OtokQualityItemInput {
  id: number
  name: string
  enabled: boolean
  url: string
  api_key: string
  models: string[]
  reasoning_effort: string
  prompt: string
  extra_body: string
}

export interface OtokQualityConfigUpdate {
  version: number
  enabled: boolean
  interval_minutes: number
  timeout_seconds: number
  concurrency: number
  keep_rounds: number
  items: OtokQualityItemInput[]
}

export interface OtokQualityItemStatus {
  item_id: number
  last_started_at: string | null
  running: boolean
}

export interface OtokQualityStatus {
  disabled: boolean
  items: OtokQualityItemStatus[]
}

/** 结果面板的数据源：用户端只能看启用的条目，管理端可以看任意条目。 */
export interface OtokQualitySource {
  listRounds(itemId: number): Promise<OtokQualityRound[]>
  getRun(runId: number): Promise<OtokQualityRunDetail>
}

export const otokQualityAPI = {
  async getItems() {
    return (await apiClient.get<OtokQualityItems>('/otok/quality/items')).data
  },
  async listRounds(itemId: number) {
    return (await apiClient.get<{ rounds: OtokQualityRound[] }>(`/otok/quality/items/${itemId}/rounds`)).data.rounds
  },
  async getRun(runId: number) {
    return (await apiClient.get<OtokQualityRunDetail>(`/otok/quality/runs/${runId}`)).data
  },
} satisfies OtokQualitySource & Record<string, unknown>

export const otokAdminQualityAPI = {
  async getConfig() {
    return (await apiClient.get<OtokQualityConfig>('/admin/otok/quality/config')).data
  },
  async updateConfig(data: OtokQualityConfigUpdate) {
    return (await apiClient.put<OtokQualityConfig>('/admin/otok/quality/config', data)).data
  },
  async getStatus() {
    return (await apiClient.get<OtokQualityStatus>('/admin/otok/quality/status')).data
  },
  async runNow(itemId: number) {
    return (await apiClient.post<{ round_id: number }>('/admin/otok/quality/run-now', { item_id: itemId })).data
  },
  async listRounds(itemId: number) {
    return (await apiClient.get<{ rounds: OtokQualityRound[] }>(`/admin/otok/quality/items/${itemId}/rounds`)).data.rounds
  },
  async getRun(runId: number) {
    return (await apiClient.get<OtokQualityRunDetail>(`/admin/otok/quality/runs/${runId}`)).data
  },
} satisfies OtokQualitySource & Record<string, unknown>
