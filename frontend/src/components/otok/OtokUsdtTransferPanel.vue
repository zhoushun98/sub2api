<template>
  <div class="space-y-4">
    <!-- 终态 -->
    <template v-if="outcome === 'success'">
      <div class="card p-6">
        <div class="flex flex-col items-center space-y-4 py-4">
          <div class="flex h-16 w-16 items-center justify-center rounded-full bg-green-100 dark:bg-green-900/30">
            <Icon name="check" size="lg" class="text-green-500" />
          </div>
          <p class="text-lg font-bold text-gray-900 dark:text-white">
            {{ props.orderType === 'subscription' ? t('payment.result.subscriptionSuccess') : t('payment.result.success') }}
          </p>
          <div v-if="paidOrder" class="w-full rounded-xl bg-gray-50 p-4 dark:bg-dark-800">
            <div class="space-y-2 text-sm">
              <div class="flex justify-between">
                <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderId') }}</span>
                <span class="font-medium text-gray-900 dark:text-white">#{{ paidOrder.id }}</span>
              </div>
              <div class="flex justify-between">
                <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.amount') }}</span>
                <span class="font-medium text-gray-900 dark:text-white">${{ paidOrder.amount.toFixed(2) }}</span>
              </div>
              <div v-if="detail" class="flex justify-between">
                <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.payAmount') }}</span>
                <span class="font-medium text-gray-900 dark:text-white">{{ detail.amount }} USDT</span>
              </div>
            </div>
          </div>
          <button class="btn btn-primary" @click="handleDone">{{ t('common.confirm') }}</button>
        </div>
      </div>
    </template>

    <template v-else-if="outcome === 'cancelled' || outcome === 'expired'">
      <div class="card p-6">
        <div class="flex flex-col items-center space-y-4 py-4">
          <div class="flex h-16 w-16 items-center justify-center rounded-full bg-orange-100 dark:bg-orange-900/30">
            <Icon name="exclamationTriangle" size="lg" class="text-orange-500" />
          </div>
          <p class="text-lg font-bold text-gray-900 dark:text-white">
            {{ outcome === 'cancelled' ? t('payment.qr.cancelled') : t('payment.qr.expired') }}
          </p>
          <p class="text-sm text-gray-500 dark:text-gray-400">
            {{ outcome === 'cancelled' ? t('payment.qr.cancelledDesc') : t('payment.qr.expiredDesc') }}
          </p>
          <button class="btn btn-primary" @click="handleDone">{{ t('common.confirm') }}</button>
        </div>
      </div>
    </template>

    <!-- 加载 -->
    <div v-else-if="!detail" class="card p-6">
      <div class="flex flex-col items-center space-y-4 py-4">
        <template v-if="loadError">
          <p class="text-sm text-red-600 dark:text-red-400">{{ loadError }}</p>
          <button class="btn btn-secondary text-sm" @click="refresh">{{ t('otok.usdt.reload') }}</button>
        </template>
        <div v-else class="h-10 w-10 animate-spin rounded-full border-4 border-primary-500 border-t-transparent"></div>
      </div>
    </div>

    <!-- 付款信息 -->
    <template v-else>
      <div class="card p-6">
        <div class="flex flex-col items-center space-y-4">
          <p class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('otok.usdt.title', { network: networkName }) }}</p>
          <div
            data-test="otok-usdt-warning"
            class="w-full rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm leading-relaxed text-amber-900 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-100"
          >
            <p class="font-semibold">{{ t('otok.usdt.networkWarning', { network: networkName }) }}</p>
            <p class="mt-1">{{ t('otok.usdt.exactAmountWarning') }}</p>
          </div>

          <div class="w-full text-center">
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('otok.usdt.amountLabel') }}</p>
            <div class="mt-1 flex items-center justify-center gap-2">
              <span data-test="otok-usdt-amount" class="text-3xl font-bold tabular-nums text-gray-900 dark:text-white">{{ detail.amount }}</span>
              <span class="text-base font-semibold text-gray-500 dark:text-gray-400">USDT</span>
              <button type="button" class="btn btn-secondary btn-sm inline-flex items-center gap-1" @click="copy(detail.amount)">
                <Icon name="copy" size="sm" />{{ t('otok.usdt.copyAmount') }}
              </button>
            </div>
          </div>

          <div class="rounded-lg border-2 border-[#26A17B] bg-white p-3 dark:border-[#26A17B]/70 dark:bg-dark-800">
            <canvas ref="qrCanvas" class="mx-auto"></canvas>
          </div>

          <div class="w-full">
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('otok.usdt.addressLabel') }}</p>
            <div class="mt-1 flex items-start gap-2">
              <code data-test="otok-usdt-address" class="min-w-0 flex-1 break-all rounded bg-gray-100 px-2 py-1.5 font-mono text-sm text-gray-900 dark:bg-dark-700 dark:text-white">{{ detail.address }}</code>
              <button type="button" class="btn btn-secondary btn-sm inline-flex shrink-0 items-center gap-1" @click="copy(detail.address)">
                <Icon name="copy" size="sm" />{{ t('otok.usdt.copyAddress') }}
              </button>
            </div>
            <p class="mt-2 break-all text-xs text-gray-400 dark:text-gray-500">
              {{ t('otok.usdt.contractLabel') }}: <span class="font-mono">{{ detail.token_contract }}</span>
            </p>
          </div>
        </div>
      </div>

      <div class="card p-4 text-center">
        <template v-if="transferState === 'review'">
          <p class="text-sm font-medium text-amber-700 dark:text-amber-300">{{ t('otok.usdt.reviewing') }}</p>
        </template>
        <template v-else-if="transferState === 'detected'">
          <div class="mx-auto mb-2 h-6 w-6 animate-spin rounded-full border-2 border-[#26A17B] border-t-transparent"></div>
          <p class="text-sm font-medium text-gray-900 dark:text-white">{{ t('otok.usdt.detected') }}</p>
          <p v-if="confirmationText" class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ confirmationText }}</p>
        </template>
        <template v-else-if="inGrace">
          <p class="text-sm font-semibold text-orange-600 dark:text-orange-400">{{ t('otok.usdt.graceTitle') }}</p>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('otok.usdt.graceHint', { time: formatTime(detail.reserved_until) }) }}</p>
        </template>
        <template v-else>
          <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('otok.usdt.expiresIn') }}</p>
          <p class="mt-1 text-2xl font-bold tabular-nums text-gray-900 dark:text-white">{{ countdownDisplay }}</p>
          <p class="mt-1 text-xs text-gray-400 dark:text-gray-500">{{ t('otok.usdt.waiting') }}</p>
        </template>
      </div>

      <div v-if="detail.can_submit_tx && transferState === 'none'" class="card p-4">
        <label class="input-label">{{ t('otok.usdt.txHashLabel') }}</label>
        <div class="mt-1 flex gap-2">
          <input
            v-model="txHash"
            data-test="otok-usdt-txhash"
            type="text"
            class="input min-w-0 flex-1 font-mono text-xs"
            spellcheck="false"
            autocomplete="off"
            :placeholder="t('otok.usdt.txHashPlaceholder')"
          />
          <button class="btn btn-primary shrink-0" :disabled="!txHash.trim() || submittingTx" @click="submitTx">
            {{ submittingTx ? t('common.processing') : t('otok.usdt.submitTx') }}
          </button>
        </div>
      </div>

      <button v-if="canCancel" class="btn btn-secondary w-full" :disabled="cancelling" @click="handleCancel">
        {{ cancelling ? t('common.processing') : t('payment.qr.cancelOrder') }}
      </button>
    </template>
  </div>
