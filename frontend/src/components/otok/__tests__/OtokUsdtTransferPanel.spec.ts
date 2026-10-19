import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const getUsdtOrder = vi.hoisted(() => vi.fn())
const submitTx = vi.hoisted(() => vi.fn())
const getOrder = vi.hoisted(() => vi.fn())
const cancelOrder = vi.hoisted(() => vi.fn())
const showError = vi.hoisted(() => vi.fn())
const showSuccess = vi.hoisted(() => vi.fn())
const toCanvas = vi.hoisted(() => vi.fn())

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
      te: () => false,
    }),
  }
})

vi.mock('@/stores', () => ({
  useAppStore: () => ({ showError, showSuccess }),
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: vi.fn() }),
}))

vi.mock('@/api/payment', () => ({
  paymentAPI: { getOrder, cancelOrder },
}))

vi.mock('@/api/otok/usdt', () => ({
  otokUsdtAPI: { getOrder: getUsdtOrder, submitTx },
}))

vi.mock('qrcode', () => ({
  default: { toCanvas },
}))

import OtokUsdtTransferPanel from '../OtokUsdtTransferPanel.vue'

const NOW = new Date('2026-10-19T12:00:00Z').getTime()

function detailFactory(overrides: Record<string, unknown> = {}) {
  return {
    order_id: 7,
    out_trade_no: 'sub2_x',
    payment_type: 'otok_usdt_trc20',
    network: 'trc20',
    network_name: 'TRON (TRC20)',
    address: 'TXYZopYRdj2D9XRtbG411XZZ3kM5VkAeBf',
    token_contract: 'TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t',
    amount: '14.10',
    station_amount: 100,
    exchange_rate: '7.1',
    fee_rate: '0',
    required_confirmations: 19,
    order_status: 'PENDING',
    reservation_status: 'pending',
    expires_at: new Date(NOW + 10 * 60_000).toISOString(),
    reserved_until: new Date(NOW + 70 * 60_000).toISOString(),
    created_at: new Date(NOW - 20 * 60_000).toISOString(),
    can_submit_tx: true,
    ...overrides,
  }
}

function mountPanel() {
  return mount(OtokUsdtTransferPanel, {
    props: { orderId: 7, paymentType: 'otok_usdt_trc20', orderType: 'balance' },
    global: { stubs: { Icon: true } },
  })
}

describe('OtokUsdtTransferPanel', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(NOW)
    for (const fn of [getUsdtOrder, submitTx, getOrder, cancelOrder, showError, showSuccess]) fn.mockReset()
    toCanvas.mockReset().mockResolvedValue(undefined)
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('展示精确金额、地址和网络警示', async () => {
    getUsdtOrder.mockResolvedValue({ data: detailFactory() })
    const wrapper = mountPanel()
    await flushPromises()

    expect(wrapper.get('[data-test="otok-usdt-amount"]').text()).toBe('14.10')
    expect(wrapper.get('[data-test="otok-usdt-address"]').text()).toBe('TXYZopYRdj2D9XRtbG411XZZ3kM5VkAeBf')
    expect(wrapper.find('[data-test="otok-usdt-warning"]').exists()).toBe(true)
    expect(toCanvas).toHaveBeenCalled()
  })

  it('过期后在宽限期内继续轮询，到账后进入成功态', async () => {
    getUsdtOrder.mockResolvedValue({
      data: detailFactory({
        order_status: 'EXPIRED',
        expires_at: new Date(NOW - 60_000).toISOString(),
      }),
    })
    const wrapper = mountPanel()
    await flushPromises()

    expect(wrapper.text()).toContain('otok.usdt.graceTitle')
    expect(wrapper.emitted('settled')).toBeUndefined()

    getUsdtOrder.mockResolvedValue({ data: detailFactory({ order_status: 'COMPLETED' }) })
    getOrder.mockResolvedValue({ data: { id: 7, amount: 100 } })
    await vi.advanceTimersByTimeAsync(10_000)
    await flushPromises()

    expect(getUsdtOrder).toHaveBeenCalledTimes(2)
    expect(wrapper.emitted('settled')?.[0]).toEqual(['success'])
    expect(wrapper.emitted('success')).toHaveLength(1)
  })

  it('宽限期结束且没有转账时判定过期', async () => {
    getUsdtOrder.mockResolvedValue({
      data: detailFactory({
        order_status: 'EXPIRED',
        expires_at: new Date(NOW - 80 * 60_000).toISOString(),
        reserved_until: new Date(NOW - 20 * 60_000).toISOString(),
        reservation_status: 'released',
      }),
    })
    const wrapper = mountPanel()
    await flushPromises()

    expect(wrapper.emitted('settled')?.[0]).toEqual(['expired'])
  })

  it('有关联转账时显示确认中，不提供 TxHash 输入', async () => {
    getUsdtOrder.mockResolvedValue({
      data: detailFactory({
        network: 'bep20',
        required_confirmations: 15,
        transfer: { id: 1, status: 'bound', confirmations: 3 },
      }),
    })
    const wrapper = mountPanel()
    await flushPromises()

    expect(wrapper.text()).toContain('otok.usdt.detected')
    expect(wrapper.find('[data-test="otok-usdt-txhash"]').exists()).toBe(false)
  })

  it('提交 TxHash 时去掉首尾空白', async () => {
    getUsdtOrder.mockResolvedValue({ data: detailFactory() })
    submitTx.mockResolvedValue({ data: detailFactory({ transfer: { id: 1, status: 'bound' } }) })
    const wrapper = mountPanel()
    await flushPromises()

    await wrapper.get('[data-test="otok-usdt-txhash"]').setValue('  abc123  ')
    const submit = wrapper.findAll('button').find((b) => b.text() === 'otok.usdt.submitTx')
    await submit!.trigger('click')
    await flushPromises()

    expect(submitTx).toHaveBeenCalledWith(7, 'abc123')
    expect(showSuccess).toHaveBeenCalled()
  })
})
