/**
 * Shared display formatters for channel-monitor-v2.
 * Kept pure so unit tests can lock metric presentation accuracy.
 *
 * Privacy: prefer rates (error_rate, RPM, TPM, success %) over absolute
 * request/error/token counts in user-facing surfaces.
 */

import type { HealthScoreBand, HealthState, MonitorHealth, MonitorMetric } from '@/api/channelMonitorV2'
import { formatCompactNumber } from '@/utils/format'

export function monitorIntlLocale(): string {
  if (typeof document !== 'undefined') {
    const htmlLang = document.documentElement.getAttribute('lang')?.trim()
    if (htmlLang) return htmlLang
  }
  if (typeof navigator !== 'undefined' && navigator.language) return navigator.language
  return 'zh-CN'
}

export function formatMonitorNumber(value: number, compactAt = 10000, locale = monitorIntlLocale()): string {
  return Intl.NumberFormat(locale, {
    notation: value >= compactAt ? 'compact' : 'standard',
    maximumFractionDigits: 1,
  }).format(value || 0)
}

export function formatMonitorRate(value: number, locale = monitorIntlLocale()): string {
  return Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(value || 0)
}

/**
 * RPM/TPM throughput display: always K/M compact (1.0K, 1.5M) for ops-readable density.
 * Prefer this over formatMonitorRate for rate KPIs and table cells.
 */
export function formatMonitorThroughput(value: number | null | undefined): string {
  if (value == null || Number.isNaN(Number(value))) return '0'
  const n = Number(value)
  if (Math.abs(n) < 1000) {
    return Intl.NumberFormat(monitorIntlLocale(), { maximumFractionDigits: 1 }).format(n)
  }
  return formatCompactNumber(n)
}

/**
 * Backend stores tokens-per-minute as `tpm`. Convert to tokens-per-second for display.
 */
export function tokensPerSecondFromTpm(tpm: number | null | undefined): number {
  if (tpm == null || Number.isNaN(Number(tpm))) return 0
  return Number(tpm) / 60
}

/** Tokens/sec from backend TPM (per-minute), compact ops formatting. */
export function formatMonitorTokensPerSecond(tpm: number | null | undefined): string {
  return formatMonitorThroughput(tokensPerSecondFromTpm(tpm))
}


export function formatMonitorPercent(value: number, locale = monitorIntlLocale()): string {
  return `${new Intl.NumberFormat(locale, {
    minimumFractionDigits: value < 0.01 ? 2 : 1,
    maximumFractionDigits: value < 0.01 ? 2 : 1,
  }).format((value || 0) * 100)}%`
}

/**
 * 用户端渠道卡片的信号状态：可用率与首 Token 各自分档后取较差者。
 * 可用率固定 ≥70% 绿 / ≥30% 黄 / <30% 红；首 Token 按 V2 配置的关注 / 异常阈值分档。
 * 无请求时为 unknown；首 Token 无样本或未配置阈值时不参与取较差。
 */
export const SIGNAL_AVAILABILITY_HEALTHY_MIN = 70
export const SIGNAL_AVAILABILITY_WARNING_MIN = 30

export interface SignalTtftThresholds {
  warning_ttft_ms: number
  critical_ttft_ms: number
}

const SIGNAL_SEVERITY: Record<HealthState, number> = { unknown: -1, healthy: 0, warning: 1, critical: 2 }

export function availabilitySignalState(percent: number | null | undefined): HealthState {
  if (percent == null || !Number.isFinite(percent)) return 'unknown'
  if (percent >= SIGNAL_AVAILABILITY_HEALTHY_MIN) return 'healthy'
  if (percent >= SIGNAL_AVAILABILITY_WARNING_MIN) return 'warning'
  return 'critical'
}

export function ttftSignalState(ms: number | null | undefined, thresholds?: SignalTtftThresholds | null): HealthState {
  if (ms == null || !Number.isFinite(ms) || !thresholds) return 'unknown'
  if (ms >= thresholds.critical_ttft_ms) return 'critical'
  if (ms >= thresholds.warning_ttft_ms) return 'warning'
  return 'healthy'
}

/**
 * 判断指标对应的时间窗口内是否有请求。
 * 用户端接口会把 request_count、ttft.sample_count 等绝对量抹成 0（后端 redactChannelMonitorV2Metric），
 * 只能依靠保留下来的错误率与延迟分位数：有任一延迟分位或错误率 > 0 即视为有请求。
 */
export function metricHasTraffic(metrics: MonitorMetric | null | undefined): boolean {
  if (!metrics) return false
  if (metrics.request_count > 0) return true
  return metrics.error_rate > 0 || metrics.ttft?.p50_ms != null || metrics.duration?.p50_ms != null
}

export function channelSignalState(metrics: MonitorMetric | null | undefined, thresholds?: SignalTtftThresholds | null): HealthState {
  if (!metrics || !metricHasTraffic(metrics)) return 'unknown'
  const availability = availabilitySignalState((1 - metrics.error_rate) * 100)
  const ttft = ttftSignalState(metrics.ttft?.p50_ms, thresholds)
  return SIGNAL_SEVERITY[ttft] > SIGNAL_SEVERITY[availability] ? ttft : availability
}

