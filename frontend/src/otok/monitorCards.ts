/**
 * otok: 渠道状态卡片页的数据整形（纯函数）。
 *
 * 数据全部来自上游 V2 被动统计接口：matrix(platform_group) 一行一张卡，
 * dimensions 补齐窗口内无流量的分组，/groups/available + /groups/rates 提供用户倍率。
 * 状态不用后端 health.overall（要求 minimum_sample，小流量分组会长期灰色），
 * 而是按可用率与首 Token 各自的阈值判定，取较差者。
 */
import type {
  MonitorCoverage,
  MonitorDimensions,
  MonitorMatrixBucket,
  MonitorMatrixRow,
  MonitorMetric,
  MonitorRange,
} from '@/api/channelMonitorV2'

export type OtokCardState = 'healthy' | 'warning' | 'critical' | 'unknown'

export interface OtokMonitorBar {
  start: string
  end: string
  state: OtokCardState
  availability: number | null
  ttftMs: number | null
  cacheRate: number | null
}

export interface OtokMonitorCard {
  key: string
  groupId: number | null
  name: string
  provider: string
  rate: number | null
  availability: number | null
  cacheRate: number | null
  ttftMs: number | null
  availabilityState: OtokCardState
  ttftState: OtokCardState
  state: OtokCardState
  bars: OtokMonitorBar[]
}

export interface OtokProviderSection {
  provider: string
  cards: OtokMonitorCard[]
}

/** 可用率 ≥70% 健康、≥30% 需关注、其余异常。 */
export const AVAILABILITY_HEALTHY = 0.7
export const AVAILABILITY_WARNING = 0.3
/** 首 Token <10s 健康、≥30s 异常、其余需关注。 */
export const TTFT_HEALTHY_MS = 10_000
export const TTFT_CRITICAL_MS = 30_000

/** 各时间窗的格数，与后端 ParseFilter 的分桶一致（90m=5m×18 …）。 */
export const BAR_COUNT: Record<MonitorRange, number> = {
  '90m': 18,
  '24h': 24,
  '7d': 14,
  '30d': 30,
}

export const PROVIDER_ORDER = [
  'openai',
  'anthropic',
  'gemini',
  'grok',
  'antigravity',
  'kimi',
  'zhipu',
  'deepseek',
] as const

const PROVIDER_ALIASES: Record<string, string> = {
  claude: 'anthropic',
  kiro: 'anthropic',
  chatgpt: 'openai',
  codex: 'openai',
}

// 图标配色与 components/user/monitor/MonitorCard.vue 的 PROVIDER_TINT 保持一致。
const PROVIDER_TINT: Record<string, string> = {
  openai: 'text-emerald-600 dark:text-emerald-300',
  anthropic: 'text-orange-600 dark:text-orange-300',
  gemini: 'text-sky-600 dark:text-sky-300',
  grok: 'text-zinc-700 dark:text-zinc-200',
  antigravity: 'text-purple-600 dark:text-purple-300',
  kimi: 'text-pink-600 dark:text-pink-300',
  zhipu: 'text-indigo-600 dark:text-indigo-300',
  deepseek: 'text-teal-600 dark:text-teal-300',
  opencode_go: 'text-amber-700 dark:text-amber-300',
}

export function providerTint(provider: string): string {
  return PROVIDER_TINT[provider] ?? 'text-gray-500 dark:text-gray-300'
}

export function normalizeProvider(platform: string | undefined | null): string {
  const value = (platform || '').trim().toLowerCase()
  if (!value) return 'unknown'
  return PROVIDER_ALIASES[value] ?? value
}

/** 用户侧 request_count 被脱敏为 0，只能靠比率判断窗口内是否有流量。 */
export function hasTraffic(metrics: MonitorMetric | null | undefined): boolean {
  if (!metrics) return false
  const successRate = (metrics as MonitorMetric & { success_rate?: number }).success_rate ?? 0
  return metrics.request_count > 0 || successRate > 0 || metrics.error_rate > 0
}

