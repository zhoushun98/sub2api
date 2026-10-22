/**
 * otok: USDT 链上充值接口。
 */
import { apiClient } from '@/api/client'
import type { BasePaginationResponse } from '@/types'

export interface OtokUsdtNetworkInfo {
  payment_type: string
  network: string
  display_name: string
  token_contract: string
  confirmations: number
}

export interface OtokUsdtQuote {
  enabled: boolean
  exchange_rate: string
  fee_rate: string
  expire_minutes: number
  networks: OtokUsdtNetworkInfo[] | null
  estimated_amount?: string
}

export interface OtokUsdtTransfer {
  id: number
  network: string
  tx_hash: string
  log_index: number
  explorer_url: string
  block_number: number
  block_time?: string
  from_address: string
  to_address: string
  amount: string
  order_id?: number
  source: 'watcher' | 'user' | 'admin'
  status: 'seen' | 'bound' | 'credited' | 'credit_failed' | 'review' | 'ignored' | 'dropped'
  note?: string
  credit_attempts: number
  last_error?: string
  last_error_at?: string
  confirmations?: number
  created_at: string
}

export interface OtokUsdtOrderDetail {
  order_id: number
  out_trade_no: string
  payment_type: string
  network: string
  network_name: string
  address: string
  token_contract: string
  amount: string
  station_amount: number
  exchange_rate: string
  fee_rate: string
  required_confirmations: number
  order_status: string
  reservation_status: string
  expires_at: string
  reserved_until: string
  created_at: string
  can_submit_tx: boolean
  transfer?: OtokUsdtTransfer
}

export interface OtokUsdtHealthCursor {
  network: string
  address: string
  last_block: number
  last_success_at?: string
  last_error?: string
  last_error_at?: string
}

export interface OtokUsdtHealth {
  testnet: boolean
  active_reservations: number
  unmatched_transfers: number
  review_transfers: number
  credit_failed_transfers: number
  awaiting_confirmation_transfers: number
  credit_blocked_transfers: number
  cursors: OtokUsdtHealthCursor[] | null
}

export const otokUsdtAPI = {
  getQuote(amount?: number) {
    return apiClient.get<OtokUsdtQuote>('/payment/otok/usdt/quote', { params: amount ? { amount } : undefined })
  },
  getOrder(orderId: number) {
    return apiClient.get<OtokUsdtOrderDetail>(`/payment/otok/usdt/orders/${orderId}`)
  },
  submitTx(orderId: number, txHash: string) {
    return apiClient.post<OtokUsdtOrderDetail>(`/payment/otok/usdt/orders/${orderId}/tx`, { tx_hash: txHash })
  },
}

export const otokAdminUsdtAPI = {
  getHealth() {
    return apiClient.get<OtokUsdtHealth>('/admin/payment/otok/usdt/health')
  },
  listTransfers(params: { page?: number; page_size?: number; status?: string; network?: string; keyword?: string }) {
    return apiClient.get<BasePaginationResponse<OtokUsdtTransfer>>('/admin/payment/otok/usdt/transfers', { params })
  },
  getOrder(orderId: number) {
    return apiClient.get<OtokUsdtOrderDetail | null>(`/admin/payment/otok/usdt/orders/${orderId}`)
  },
  bindTx(orderId: number, data: { tx_hash: string; log_index?: number; reason: string }) {
    return apiClient.post<OtokUsdtOrderDetail>(`/admin/payment/otok/usdt/orders/${orderId}/bind-tx`, data)
  },
  ignoreTransfer(id: number, note: string) {
    return apiClient.post(`/admin/payment/otok/usdt/transfers/${id}/ignore`, { note })
  },
}