// 色块高度随状态递减：绿满格、黄约 2/3、红约 1/3、无数据只留一道底线
export const SIGNAL_STATE_STYLE: Record<HealthState, { barClass: string; textClass: string; badgeClass: string; heightPct: number }> = {
  healthy: {
    barClass: 'bg-emerald-500 dark:bg-emerald-400',
    textClass: 'text-emerald-600 dark:text-emerald-300',
    badgeClass: 'bg-emerald-500/15 text-emerald-700 dark:bg-emerald-400/15 dark:text-emerald-300',
    heightPct: 100,
  },
  warning: {
    barClass: 'bg-amber-500 dark:bg-amber-400',
    textClass: 'text-amber-600 dark:text-amber-300',
    badgeClass: 'bg-amber-500/15 text-amber-700 dark:bg-amber-400/15 dark:text-amber-300',
    heightPct: 65,
  },
  critical: {
    barClass: 'bg-red-500 dark:bg-red-400',
    textClass: 'text-red-600 dark:text-red-400',
    badgeClass: 'bg-red-500/15 text-red-700 dark:bg-red-400/15 dark:text-red-300',
    heightPct: 35,
  },
  unknown: {
    barClass: 'bg-gray-300 dark:bg-dark-600',
    textClass: 'text-gray-900 dark:text-gray-100',
    badgeClass: 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300',
    heightPct: 15,
  },
}

export function formatSignalSeconds(ms: number): string {
  return `${Number((ms / 1000).toFixed(1))}s`
}

export function formatMonitorMs(value: number | null | undefined): string {
  if (value == null) return '-'
  return value >= 1000 ? `${(value / 1000).toFixed(1)}s` : `${Math.round(value)}ms`
}

export function formatMonitorSuccessRate(successRequests: number, requestCount: number): string {
  if (!requestCount) return '-'
  return formatMonitorPercent(successRequests / requestCount)
}

export function formatMonitorSuccessRateFromError(errorRate: number): string {
  return formatMonitorPercent(1 - (errorRate || 0))
}

/**
 * Map continuous 0–100 score to 11 fine bands for multi-stop green→yellow→red.
 * score10 = best (green), score0 = worst (red).
 */
export function scoreToBand(score: number | null | undefined): HealthScoreBand {
  if (score == null || Number.isNaN(score)) return 'unknown'
  const clamped = Math.max(0, Math.min(100, score))
  // 0–100 → score0..score10 (11 stops for green→yellow→red gradient)
  const band = Math.round(clamped / 10)
  return `score${Math.max(0, Math.min(10, band))}` as HealthScoreBand
}

export type HealthDisplayMode = 'overall' | 'success' | 'ttft' | 'cache'

/** Resolve the score used for a health mode. */
export function healthModeScore(
  health: MonitorHealth,
  mode: HealthDisplayMode,
): number | null {
  if (mode === 'success') {
    return health.error_rate_score ?? null
  }
  if (mode === 'ttft') {
    return health.ttft_score ?? null
  }
  if (mode === 'cache') {
    return health.cache_score ?? null
  }
  return health.score ?? null
}

export function healthScoreClass(
  health: MonitorHealth,
  mode: HealthDisplayMode,
  requestCount: number,
): string {
  const score = healthModeScore(health, mode)
  // Sync-only traffic has no first-token samples. Never paint TTFT as red/empty-fail.
  if (mode === 'ttft' && score == null) return 'health-unknown'
  if (score == null) {
    if (requestCount <= 0) return 'health-unknown'
    // Fall back to coarse state when score is absent (older payloads).
    const coarse =
      mode === 'success'
        ? health.error_rate
        : mode === 'cache'
          ? health.cache
          : health.overall
    return healthStateClass(coarse)
  }
  return `health-${scoreToBand(score)}`
}

/** Missing first-token samples are "not applicable", not a failed latency budget. */
export function isTtftUnavailable(ttft?: { p50_ms?: number | null; sample_count?: number } | null): boolean {
  if (ttft == null) return true
  return ttft.p50_ms == null
}

export function ttftDisplayState(
  state: HealthState | undefined,
  ttft?: { p50_ms?: number | null; sample_count?: number } | null,
): HealthState | undefined {
  if (isTtftUnavailable(ttft)) return 'unknown'
  return state
}

export function healthStateClass(state: string | undefined): string {
  return `health-${state || 'unknown'}`
}

/** Privacy-safe latency summary: avg + p50 + p90 (no absolute sample counts). */
export function formatLatencyPrivacy(
  p50: number | null | undefined,
  p90: number | null | undefined,
  avg?: number | null | undefined,
  p95?: number | null | undefined,
): string {
  const parts: string[] = []
  if (avg != null) parts.push(`AVG ${formatMonitorMs(avg)}`)
  if (p50 != null) parts.push(`P50 ${formatMonitorMs(p50)}`)
  if (p90 != null) parts.push(`P90 ${formatMonitorMs(p90)}`)
  // p95 only as fallback when p90 missing (older payloads)
  if (p90 == null && p95 != null) parts.push(`P95 ${formatMonitorMs(p95)}`)
  return parts.length ? parts.join(' · ') : '-'
}

/**
 * KPI secondary line for latency: AVG + P90 only (P50 is the primary value).
 * Falls back to P95 when P90 is absent. Delimiter is " · " so MetricCell can
 * split into non-truncated chips.
 */
export function formatLatencyKpiSecondary(
  avg?: number | null | undefined,
  p90?: number | null | undefined,
  p95?: number | null | undefined,
): string {
  const parts: string[] = []
  if (avg != null && Number.isFinite(avg)) parts.push(`AVG ${formatMonitorMs(avg)}`)
  if (p90 != null && Number.isFinite(p90)) parts.push(`P90 ${formatMonitorMs(p90)}`)
  else if (p95 != null && Number.isFinite(p95)) parts.push(`P95 ${formatMonitorMs(p95)}`)
  return parts.length ? parts.join(' · ') : '-'
}

/** Fallback when i18n key is missing; prefer `channelMonitorV2.errorCategories.*`. */
export function monitorErrorCategoryLabel(category: string): string {
  return category
}