export function availabilityOf(metrics: MonitorMetric | null | undefined): number | null {
  if (!metrics || !hasTraffic(metrics)) return null
  return Math.min(1, Math.max(0, 1 - metrics.error_rate))
}

export function ttftOf(metrics: MonitorMetric | null | undefined): number | null {
  if (!metrics || !hasTraffic(metrics)) return null
  const value = metrics.ttft?.p50_ms
  return value != null && Number.isFinite(value) && value > 0 ? value : null
}

export function cacheRateOf(metrics: MonitorMetric | null | undefined): number | null {
  if (!metrics || !hasTraffic(metrics)) return null
  return Number.isFinite(metrics.cache_rate) ? metrics.cache_rate : null
}

export function availabilityState(value: number | null): OtokCardState {
  if (value == null) return 'unknown'
  if (value >= AVAILABILITY_HEALTHY) return 'healthy'
  if (value >= AVAILABILITY_WARNING) return 'warning'
  return 'critical'
}

export function ttftState(ms: number | null): OtokCardState {
  if (ms == null) return 'unknown'
  if (ms < TTFT_HEALTHY_MS) return 'healthy'
  if (ms >= TTFT_CRITICAL_MS) return 'critical'
  return 'warning'
}

const STATE_RANK: Record<OtokCardState, number> = {
  unknown: 0,
  healthy: 1,
  warning: 2,
  critical: 3,
}

/** 取较差状态；unknown 不参与比较，两者都 unknown 才是 unknown。 */
export function worseState(a: OtokCardState, b: OtokCardState): OtokCardState {
  return STATE_RANK[a] >= STATE_RANK[b] ? a : b
}

/**
 * 按 [requested_start, requested_end) 生成完整的桶序列，再用 row.buckets 填充。
 * buckets 是稀疏的，缺的格子视为无数据。末尾截取 BAR_COUNT[range] 格，保证条数稳定。
 */
export function fillBars(
  coverage: MonitorCoverage | null | undefined,
  buckets: MonitorMatrixBucket[] | null | undefined,
  range: MonitorRange,
): OtokMonitorBar[] {
  if (!coverage) return []
  const step = Math.max(60, coverage.bucket_seconds || 0) * 1000
  const start = new Date(coverage.requested_start).getTime()
  const requestedEnd = coverage.requested_end ? new Date(coverage.requested_end).getTime() : NaN
  const end = Number.isFinite(requestedEnd) && requestedEnd > start
    ? requestedEnd
    : new Date(coverage.data_through).getTime()
  if (![start, end].every(Number.isFinite) || start >= end) return []

  const byStart = new Map<number, MonitorMatrixBucket>()
  for (const bucket of buckets ?? []) {
    const ts = new Date(bucket.bucket_start).getTime()
    if (Number.isFinite(ts)) byStart.set(ts, bucket)
  }

  const bars: OtokMonitorBar[] = []
  for (let cursor = Math.floor(start / step) * step; cursor < end; cursor += step) {
    const metrics = byStart.get(cursor)?.metrics
    const availability = availabilityOf(metrics)
    bars.push({
      start: new Date(cursor).toISOString(),
      end: new Date(cursor + step).toISOString(),
      state: availabilityState(availability),
      availability,
      ttftMs: ttftOf(metrics),
      cacheRate: cacheRateOf(metrics),
    })
  }
  return bars.slice(-BAR_COUNT[range])
}

export interface BuildCardsInput {
  range: MonitorRange
  coverage: MonitorCoverage | null | undefined
  rows: MonitorMatrixRow[]
  dimensionGroups: MonitorDimensions['groups']
  /** /groups/available 返回的分组默认倍率。 */
  groupRates: Record<number, number>
  /** /groups/rates 返回的用户专属倍率，优先于默认倍率。 */
  userRates: Record<number, number>
}

export function effectiveRate(
  groupId: number | null,
  groupRates: Record<number, number>,
  userRates: Record<number, number>,
): number | null {
  if (groupId == null) return null
  const custom = userRates[groupId]
  if (custom != null && Number.isFinite(custom)) return custom
  const base = groupRates[groupId]
  return base != null && Number.isFinite(base) ? base : null
}

