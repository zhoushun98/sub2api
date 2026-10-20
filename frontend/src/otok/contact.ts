/**
 * otok: 「联系我们」的数据缓存与纯函数。
 *
 * 每次切路由 AppLayout 都会重新挂载侧边栏，所以缓存放在模块级而不是组件里。
 * 摘要在侧边栏挂载时拉一次决定入口显隐；二维码体积大，第一次打开弹窗才拉。
 */
import { ref } from 'vue'
import { otokContactAPI, type OtokContactQRCodes, type OtokContactSummary } from '@/api/otok/contact'

export const otokContactSummary = ref<OtokContactSummary | null>(null)
export const otokContactQRCodes = ref<OtokContactQRCodes | null>(null)

let summaryPromise: Promise<OtokContactSummary | null> | null = null
let qrPromise: Promise<OtokContactQRCodes | null> | null = null

/** 拉取摘要；并发调用只发一次请求，失败后下次挂载会重试。 */
export function ensureOtokContactSummary(): Promise<OtokContactSummary | null> {
  if (otokContactSummary.value) return Promise.resolve(otokContactSummary.value)
  if (!summaryPromise) {
    summaryPromise = otokContactAPI
      .getSummary()
      .then((res) => {
        otokContactSummary.value = res.data
        return res.data
      })
      .catch(() => {
        summaryPromise = null
        return null
      })
  }
  return summaryPromise
}

/** 拉取二维码；与摘要版本一致时复用缓存。 */
export function ensureOtokContactQRCodes(): Promise<OtokContactQRCodes | null> {
  const version = otokContactSummary.value?.version
  if (otokContactQRCodes.value && otokContactQRCodes.value.version === version) {
    return Promise.resolve(otokContactQRCodes.value)
  }
  if (!qrPromise) {
    qrPromise = otokContactAPI
      .getQRCodes()
      .then((res) => {
        otokContactQRCodes.value = res.data
        return res.data
      })
      .catch(() => null)
      .finally(() => {
        qrPromise = null
      })
  }
  return qrPromise
}

/** 管理员保存后同步侧边栏：更新摘要并丢弃旧二维码。 */
export function otokContactApplySaved(summary: OtokContactSummary) {
  otokContactSummary.value = summary
  otokContactQRCodes.value = null
  summaryPromise = null
}

export interface OtokContactSections {
  wechat: boolean
  qq: boolean
  email: boolean
  any: boolean
}

export function otokContactSections(
  s: Pick<OtokContactSummary, 'email' | 'qq_group' | 'has_wechat_qr' | 'has_qq_group_qr'> | null,
): OtokContactSections {
  const wechat = !!s?.has_wechat_qr
  const qq = !!(s?.has_qq_group_qr || s?.qq_group)
  const email = !!s?.email
  return { wechat, qq, email, any: wechat || qq || email }
}

// 以下校验规则与后端 service/otok_contact.go 保持一致。

const EMAIL_RE = /^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$/
const QQ_GROUP_RE = /^[1-9][0-9]{4,11}$/
const QR_RE = /^data:image\/(png|jpeg|webp);base64,/

export const OTOK_CONTACT_MAX_QR_BYTES = 256 * 1024

export function isValidOtokContactEmail(value: string): boolean {
  const v = value.trim()
  return v === '' || (v.length <= 254 && EMAIL_RE.test(v))
}

export function isValidOtokQQGroup(value: string): boolean {
  const v = value.trim()
  return v === '' || QQ_GROUP_RE.test(v)
}

/** 只放行 PNG / JPEG / WebP 的 data URL（ImageUpload 的 accept 是 image/*，会放进 GIF、SVG）。 */
export function isAllowedOtokQrDataUrl(value: string): boolean {
  return value === '' || QR_RE.test(value)
}

/** 仅供测试重置模块状态。 */
export function __resetOtokContactCache() {
  otokContactSummary.value = null
  otokContactQRCodes.value = null
  summaryPromise = null
  qrPromise = null
}
