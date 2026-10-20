import { beforeEach, describe, expect, it, vi } from 'vitest'

const getSummary = vi.hoisted(() => vi.fn())
const getQRCodes = vi.hoisted(() => vi.fn())

vi.mock('@/api/otok/contact', () => ({
  otokContactAPI: { getSummary, getQRCodes },
}))

import {
  __resetOtokContactCache,
  ensureOtokContactQRCodes,
  ensureOtokContactSummary,
  isAllowedOtokQrDataUrl,
  isValidOtokContactEmail,
  isValidOtokQQGroup,
  otokContactApplySaved,
  otokContactQRCodes,
  otokContactSections,
  otokContactSummary,
} from '../contact'

const SUMMARY = {
  enabled: true,
  email: 'admin@otokapi.com',
  qq_group: '123456',
  has_wechat_qr: true,
  has_qq_group_qr: false,
  version: 'v1',
}

beforeEach(() => {
  __resetOtokContactCache()
  getSummary.mockReset()
  getQRCodes.mockReset()
})

describe('otokContactSections', () => {
  it('按配置决定显示哪些块', () => {
    expect(otokContactSections(null)).toEqual({ wechat: false, qq: false, email: false, any: false })
    expect(otokContactSections({ email: 'a@b.com', qq_group: '', has_wechat_qr: false, has_qq_group_qr: false }))
      .toEqual({ wechat: false, qq: false, email: true, any: true })
    // 只有群号、没有群二维码时仍显示 QQ 块
    expect(otokContactSections({ email: '', qq_group: '12345', has_wechat_qr: false, has_qq_group_qr: false }).qq).toBe(true)
    expect(otokContactSections({ email: '', qq_group: '', has_wechat_qr: false, has_qq_group_qr: true }).qq).toBe(true)
    expect(otokContactSections({ email: '', qq_group: '', has_wechat_qr: true, has_qq_group_qr: false }).wechat).toBe(true)
  })
})

describe('摘要缓存', () => {
  it('并发调用只请求一次', async () => {
    getSummary.mockResolvedValue({ data: SUMMARY })
    const [a, b] = await Promise.all([ensureOtokContactSummary(), ensureOtokContactSummary()])
    expect(a).toEqual(SUMMARY)
    expect(b).toEqual(SUMMARY)
    await ensureOtokContactSummary()
    expect(getSummary).toHaveBeenCalledTimes(1)
  })

  it('失败后下次会重试', async () => {
    getSummary.mockRejectedValueOnce(new Error('boom')).mockResolvedValueOnce({ data: SUMMARY })
    expect(await ensureOtokContactSummary()).toBeNull()
    expect(await ensureOtokContactSummary()).toEqual(SUMMARY)
    expect(getSummary).toHaveBeenCalledTimes(2)
  })
})

describe('二维码缓存', () => {
  it('版本一致时复用，保存后丢弃', async () => {
    getSummary.mockResolvedValue({ data: SUMMARY })
    getQRCodes.mockResolvedValue({ data: { wechat_qr: 'data:image/png;base64,AA', qq_group_qr: '', version: 'v1' } })
    await ensureOtokContactSummary()
    await ensureOtokContactQRCodes()
    await ensureOtokContactQRCodes()
    expect(getQRCodes).toHaveBeenCalledTimes(1)

    otokContactApplySaved({ ...SUMMARY, version: 'v2' })
    expect(otokContactSummary.value?.version).toBe('v2')
    expect(otokContactQRCodes.value).toBeNull()
    getQRCodes.mockResolvedValue({ data: { wechat_qr: 'data:image/png;base64,BB', qq_group_qr: '', version: 'v2' } })
    await ensureOtokContactQRCodes()
    expect(getQRCodes).toHaveBeenCalledTimes(2)
    expect(otokContactQRCodes.value?.wechat_qr).toBe('data:image/png;base64,BB')
  })
})

describe('校验（与后端一致）', () => {
  it('邮箱', () => {
    expect(isValidOtokContactEmail('')).toBe(true)
    expect(isValidOtokContactEmail(' admin@otokapi.com ')).toBe(true)
    expect(isValidOtokContactEmail('Name <a@b.com>')).toBe(false)
    expect(isValidOtokContactEmail('a@b')).toBe(false)
    expect(isValidOtokContactEmail('a b@c.com')).toBe(false)
    expect(isValidOtokContactEmail('a?x@b.com')).toBe(false)
    expect(isValidOtokContactEmail('a'.repeat(250) + '@b.com')).toBe(false)
  })

  it('QQ 群号', () => {
    expect(isValidOtokQQGroup('')).toBe(true)
    expect(isValidOtokQQGroup('12345')).toBe(true)
    expect(isValidOtokQQGroup('01234')).toBe(false)
    expect(isValidOtokQQGroup('1234')).toBe(false)
    expect(isValidOtokQQGroup('abc12')).toBe(false)
  })

  it('二维码只接受 PNG / JPEG / WebP', () => {
    expect(isAllowedOtokQrDataUrl('')).toBe(true)
    expect(isAllowedOtokQrDataUrl('data:image/png;base64,AA')).toBe(true)
    expect(isAllowedOtokQrDataUrl('data:image/jpeg;base64,AA')).toBe(true)
    expect(isAllowedOtokQrDataUrl('data:image/webp;base64,AA')).toBe(true)
    expect(isAllowedOtokQrDataUrl('data:image/gif;base64,AA')).toBe(false)
    expect(isAllowedOtokQrDataUrl('data:image/svg+xml;base64,AA')).toBe(false)
    expect(isAllowedOtokQrDataUrl('https://example.com/qr.png')).toBe(false)
  })
})
