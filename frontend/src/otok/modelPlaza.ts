/**
 * otok: 模型广场按平台展示。
 *
 * 上游按分组分节，每个分组一张价格表（实付价、官方价、折扣倍率三块），分组多了很乱。
 * fork 改为按平台汇总，只显示标准价，也就是渠道定价里的单价（实际扣费 = 标准价 × 分组倍率）：
 *   - 同一模型在各分组的价格（含分时倍率、思考等级倍率）完全相同时只列一行，不显示分组；
 *   - 价格不同时每个价格版本各列一行，并标出适用的分组；
 *   - 配了分时倍率的（只对 token 计费生效），在基础价那行下面按倍率各加一行时段价。
 *
 * 价格取接口里的 `pricing`（计费实际使用的渠道价，未乘分组倍率），没有时退回
 * `official_pricing`（渠道未配置定价时，计费同样回落到这份价卡）。
 * 不直接用 official_pricing：国产模型的渠道价按人民币数字填写，与 LiteLLM 美元价不同，
 * 只有渠道价才满足「实付 = 标准价 × 倍率」。
 */
import type { UserPricingInterval } from '@/api/channels'
import { resolveIntervalPrices } from '@/utils/pricing'
import type { ModelPlazaGroup, PlazaModel } from '@/api/modelPlaza'

/** 展示用的单价（USD per token / per request），字段缺失为 null。 */
export interface OtokPlazaPrice {
  billingMode: string
  input: number | null
  output: number | null
  cacheWrite: number | null
  cacheWrite1h: number | null
  cacheWrite12h: number | null
  cacheRead: number | null
  perRequest: number | null
  /** token 计费的上下文分档（已换算成绝对单价，按下限升序）；单档为空。 */
  intervals: UserPricingInterval[]
  /** 按次 / 按图计费的分档（只保留配了按次价的档）。 */
  requestTiers: UserPricingInterval[]
}

/** 分时倍率：同一倍率的多个时段合并成一档。 */
export interface OtokPlazaTimeTier {
  multiplier: number
  /** 「09:00–12:00」形式，按开始时间升序。 */
  windows: string[]
  weekdaysOnly: boolean
  timezone: string
}

/** 表格的一行。 */
export interface OtokPlazaRow {
  key: string
  name: string
  /** 本行价格（时段行已乘倍率）。 */
  price: OtokPlazaPrice | null
  model: PlazaModel
  /** 同名模型有多个价格版本时，列出适用的分组；否则为空。 */
  groups: string[]
  /** 时段行对应的分时档；基础行为 null。 */
  timeTier: OtokPlazaTimeTier | null
  /** 基础行且该版本配了分时倍率（展示「其他时段」）。 */
  hasTimeTiers: boolean
  /** 同一模型版本的第一行（时段行为 false），用于决定是否显示模型名。 */
  first: boolean
}

export interface OtokPlazaPlatform {
  platform: string
  /** 去重后的模型数（不计价格版本和时段行）。 */
  modelCount: number
  rows: OtokPlazaRow[]
}

/** 平台展示顺序：常用的排前面，其余按字母序。 */
const PLATFORM_ORDER = ['openai', 'anthropic', 'gemini', 'grok', 'deepseek', 'kimi', 'zhipu']

function platformRank(p: string): number {
  const idx = PLATFORM_ORDER.indexOf(p)
  return idx === -1 ? PLATFORM_ORDER.length : idx
}

function sortByContext(intervals: UserPricingInterval[]): UserPricingInterval[] {
  return [...intervals].sort((a, b) => a.min_tokens - b.min_tokens)
}

