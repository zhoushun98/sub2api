<template>
  <AppLayout>
    <div class="space-y-4">
      <!-- 监听健康状态 -->
      <div class="card p-4">
        <div class="mb-3 flex items-center justify-between">
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
            {{ t('otok.usdt.health') }}
            <span v-if="health?.testnet" class="ml-2 rounded bg-amber-100 px-1.5 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-300">{{ t('otok.usdt.testnet') }}</span>
          </h3>
        </div>
        <div class="grid grid-cols-2 gap-3 sm:grid-cols-5">
          <div v-for="stat in healthStats" :key="stat.key" class="rounded-lg bg-gray-50 px-3 py-2 dark:bg-dark-800">
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ stat.label }}</p>
            <p :class="['text-lg font-semibold tabular-nums', stat.warn && stat.value > 0 ? 'text-red-600 dark:text-red-400' : 'text-gray-900 dark:text-white']">{{ stat.value }}</p>
          </div>
        </div>
        <div v-if="health?.cursors?.length" class="mt-3 overflow-x-auto">
          <table class="w-full text-left text-xs">
            <thead class="text-gray-500 dark:text-gray-400">
              <tr>
                <th class="py-1 pr-3 font-medium">{{ t('otok.usdt.network') }}</th>
                <th class="py-1 pr-3 font-medium">{{ t('otok.usdt.cursorAddress') }}</th>
                <th class="py-1 pr-3 font-medium">{{ t('otok.usdt.lastBlock') }}</th>
                <th class="py-1 pr-3 font-medium">{{ t('otok.usdt.lastSuccessAt') }}</th>
                <th class="py-1 font-medium">{{ t('otok.usdt.lastError') }}</th>
              </tr>
            </thead>
            <tbody class="text-gray-700 dark:text-gray-300">
              <tr v-for="cur in health.cursors" :key="cur.network + cur.address" class="border-t border-gray-100 dark:border-dark-700">
                <td class="py-1 pr-3">{{ networkLabel(cur.network) }}</td>
                <td class="py-1 pr-3 font-mono">{{ cur.address === '*' ? '-' : shortHash(cur.address) }}</td>
                <td class="py-1 pr-3 tabular-nums">{{ cur.last_block || '-' }}</td>
                <td class="py-1 pr-3">{{ cur.last_success_at ? formatOrderDateTime(cur.last_success_at) : t('otok.usdt.neverScanned') }}</td>
                <td class="py-1 text-red-600 dark:text-red-400">
                  <span v-if="isErrorNewer(cur)" :title="cur.last_error">{{ formatOrderDateTime(cur.last_error_at || '') }} · {{ truncate(cur.last_error || '', 80) }}</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      <!-- 筛选 -->
      <div class="card p-4">
        <div class="flex flex-wrap items-center gap-3">
          <div class="flex-1 sm:max-w-72">
            <input v-model="filters.keyword" type="text" class="input" :placeholder="t('otok.usdt.searchPlaceholder')" @input="debouncedLoad" />
          </div>
          <Select v-model="filters.status" :options="statusOptions" class="w-36" @change="reload" />
          <Select v-model="filters.network" :options="networkOptions" class="w-40" @change="reload" />
          <div class="flex flex-1 justify-end">
            <button class="btn btn-secondary" :disabled="loading" :title="t('common.refresh')" @click="reload">
              <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
            </button>
          </div>
        </div>
      </div>

      <!-- 列表 -->
      <div class="card overflow-x-auto">
        <table class="w-full text-left text-sm">
          <thead class="whitespace-nowrap border-b border-gray-200 text-xs text-gray-500 dark:border-dark-700 dark:text-gray-400">
            <tr>
              <th class="px-4 py-3 font-medium">{{ t('otok.usdt.createdAt') }}</th>
              <th class="px-4 py-3 font-medium">{{ t('otok.usdt.network') }}</th>
              <th class="px-4 py-3 font-medium">{{ t('otok.usdt.amount') }}</th>
              <th class="px-4 py-3 font-medium">TxHash</th>
              <th class="px-4 py-3 font-medium">{{ t('otok.usdt.fromAddress') }}</th>
              <th class="px-4 py-3 font-medium">{{ t('otok.usdt.status') }}</th>
              <th class="px-4 py-3 font-medium">{{ t('otok.usdt.order') }}</th>
              <th class="px-4 py-3 font-medium">{{ t('otok.usdt.note') }}</th>
              <th class="px-4 py-3 font-medium"></th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-if="!loading && items.length === 0">
              <td colspan="9" class="px-4 py-10 text-center text-gray-500 dark:text-gray-400">{{ t('otok.usdt.empty') }}</td>
            </tr>
            <tr v-for="row in items" :key="row.id" class="text-gray-700 dark:text-gray-300">
              <td class="whitespace-nowrap px-4 py-2 text-xs">{{ formatOrderDateTime(row.block_time || row.created_at) }}</td>
              <td class="whitespace-nowrap px-4 py-2 text-xs">{{ networkLabel(row.network) }}</td>
              <td class="whitespace-nowrap px-4 py-2 font-medium tabular-nums text-gray-900 dark:text-white">{{ row.amount }}</td>
              <td class="px-4 py-2 font-mono text-xs">
                <a :href="row.explorer_url" target="_blank" rel="noopener noreferrer" class="text-primary-600 hover:underline dark:text-primary-400" :title="row.tx_hash">{{ shortHash(row.tx_hash) }}</a>
                <span v-if="row.log_index > 0" class="text-gray-400"> #{{ row.log_index }}</span>
              </td>
              <td class="px-4 py-2 font-mono text-xs" :title="row.from_address">{{ shortHash(row.from_address) }}</td>
              <td class="whitespace-nowrap px-4 py-2 text-xs">
                <span :class="['rounded-full px-2 py-0.5 font-medium', statusClass(row.status)]">{{ statusLabel('transferStatus', row.status) }}</span>
                <span class="ml-1 text-gray-400">{{ statusLabel('sources', row.source) }}</span>
              </td>
              <td class="whitespace-nowrap px-4 py-2 text-xs">{{ row.order_id ? `#${row.order_id}` : '-' }}</td>
              <td class="max-w-xs px-4 py-2 text-xs text-gray-500 dark:text-gray-400" :title="row.note">{{ truncate(row.note || '', 60) }}</td>
              <td class="whitespace-nowrap px-4 py-2 text-right">
                <button
                  v-if="!row.order_id && (row.status === 'seen' || row.status === 'review')"
                  class="rounded-md px-2 py-1 text-xs font-medium text-gray-600 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-dark-600"
                  @click="openIgnore(row)"
                >{{ t('otok.usdt.ignore') }}</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <Pagination
        v-if="pagination.total > 0"
        :page="pagination.page"
        :total="pagination.total"
        :page-size="pagination.page_size"
        @update:page="onPageChange"
        @update:pageSize="onPageSizeChange"
      />
    </div>

    <BaseDialog :show="!!ignoring" :title="t('otok.usdt.ignore')" width="normal" @close="ignoring = null">
      <div class="space-y-3">
        <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('otok.usdt.ignoreConfirm') }}</p>
        <input v-model="ignoreNote" type="text" class="input" :placeholder="t('otok.usdt.ignoreNotePlaceholder')" />
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button class="btn btn-secondary" @click="ignoring = null">{{ t('common.cancel') }}</button>
          <button class="btn btn-primary" :disabled="ignoreSubmitting" @click="confirmIgnore">{{ ignoreSubmitting ? t('common.processing') : t('common.confirm') }}</button>
        </div>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
