import { describe, expect, it } from 'vitest'
import { PROVIDER_CONFIG_FIELDS, PROVIDER_SUPPORTED_TYPES } from '@/components/payment/providerConfig'
import { OTOK_METHOD_ICONS, OTOK_USDT_PAYMENT_TYPES, isOtokUsdtPaymentType, otokEstimateUsdt } from '../payment'
import { otokMergeLocale } from '../i18nMerge'

describe('otok payment helpers', () => {
  it('只识别 otok 命名空间下的 USDT 支付类型', () => {
    for (const type of OTOK_USDT_PAYMENT_TYPES) {
      expect(isOtokUsdtPaymentType(type)).toBe(true)
    }
    expect(isOtokUsdtPaymentType('usdt_trc20')).toBe(false)
    expect(isOtokUsdtPaymentType('alipay')).toBe(false)
    expect(isOtokUsdtPaymentType('')).toBe(false)
    expect(isOtokUsdtPaymentType(undefined)).toBe(false)
  })

  it('预估公式与后端 otokUSDTQuoteCents 一致', () => {
    expect(otokEstimateUsdt(100, 7.1, 0)).toBe(14.09)
    expect(otokEstimateUsdt(71, 7.1, 0)).toBe(10)
    expect(otokEstimateUsdt(100, 7.1, 1.5)).toBe(14.3)
    expect(otokEstimateUsdt(0.01, 7.1, 0)).toBe(0.01)
    expect(otokEstimateUsdt(100, 0, 0)).toBe(0)
    expect(otokEstimateUsdt(0, 7.1, 0)).toBe(0)
  })

  it('合并进上游服务商配置且不影响原有服务商', () => {
    expect(PROVIDER_SUPPORTED_TYPES.otok_usdt).toEqual([...OTOK_USDT_PAYMENT_TYPES])
    expect(PROVIDER_SUPPORTED_TYPES.easypay).toEqual(['alipay', 'wxpay'])
    const keys = PROVIDER_CONFIG_FIELDS.otok_usdt.map((f) => f.key)
    expect(keys).toContain('exchangeRate')
    expect(keys).toContain('trc20Address')
    expect(PROVIDER_CONFIG_FIELDS.otok_usdt.find((f) => f.key === 'tronGridApiKey')?.sensitive).toBe(true)
    expect(PROVIDER_CONFIG_FIELDS.otok_usdt.find((f) => f.key === 'exchangeRate')?.optional).toBeFalsy()
  })
})

describe('otokMergeLocale', () => {
  it('逐层合并而不是整块覆盖', () => {
    const base = { payment: { methods: { alipay: '支付宝' }, title: '充值' }, common: { ok: '确定' } }
    const merged = otokMergeLocale(base, { payment: { methods: { otok_usdt_trc20: 'USDT-TRC20' } }, otok: { a: 'b' } })
    expect(merged).toEqual({
      payment: { methods: { alipay: '支付宝', otok_usdt_trc20: 'USDT-TRC20' }, title: '充值' },
      common: { ok: '确定' },
      otok: { a: 'b' },
    })
    expect(base.payment.methods).toEqual({ alipay: '支付宝' })
  })
})

describe('OTOK_METHOD_ICONS', () => {
  it('每个网络都有独立的链图标', () => {
    const icons = OTOK_USDT_PAYMENT_TYPES.map((type) => OTOK_METHOD_ICONS[type])
    expect(icons.every(Boolean)).toBe(true)
    expect(new Set(icons).size).toBe(OTOK_USDT_PAYMENT_TYPES.length)
  })
})
