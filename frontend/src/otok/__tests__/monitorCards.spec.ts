import { describe, expect, it } from 'vitest'
import type { MonitorCoverage, MonitorMatrixRow, MonitorMetric } from '@/api/channelMonitorV2'
import {
  availabilityState,
  buildCards,
  effectiveRate,
  fillBars,
  formatPercent,
  formatRate,
  formatTtft,
  groupByProvider,
  hasTraffic,
  normalizeProvider,
  ttftState,
  worseState,
  type OtokMonitorCard,
} from '../monitorCards'

function metric(overrides: Partial<MonitorMetric> & { success_rate?: number } = {}): MonitorMetric {
  return {
    success_requests: 0,
    error_requests: 0,
    request_count: 0,
    token_count: 0,
    rpm: 0,
    tpm: 0,
    error_rate: 0,
    cache_rate: 0,
    cache_rate_numerator: 0,
    cache_rate_denominator: 0,
    ttft: { sample_count: 0, p50_ms: null, p95_ms: null, avg_ms: null },
    duration: { sample_count: 0, p50_ms: null, p95_ms: null, avg_ms: null },
    ...overrides,
  } as MonitorMetric
}

function coverage(start: string, end: string, bucketSeconds: number): MonitorCoverage {
  return {
    requested_start: start,
    requested_end: end,
    coverage_start: start,
    data_through: end,
    computed_at: end,
    aggregation_lag_seconds: 0,
    coverage_complete: true,
    bucket_seconds: bucketSeconds,
  }
}

const COVERAGE_90M = coverage('2026-10-20T05:00:00Z', '2026-10-20T06:30:00Z', 300)

describe('状态阈值', () => {
  it('可用率 ≥70% 健康、≥30% 需关注、其余异常', () => {
    expect(availabilityState(0.7)).toBe('healthy')
    expect(availabilityState(0.6999)).toBe('warning')
    expect(availabilityState(0.3)).toBe('warning')
    expect(availabilityState(0.2999)).toBe('critical')
    expect(availabilityState(null)).toBe('unknown')
  })

  it('首 Token <10s 健康、≥30s 异常', () => {
    expect(ttftState(9_999)).toBe('healthy')
    expect(ttftState(10_000)).toBe('warning')
    expect(ttftState(29_999)).toBe('warning')
    expect(ttftState(30_000)).toBe('critical')
    expect(ttftState(null)).toBe('unknown')
  })

  it('取较差状态，unknown 不拉低结果', () => {
    expect(worseState('healthy', 'warning')).toBe('warning')
    expect(worseState('critical', 'healthy')).toBe('critical')
    expect(worseState('healthy', 'unknown')).toBe('healthy')
    expect(worseState('unknown', 'unknown')).toBe('unknown')
  })
})

describe('无数据判定', () => {
  it('用户侧计数被脱敏为 0 时按比率判断', () => {
    expect(hasTraffic(metric())).toBe(false)
    expect(hasTraffic(metric({ success_rate: 1 }))).toBe(true)
    expect(hasTraffic(metric({ error_rate: 1 }))).toBe(true)
    expect(hasTraffic(metric({ request_count: 3 }))).toBe(true)
    expect(hasTraffic(undefined)).toBe(false)
  })
})

describe('fillBars', () => {
  it('补齐稀疏桶并按时间对齐，90m 固定 18 格', () => {
    const bars = fillBars(
      COVERAGE_90M,
      [
        { bucket_start: '2026-10-20T05:00:00Z', metrics: metric({ success_rate: 1 }), health: {} as never },
        { bucket_start: '2026-10-20T06:25:00.000Z', metrics: metric({ success_rate: 0.5, error_rate: 0.5 }), health: {} as never },
      ],
      '90m',
    )
    expect(bars).toHaveLength(18)
    expect(bars[0].state).toBe('healthy')
    expect(bars[1].state).toBe('unknown')
    expect(bars[17].start).toBe('2026-10-20T06:25:00.000Z')
    expect(bars[17].availability).toBe(0.5)
    expect(bars[17].state).toBe('warning')
  })

  it('起点未对齐时截取末尾 N 格', () => {
    const bars = fillBars(coverage('2026-10-13T07:00:00Z', '2026-10-20T07:00:00Z', 43_200), [], '7d')
    expect(bars).toHaveLength(14)
    expect(bars.every((b) => b.state === 'unknown')).toBe(true)
  })

  it('没有 coverage 时返回空', () => {
    expect(fillBars(null, [], '24h')).toEqual([])
  })
})