/** 合成单个模型的展示价：渠道价优先，没有时用官方价卡。 */
export function otokPlazaPrice(m: PlazaModel): OtokPlazaPrice | null {
  const p = m.pricing
  if (p) {
    const billingMode = p.billing_mode || 'token'
    const ivs = sortByContext(p.intervals ?? [])
    const cacheWrite1h = p.cache_write_1h_price ?? null
    return {
      billingMode,
      input: p.input_price,
      output: p.output_price,
      cacheWrite: p.cache_write_price,
      cacheWrite1h,
      cacheWrite12h: p.cache_write_12h_price ?? null,
      cacheRead: p.cache_read_price,
      perRequest: p.per_request_price,
      // resolveIntervalPrices 会把 5m 写入价顺带填进 1h，基础价没有 1h 价（如 OpenAI）时去掉，避免误显示 1h 档。
      intervals:
        billingMode === 'token' && ivs.length > 1
          ? ivs.map((iv) => {
              const r = resolveIntervalPrices(iv, p)
              return cacheWrite1h == null && iv.cache_write_1h_price == null ? { ...r, cache_write_1h_price: null } : r
            })
          : [],
      requestTiers: billingMode === 'token' ? [] : ivs.filter((iv) => iv.per_request_price != null)
    }
  }
  const o = m.official_pricing
  if (o) {
    const ivs = sortByContext(o.intervals ?? [])
    return {
      billingMode: 'token',
      input: o.input_price,
      output: o.output_price,
      cacheWrite: o.cache_write_price,
      cacheWrite1h: o.cache_write_1h_price ?? null,
      cacheWrite12h: o.cache_write_12h_price ?? null,
      cacheRead: o.cache_read_price,
      perRequest: null,
      intervals: ivs.length > 1 ? ivs : [],
      requestTiers: []
    }
  }
  return null
}

/** 分时倍率按倍率合并成档（只对 token 计费生效，与后端 calculateTokenCost 一致）。 */
export function otokPlazaTimeTiers(m: PlazaModel, price: OtokPlazaPrice | null): OtokPlazaTimeTier[] {
  const tp = m.time_pricing
  if (!tp || !price || price.billingMode !== 'token') return []
  const clock = (v: string) => v.replace(/^(\d{2}:\d{2}):00$/, '$1')
  const tiers: OtokPlazaTimeTier[] = []
  const periods = [...(tp.periods ?? [])]
    .filter((p) => p.multiplier !== 1)
    .sort((a, b) => a.start_time.localeCompare(b.start_time))
  for (const p of periods) {
    let tier = tiers.find((t) => t.multiplier === p.multiplier)
    if (!tier) {
      tier = { multiplier: p.multiplier, windows: [], weekdaysOnly: tp.weekdays_only === true, timezone: tp.timezone }
      tiers.push(tier)
    }
    tier.windows.push(`${clock(p.start_time)}–${clock(p.end_time)}`)
  }
  return tiers.sort((a, b) => b.multiplier - a.multiplier)
}

/** 把展示价整体乘一个倍率（时段行用），去掉浮点噪声。 */
export function otokScalePrice(price: OtokPlazaPrice, multiplier: number): OtokPlazaPrice {
  const mul = (v: number | null | undefined) => (v == null ? null : Number((v * multiplier).toPrecision(12)))
  const scaleIv = (iv: UserPricingInterval): UserPricingInterval => ({
    ...iv,
    input_price: mul(iv.input_price),
    output_price: mul(iv.output_price),
    cache_write_price: mul(iv.cache_write_price),
    cache_write_1h_price: mul(iv.cache_write_1h_price),
    cache_write_12h_price: mul(iv.cache_write_12h_price),
    cache_read_price: mul(iv.cache_read_price),
    per_request_price: mul(iv.per_request_price)
  })
  return {
    ...price,
    input: mul(price.input),
    output: mul(price.output),
    cacheWrite: mul(price.cacheWrite),
    cacheWrite1h: mul(price.cacheWrite1h),
    cacheWrite12h: mul(price.cacheWrite12h),
    cacheRead: mul(price.cacheRead),
    perRequest: mul(price.perRequest),
    intervals: price.intervals.map(scaleIv),
    requestTiers: price.requestTiers.map(scaleIv)
  }
}

