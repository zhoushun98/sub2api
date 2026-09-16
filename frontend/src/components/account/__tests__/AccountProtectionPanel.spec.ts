import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, shallowMount } from '@vue/test-utils'
import type { Account } from '@/types'

const mocks = vi.hoisted(() => ({
  preview: vi.fn(), apply: vi.fn(), revert: vi.fn(), set: vi.fn(), list: vi.fn(),
  success: vi.fn(), info: vi.fn(), error: vi.fn()
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: mocks.error, showSuccess: mocks.success, showInfo: mocks.info }) }))
vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      previewAntiDegrade: mocks.preview, applyAntiDegrade: mocks.apply, revertAntiDegrade: mocks.revert,
      setProtection: mocks.set, listAntiDegradeStrategies: mocks.list
    }
  }
}))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

import AccountProtectionPanel from '../AccountProtectionPanel.vue'

const BaseDialogStub = defineComponent({
  props: ['show', 'title'],
  template: '<div v-if="show" :data-title="title"><slot /><slot name="footer" /></div>'
})
const ConfirmDialogStub = defineComponent({
  props: ['show'],
  emits: ['confirm', 'cancel'],
  template: '<div v-if="show" data-testid="confirm-stub" />'
})
const account = (extra: Record<string, unknown> = {}, overrides: Partial<Account> = {}) => ({
  id: 42, name: 'a', platform: 'openai', type: 'oauth', credentials: {}, extra, proxy_id: null,
  concurrency: 10, priority: 1, rate_multiplier: 1, status: 'active', group_ids: [], expires_at: null,
  auto_pause_on_expired: false, updated_at: '2026-09-16T00:00:00Z', ...overrides
} as Account)
const strategies = [
  { id: 'legacy', name: 'legacy', description: '', category: '常用', identity_mode: 'session', tls_profile: 'nodejs24', max_concurrency: 16, risk: '中', apply_supported: true },
  { id: 'mode1', name: 'mode1', description: '', category: '常用', identity_mode: 'device', tls_profile: 'standard', max_concurrency: 16, risk: '中', apply_supported: true },
  { id: 'mode2', name: 'mode2', description: '', category: '诊断', identity_mode: 'full', tls_profile: 'nodejs22', max_concurrency: 8, risk: '高', apply_supported: true, diagnostic_only: true }
]
const mount = (value = account()) => shallowMount(AccountProtectionPanel, {
  props: { account: value, requestIntegrityMode: 'default' },
  global: { stubs: { BaseDialog: BaseDialogStub, ConfirmDialog: ConfirmDialogStub } }
})

describe('AccountProtectionPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.list.mockResolvedValue(strategies)
  })

  it('loads strategies, previews the clicked one and applies it', async () => {
    mocks.preview.mockResolvedValue({ account_id: 42, enabled: false, eligible: true, active_mode: '', changes: [{ key: 'strategy', to: 'legacy' }] })
    const applied = account({ anti_degradation: true, anti_degrade: { enabled: true, mode: 'legacy' }, enable_tls_fingerprint: true, tls_fingerprint_builtin: 'nodejs24' }, { anti_degradation: true, protection_mode: 'legacy', concurrency: 16 })
    mocks.apply.mockResolvedValue(applied)
    const wrapper = mount()
    await flushPromises()
    expect(mocks.list).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-testid="anti-degrade-mode-legacy"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="anti-degrade-mode-mode2"]').isVisible()).toBe(false)
    await wrapper.get('[data-testid="anti-degrade-mode-legacy"]').trigger('click')
    await flushPromises()
    expect(mocks.preview).toHaveBeenCalledWith(42, 'legacy')
    await wrapper.get('[data-testid="anti-degrade-apply"]').trigger('click')
    await flushPromises()
    expect(mocks.apply).toHaveBeenCalledWith(42, 'legacy')
    expect(wrapper.emitted('updated')?.[0]).toEqual([applied])
    expect(wrapper.get('[data-testid="anti-degrade-status"]').text()).toContain('admin.accounts.antiDegradeEnabled')
  })

  it('turning the switch on calls setProtection; turning off asks for confirmation then reverts', async () => {
    const on = account({}, { anti_degradation: true, protection_mode: 'legacy' })
    mocks.set.mockResolvedValue(on)
    const wrapper = mount()
    await flushPromises()
    await wrapper.get('[data-testid="anti-degrade-toggle"]').trigger('click')
    await flushPromises()
    expect(mocks.set).toHaveBeenCalledWith(42, true)
    expect(wrapper.emitted('updated')?.[0]).toEqual([on])

    mocks.revert.mockResolvedValue(account())
    await wrapper.get('[data-testid="anti-degrade-toggle"]').trigger('click')
    expect(mocks.revert).not.toHaveBeenCalled()
    wrapper.findComponent(ConfirmDialogStub).vm.$emit('confirm')
    await flushPromises()
    expect(mocks.revert).toHaveBeenCalledWith(42, true)
  })

  it('hides strategy buttons for accounts that do not support identity strategies', async () => {
    const wrapper = mount(account({}, { type: 'apikey' }))
    await flushPromises()
    expect(wrapper.find('[data-testid="anti-degrade-strategies"]').exists()).toBe(false)
  })

  it('emits request integrity changes to the parent', async () => {
    const wrapper = mount()
    await flushPromises()
    await wrapper.get('[data-testid="request-integrity-select"]').setValue('observe')
    expect(wrapper.emitted('update:requestIntegrityMode')?.[0]).toEqual(['observe'])
  })
})
