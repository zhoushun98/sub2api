/**
 * otok: 「降智检测」的纯逻辑和侧边栏菜单显隐缓存。
 */
import { ref } from 'vue'
import {
  otokQualityAPI,
  type OtokQualityItems,
  type OtokQualityProtocol,
  type OtokQualityRound,
  type OtokQualityRunSummary,
} from '@/api/otok/quality'

export type OtokQualityRoundState = 'running' | 'succeeded' | 'partial' | 'failed'

/** 预览按这个尺寸渲染，再等比缩放到容器宽度；示例提示词也要求页面适配 1200×800。 */
export const OTOK_QUALITY_PREVIEW_WIDTH = 1200
export const OTOK_QUALITY_PREVIEW_HEIGHT = 800

/** 有进行中的轮次时，结果面板按这个间隔轮询。 */
export const OTOK_QUALITY_POLL_MS = 15_000

// 以下上限与后端 service/otok_quality_config.go 保持一致。
export const OTOK_QUALITY_MAX_ITEMS = 50
export const OTOK_QUALITY_MAX_MODELS = 5
export const OTOK_QUALITY_MAX_NAME = 64
export const OTOK_QUALITY_MAX_PROMPT = 8000
export const OTOK_QUALITY_MAX_EXTRA_BODY_BYTES = 4000
export const OTOK_QUALITY_MAX_API_KEY = 512
export const OTOK_QUALITY_MODEL_RE = /^[A-Za-z0-9._:/-]{1,128}$/
export const OTOK_QUALITY_EFFORTS = ['', 'none', 'minimal', 'low', 'medium', 'high', 'xhigh'] as const

export const OTOK_QUALITY_SAMPLE_PROMPT = `请创作一个「鹈鹕骑自行车」的单文件 HTML 页面：用内联 SVG 画一只鹈鹕骑在自行车上，画面完整、比例协调、细节丰富，可以加背景和动画。
要求：
1. 只能使用内联 SVG、CSS 和 JavaScript，不要使用外部图片、外部字体、CDN 脚本或任何外部资源（预览环境不能联网）。
2. 页面在 1200×800 左右的窗口里完整显示，不出现滚动条。
3. 只输出完整的 HTML 代码（从 <!DOCTYPE html> 开始），不要任何解释。`

export function isOtokQualityRunPending(run: Pick<OtokQualityRunSummary, 'status'>): boolean {
  return run.status === 'queued' || run.status === 'running'
}

/** 一轮的整体状态：有进行中 → running；全成功 → succeeded；全失败 → failed；其余 → partial。 */
export function otokQualityRoundState(round: Pick<OtokQualityRound, 'runs' | 'finished_at'>): OtokQualityRoundState {
  const runs = round.runs ?? []
  if (runs.some(isOtokQualityRunPending) || (runs.length === 0 && !round.finished_at)) return 'running'
  const ok = runs.filter((r) => r.status === 'succeeded').length
  if (ok === runs.length && ok > 0) return 'succeeded'
  if (ok === 0) return 'failed'
  return 'partial'
}

export function hasOtokQualityPending(rounds: OtokQualityRound[]): boolean {
  return rounds.some((r) => otokQualityRoundState(r) === 'running')
}

/** 毫秒 → "248.3s"；空值返回 "—"。 */
export function formatOtokQualityDuration(ms: number | null | undefined): string {
  if (ms == null || !Number.isFinite(ms) || ms < 0) return '—'
  return `${(ms / 1000).toFixed(1)}s`
}

export function formatOtokQualityTime(value: string | null | undefined): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString(undefined, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  })
}

/** 选中轮次时默认打开的模型：优先第一个有 HTML 的。 */
export function defaultOtokQualityRun(round: OtokQualityRound | null | undefined): OtokQualityRunSummary | null {
  if (!round || round.runs.length === 0) return null
  return round.runs.find((r) => r.has_html) ?? round.runs[0]
}

/** "a, b，c\nd" → ["a","b","c","d"]，去重去空。 */
export function parseOtokQualityModels(raw: string): string[] {
  const out: string[] = []
  for (const part of raw.split(/[\s,，]+/)) {
    const m = part.trim()
    if (m && !out.includes(m)) out.push(m)
  }
  return out
}

