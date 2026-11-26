import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import type { OtokQualityRound, OtokQualityRunDetail, OtokQualitySource } from '@/api/otok/quality'
import { OTOK_QUALITY_PREVIEW_CSP_PREFIX } from '@/otok/quality'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => (params ? `${key}:${JSON.stringify(params)}` : key),
      te: () => false,
    }),
  }
})

import OtokQualityPanel from '../quality/OtokQualityPanel.vue'

const HTML = '<!DOCTYPE html><html><body><script>alert(1)</script><svg></svg></body></html>'
const PLAIN = '这是纯文本答案 <b>不该加粗</b>'

function rounds(pending = false): OtokQualityRound[] {
  return [
    {
      id: 2,
      item_id: 7,
      trigger: 'schedule',
      prompt: '画一只鹈鹕 <i>x</i>',
      started_at: '2026-10-20T14:48:30Z',
      finished_at: pending ? null : '2026-10-20T14:52:39Z',
      runs: [
        { id: 21, model: 'gpt-6-astra', status: pending ? 'running' : 'succeeded', attempt: 1, duration_ms: pending ? null : 248_300, first_token_ms: null, output_tokens: 3000, reasoning_tokens: null, has_html: !pending, error_code: '', started_at: null, finished_at: null },
        { id: 22, model: 'gpt-6.1-sol', status: 'failed', attempt: 2, duration_ms: 5000, first_token_ms: null, output_tokens: null, reasoning_tokens: null, has_html: false, error_code: 'http_5xx', error_message: 'bad gateway from upstream.example', http_status: 502, started_at: null, finished_at: null },
        { id: 23, model: 'claude-x', status: 'succeeded', attempt: 1, duration_ms: 3000, first_token_ms: 1000, output_tokens: 10, reasoning_tokens: null, has_html: false, error_code: '', started_at: null, finished_at: null },
      ],
    },
    {
      id: 1,
      item_id: 7,
      trigger: 'manual',
      prompt: '旧提示词',
      started_at: '2026-10-20T14:41:25Z',
      finished_at: '2026-10-20T14:43:30Z',
      runs: [
        { id: 11, model: 'gpt-6-astra', status: 'succeeded', attempt: 1, duration_ms: 124_600, first_token_ms: null, output_tokens: null, reasoning_tokens: null, has_html: true, error_code: '', started_at: null, finished_at: null },
      ],
    },
  ]
}

function detail(id: number): OtokQualityRunDetail {
  const base: OtokQualityRunDetail = {
    id, model: 'gpt-6-astra', status: 'succeeded', attempt: 1, duration_ms: 1, first_token_ms: null, output_tokens: null,
    reasoning_tokens: null, has_html: true, error_code: '', started_at: null, finished_at: null,
    round_id: 2, item_id: 7, input_tokens: null,
    output_text: '```html\n' + HTML + '\n```', output_html: HTML,
  }
  if (id === 22) return { ...base, model: 'gpt-6.1-sol', status: 'failed', has_html: false, error_code: 'http_5xx', error_message: 'bad gateway from upstream.example', http_status: 502, output_text: '', output_html: '' }
  if (id === 23) return { ...base, model: 'claude-x', has_html: false, output_text: PLAIN, output_html: '' }
  return base
}

function source(list: () => OtokQualityRound[]): OtokQualitySource & { listRounds: ReturnType<typeof vi.fn>; getRun: ReturnType<typeof vi.fn> } {
  return {
    listRounds: vi.fn(async () => list()),
    getRun: vi.fn(async (id: number) => detail(id)),
  }
}

function mountPanel(src: OtokQualitySource, admin = false) {
  return mount(OtokQualityPanel, { props: { itemId: 7, source: src, admin } })
}