</template>

<script setup lang="ts">
// otok: USDT 链上转账付款面板，替代上游 PaymentStatusPanel（其倒计时到点即判定过期，不适合链上宽限期）。
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import QRCode from 'qrcode'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores'
import { useClipboard } from '@/composables/useClipboard'
import { paymentAPI } from '@/api/payment'
import { otokUsdtAPI, type OtokUsdtOrderDetail } from '@/api/otok/usdt'
import { extractI18nErrorMessage } from '@/utils/apiError'
import { formatOrderDateTime } from '@/components/payment/orderUtils'
import type { PaymentOrder } from '@/types/payment'

const props = defineProps<{
  orderId: number
  paymentType: string
  expiresAt?: string
  orderType?: string
  outTradeNo?: string
}>()

type PaymentOutcome = 'success' | 'cancelled' | 'expired'

const emit = defineEmits<{ done: []; success: []; settled: [outcome: PaymentOutcome] }>()

const { t, te } = useI18n()
const appStore = useAppStore()
const { copyToClipboard } = useClipboard()

const POLL_INTERVAL_MS = 5000
const GRACE_POLL_INTERVAL_MS = 10000

const detail = ref<OtokUsdtOrderDetail | null>(null)
const loadError = ref('')
const outcome = ref<PaymentOutcome | null>(null)
const paidOrder = ref<PaymentOrder | null>(null)
const now = ref(Date.now())
const txHash = ref('')
const submittingTx = ref(false)
const cancelling = ref(false)
const qrCanvas = ref<HTMLCanvasElement | null>(null)

let pollTimer: ReturnType<typeof setTimeout> | null = null
let clockTimer: ReturnType<typeof setInterval> | null = null
let inFlight = false

const networkName = computed(() => {
  const network = detail.value?.network || ''
  const key = `otok.usdt.networks.${network}`
  return te(key) ? t(key) : (detail.value?.network_name || network)
})