// otok: USDT 链上到账记录、未匹配 / 待审核转账与监听健康状态。
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import { otokAdminUsdtAPI, type OtokUsdtHealth, type OtokUsdtHealthCursor, type OtokUsdtTransfer } from '@/api/otok/usdt'
import { extractI18nErrorMessage } from '@/utils/apiError'
import { formatOrderDateTime } from '@/components/payment/orderUtils'

const { t, te } = useI18n()
const appStore = useAppStore()

const TRANSFER_STATUSES = ['seen', 'review', 'bound', 'credit_failed', 'credited', 'ignored', 'dropped'] as const
const NETWORKS = ['trc20', 'bep20', 'polygon', 'erc20'] as const

const loading = ref(false)
const items = ref<OtokUsdtTransfer[]>([])
const health = ref<OtokUsdtHealth | null>(null)
const filters = reactive({ keyword: '', status: '', network: '' })
const pagination = reactive({ page: 1, page_size: 20, total: 0 })
const ignoring = ref<OtokUsdtTransfer | null>(null)
const ignoreNote = ref('')
const ignoreSubmitting = ref(false)

const statusOptions = computed(() => [
  { value: '', label: t('otok.usdt.filterAll') },
  ...TRANSFER_STATUSES.map((value) => ({ value, label: statusLabel('transferStatus', value) })),
])