describe('OtokQualityPanel', () => {
  beforeEach(() => setActivePinia(createPinia()))
  afterEach(() => vi.useRealTimers())

  it('默认预览最新一轮第一个有 HTML 的模型，iframe 不给任何沙箱权限并带固定 CSP 前缀', async () => {
    const src = source(() => rounds())
    const wrapper = mountPanel(src)
    await flushPromises()

    expect(src.listRounds).toHaveBeenCalledWith(7)
    expect(wrapper.findAll('[data-testid^="otok-quality-round-"]')).toHaveLength(2)
    expect(wrapper.text()).toContain('gpt-6-astra · 248.3s')
    expect(wrapper.text()).toContain('otok.quality.roundStates.partial')
    expect(src.getRun).toHaveBeenCalledWith(21)

    const iframe = wrapper.find('[data-testid="otok-quality-iframe"]')
    expect(iframe.exists()).toBe(true)
    expect(iframe.attributes('sandbox')).toBe('')
    expect(iframe.attributes('referrerpolicy')).toBe('no-referrer')
    expect(iframe.attributes('srcdoc')).toBe(OTOK_QUALITY_PREVIEW_CSP_PREFIX + HTML)

    await wrapper.find('[data-testid="otok-quality-toggle-preview"]').trigger('click')
    expect(wrapper.find('[data-testid="otok-quality-iframe"]').exists()).toBe(false)
  })

  it('原文和提示词以纯文本展示，不走 v-html', async () => {
    const wrapper = mountPanel(source(() => rounds()))
    await flushPromises()
    await wrapper.find('[data-testid="otok-quality-toggle-source"]').trigger('click')
    const pre = wrapper.find('[data-testid="otok-quality-source"]')
    expect(pre.text()).toContain('<script>alert(1)</script>')
    expect(pre.element.querySelector('script')).toBeNull()

    const prompt = wrapper.find('[data-testid="otok-quality-prompt"]')
    expect(prompt.text()).toContain('画一只鹈鹕 <i>x</i>')
    expect(prompt.element.querySelector('i')).toBeNull()
  })

  it('没有 HTML 时直接显示文本，不渲染 iframe', async () => {
    const src = source(() => rounds())
    const wrapper = mountPanel(src)
    await flushPromises()
    await wrapper.findAll('[role="tab"]')[2].trigger('click')
    await flushPromises()
    expect(src.getRun).toHaveBeenCalledWith(23)
    expect(wrapper.find('[data-testid="otok-quality-iframe"]').exists()).toBe(false)
    const text = wrapper.find('[data-testid="otok-quality-text"]')
    expect(text.text()).toContain(PLAIN)
    expect(text.element.querySelector('b')).toBeNull()
  })

  it('用户端只显示错误类型，管理端额外显示错误原文', async () => {
    const userView = mountPanel(source(() => rounds()))
    await flushPromises()
    await userView.findAll('[role="tab"]')[1].trigger('click')
    await flushPromises()
    const userErr = userView.find('[data-testid="otok-quality-error"]')
    expect(userErr.text()).toContain('otok.quality.errorCodes.http_5xx')
    expect(userErr.text()).not.toContain('bad gateway')
    expect(userView.find('[data-testid="otok-quality-error-detail"]').exists()).toBe(false)

    const adminView = mountPanel(source(() => rounds()), true)
    await flushPromises()
    await adminView.findAll('[role="tab"]')[1].trigger('click')
    await flushPromises()
    expect(adminView.find('[data-testid="otok-quality-error"]').text()).toContain('otok.quality.errorCodes.http_5xx')
    expect(adminView.find('[data-testid="otok-quality-error-detail"]').text()).toContain('bad gateway from upstream.example')
  })

  it('切换轮次会拉对应记录并显示该轮提示词', async () => {
    const src = source(() => rounds())
    const wrapper = mountPanel(src)
    await flushPromises()
    await wrapper.find('[data-testid="otok-quality-round-1"]').trigger('click')
    await flushPromises()
    expect(src.getRun).toHaveBeenCalledWith(11)
    expect(wrapper.find('[data-testid="otok-quality-prompt"]').text()).toContain('旧提示词')
  })

  it('慢请求未返回时切回已缓存的记录，骨架屏不会卡住', async () => {
    const src = source(() => rounds())
    let release: (() => void) | null = null
    src.getRun.mockImplementation((id: number) => {
      if (id !== 11) return Promise.resolve(detail(id))
      return new Promise<OtokQualityRunDetail>((resolve) => {
        release = () => resolve({ ...detail(11), output_html: '<p>old</p>' })
      })
    })
    const wrapper = mountPanel(src)
    await flushPromises()
    expect(src.getRun).toHaveBeenCalledWith(21)

    await wrapper.find('[data-testid="otok-quality-round-1"]').trigger('click')
    await flushPromises()
    expect(src.getRun).toHaveBeenCalledWith(11)

    await wrapper.find('[data-testid="otok-quality-round-2"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('.animate-pulse').exists()).toBe(false)
    expect(wrapper.find('[data-testid="otok-quality-iframe"]').attributes('srcdoc')).toBe(OTOK_QUALITY_PREVIEW_CSP_PREFIX + HTML)

    release!()
    await flushPromises()
    expect(wrapper.find('.animate-pulse').exists()).toBe(false)
    expect(wrapper.find('[data-testid="otok-quality-iframe"]').attributes('srcdoc')).toBe(OTOK_QUALITY_PREVIEW_CSP_PREFIX + HTML)
  })

  it('有进行中的轮次时轮询，完成后停止', async () => {
    vi.useFakeTimers()
    let pending = true
    const src = source(() => rounds(pending))
    mountPanel(src)
    await flushPromises()
    expect(src.listRounds).toHaveBeenCalledTimes(1)

    pending = false
    await vi.advanceTimersByTimeAsync(15_000)
    expect(src.listRounds).toHaveBeenCalledTimes(2)

    await vi.advanceTimersByTimeAsync(60_000)
    expect(src.listRounds).toHaveBeenCalledTimes(2)
  })

  it('没有结果时显示空态', async () => {
    const wrapper = mountPanel(source(() => []))
    await flushPromises()
    expect(wrapper.find('[data-testid="otok-quality-empty"]').exists()).toBe(true)
  })
})
