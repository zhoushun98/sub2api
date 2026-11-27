import { describe, expect, it } from 'vitest'
import type { UserSupportedModelPricing } from '@/api/channels'
import type { ModelPlazaGroup, PlazaModel, PlazaTimePricing } from '@/api/modelPlaza'
import { otokFilterPlazaPlatforms, otokPlazaPlatforms, otokPlazaPrice, otokPlazaTimeTiers, otokScalePrice } from '../modelPlaza'

function pricing(overrides: Partial<UserSupportedModelPricing> = {}): UserSupportedModelPricing {
  return {
    billing_mode: 'token',
    input_price: 2e-6,
    output_price: 10e-6,
    cache_write_price: 2.5e-6,
    cache_read_price: 0.1e-6,
    image_input_price: null,
    image_output_price: null,
    per_request_price: null,
    intervals: [],
    ...overrides
  }
}

function model(name: string, platform: string, overrides: Partial<PlazaModel> = {}): PlazaModel {
  return { name, platform, pricing: pricing(), official_pricing: null, ...overrides }
}

function group(id: number, platform: string, rate: number, models: PlazaModel[], name = `g${id}`): ModelPlazaGroup {
  return {
    id,
    name,
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

/** DeepSeek 官方峰谷：北京时间工作日 09–12、14–18 点 ×2。 */
const deepseekPeak: PlazaTimePricing = {
  timezone: 'Asia/Shanghai',
  weekdays_only: true,
  periods: [
    { start_time: '14:00:00', end_time: '18:00:00', multiplier: 2 },
    { start_time: '09:00:00', end_time: '12:00:00', multiplier: 2 }
  ]
}

const flash = () => pricing({ input_price: 1e-6, output_price: 4e-6, cache_write_price: null, cache_read_price: 0.02e-6 })

describe('otokPlazaPlatforms', () => {
  it('价格相同的同名模型只列一行、不标分组；平台按固定顺序、模型按版本号倒序', () => {
    const list = otokPlazaPlatforms([
      group(1, 'anthropic', 0.08, [model('claude-opus-5', 'anthropic')]),
      group(2, 'openai', 0.1, [model('gpt-6.1-sol', 'openai'), model('gpt-5.6-sol', 'openai')]),
      group(3, 'openai', 0.15, [model('gpt-6.1-sol', 'openai'), model('gpt-6.2-sol', 'openai'), model('gpt-6.10-sol', 'openai')]),
      group(4, 'anthropic', 0.3, [model('claude-opus-5', 'anthropic')])
    ])
    expect(list.map((p) => p.platform)).toEqual(['openai', 'anthropic'])
    expect(list[0].rows.map((r) => r.name)).toEqual(['gpt-6.10-sol', 'gpt-6.2-sol', 'gpt-6.1-sol', 'gpt-5.6-sol'])
    expect(list[0].rows.every((r) => r.groups.length === 0)).toBe(true)
    expect(list[0].modelCount).toBe(4)
    expect(list[1].rows).toHaveLength(1)
  })

  it('价格不同时每个版本一行，并标出适用的分组', () => {
    const list = otokPlazaPlatforms([
      group(1, 'anthropic', 0.08, [model('claude-opus-5', 'anthropic')], 'Kiro'),
      group(2, 'anthropic', 0.1, [model('claude-opus-5', 'anthropic', { pricing: pricing({ input_price: 3e-6 }) })], 'Max'),
      group(3, 'anthropic', 0.3, [model('claude-opus-5', 'anthropic')], 'Kiro 正价')
    ])
    const rows = list[0].rows
    expect(rows.map((r) => [r.price?.input, r.groups])).toEqual([
      [2e-6, ['Kiro', 'Kiro 正价']],
      [3e-6, ['Max']]
    ])
    expect(list[0].modelCount).toBe(1)
  })

  it('固定价分组 + 分时分组：两个版本，分时版本下面跟一行高峰价', () => {
    const list = otokPlazaPlatforms([
      group(1, 'deepseek', 0.1, [model('deepseek-v4.1-flash', 'deepseek', { pricing: flash() })], 'DeepSeek'),
      group(2, 'deepseek', 0.1, [model('deepseek-v4.1-flash', 'deepseek', { pricing: flash(), time_pricing: deepseekPeak })], 'DeepSeek 分时')
    ])
    const rows = list[0].rows
    expect(rows.map((r) => [r.first, r.groups, r.hasTimeTiers, r.timeTier?.windows ?? null, r.price?.input])).toEqual([
      [true, ['DeepSeek'], false, null, 1e-6],
      [true, ['DeepSeek 分时'], true, null, 1e-6],
      [false, ['DeepSeek 分时'], false, ['09:00–12:00', '14:00–18:00'], 2e-6]
    ])
    expect(rows[2].price?.output).toBe(8e-6)
    expect(rows[2].timeTier?.weekdaysOnly).toBe(true)
  })

  it('只有一个分时版本时不标分组，但仍有时段行', () => {
    const list = otokPlazaPlatforms([
      group(1, 'deepseek', 0.1, [model('deepseek-v4-pro', 'deepseek', { time_pricing: deepseekPeak })])
    ])
    expect(list[0].rows.map((r) => [r.groups.length, r.timeTier ? 'tier' : 'base'])).toEqual([
      [0, 'base'],
      [0, 'tier']
    ])
  })

  it('思考等级倍率不同也算不同版本', () => {
    const list = otokPlazaPlatforms([
      group(1, 'openai', 0.1, [model('gpt-6-sol', 'openai')]),
      group(2, 'openai', 0.2, [model('gpt-6-sol', 'openai', { pricing: pricing({ reasoning_effort_multipliers: { xhigh: 2 } }) })])
    ])
    expect(list[0].rows).toHaveLength(2)
  })

  it('有价格的版本存在时，丢掉没有价格的版本', () => {
    const list = otokPlazaPlatforms([
      group(1, 'openai', 0.1, [model('gpt-6-sol', 'openai', { pricing: null })]),
      group(2, 'openai', 0.2, [model('gpt-6-sol', 'openai')])
    ])
    expect(list[0].rows).toHaveLength(1)
    expect(list[0].rows[0].price?.input).toBe(2e-6)
    expect(list[0].rows[0].groups).toEqual([])
  })

  it('模型自带平台时按模型平台归类（Composite 分组）', () => {
    const list = otokPlazaPlatforms([
      group(1, 'composite', 0.1, [model('gpt-6-sol', 'openai'), model('claude-opus-5', 'anthropic')])
    ])
    expect(list.map((p) => p.platform)).toEqual(['openai', 'anthropic'])
  })
})

describe('otokPlazaTimeTiers', () => {
  it('同倍率的时段合并、按开始时间排序；倍率为 1 的时段忽略', () => {
    const tiers = otokPlazaTimeTiers(
      model('x', 'deepseek', {
        time_pricing: {
          timezone: 'Asia/Shanghai',
          periods: [
            { start_time: '14:00:00', end_time: '18:00:00', multiplier: 2 },
            { start_time: '00:30', end_time: '08:30', multiplier: 0.5 },
            { start_time: '09:00:00', end_time: '12:00:00', multiplier: 2 },
            { start_time: '20:00:00', end_time: '21:00:00', multiplier: 1 }
          ]
        }
      }),
      otokPlazaPrice(model('x', 'deepseek'))
    )
    expect(tiers.map((t) => [t.multiplier, t.windows, t.weekdaysOnly])).toEqual([
      [2, ['09:00–12:00', '14:00–18:00'], false],
      [0.5, ['00:30–08:30'], false]
    ])
  })

  it('按次 / 按图计费不显示时段（分时倍率只对 token 计费生效）', () => {
    const m = model('img', 'openai', { pricing: pricing({ billing_mode: 'image', per_request_price: 0.1 }), time_pricing: deepseekPeak })
    expect(otokPlazaTimeTiers(m, otokPlazaPrice(m))).toEqual([])
  })
})

describe('otokScalePrice', () => {
  it('所有单价和分档都乘倍率，并去掉浮点噪声', () => {
    const base = otokPlazaPrice(model('x', 'openai', {
      pricing: pricing({
        input_price: 0.1e-6,
        cache_write_12h_price: 5e-6,
        intervals: [
          { min_tokens: 0, max_tokens: 272000, input_price: 0.1e-6, output_price: 10e-6, cache_write_price: 2.5e-6, cache_read_price: 0.1e-6, per_request_price: null },
          { min_tokens: 272000, max_tokens: null, input_price: 0.2e-6, output_price: 15e-6, cache_write_price: 5e-6, cache_read_price: 0.2e-6, per_request_price: null }
        ]
      })
    }))!
    const scaled = otokScalePrice(base, 3)
    expect(scaled.input).toBe(0.3e-6)
    expect(scaled.cacheWrite12h).toBe(15e-6)
    expect(scaled.intervals[1].input_price).toBe(0.6e-6)
    expect(base.input).toBe(0.1e-6)
  })
})

describe('otokPlazaPrice', () => {
  it('用渠道价，不乘分组倍率', () => {
    const p = otokPlazaPrice(model('deepseek-v4-pro', 'deepseek', {
      pricing: pricing({ input_price: 4.5e-6, output_price: 13.5e-6 }),
      official_pricing: { input_price: 0.66e-6, output_price: 1.98e-6, cache_write_price: null, cache_read_price: 0.022e-6 }
    }))
    expect(p?.input).toBe(4.5e-6)
    expect(p?.output).toBe(13.5e-6)
  })

  it('没有渠道价时退回官方价卡', () => {
    const p = otokPlazaPrice(model('x', 'openai', {
      pricing: null,
      official_pricing: { input_price: 1e-6, output_price: 2e-6, cache_write_price: null, cache_read_price: null }
    }))
    expect(p?.input).toBe(1e-6)
    expect(p?.billingMode).toBe('token')
  })

  it('多档时按档给出绝对单价，倍率档按基础价换算', () => {
    const p = otokPlazaPrice(model('gpt-6.2-astra', 'openai', {
      pricing: pricing({
        input_price: 8e-6,
        output_price: 40e-6,
        cache_write_price: 10e-6,
        cache_write_12h_price: 20e-6,
        cache_read_price: 0.4e-6,
        intervals: [
          { min_tokens: 272000, max_tokens: null, input_price: null, input_multiplier: 2, output_price: 60e-6, cache_write_price: null, cache_read_price: null, per_request_price: null },
          { min_tokens: 0, max_tokens: 272000, input_price: 8e-6, output_price: 40e-6, cache_write_price: 10e-6, cache_read_price: 0.4e-6, per_request_price: null }
        ]
      })
    }))
    expect(p?.intervals.map((iv) => iv.min_tokens)).toEqual([0, 272000])
    expect(p?.intervals[1].input_price).toBe(16e-6)
    expect(p?.intervals[1].output_price).toBe(60e-6)
  })

  it('基础价没有 1h 价时，分档里也不出现 1h；有 1h 价时保留', () => {
    const ivs = [
      { min_tokens: 0, max_tokens: 272000, input_price: 2e-6, output_price: 10e-6, cache_write_price: 2.5e-6, cache_read_price: 0.1e-6, per_request_price: null },
      { min_tokens: 272000, max_tokens: null, input_price: 4e-6, output_price: 15e-6, cache_write_price: 5e-6, cache_read_price: 0.2e-6, per_request_price: null }
    ]
    const openai = otokPlazaPrice(model('gpt-6.2-sol', 'openai', { pricing: pricing({ intervals: ivs }) }))
    expect(openai?.intervals.map((iv) => iv.cache_write_1h_price)).toEqual([null, null])
    const claude = otokPlazaPrice(model('claude-haiku-5-5', 'anthropic', { pricing: pricing({ cache_write_1h_price: 0.2e-6, intervals: ivs }) }))
    expect(claude?.intervals[0].cache_write_1h_price).not.toBeNull()
  })

  it('按图计费只保留配了按次价的档', () => {
    const p = otokPlazaPrice(model('gpt-image-2', 'openai', {
      pricing: pricing({
        billing_mode: 'image',
        per_request_price: 0.1,
        intervals: [
          { min_tokens: 0, max_tokens: null, tier_label: '1K', input_price: null, output_price: null, cache_write_price: null, cache_read_price: null, per_request_price: 0.1 },
          { min_tokens: 0, max_tokens: null, tier_label: '4K', input_price: null, output_price: null, cache_write_price: null, cache_read_price: null, per_request_price: null }
        ]
      })
    }))
    expect(p?.requestTiers.map((iv) => iv.tier_label)).toEqual(['1K'])
    expect(p?.intervals).toEqual([])
  })

  it('既无渠道价也无官方价时为 null', () => {
    expect(otokPlazaPrice(model('x', 'openai', { pricing: null, official_pricing: null }))).toBeNull()
  })
})

describe('otokFilterPlazaPlatforms', () => {
  const list = otokPlazaPlatforms([
    group(1, 'openai', 0.1, [model('gpt-6-sol', 'openai'), model('gpt-6-astra', 'openai')]),
    group(2, 'deepseek', 0.1, [model('deepseek-v4-pro', 'deepseek', { time_pricing: deepseekPeak })])
  ])

  it('按平台过滤', () => {
    expect(otokFilterPlazaPlatforms(list, 'deepseek', '').map((p) => p.platform)).toEqual(['deepseek'])
  })

  it('按模型名搜索，时段行跟着基础行保留，无命中的平台整段隐藏', () => {
    expect(otokFilterPlazaPlatforms(list, 'all', 'ASTRA').map((p) => [p.platform, p.rows.length, p.modelCount])).toEqual([
      ['openai', 1, 1]
    ])
    expect(otokFilterPlazaPlatforms(list, 'all', 'v4-pro')[0].rows).toHaveLength(2)
  })
})
