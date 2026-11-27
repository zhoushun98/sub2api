import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import type { ModelPlazaGroup, ModelPlazaResponse, PlazaModel } from '@/api/modelPlaza'

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: vi.fn().mockResolvedValue(true) })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => (params ? `${key}:${JSON.stringify(params)}` : key)
    })
  }
})

import OtokModelPlazaContent from '../modelPlaza/OtokModelPlazaContent.vue'

function model(name: string, platform: string, input: number): PlazaModel {
  return {
    name,
    platform,
    pricing: {
      billing_mode: 'token',
      input_price: input,
      output_price: input * 5,
      cache_write_price: null,
      cache_read_price: input / 10,
      image_input_price: null,
      image_output_price: null,
      per_request_price: null,
      intervals: []
    },
    official_pricing: null
  }
}

function group(id: number, platform: string, rate: number, models: PlazaModel[]): ModelPlazaGroup {
  return {
    id,
    name: `分组${id}`,
    description: '',
    platform,
    subscription_type: 'standard',
    rate_multiplier: rate,
    peak_rate_enabled: false,
    peak_start: '',
    peak_end: '',
    peak_rate_multiplier: 1,
    is_exclusive: false,
    image_rate_independent: false,
    image_rate_multiplier: 1,
    video_rate_independent: false,
    video_rate_multiplier: 1,
    long_context_pricing_enabled: true,
    models
  }
}

const response: ModelPlazaResponse = {
  description: '',
  groups: [
    group(1, 'openai', 0.1, [model('gpt-6.2-sol', 'openai', 2e-6)]),
    group(2, 'openai', 0.3, [model('gpt-6.2-sol', 'openai', 2e-6), model('gpt-6-astra', 'openai', 10e-6)]),
    group(3, 'deepseek', 0.1, [model('deepseek-v4-pro', 'deepseek', 4.5e-6)])
  ]
}

function mountContent() {
  return mount(OtokModelPlazaContent, {
    props: { response, loading: false, embedded: true },
    global: { stubs: { PlatformIcon: true, Icon: true } }
  })
}

describe('OtokModelPlazaContent', () => {
  it('按平台分节、模型去重，只显示标准价，不出现分组和倍率', () => {
    const w = mountContent()
    expect(w.findAll('section').map((s) => s.attributes('data-test'))).toEqual(['section-openai', 'section-deepseek'])
    expect(w.findAll('[data-test="model-gpt-6.2-sol"]')).toHaveLength(1)
    const row = w.get('[data-test="model-gpt-6.2-sol"]').text()
    expect(row).toContain('$2.00')
    expect(row).toContain('$10.00')
    expect(w.text()).not.toContain('分组1')
    expect(w.text()).not.toContain('0.1x')
    expect(w.text()).not.toContain('modelPlaza.table.officialPrice')
    expect(w.text()).not.toContain('modelPlaza.table.paidPrice')
  })

  it('点平台只显示该平台', async () => {
    const w = mountContent()
    await w.get('[data-test="platform-deepseek"]').trigger('click')
    expect(w.findAll('section').map((s) => s.attributes('data-test'))).toEqual(['section-deepseek'])
    expect(w.get('[data-test="model-deepseek-v4-pro"]').text()).toContain('$4.50')
    await w.get('[data-test="platform-all"]').trigger('click')
    expect(w.findAll('section')).toHaveLength(2)
  })

  it('搜索无结果时显示提示', async () => {
    const w = mountContent()
    await w.get('input').setValue('not-exist')
    expect(w.findAll('section')).toHaveLength(0)
    expect(w.text()).toContain('modelPlaza.noSearchResult')
  })
})

describe('OtokModelPlazaContent 价格版本与分时', () => {
  const ds = (timePricing: boolean): PlazaModel => ({
    ...model('deepseek-v4.1-flash', 'deepseek', 1e-6),
    ...(timePricing
      ? {
          time_pricing: {
            timezone: 'Asia/Shanghai',
            weekdays_only: true,
            periods: [
              { start_time: '09:00:00', end_time: '12:00:00', multiplier: 2 },
              { start_time: '14:00:00', end_time: '18:00:00', multiplier: 2 }
            ]
          }
        }
      : {})
  })
  const twoGroups: ModelPlazaResponse = {
    description: '',
    groups: [
      { ...group(1, 'deepseek', 0.2, [ds(false)]), name: 'DeepSeek' },
      { ...group(2, 'deepseek', 0.1, [ds(true)]), name: 'DeepSeek 分时' }
    ]
  }

  it('固定价和分时分组各一行并标分组，分时下面跟高峰行', () => {
    const w = mount(OtokModelPlazaContent, {
      props: { response: twoGroups, loading: false, embedded: true },
      global: { stubs: { PlatformIcon: true, Icon: true } }
    })
    const bases = w.findAll('[data-test="model-deepseek-v4.1-flash"]')
    expect(bases).toHaveLength(2)
    expect(bases[0].text()).toContain('otok.modelPlaza.groupsBadge:{"names":"DeepSeek"}')
    expect(bases[0].text()).not.toContain('otok.modelPlaza.otherHours')
    expect(bases[1].text()).toContain('otok.modelPlaza.groupsBadge:{"names":"DeepSeek 分时"}')
    expect(bases[1].text()).toContain('otok.modelPlaza.otherHours')
    expect(bases[1].text()).toContain('$1.00')
    const tier = w.get('[data-test="model-deepseek-v4.1-flash-tier"]').text()
    expect(tier).toContain('otok.modelPlaza.timeTierWeekdays:{"windows":"09:00–12:00、14:00–18:00","multiplier":2}')
    expect(tier).toContain('$2.00')
    expect(tier).toContain('$10.00')
  })
})

describe('OtokModelPlazaContent 缓存写入为 0', () => {
  it('缓存写入价为 0 时显示 "-"', () => {
    const m = model('glm-5.3', 'zhipu', 8e-6)
    m.pricing!.cache_write_price = 0
    const w = mount(OtokModelPlazaContent, {
      props: { response: { description: '', groups: [group(1, 'zhipu', 0.1, [m])] }, loading: false, embedded: true },
      global: { stubs: { PlatformIcon: true, Icon: true } }
    })
    const cells = w.get('[data-test="model-glm-5.3"]').findAll('td')
    expect(cells[3].text()).toBe('-')
  })
})
