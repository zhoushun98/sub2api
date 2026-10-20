import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { reactive } from 'vue'

const getSummary = vi.hoisted(() => vi.fn())
const getQRCodes = vi.hoisted(() => vi.fn())
const copyToClipboard = vi.hoisted(() => vi.fn())
const appStore = vi.hoisted(() => ({
  sidebarCollapsed: false,
  mobileOpen: false,
  setMobileOpen: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key, te: () => false }),
  }
})

const store = reactive(appStore)
vi.mock('@/stores/app', () => ({ useAppStore: () => store }))
vi.mock('@/stores', () => ({ useAppStore: () => store }))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard }),
}))

vi.mock('@/api/otok/contact', () => ({
  otokContactAPI: { getSummary, getQRCodes },
}))

import { __resetOtokContactCache } from '@/otok/contact'
import OtokContactMenuItem from '../contact/OtokContactMenuItem.vue'

const WECHAT_QR = 'data:image/png;base64,V0VDSEFU'
const QQ_QR = 'data:image/png;base64,UVE='

function summary(overrides: Record<string, unknown> = {}) {
  return {
    enabled: true,
    email: 'admin@otokapi.com',
    qq_group: '123456',
    has_wechat_qr: true,
    has_qq_group_qr: true,
    version: 'v1',
    ...overrides,
  }
}

async function mountItem() {
  const wrapper = mount(OtokContactMenuItem, { attachTo: document.body })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  __resetOtokContactCache()
  getSummary.mockReset()
  getQRCodes.mockReset()
  copyToClipboard.mockReset()
  store.sidebarCollapsed = false
  store.mobileOpen = false
  store.setMobileOpen.mockReset()
  document.body.innerHTML = ''
})

describe('OtokContactMenuItem', () => {
  it('未配置任何联系方式时不显示入口', async () => {
    getSummary.mockResolvedValue({ data: summary({ enabled: false, email: '', qq_group: '', has_wechat_qr: false, has_qq_group_qr: false }) })
    const wrapper = await mountItem()
    expect(wrapper.find('[data-testid="otok-contact-menu"]').exists()).toBe(false)
  })

  it('挂载时不拉二维码，点开才拉，并显示两张卡片和邮箱', async () => {
    getSummary.mockResolvedValue({ data: summary() })
    getQRCodes.mockResolvedValue({ data: { wechat_qr: WECHAT_QR, qq_group_qr: QQ_QR, version: 'v1' } })
    const wrapper = await mountItem()
    expect(getQRCodes).not.toHaveBeenCalled()

    await wrapper.get('[data-testid="otok-contact-menu"]').trigger('click')
    await flushPromises()
    expect(getQRCodes).toHaveBeenCalledTimes(1)

    const body = document.body
    expect(body.querySelector('[data-testid="otok-contact-wechat"] img')?.getAttribute('src')).toBe(WECHAT_QR)
    expect(body.querySelector('[data-testid="otok-contact-qq"] img')?.getAttribute('src')).toBe(QQ_QR)
    expect(body.querySelector('[data-testid="otok-contact-email"] a')?.getAttribute('href')).toBe('mailto:admin@otokapi.com')
    wrapper.unmount()
  })

  it('只配了邮箱时不请求二维码', async () => {
    getSummary.mockResolvedValue({ data: summary({ qq_group: '', has_wechat_qr: false, has_qq_group_qr: false }) })
    const wrapper = await mountItem()
    await wrapper.get('[data-testid="otok-contact-menu"]').trigger('click')
    await flushPromises()
    expect(getQRCodes).not.toHaveBeenCalled()
    expect(document.body.querySelector('[data-testid="otok-contact-wechat"]')).toBeNull()
    expect(document.body.querySelector('[data-testid="otok-contact-qq"]')).toBeNull()
    expect(document.body.querySelector('[data-testid="otok-contact-email"]')).not.toBeNull()
    wrapper.unmount()
  })

  it('点击群号和邮箱复制按钮会复制', async () => {
    getSummary.mockResolvedValue({ data: summary({ has_wechat_qr: false, has_qq_group_qr: false }) })
    const wrapper = await mountItem()
    await wrapper.get('[data-testid="otok-contact-menu"]').trigger('click')
    await flushPromises()
    ;(document.body.querySelector('[data-testid="otok-contact-qq-group"]') as HTMLElement).click()
    ;(document.body.querySelector('[data-testid="otok-contact-email-copy"]') as HTMLElement).click()
    expect(copyToClipboard).toHaveBeenNthCalledWith(1, '123456', 'otok.contact.groupCopied')
    expect(copyToClipboard).toHaveBeenNthCalledWith(2, 'admin@otokapi.com', 'otok.contact.emailCopied')
    wrapper.unmount()
  })

  it('折叠时隐藏文字并带 title；移动端点击先收起抽屉', async () => {
    getSummary.mockResolvedValue({ data: summary() })
    getQRCodes.mockResolvedValue({ data: { wechat_qr: '', qq_group_qr: '', version: 'v1' } })
    store.sidebarCollapsed = true
    store.mobileOpen = true
    const wrapper = await mountItem()
    const btn = wrapper.get('[data-testid="otok-contact-menu"]')
    expect(btn.attributes('title')).toBe('otok.nav.contactUs')
    expect(btn.find('.sidebar-label').classes()).toContain('sidebar-label-collapsed')
    await btn.trigger('click')
    expect(store.setMobileOpen).toHaveBeenCalledWith(false)
    wrapper.unmount()
  })
})