const networkOptions = computed(() => [
  { value: '', label: t('otok.usdt.filterAllNetworks') },
  ...NETWORKS.map((value) => ({ value, label: networkLabel(value) })),
])

const healthStats = computed(() => {
  const h = health.value
  return [
    { key: 'active', label: t('otok.usdt.activeReservations'), value: h?.active_reservations ?? 0, warn: false },
    { key: 'unmatched', label: t('otok.usdt.unmatchedTransfers'), value: h?.unmatched_transfers ?? 0, warn: true },
    { key: 'review', label: t('otok.usdt.reviewTransfers'), value: h?.review_transfers ?? 0, warn: true },
    { key: 'failed', label: t('otok.usdt.creditFailedTransfers'), value: h?.credit_failed_transfers ?? 0, warn: true },
    { key: 'awaiting', label: t('otok.usdt.awaitingConfirmation'), value: h?.awaiting_confirmation_transfers ?? 0, warn: false },
  ]
})

function networkLabel(network: string): string {
  const key = `otok.usdt.networks.${network}`
  return te(key) ? t(key) : network
}

function statusLabel(group: 'transferStatus' | 'sources', value: string): string {
  const key = `otok.usdt.${group}.${value}`
  return te(key) ? t(key) : value
}

function statusClass(status: string): string {
  switch (status) {
    case 'credited': return 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-300'
    case 'bound': return 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300'
    case 'review':
    case 'credit_failed': return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
    case 'seen': return 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
    default: return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-400'
  }
}

function shortHash(value: string): string {
  if (!value || value.length <= 18) return value || '-'
  return `${value.slice(0, 10)}…${value.slice(-6)}`
}

function truncate(value: string, max: number): string {
  return value.length > max ? `${value.slice(0, max)}…` : value
}

function isErrorNewer(cur: OtokUsdtHealthCursor): boolean {
  if (!cur.last_error || !cur.last_error_at) return false
  if (!cur.last_success_at) return true
  return new Date(cur.last_error_at).getTime() >= new Date(cur.last_success_at).getTime()
}

async function loadHealth() {
  try {
    health.value = (await otokAdminUsdtAPI.getHealth()).data
  } catch {
    health.value = null
  }
}

async function loadTransfers() {
  loading.value = true
  try {
    const res = await otokAdminUsdtAPI.listTransfers({
      page: pagination.page,
      page_size: pagination.page_size,
      status: filters.status || undefined,
      network: filters.network || undefined,
      keyword: filters.keyword.trim() || undefined,
    })
    items.value = res.data.items || []
    pagination.total = res.data.total
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    loading.value = false
  }
}

function reload() {
  pagination.page = 1
  void loadTransfers()
  void loadHealth()
}

let debounceTimer: ReturnType<typeof setTimeout> | null = null
function debouncedLoad() {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(reload, 400)
}

function onPageChange(page: number) {
  pagination.page = page
  void loadTransfers()
}

function onPageSizeChange(size: number) {
  pagination.page_size = size
  pagination.page = 1
  void loadTransfers()
}

function openIgnore(row: OtokUsdtTransfer) {
  ignoring.value = row
  ignoreNote.value = ''
}

async function confirmIgnore() {
  if (!ignoring.value || ignoreSubmitting.value) return
  ignoreSubmitting.value = true
  try {
    await otokAdminUsdtAPI.ignoreTransfer(ignoring.value.id, ignoreNote.value.trim())
    appStore.showSuccess(t('otok.usdt.ignoreSuccess'))
    ignoring.value = null
    void loadTransfers()
    void loadHealth()
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    ignoreSubmitting.value = false
  }
}

onMounted(() => {
  void loadTransfers()
  void loadHealth()
})
</script>
