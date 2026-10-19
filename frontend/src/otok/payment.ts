/**
 * otok: USDT 链上充值（fork 定制）的常量与工具。
 * 上游文件只通过展开 / 调用本文件的导出接入，见 docs/otok/UPSTREAM_PATCHES.md。
 */
import type { ConfigFieldDef } from '@/components/payment/providerConfig'
import usdtIcon from '@/assets/icons/usdt.svg'

export const OTOK_USDT_PROVIDER_KEY = 'otok_usdt'

export const OTOK_USDT_PAYMENT_TYPES = [
  'otok_usdt_trc20',
  'otok_usdt_bep20',
  'otok_usdt_polygon',
  'otok_usdt_erc20',
] as const

export type OtokUsdtPaymentType = (typeof OTOK_USDT_PAYMENT_TYPES)[number]

export function isOtokUsdtPaymentType(type: string | null | undefined): type is OtokUsdtPaymentType {
  return !!type && (OTOK_USDT_PAYMENT_TYPES as readonly string[]).includes(type)
}

/** 合并进上游 PROVIDER_SUPPORTED_TYPES。 */
export const OTOK_PROVIDER_SUPPORTED_TYPES: Record<string, string[]> = {
  [OTOK_USDT_PROVIDER_KEY]: [...OTOK_USDT_PAYMENT_TYPES],
}

/** 合并进上游 PROVIDER_CONFIG_FIELDS；标签按 admin.settings.payment.field_<key> 取 i18n。 */
export const OTOK_PROVIDER_CONFIG_FIELDS: Record<string, ConfigFieldDef[]> = {
  [OTOK_USDT_PROVIDER_KEY]: [
    { key: 'exchangeRate', label: '', sensitive: false, hintKey: 'admin.settings.payment.field_exchangeRateHint' },
    { key: 'feeRate', label: '', sensitive: false, optional: true, clearable: true, defaultValue: '0', hintKey: 'admin.settings.payment.field_otokFeeRateHint' },
    { key: 'trc20Address', label: '', sensitive: false, optional: true, clearable: true },
    { key: 'bep20Address', label: '', sensitive: false, optional: true, clearable: true },
    { key: 'polygonAddress', label: '', sensitive: false, optional: true, clearable: true },
    { key: 'erc20Address', label: '', sensitive: false, optional: true, clearable: true, hintKey: 'admin.settings.payment.field_otokAddressHint' },
    { key: 'tronGridApiKey', label: '', sensitive: true, optional: true, hintKey: 'admin.settings.payment.field_tronGridApiKeyHint' },
    { key: 'bscRpcUrl', label: '', sensitive: false, optional: true, clearable: true },
    { key: 'polygonRpcUrl', label: '', sensitive: false, optional: true, clearable: true },
    { key: 'ethRpcUrl', label: '', sensitive: false, optional: true, clearable: true, hintKey: 'admin.settings.payment.field_otokRpcUrlHint' },
    { key: 'expireMinutes', label: '', sensitive: false, optional: true, clearable: true, defaultValue: '30' },
    { key: 'graceMinutes', label: '', sensitive: false, optional: true, clearable: true, defaultValue: '60', hintKey: 'admin.settings.payment.field_graceMinutesHint' },
  ],
}

/**
 * 预估应付 USDT：ceil(站内金额 × (1 + 手续费%/100) ÷ 汇率, 2 位小数)。
 * 与后端 otokUSDTQuoteCents 同一公式；实际金额另有 0.00~0.99 的识别尾差。
 */
export function otokEstimateUsdt(amount: number, exchangeRate: number, feeRatePercent: number): number {
  if (!(amount > 0) || !(exchangeRate > 0)) return 0
  const fee = feeRatePercent > 0 ? feeRatePercent : 0
  // 先按分取整再除，规避浮点误差（例如 71 / 7.1）
  const cents = Math.ceil(Number(((Math.round(amount * 100) * (100 + fee)) / 100 / exchangeRate).toFixed(6)))
  return Math.max(cents, 1) / 100
}

export function formatUsdt(amount: number | string): string {
  const n = typeof amount === 'string' ? Number(amount) : amount
  if (!Number.isFinite(n)) return String(amount)
  return n.toFixed(2)
}

/** 合并进上游 PaymentMethodSelector 的 METHOD_ICONS。 */
export const OTOK_METHOD_ICONS: Record<string, string> = Object.fromEntries(
  OTOK_USDT_PAYMENT_TYPES.map((type) => [type, usdtIcon]),
)
