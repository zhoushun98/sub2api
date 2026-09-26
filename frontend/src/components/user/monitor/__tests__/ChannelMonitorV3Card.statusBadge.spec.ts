import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import type { MonitorMatrixBucket, MonitorMatrixRow, MonitorMetric } from '@/api/channelMonitorV2'
import ChannelMonitorV3Card from '../ChannelMonitorV3Card.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key, te: () => true }),
  }
})

const THRESHOLDS = { warning_ttft_ms: 8000, critical_ttft_ms: 20000 }

function metrics(requests: number, errorRate: number, ttftMs = 2000): MonitorMetric {
  return {
    success_requests: Math.round(requests * (1 - errorRate)),
    error_requests: Math.round(requests * errorRate),
    request_count: requests,
    token_count: 1000,
    rpm: 1,
    tpm: 10,
    error_rate: errorRate,
    cache_rate: 0.5,
    cache_rate_numerator: 50,
    cache_rate_denominator: 100,
    ttft: { sample_count: requests, p50_ms: requests ? ttftMs : null, p95_ms: null, avg_ms: null },
    duration: { sample_count: 0, p50_ms: null, p95_ms: null, avg_ms: null },
  }
}

function bucket(start: string, m: MonitorMetric): MonitorMatrixBucket {
  return {
    bucket_start: start,
    metrics: m,
    health: { overall: 'unknown', error_rate: 'unknown', ttft: 'unknown', cache: 'unknown', minimum_sample: 50 },
  }
}

function mountCard(rowMetrics: MonitorMetric, buckets: MonitorMatrixBucket[] = [bucket('2026-09-09T12:00:00Z', rowMetrics)]) {
  const row: MonitorMatrixRow = {
    platform: 'openai',
    group_id: 1,
    group_name: 'Codex',
    metrics: rowMetrics,
    health: { overall: 'unknown', error_rate: 'unknown', ttft: 'unknown', cache: 'unknown', minimum_sample: 50 },
    buckets,
  }
  return mount(ChannelMonitorV3Card, {
    props: { row, countdownSeconds: 0, timelineLength: 12, ttftThresholds: THRESHOLDS },
    global: { stubs: { ProviderIcon: true, ChannelMonitorV3Timeline: true } },
  })
}

function badgeState(m: MonitorMetric) {
  return mountCard(m).get('[data-testid="channel-status-badge"]').attributes('data-state')
}

function redacted(m: MonitorMetric, ttftP50: number | null): MonitorMetric {
  return { ...m, success_requests: 0, error_requests: 0, request_count: 0, cache_rate_numerator: 0, cache_rate_denominator: 0, ttft: { sample_count: 0, p50_ms: ttftP50, p95_ms: null, avg_ms: null } }
}

describe('ChannelMonitorV3Card status badge', () => {
  it('derives the badge from availability and first token', () => {
    expect(badgeState(metrics(100, 0.02))).toBe('healthy')
    expect(badgeState(metrics(100, 0.5))).toBe('warning')
    expect(badgeState(metrics(100, 0.9))).toBe('critical')
    expect(badgeState(metrics(100, 0.02, 12000))).toBe('warning')
  })

  it('shows 样本不足 and dashes when the range has no traffic', () => {
    const wrapper = mountCard(metrics(0, 0))
    const badge = wrapper.get('[data-testid="channel-status-badge"]')
    expect(badge.attributes('data-state')).toBe('unknown')
    expect(badge.text()).toBe('channelMonitorV3.unknown')
    expect(wrapper.findAll('.channel-signal-card__value').map(node => node.text())).toEqual(['-', '-', '-'])
  })

  it('uses the selected-range aggregate instead of the newest bucket', () => {
    // 最新桶只有几次请求且全部失败，但整个范围是健康的：卡片应反映整体
    const wrapper = mountCard(metrics(500, 0.02), [
      bucket('2026-09-09T12:00:00Z', metrics(495, 0.01)),
      bucket('2026-09-09T12:05:00Z', metrics(5, 1)),
    ])
    expect(wrapper.get('[data-testid="channel-status-badge"]').attributes('data-state')).toBe('healthy')
  })
})

describe('ChannelMonitorV3Card with user-facing redacted metrics', () => {
  it('shows real values instead of 样本不足 when counts are zeroed by the API', () => {
    const wrapper = mountCard(redacted(metrics(100, 0.02, 3000), 3000))
    expect(wrapper.get('[data-testid="channel-status-badge"]').attributes('data-state')).toBe('healthy')
    expect(wrapper.findAll('.channel-signal-card__value').map(node => node.text())).not.toContain('-')
    expect(wrapper.get('[data-testid="channel-status-ttft"]').attributes('title')).toBeUndefined()
  })

  it('explains a missing first token when the range only has non-streaming requests', () => {
    const wrapper = mountCard(redacted(metrics(100, 0.02), null))
    const ttft = wrapper.get('[data-testid="channel-status-ttft"]')
    expect(ttft.attributes('title')).toBe('channelMonitorV3.ttftNoStream')
    expect(ttft.get('.channel-signal-card__value').text()).toBe('-')
    expect(wrapper.get('[data-testid="channel-status-badge"]').attributes('data-state')).toBe('healthy')
  })
})