describe('倍率', () => {
  it('用户专属倍率优先，其次分组默认倍率', () => {
    expect(effectiveRate(1, { 1: 0.15 }, { 1: 0.1 })).toBe(0.1)
    expect(effectiveRate(1, { 1: 0.15 }, {})).toBe(0.15)
    expect(effectiveRate(2, { 1: 0.15 }, {})).toBeNull()
    expect(effectiveRate(null, {}, {})).toBeNull()
  })

  it('格式化去掉多余的 0', () => {
    expect(formatRate(0.1)).toBe('0.1x')
    expect(formatRate(0.135)).toBe('0.135x')
    expect(formatRate(1)).toBe('1x')
    expect(formatRate(null)).toBe('-')
  })
})

describe('格式化', () => {
  it('百分比与首 Token', () => {
    expect(formatPercent(0.8612)).toBe('86.1%')
    expect(formatPercent(null)).toBe('-')
    expect(formatTtft(850)).toBe('850ms')
    expect(formatTtft(18_240)).toBe('18.2s')
    expect(formatTtft(null)).toBe('-')
  })
})

describe('buildCards / groupByProvider', () => {
  const rows: MonitorMatrixRow[] = [
    {
      platform: 'openai',
      group_id: 127,
      group_name: 'GPT 稳定',
      metrics: metric({ success_rate: 0.99, error_rate: 0.01, cache_rate: 0.9, ttft: { sample_count: 0, p50_ms: 1900, p95_ms: null, avg_ms: null } }),
      health: {} as never,
      buckets: [],
    },
    {
      platform: 'kiro',
      group_id: 136,
      group_name: 'Claude Kiro',
      metrics: metric({ success_rate: 0.2, error_rate: 0.8, ttft: { sample_count: 0, p50_ms: 2000, p95_ms: null, avg_ms: null } }),
      health: {} as never,
      buckets: [],
    },
    {
      platform: 'openai',
      group_id: 135,
      group_name: 'GPT 特惠',
      metrics: metric({ success_rate: 1, ttft: { sample_count: 0, p50_ms: 15_000, p95_ms: null, avg_ms: null } }),
      health: {} as never,
      buckets: [],
    },
  ]
  const dimensionGroups = [
    { id: 127, name: 'GPT 稳定', platform: 'openai', request_count: 0 },
    { id: 136, name: 'Claude Kiro', platform: 'anthropic', request_count: 0 },
    { id: 135, name: 'GPT 特惠', platform: 'openai', request_count: 0 },
    { id: 274, name: 'Kimi', platform: 'kimi', request_count: 0 },
  ]

  const cards = buildCards({
    range: '90m',
    coverage: COVERAGE_90M,
    rows,
    dimensionGroups,
    groupRates: { 127: 0.15, 135: 0.08, 136: 0.1, 274: 0.1 },
    userRates: {},
  })
  const byId = (id: number) => cards.find((c) => c.groupId === id) as OtokMonitorCard

  it('每个分组一张卡，无流量分组补成「无数据」', () => {
    expect(cards).toHaveLength(4)
    const kimi = byId(274)
    expect(kimi.state).toBe('unknown')
    expect(kimi.availability).toBeNull()
    expect(kimi.bars).toHaveLength(18)
  })

  it('卡片状态取可用率与首 Token 的较差者', () => {
    expect(byId(127).state).toBe('healthy')
    expect(byId(135).state).toBe('warning')
    expect(byId(136).state).toBe('critical')
  })

  it('厂商以分组平台为准（kiro 归到 anthropic）', () => {
    expect(byId(136).provider).toBe('anthropic')
    expect(normalizeProvider('Claude')).toBe('anthropic')
    expect(normalizeProvider('')).toBe('unknown')
  })

  it('按固定厂商顺序分区，区内按倍率升序', () => {
    const sections = groupByProvider(cards)
    expect(sections.map((s) => s.provider)).toEqual(['openai', 'anthropic', 'kimi'])
    expect(sections[0].cards.map((c) => c.name)).toEqual(['GPT 特惠', 'GPT 稳定'])
  })
})
