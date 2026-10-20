/**
 * otok: 侧边栏「联系我们」接口。
 */
import { apiClient } from '@/api/client'

/** 侧边栏用的摘要，不含二维码图片。 */
export interface OtokContactSummary {
  enabled: boolean
  email: string
  qq_group: string
  has_wechat_qr: boolean
  has_qq_group_qr: boolean
  version: string
}

export interface OtokContactQRCodes {
  wechat_qr: string
  qq_group_qr: string
  version: string
}

/** 管理员配置的完整内容；空串表示未配置。二维码为 data URL。 */
export interface OtokContactConfig {
  email: string
  qq_group: string
  wechat_qr: string
  qq_group_qr: string
}

export const otokContactAPI = {
  getSummary() {
    return apiClient.get<OtokContactSummary>('/otok/contact')
  },
  getQRCodes() {
    return apiClient.get<OtokContactQRCodes>('/otok/contact/qrcodes')
  },
}

export const otokAdminContactAPI = {
  get() {
    return apiClient.get<OtokContactConfig>('/admin/otok/contact')
  },
  update(data: OtokContactConfig) {
    return apiClient.put<{ config: OtokContactConfig; summary: OtokContactSummary }>('/admin/otok/contact', data)
  },
}