/** 按完整接口地址的路径结尾识别协议；地址本身不合规时返回 null。规则同后端保存校验。 */
export function detectOtokQualityProtocol(raw: string): OtokQualityProtocol | null {
  const value = (raw ?? '').trim()
  if (!value) return null
  let url: URL
  try {
    url = new URL(value)
  } catch {
    return null
  }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') return null
  if (!url.hostname || url.username || url.password || value.includes('#')) return null
  const path = url.pathname.replace(/\/+$/, '')
  if (path.endsWith('/chat/completions')) return 'chat'
  if (path.endsWith('/responses')) return 'responses'
  if (path.endsWith('/messages')) return 'messages'
  return null
}

/** 空串表示不附加；否则必须是 JSON 对象。 */
export function isValidOtokQualityExtraBody(raw: string): boolean {
  const value = (raw ?? '').trim()
  if (!value) return true
  try {
    const parsed: unknown = JSON.parse(value)
    return typeof parsed === 'object' && parsed !== null && !Array.isArray(parsed)
  } catch {
    return false
  }
}

export interface OtokQualityEstimateInput {
  enabled: boolean
  interval_minutes: number
  items: { enabled: boolean; models: string[] }[]
}

/** 每天大约要发多少次请求：启用条目的模型数之和 × 每天轮数。总开关关闭时为 0。 */
export function estimateOtokQualityDailyRequests(cfg: OtokQualityEstimateInput): number {
  if (!cfg.enabled || !cfg.interval_minutes || cfg.interval_minutes <= 0) return 0
  const roundsPerDay = Math.ceil(1440 / cfg.interval_minutes)
  return cfg.items.filter((i) => i.enabled).reduce((sum, i) => sum + i.models.length * roundsPerDay, 0)
}

const KNOWN_ERROR_CODES = new Set([
  'timeout',
  'interrupted',
  'shutdown',
  'stream_interrupted',
  'response_failed',
  'response_incomplete',
  'stream_error',
  'empty_output',
  'network_error',
  'connection_refused',
  'key_error',
  'unexpected_response',
  'http_429',
  'http_3xx',
  'http_4xx',
  'http_5xx',
])

/** 错误类型 → otok.quality.errorCodes 下的 key；带精确状态码的旧记录按段归并。 */
export function otokQualityErrorCodeKey(code: string): string {
  if (KNOWN_ERROR_CODES.has(code)) return code
  if (/^http_5\d\d$/.test(code)) return 'http_5xx'
  if (/^http_4\d\d$/.test(code)) return 'http_4xx'
  if (/^http_3\d\d$/.test(code)) return 'http_3xx'
  return 'unknown'
}

// ---- 侧边栏菜单显隐 ----
// 每次切路由 AppLayout 都会重新挂载侧边栏，所以缓存放在模块级。

/** 用户可见的条目；null 表示还没加载成功过。 */
export const otokQualityItems = ref<OtokQualityItems | null>(null)

let itemsPromise: Promise<OtokQualityItems | null> | null = null
let itemsSeq = 0

function fetchOtokQualityItems(): Promise<OtokQualityItems | null> {
  const seq = ++itemsSeq
  const p: Promise<OtokQualityItems | null> = otokQualityAPI
    .getItems()
    .then((data) => {
      if (seq === itemsSeq) otokQualityItems.value = data
      return data
    })
    .catch(() => {
      if (itemsPromise === p) itemsPromise = null
      return null
    })
  itemsPromise = p
  return p
}

/** 已加载过（含空列表）就不再请求；并发调用共用一个请求；失败不写缓存，下次调用重试。 */
export function ensureOtokQualityItems(): Promise<OtokQualityItems | null> {
  if (otokQualityItems.value) return Promise.resolve(otokQualityItems.value)
  return itemsPromise ?? fetchOtokQualityItems()
}

/** 侧边栏 featureFlag：取值时自己触发加载，没加载完返回 false，避免菜单先出现再消失。 */
export const otokQualityMenuFlag = (): boolean => {
  void ensureOtokQualityItems()
  return (otokQualityItems.value?.items.length ?? 0) > 0
}

/** 用户页拉到最新条目后写回缓存。 */
export function otokQualityApplyItems(data: OtokQualityItems) {
  itemsSeq++
  otokQualityItems.value = data
}

/** 后台保存后强制重新拉取；拉到之前保留旧值，菜单不闪。 */
export function refreshOtokQualityItems(): Promise<OtokQualityItems | null> {
  return fetchOtokQualityItems()
}

/** 仅供测试重置模块状态。 */
export function __resetOtokQualityItems() {
  otokQualityItems.value = null
  itemsPromise = null
  itemsSeq = 0
}
