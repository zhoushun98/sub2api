import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import type { MonitorMatrixBucket, MonitorMetric } from '@/api/channelMonitorV2'
import ChannelMonitorV3Timeline from '../ChannelMonitorV3Timeline.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key, locale: { value: 'zh-CN' } }),
  }
})

const THRESHOLDS = { warning_ttft_ms: 8000, critical_ttft_ms: 20000 }

function metrics(requests: number, errorRate: number, ttftMs: number | null = 3000): MonitorMetric {
  return {
    success_requests: Math.round(requests * (1 - errorRate)),
    error_requests: Math.round(requests * errorRate),
    request_count: requests,
    token_count: 0,
    rpm: 0,
    tpm: 0,
    error_rate: errorRate,
    cache_rate: 0,
    cache_rate_numerator: 0,
    cache_rate_denominator: 0,
    ttft: { sample_count: ttftMs == null ? 0 : requests, p50_ms: ttftMs, p95_ms: null, avg_ms: null },
    duration: { sample_count: 0, p50_ms: null, p95_ms: null, avg_ms: null },
  }
}

// 后端综合健康统一给 unknown，确认色块只看可用率与首 Token，不再受后端样本门槛影响
function bucket(m: MonitorMetric): MonitorMatrixBucket {
  return {
    bucket_start: '2026-09-11T12:00:00Z',
    metrics: m,
    health: { overall: 'unknown', error_rate: 'unknown', ttft: 'unknown', cache: 'unknown', minimum_sample: 50 },
  }
}

function barFor(m: MonitorMetric) {
  const wrapper = mount(ChannelMonitorV3Timeline, {
    props: { buckets: [bucket(m)], countdownSeconds: 0, length: 1, ttftThresholds: THRESHOLDS },
  })
  const bar = wrapper.get('.v3-soft-glass-bar')
  return { classes: bar.classes(), height: (bar.element as HTMLElement).style.height }
}

describe('ChannelMonitorV3Timeline signal bars', () => {
  it('paints buckets without requests as a short gray baseline', () => {
    const bar = barFor(metrics(0, 0))
    expect(bar.classes).toContain('bg-gray-300')
    expect(bar.height).toBe('15%')
  })

  it('scales height with state: green full, amber two-thirds, red one-third', () => {
    expect(barFor(metrics(20, 0.1))).toMatchObject({ height: '100%' })
    expect(barFor(metrics(20, 0.1)).classes).toContain('bg-emerald-500')

    expect(barFor(metrics(20, 0.5))).toMatchObject({ height: '65%' })
    expect(barFor(metrics(20, 0.5)).classes).toContain('bg-amber-500')

    expect(barFor(metrics(20, 0.8))).toMatchObject({ height: '35%' })
    expect(barFor(metrics(20, 0.8)).classes).toContain('bg-red-500')
  })

  it('takes the worse of availability and first token', () => {
    const slow = barFor(metrics(20, 0, 25000))
    expect(slow.classes).toContain('bg-red-500')
    expect(slow.height).toBe('35%')
  })
})

describe('ChannelMonitorV3Timeline tooltip arrow', () => {
  it('points the arrow at the hovered bar when the tooltip is clamped to the viewport edge', async () => {
    const restore: Array<() => void> = []
    const stub = (target: object, key: string, value: unknown) => {
      const original = Object.getOwnPropertyDescriptor(target, key)
      Object.defineProperty(target, key, { configurable: true, get: () => value })
      restore.push(() => (original ? Object.defineProperty(target, key, original) : delete (target as Record<string, unknown>)[key]))
    }
    stub(document.documentElement, 'clientWidth', 390)
    stub(HTMLElement.prototype, 'offsetWidth', 280)

    const wrapper = mount(ChannelMonitorV3Timeline, {
      props: { buckets: [bucket(metrics(20, 0.1))], countdownSeconds: 0, length: 1, ttftThresholds: THRESHOLDS },
      attachTo: document.body,
    })
    const slot = wrapper.get('.v3-bar-slot')
    // 色块中心在 x=40：以它居中会越过左侧留白，提示框需贴到 16px
    slot.element.getBoundingClientRect = () => ({ left: 34, width: 12, top: 500, right: 46, bottom: 524, height: 24, x: 34, y: 500, toJSON: () => ({}) })
    await slot.trigger('mouseenter')
    await new Promise(resolve => setTimeout(resolve, 0))

    const tooltip = document.body.querySelector<HTMLElement>('[data-testid="channel-timeline-tooltip"]')
    expect(tooltip).not.toBeNull()
    expect(tooltip!.style.getPropertyValue('--tooltip-left')).toBe('16px')
    expect(tooltip!.style.getPropertyValue('--tooltip-arrow-left')).toBe('24px')

    wrapper.unmount()
    restore.forEach(fn => fn())
  })
})
