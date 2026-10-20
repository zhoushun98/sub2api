import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import type { OtokPelicanRound, OtokPelicanRunDetail, OtokPelicanSource } from '@/api/otok/pelican'

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

import OtokPelicanDialog from '../monitor/OtokPelicanDialog.vue'

const HTML = '<!DOCTYPE html><html><body><script>alert(1)</script><svg></svg></body></html>'

function rounds(pending = false): OtokPelicanRound[] {
  return [
    {
      id: 2,
      group_id: 127,
      trigger: 'schedule',
      started_at: '2026-10-20T14:48:30Z',
      finished_at: pending ? null : '2026-10-20T14:52:39Z',
      runs: [
        { id: 21, model: 'gpt-6-astra', status: pending ? 'running' : 'succeeded', attempt: 1, duration_ms: pending ? null : 248_300, first_token_ms: null, output_tokens: 3000, reasoning_tokens: null, has_html: !pending, error_code: '', error_message: '', started_at: null, finished_at: null },
        { id: 22, model: 'gpt-6.1-sol', status: 'failed', attempt: 2, duration_ms: 5000, first_token_ms: null, output_tokens: null, reasoning_tokens: null, has_html: false, error_code: 'http_502', error_message: 'bad gateway', started_at: null, finished_at: null },
      ],
    },
    {
      id: 1,
      group_id: 127,
      trigger: 'manual',
      started_at: '2026-10-20T14:41:25Z',
      finished_at: '2026-10-20T14:43:30Z',
      runs: [
        { id: 11, model: 'gpt-6-astra', status: 'succeeded', attempt: 1, duration_ms: 124_600, first_token_ms: null, output_tokens: null, reasoning_tokens: null, has_html: true, error_code: '', error_message: '', started_at: null, finished_at: null },
      ],
    },
  ]
}

function detail(id: number, overrides: Partial<OtokPelicanRunDetail> = {}): OtokPelicanRunDetail {
  return {
    id, model: 'gpt-6-astra', status: 'succeeded', attempt: 1, duration_ms: 1, first_token_ms: null, output_tokens: null,
    reasoning_tokens: null, has_html: true, error_code: '', error_message: '', started_at: null, finished_at: null,
    round_id: 2, group_id: 127, reasoning_effort: 'medium', response_model: 'gpt-6-astra', input_tokens: null, http_status: 200,
    output_text: '```html\n' + HTML + '\n```', output_html: HTML, ...overrides,
  }
}

function source(list: () => OtokPelicanRound[]): OtokPelicanSource & { listRounds: ReturnType<typeof vi.fn>; getRun: ReturnType<typeof vi.fn> } {
  return {
    listRounds: vi.fn(async () => list()),
    getRun: vi.fn(async (id: number) => detail(id)),
  }
}

function mountDialog(src: OtokPelicanSource) {
  return mount(OtokPelicanDialog, {
    props: { show: true, groupId: 127, groupName: 'GPT 稳定', source: src },
    global: { stubs: { BaseDialog: { props: ['show', 'title'], template: '<div><h3>{{ title }}</h3><slot /></div>' } } },
  })
}

describe('OtokPelicanDialog', () => {
  beforeEach(() => setActivePinia(createPinia()))
  afterEach(() => vi.useRealTimers())

  it('列出轮次并默认预览最新一轮第一个有 HTML 的模型，iframe 不给任何沙箱权限', async () => {
    const src = source(() => rounds())
    const wrapper = mountDialog(src)
    await flushPromises()

    expect(wrapper.text()).toContain('otok.pelican.dialogTitle:{"name":"GPT 稳定"}')
    expect(wrapper.findAll('[data-testid^="otok-pelican-round-"]')).toHaveLength(2)
    expect(wrapper.text()).toContain('gpt-6-astra · 248.3s')
    expect(wrapper.text()).toContain('otok.pelican.roundStates.partial')
    expect(src.getRun).toHaveBeenCalledWith(21)

    const iframe = wrapper.find('[data-testid="otok-pelican-iframe"]')
    expect(iframe.exists()).toBe(true)
    expect(iframe.attributes('sandbox')).toBe('')
    expect(iframe.attributes('srcdoc')).toBe(HTML)

    // 停止预览即卸载 iframe
    await wrapper.find('[data-testid="otok-pelican-toggle-preview"]').trigger('click')
    expect(wrapper.find('[data-testid="otok-pelican-iframe"]').exists()).toBe(false)
  })

  it('源码以纯文本展示，不解析其中的 HTML', async () => {
    const wrapper = mountDialog(source(() => rounds()))
    await flushPromises()
    await wrapper.find('[data-testid="otok-pelican-toggle-source"]').trigger('click')
    const pre = wrapper.find('[data-testid="otok-pelican-source"]')
    expect(pre.text()).toContain('<script>alert(1)</script>')
    expect(pre.element.querySelector('script')).toBeNull()
  })

  it('切换到失败的模型显示错误原因，切换轮次会拉对应记录', async () => {
    const src = source(() => rounds())
    const wrapper = mountDialog(src)
    await flushPromises()
    const tabs = wrapper.findAll('[role="tab"]')
    await tabs[1].trigger('click')
    await flushPromises()
    expect(src.getRun).toHaveBeenCalledWith(22)
    expect(wrapper.find('[data-testid="otok-pelican-error"]').text()).toContain('otok.pelican.errorCodes.http_5xx')
    expect(wrapper.find('[data-testid="otok-pelican-error"]').text()).toContain('bad gateway')

    await wrapper.find('[data-testid="otok-pelican-round-1"]').trigger('click')
    await flushPromises()
    expect(src.getRun).toHaveBeenCalledWith(11)
  })

  it('有进行中的轮次时轮询，完成后停止', async () => {
    vi.useFakeTimers()
    let pending = true
    const src = source(() => rounds(pending))
    mountDialog(src)
    await flushPromises()
    expect(src.listRounds).toHaveBeenCalledTimes(1)

    pending = false
    await vi.advanceTimersByTimeAsync(15_000)
    expect(src.listRounds).toHaveBeenCalledTimes(2)

    await vi.advanceTimersByTimeAsync(60_000)
    expect(src.listRounds).toHaveBeenCalledTimes(2)
  })

  it('没有结果时显示空态', async () => {
    const wrapper = mountDialog(source(() => []))
    await flushPromises()
    expect(wrapper.find('[data-testid="otok-pelican-empty"]').exists()).toBe(true)
  })
})
