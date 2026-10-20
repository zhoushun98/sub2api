import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import type { OtokMonitorCard } from '@/otok/monitorCards'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}:${JSON.stringify(params)}` : key,
      te: () => false,
    }),
  }
})

import OtokMonitorCardView from '../monitor/OtokMonitorCard.vue'

function card(overrides: Partial<OtokMonitorCard> = {}): OtokMonitorCard {
  return {
    key: 'openai:127',
    groupId: 127,
    name: 'GPT 稳定',
    provider: 'openai',
    rate: 0.15,
    availability: 0.997,
    cacheRate: 0.919,
    ttftMs: 1900,
    availabilityState: 'healthy',
    ttftState: 'healthy',
    state: 'healthy',
    bars: [
      { start: '2026-10-20T06:20:00.000Z', end: '2026-10-20T06:25:00.000Z', state: 'healthy', availability: 1, ttftMs: 1800, cacheRate: 0.9 },
      { start: '2026-10-20T06:25:00.000Z', end: '2026-10-20T06:30:00.000Z', state: 'unknown', availability: null, ttftMs: null, cacheRate: null },
    ],
    ...overrides,
  }
}

describe('OtokMonitorCard', () => {
  it('悬停色条时弹出详情，移开后消失', async () => {
    const wrapper = mount(OtokMonitorCardView, {
      props: { card: card(), countdownSeconds: 10 },
      attachTo: document.body,
    })
    const slots = wrapper.findAll('.otok-bar-slot')
    await slots[0].trigger('mouseenter')
    const tip = document.body.querySelector('[data-testid="otok-monitor-bar-tooltip"]')
    expect(tip?.textContent).toContain('otok.channelCards.barTooltip')
    expect(tip?.textContent).toContain('100.0%')
    await wrapper.find('.otok-bars').trigger('mouseleave')
    await new Promise((r) => setTimeout(r, 200))
    expect(document.body.querySelector('[data-testid="otok-monitor-bar-tooltip"]')).toBeNull()
    wrapper.unmount()
  })

  it('展示名称、倍率、三项指标、状态徽章和时间条', () => {
    const wrapper = mount(OtokMonitorCardView, { props: { card: card(), countdownSeconds: 49 } })
    const text = wrapper.text()
    expect(text).toContain('GPT 稳定')
    expect(text).toContain('0.15x')
    expect(text).toContain('91.9%')
    expect(text).toContain('99.7%')
    expect(text).toContain('1.9s')
    expect(wrapper.get('[data-testid="otok-monitor-state"]').text()).toBe('otok.channelCards.states.healthy')
    expect(wrapper.findAll('[data-testid="otok-monitor-bar"]')).toHaveLength(2)
    expect(text).toContain('"n":49')
  })

  it('无数据时指标显示 -，徽章为无数据', () => {
    const wrapper = mount(OtokMonitorCardView, {
      props: {
        card: card({
          availability: null,
          cacheRate: null,
          ttftMs: null,
          availabilityState: 'unknown',
          ttftState: 'unknown',
          state: 'unknown',
          rate: null,
        }),
        countdownSeconds: 10,
      },
    })
    expect(wrapper.get('[data-testid="otok-monitor-state"]').text()).toBe('otok.channelCards.states.unknown')
    expect(wrapper.findAll('.font-mono.text-lg').map((el) => el.text())).toEqual(['-', '-', '-'])
    expect(wrapper.text()).not.toContain('userRate')
  })

  it('不提供降智检测入口', () => {
    const wrapper = mount(OtokMonitorCardView, { props: { card: card(), countdownSeconds: 10 } })
    // 卡片里唯一的按钮是时间条的色条（用于悬停 / 聚焦查看详情）
    const buttons = wrapper.findAll('button')
    expect(buttons.every((b) => b.attributes('data-testid') === 'otok-monitor-bar')).toBe(true)
    expect(wrapper.text()).not.toMatch(/降智|quality/i)
  })

  it('启用降智检测时显示按钮并发出 pelican 事件，未启用时不渲染', async () => {
    const off = mount(OtokMonitorCardView, { props: { card: card(), countdownSeconds: 10 } })
    expect(off.find('[data-testid="otok-monitor-pelican"]').exists()).toBe(false)

    const on = mount(OtokMonitorCardView, { props: { card: card(), countdownSeconds: 10, pelicanEnabled: true } })
    const btn = on.find('[data-testid="otok-monitor-pelican"]')
    expect(btn.text()).toBe('otok.pelican.button')
    await btn.trigger('click')
    expect(on.emitted('pelican')).toHaveLength(1)
  })
})