function makeCard(
  key: string,
  groupId: number | null,
  name: string,
  provider: string,
  row: MonitorMatrixRow | null,
  input: BuildCardsInput,
): OtokMonitorCard {
  const metrics = row?.metrics
  const availability = availabilityOf(metrics)
  const ttftMs = ttftOf(metrics)
  const aState = availabilityState(availability)
  const tState = ttftState(ttftMs)
  return {
    key,
    groupId,
    name,
    provider,
    rate: effectiveRate(groupId, input.groupRates, input.userRates),
    availability,
    cacheRate: cacheRateOf(metrics),
    ttftMs,
    availabilityState: aState,
    ttftState: tState,
    state: worseState(aState, tState),
    bars: fillBars(input.coverage, row?.buckets, input.range),
  }
}

export function buildCards(input: BuildCardsInput): OtokMonitorCard[] {
  const cards: OtokMonitorCard[] = []
  const covered = new Set<number>()
  const dimById = new Map(input.dimensionGroups.map((g) => [Number(g.id), g]))

  for (const row of input.rows) {
    const groupId = row.group_id != null && Number(row.group_id) > 0 ? Number(row.group_id) : null
    if (groupId == null) continue
    covered.add(groupId)
    const dim = dimById.get(groupId)
    // composite 分组会按真实平台拆成多行，此时用行平台；否则以分组平台为准。
    const groupPlatform = normalizeProvider(dim?.platform)
    const provider = groupPlatform !== 'unknown' && groupPlatform !== 'composite'
      ? groupPlatform
      : normalizeProvider(row.platform)
    const name = row.group_name || dim?.name || `#${groupId}`
    cards.push(makeCard(`${row.platform}:${groupId}`, groupId, name, provider, row, input))
  }

  for (const dim of input.dimensionGroups) {
    const groupId = Number(dim.id)
    if (!Number.isInteger(groupId) || groupId <= 0 || covered.has(groupId)) continue
    const provider = normalizeProvider(dim.platform)
    cards.push(makeCard(`${provider}:${groupId}`, groupId, dim.name || `#${groupId}`, provider, null, input))
  }
  return cards
}

function providerRank(provider: string): number {
  const idx = (PROVIDER_ORDER as readonly string[]).indexOf(provider)
  return idx === -1 ? PROVIDER_ORDER.length : idx
}

/** 按厂商分区（固定顺序，未知厂商按名称排在最后），区内按倍率升序、再按名称。 */
export function groupByProvider(cards: OtokMonitorCard[]): OtokProviderSection[] {
  const map = new Map<string, OtokMonitorCard[]>()
  for (const card of cards) {
    const list = map.get(card.provider)
    if (list) list.push(card)
    else map.set(card.provider, [card])
  }
  const sections = [...map.entries()].map(([provider, list]) => ({
    provider,
    cards: [...list].sort((a, b) => {
      const ra = a.rate ?? Number.POSITIVE_INFINITY
      const rb = b.rate ?? Number.POSITIVE_INFINITY
      if (ra !== rb) return ra - rb
      return a.name.localeCompare(b.name)
    }),
  }))
  return sections.sort((a, b) => {
    const diff = providerRank(a.provider) - providerRank(b.provider)
    return diff !== 0 ? diff : a.provider.localeCompare(b.provider)
  })
}

export function formatPercent(value: number | null): string {
  if (value == null || !Number.isFinite(value)) return '-'
  return `${(value * 100).toFixed(1)}%`
}

export function formatTtft(ms: number | null): string {
  if (ms == null || !Number.isFinite(ms)) return '-'
  if (ms < 1000) return `${Math.round(ms)}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

/** 倍率去掉多余的 0：0.1 → 0.1x，0.135 → 0.135x。 */
export function formatRate(rate: number | null): string {
  if (rate == null || !Number.isFinite(rate)) return '-'
  return `${Number(rate.toFixed(4))}x`
}
