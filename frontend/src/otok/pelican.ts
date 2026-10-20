/**
 * otok: 「降智检测」（鹈鹕测试）的纯逻辑：状态归纳、耗时格式化、预览文档、后台模型输入解析。
 */
import type { OtokPelicanConfig, OtokPelicanRound, OtokPelicanRunSummary } from '@/api/otok/pelican'

export type OtokPelicanRoundState = 'running' | 'succeeded' | 'partial' | 'failed'

/** 预览按这个尺寸渲染，再等比缩放到容器宽度；默认 prompt 也要求页面适配 1200×800。 */
export const OTOK_PELICAN_PREVIEW_WIDTH = 1200
export const OTOK_PELICAN_PREVIEW_HEIGHT = 800

/** 有进行中的轮次时，弹窗按这个间隔轮询。 */
export const OTOK_PELICAN_POLL_MS = 15_000

export function isOtokPelicanRunPending(run: Pick<OtokPelicanRunSummary, 'status'>): boolean {
  return run.status === 'queued' || run.status === 'running'
}

/** 一轮的整体状态：有进行中 → running；全成功 → succeeded；全失败 → failed；其余 → partial。 */
export function otokPelicanRoundState(round: Pick<OtokPelicanRound, 'runs' | 'finished_at'>): OtokPelicanRoundState {
  const runs = round.runs ?? []
  if (runs.some(isOtokPelicanRunPending) || (runs.length === 0 && !round.finished_at)) return 'running'
  const ok = runs.filter((r) => r.status === 'succeeded').length
  if (ok === runs.length && ok > 0) return 'succeeded'
  if (ok === 0) return 'failed'
  return 'partial'
}

export function hasOtokPelicanPending(rounds: OtokPelicanRound[]): boolean {
  return rounds.some((r) => otokPelicanRoundState(r) === 'running')
}

/** 毫秒 → "248.3s"；空值返回 "—"。 */
export function formatOtokPelicanDuration(ms: number | null | undefined): string {
  if (ms == null || !Number.isFinite(ms) || ms < 0) return '—'
  return `${(ms / 1000).toFixed(1)}s`
}

export function formatOtokPelicanTime(value: string | null | undefined): string {
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

/** 后端已经抽出 HTML；这里只兜底补一个最小骨架，避免片段在 iframe 里没有字符集。 */
export function buildOtokPelicanPreviewDoc(html: string): string {
  const trimmed = (html ?? '').trim()
  if (!trimmed) return ''
  if (/<html[\s>]/i.test(trimmed) || /^<!doctype/i.test(trimmed)) return trimmed
  return `<!DOCTYPE html><html><head><meta charset="utf-8"></head><body>${trimmed}</body></html>`
}

/** 选中轮次时默认打开的模型：优先第一个有 HTML 的。 */
export function defaultOtokPelicanRun(round: OtokPelicanRound | null | undefined): OtokPelicanRunSummary | null {
  if (!round || round.runs.length === 0) return null
  return round.runs.find((r) => r.has_html) ?? round.runs[0]
}

/** "a, b，c\nd" → ["a","b","c","d"]，去重去空。 */
export function parseOtokPelicanModels(raw: string): string[] {
  const out: string[] = []
  for (const part of raw.split(/[\s,，]+/)) {
    const m = part.trim()
    if (m && !out.includes(m)) out.push(m)
  }
  return out
}

export const OTOK_PELICAN_MODEL_RE = /^[A-Za-z0-9._:/-]{1,128}$/

/** 每天大约要发多少次请求：启用分组 × 模型数 × 每天轮数。总开关关闭时为 0。 */
export function estimateOtokPelicanDailyRequests(cfg: OtokPelicanConfig): number {
  if (!cfg.enabled || !cfg.interval_minutes) return 0
  const roundsPerDay = Math.ceil(1440 / cfg.interval_minutes)
  return cfg.groups
    .filter((g) => g.enabled)
    .reduce((sum, g) => sum + (g.models.length > 0 ? g.models.length : cfg.default_models.length) * roundsPerDay, 0)
}