/** 价格版本的指纹：展示价 + 分时倍率 + 思考等级倍率都相同才算同一版本。 */
function variantKey(price: OtokPlazaPrice | null, m: PlazaModel): string {
  const efforts = Object.entries(m.pricing?.reasoning_effort_multipliers ?? {})
    .filter(([, v]) => v > 0 && v !== 1)
    .sort(([a], [b]) => a.localeCompare(b))
  return JSON.stringify([price, otokPlazaTimeTiers(m, price), efforts])
}

interface Variant {
  key: string
  price: OtokPlazaPrice | null
  model: PlazaModel
  groups: string[]
}

/**
 * 把分组列表汇总成平台列表。同一平台同名模型按价格版本归并（接口里分组已按倍率升序，
 * 版本按首次出现的顺序排列）；有价格的版本存在时，丢掉没有价格的版本。
 * 模型名按版本号倒序（新版本在前）。
 */
export function otokPlazaPlatforms(groups: ModelPlazaGroup[]): OtokPlazaPlatform[] {
  const byPlatform = new Map<string, Map<string, Variant[]>>()
  for (const g of groups) {
    for (const m of g.models ?? []) {
      const platform = m.platform || g.platform
      if (!platform) continue
      let models = byPlatform.get(platform)
      if (!models) {
        models = new Map()
        byPlatform.set(platform, models)
      }
      let variants = models.get(m.name)
      if (!variants) {
        variants = []
        models.set(m.name, variants)
      }
      const price = otokPlazaPrice(m)
      const key = variantKey(price, m)
      const existing = variants.find((v) => v.key === key)
      if (existing) {
        if (!existing.groups.includes(g.name)) existing.groups.push(g.name)
      } else {
        variants.push({ key, price, model: m, groups: [g.name] })
      }
    }
  }

  return [...byPlatform.entries()]
    .map(([platform, models]) => {
      const names = [...models.keys()].sort((a, b) => b.localeCompare(a, undefined, { numeric: true, sensitivity: 'base' }))
      const rows: OtokPlazaRow[] = []
      for (const name of names) {
        let variants = models.get(name) ?? []
        if (variants.some((v) => v.price != null)) variants = variants.filter((v) => v.price != null)
        const showGroups = variants.length > 1
        variants.forEach((v, vi) => {
          const tiers = otokPlazaTimeTiers(v.model, v.price)
          const groupsShown = showGroups ? v.groups : []
          rows.push({
            key: `${platform}:${name}:${vi}`,
            name,
            price: v.price,
            model: v.model,
            groups: groupsShown,
            timeTier: null,
            hasTimeTiers: tiers.length > 0,
            first: true
          })
          tiers.forEach((tier, ti) => {
            rows.push({
              key: `${platform}:${name}:${vi}:t${ti}`,
              name,
              price: v.price ? otokScalePrice(v.price, tier.multiplier) : null,
              model: v.model,
              groups: groupsShown,
              timeTier: tier,
              hasTimeTiers: false,
              first: false
            })
          })
        })
      }
      return { platform, modelCount: names.length, rows }
    })
    .sort((a, b) => platformRank(a.platform) - platformRank(b.platform) || a.platform.localeCompare(b.platform))
}

/** 按平台和模型名关键字过滤；关键字为空时只按平台过滤。 */
export function otokFilterPlazaPlatforms(
  list: OtokPlazaPlatform[],
  platform: string,
  query: string
): OtokPlazaPlatform[] {
  const q = query.trim().toLowerCase()
  return list
    .filter((p) => platform === 'all' || p.platform === platform)
    .map((p) => {
      if (!q) return p
      const rows = p.rows.filter((r) => r.name.toLowerCase().includes(q))
      return { ...p, rows, modelCount: new Set(rows.map((r) => r.name)).size }
    })
    .filter((p) => p.rows.length > 0)
}
