<template>
  <div v-if="detail" data-test="otok-usdt-order-info" class="rounded-lg border border-emerald-200 bg-emerald-50/60 p-3 dark:border-emerald-800 dark:bg-emerald-900/10">
    <h4 class="mb-2 text-sm font-semibold text-emerald-800 dark:text-emerald-300">{{ t('otok.usdt.orderInfo') }}</h4>
    <div class="grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
      <div>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('otok.usdt.network') }}</p>
        <p class="text-gray-900 dark:text-white">{{ networkLabel(detail.network, detail.network_name) }}</p>
      </div>
      <div>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('otok.usdt.amountLabel') }}</p>
        <p class="font-medium text-gray-900 dark:text-white">{{ detail.amount }} USDT</p>
      </div>
      <div>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('otok.usdt.stationAmount') }}</p>
        <p class="text-gray-900 dark:text-white">¥{{ detail.station_amount.toFixed(2) }}</p>
      </div>
      <div>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('otok.usdt.exchangeRate') }}</p>
        <p class="text-gray-900 dark:text-white">1 USDT = {{ detail.exchange_rate }} · {{ detail.fee_rate }}%</p>
      </div>
      <div>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('otok.usdt.reservationStatus') }}</p>
        <p class="text-gray-900 dark:text-white">{{ statusLabel('reservation', detail.reservation_status) }}</p>
      </div>
      <div>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('otok.usdt.reservedUntil') }}</p>
        <p class="text-gray-900 dark:text-white">{{ formatOrderDateTime(detail.reserved_until) }}</p>
      </div>
      <div class="col-span-2">
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('otok.usdt.addressLabel') }}</p>
        <p class="break-all font-mono text-xs text-gray-900 dark:text-white">{{ detail.address }}</p>
      </div>
      <div class="col-span-2">
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('otok.usdt.transfer') }}</p>
        <div v-if="detail.transfer" class="space-y-0.5 text-xs text-gray-900 dark:text-white">
          <a :href="detail.transfer.explorer_url" target="_blank" rel="noopener noreferrer" class="break-all font-mono text-primary-600 hover:underline dark:text-primary-400">
            {{ detail.transfer.tx_hash }}
          </a>
          <p>
            {{ detail.transfer.amount }} USDT · {{ statusLabel('transferStatus', detail.transfer.status) }}
            · {{ statusLabel('sources', detail.transfer.source) }}
            <template v-if="detail.transfer.confirmations != null"> · {{ t('otok.usdt.confirmations', { current: detail.transfer.confirmations, required: detail.required_confirmations }) }}</template>
          </p>
          <p v-if="detail.transfer.note" class="text-gray-500 dark:text-gray-400">{{ detail.transfer.note }}</p>
        </div>
        <p v-else class="text-xs text-gray-500 dark:text-gray-400">{{ t('otok.usdt.noTransfer') }}</p>
      </div>
    </div>

    <div v-if="canBind" class="mt-3 border-t border-emerald-200 pt-3 dark:border-emerald-800">
      <button v-if="!bindOpen" type="button" class="btn btn-secondary btn-sm" @click="bindOpen = true">{{ t('otok.usdt.bindTx') }}</button>
      <div v-else class="space-y-2">
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('otok.usdt.bindTxHint') }}</p>
        <input v-model="bindForm.txHash" type="text" class="input font-mono text-xs" spellcheck="false" :placeholder="t('otok.usdt.txHashPlaceholder')" />
        <div class="grid grid-cols-3 gap-2">
          <input v-model="bindForm.logIndex" type="number" min="0" class="input text-xs" :placeholder="t('otok.usdt.logIndex')" />
          <input v-model="bindForm.reason" type="text" class="input col-span-2 text-xs" :placeholder="t('otok.usdt.reason')" />
        </div>
        <div class="flex justify-end gap-2">
          <button type="button" class="btn btn-secondary btn-sm" @click="bindOpen = false">{{ t('common.cancel') }}</button>
          <button type="button" class="btn btn-primary btn-sm" :disabled="binding || !bindForm.txHash.trim() || !bindForm.reason.trim()" @click="submitBind">
            {{ binding ? t('common.processing') : t('otok.usdt.bindTx') }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
// otok: 后台订单详情中的 USDT 链上信息与手动补单入口。
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores'
import { otokAdminUsdtAPI, type OtokUsdtOrderDetail } from '@/api/otok/usdt'
import { extractI18nErrorMessage } from '@/utils/apiError'
import { formatOrderDateTime } from '@/components/payment/orderUtils'

const props = defineProps<{ orderId: number }>()
const emit = defineEmits<{ updated: [] }>()

const { t, te } = useI18n()
const appStore = useAppStore()

const detail = ref<OtokUsdtOrderDetail | null>(null)
const bindOpen = ref(false)
const binding = ref(false)
const bindForm = reactive({ txHash: '', logIndex: '', reason: '' })

const PAID_LIKE = ['PAID', 'RECHARGING', 'COMPLETED', 'REFUND_REQUESTED', 'REFUNDING', 'REFUND_PENDING', 'PARTIALLY_REFUNDED', 'REFUNDED', 'REFUND_FAILED']

const canBind = computed(() => {
  const d = detail.value
  if (!d || PAID_LIKE.includes(d.order_status)) return false
  return !d.transfer || d.transfer.status === 'review'
})

function networkLabel(network: string, fallback: string): string {
  const key = `otok.usdt.networks.${network}`
  return te(key) ? t(key) : fallback || network
}

function statusLabel(group: 'reservation' | 'transferStatus' | 'sources', value: string): string {
  const key = `otok.usdt.${group}.${value}`
  return te(key) ? t(key) : value
}

async function load() {
  detail.value = null
  bindOpen.value = false
  if (!props.orderId) return
  try {
    detail.value = (await otokAdminUsdtAPI.getOrder(props.orderId)).data
  } catch {
    detail.value = null
  }
}

async function submitBind() {
  if (binding.value) return
  const logIndex = bindForm.logIndex === '' ? undefined : Number(bindForm.logIndex)
  binding.value = true
  try {
    const res = await otokAdminUsdtAPI.bindTx(props.orderId, {
      tx_hash: bindForm.txHash.trim(),
      log_index: Number.isInteger(logIndex) ? logIndex : undefined,
      reason: bindForm.reason.trim(),
    })
    detail.value = res.data
    bindOpen.value = false
    bindForm.txHash = ''
    bindForm.logIndex = ''
    bindForm.reason = ''
    appStore.showSuccess(t('otok.usdt.bindSuccess'))
    emit('updated')
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    binding.value = false
  }
}

watch(() => props.orderId, () => { void load() }, { immediate: true })
</script>