const expiresAtMs = computed(() => new Date(detail.value?.expires_at || props.expiresAt || 0).getTime())
const remainingSeconds = computed(() => Math.max(0, Math.floor((expiresAtMs.value - now.value) / 1000)))
const inGrace = computed(() => !!detail.value && now.value >= expiresAtMs.value)

const countdownDisplay = computed(() => {
  const m = Math.floor(remainingSeconds.value / 60)
  const s = remainingSeconds.value % 60
  return `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
})

/** none = 尚未检测到转账；detected = 已绑定等待确认 / 入账重试；review = 进入人工审核。 */
const transferState = computed<'none' | 'detected' | 'review'>(() => {
  const status = detail.value?.transfer?.status
  if (!status) return 'none'
  if (status === 'review') return 'review'
  if (status === 'bound' || status === 'credit_failed' || status === 'credited') return 'detected'
  return 'none'
})

const confirmationText = computed(() => {
  const d = detail.value
  const current = d?.transfer?.confirmations
  if (!d || current == null || d.network === 'trc20') return ''
  return t('otok.usdt.confirmations', { current: Math.min(current, d.required_confirmations), required: d.required_confirmations })
})

const canCancel = computed(() => detail.value?.order_status === 'PENDING' && !inGrace.value && transferState.value === 'none')

function isSuccessStatus(status: string | undefined): boolean {
  return status === 'COMPLETED' || status === 'PAID' || status === 'RECHARGING'
}

function formatTime(value: string): string {
  return formatOrderDateTime(value)
}

function setOutcome(next: PaymentOutcome) {
  if (outcome.value === next) return
  outcome.value = next
  cleanup()
  emit('settled', next)
}

async function markSuccess() {
  try {
    paidOrder.value = (await paymentAPI.getOrder(props.orderId)).data
  } catch {
    paidOrder.value = null
  }
  setOutcome('success')
  emit('success')
}

function applyDetail(next: OtokUsdtOrderDetail) {
  detail.value = next
  if (isSuccessStatus(next.order_status)) {
    void markSuccess()
    return
  }
  if (next.order_status === 'FAILED') {
    setOutcome('expired')
    return
  }
  // 宽限期结束、金额已释放且没有关联转账时才判定为过期；有转账时交给后台继续处理。
  const windowClosed = Date.now() >= new Date(next.reserved_until).getTime() || next.reservation_status === 'released'
  if (windowClosed && !next.transfer) {
    setOutcome(next.order_status === 'CANCELLED' ? 'cancelled' : 'expired')
  }
}

async function refresh() {
  if (!props.orderId || outcome.value || inFlight) return
  inFlight = true
  try {
    const res = await otokUsdtAPI.getOrder(props.orderId)
    if (outcome.value) return
    loadError.value = ''
    applyDetail(res.data)
  } catch (err: unknown) {
    if (!detail.value) loadError.value = extractI18nErrorMessage(err, t, 'payment.errors', t('otok.usdt.loadFailed'))
  } finally {
    inFlight = false
  }
}

function schedulePoll() {
  if (outcome.value) return
  const delay = inGrace.value ? GRACE_POLL_INTERVAL_MS : POLL_INTERVAL_MS
  pollTimer = setTimeout(async () => {
    await refresh()
    schedulePoll()
  }, delay)
}

async function submitTx() {
  const hash = txHash.value.trim()
  if (!hash || submittingTx.value) return
  submittingTx.value = true
  try {
    const res = await otokUsdtAPI.submitTx(props.orderId, hash)
    appStore.showSuccess(t('otok.usdt.submitTxSuccess'))
    txHash.value = ''
    applyDetail(res.data)
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    submittingTx.value = false
  }
}

async function handleCancel() {
  if (cancelling.value) return
  cancelling.value = true
  try {
    await paymentAPI.cancelOrder(props.orderId)
    setOutcome('cancelled')
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    cancelling.value = false
  }
}

function copy(text: string) {
  void copyToClipboard(text, t('otok.usdt.copied'))
}

async function renderQR() {
  await nextTick()
  if (!qrCanvas.value || !detail.value?.address) return
  await QRCode.toCanvas(qrCanvas.value, detail.value.address, { width: 200, margin: 1, errorCorrectionLevel: 'M' })
}

function handleDone() {
  cleanup()
  emit('done')
}

function cleanup() {
  if (pollTimer) { clearTimeout(pollTimer); pollTimer = null }
  if (clockTimer) { clearInterval(clockTimer); clockTimer = null }
}

watch(() => detail.value?.address, () => { void renderQR() })

onMounted(async () => {
  clockTimer = setInterval(() => { now.value = Date.now() }, 1000)
  await refresh()
  schedulePoll()
})
onUnmounted(() => cleanup())
</script>
