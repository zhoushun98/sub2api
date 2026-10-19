<template>
  <AppLayout>
    <div class="space-y-6 pb-12">
      <!-- 头部：标题 + 更新时间 + 刷新；工具条：时间窗 + 口径说明 + 汇总 -->
      <section class="card !rounded-3xl !border-0 p-0 shadow-sm ring-1 ring-gray-900/5 dark:!bg-dark-800 dark:ring-dark-700">
        <header class="flex flex-wrap items-start justify-between gap-4 border-b border-gray-100 px-5 py-4 dark:border-dark-700 sm:px-6">
          <div class="min-w-0">
            <h1 class="flex items-center gap-2 text-xl font-black text-gray-900 dark:text-white">
              <span class="inline-flex h-8 w-8 items-center justify-center rounded-xl bg-blue-50 text-blue-500 dark:bg-blue-900/30 dark:text-blue-400">
                <Icon name="chart" size="sm" />
              </span>
              {{ t('otok.channelCards.title') }}
            </h1>
            <div class="mt-1.5 flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
              <span
                class="inline-flex h-2 w-2 shrink-0 rounded-full"
                :class="loading ? 'bg-gray-400' : 'bg-green-500'"
              ></span>
              <span v-if="loading" class="inline-flex items-center gap-1 text-primary-600 dark:text-primary-300">
                <LoadingSpinner size="sm" />
                {{ t('otok.channelCards.updating') }}
              </span>
              <span v-else-if="snapshot?.coverage.data_through">
                {{ t('otok.channelCards.updatedTo', { time: formatTime(snapshot.coverage.data_through) }) }}
              </span>
            </div>
          </div>
          <button
            class="btn btn-secondary btn-icon flex h-8 w-8 items-center justify-center rounded-lg"
            type="button"
            :title="t('common.refresh')"
            :disabled="loading"
            @click="reload(true)"
          >
            <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
          </button>
        </header>

        <div
          v-if="bootstrapActive"
          class="border-b border-blue-100 bg-blue-50/90 px-5 py-3 dark:border-blue-900/40 dark:bg-blue-950/40 sm:px-6"
          role="status"
        >
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div class="min-w-0 flex-1">
              <p class="text-sm font-semibold text-blue-900 dark:text-blue-100">
                {{ t('channelMonitorV2.bootstrap.title') }}
              </p>
              <p class="mt-0.5 text-xs text-blue-800/80 dark:text-blue-200/80">
                {{ t('channelMonitorV2.bootstrap.description') }}
              </p>
            </div>
            <span class="shrink-0 text-xs font-medium tabular-nums text-blue-700 dark:text-blue-300">
              {{ t('channelMonitorV2.bootstrap.progress', { percent: bootstrapPercent }) }}
            </span>
          </div>
        </div>

        <div class="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-3 sm:px-5">
          <div class="tabs inline-flex shrink-0" role="group">
            <button
              v-for="option in RANGES"
              :key="option"
              type="button"
              class="tab !px-2.5 !py-1 text-xs"
              :class="range === option ? 'tab-active' : ''"
              @click="setRange(option)"
            >
              {{ t(`channelMonitorV2.ranges.${option}`) }}
            </button>
          </div>
          <span class="hidden h-5 w-px bg-gray-200 dark:bg-dark-700 md:block" aria-hidden="true"></span>
          <!-- 窄屏：口径说明单独占最后一行；md 起与时间窗、汇总同一行 -->
          <span class="order-last w-full text-xs leading-relaxed text-gray-500 dark:text-gray-400 md:order-none md:w-auto md:min-w-0 md:flex-1">
            {{ t('otok.channelCards.legend') }}
          </span>
          <span v-if="summary" class="ml-auto shrink-0 text-xs font-medium tabular-nums text-gray-600 dark:text-gray-300">
            {{ summary }}
          </span>
        </div>
      </section>

      <!-- 首次加载骨架 -->
      <div v-if="loading && !loaded" class="grid grid-cols-1 gap-5 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
        <div
          v-for="i in 4"
          :key="i"
          class="h-[260px] animate-pulse rounded-2xl bg-gray-100 dark:bg-dark-800/60"
        ></div>
      </div>

      <EmptyState
        v-else-if="sections.length === 0"
        :title="t('otok.channelCards.empty')"
        :description="t('otok.channelCards.emptyHint')"
      />

      <template v-else>
        <section v-for="section in sections" :key="section.provider" class="space-y-4">
          <h2 class="flex items-center gap-3">
            <span
              class="grid h-9 w-9 place-items-center rounded-xl ring-1 ring-black/5 dark:ring-white/10"
              :class="[providerGradient(section.provider), providerTint(section.provider)]"
            >
              <ProviderIcon :provider="section.provider as Provider" :size="20" />
            </span>
            <span class="text-lg font-bold text-gray-900 dark:text-white">{{ providerLabel(section.provider) }}</span>
            <span class="rounded-full bg-gray-100 px-2 py-0.5 text-xs font-semibold text-gray-600 dark:bg-dark-700 dark:text-gray-300">
              {{ section.cards.length }}
            </span>
          </h2>
          <div class="grid grid-cols-1 gap-5 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
            <OtokMonitorCard
              v-for="card in section.cards"
              :key="card.key"
              :card="card"
              :countdown-seconds="countdown"
            />
          </div>
        </section>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import ProviderIcon from '@/components/user/monitor/ProviderIcon.vue'
import OtokMonitorCard from '@/components/otok/monitor/OtokMonitorCard.vue'
import type { Provider } from '@/api/admin/channelMonitor'
import * as monitorAPI from '@/api/channelMonitorV2'
import type {
  MonitorDimensions,
  MonitorFilter,
  MonitorMatrixResponse,
  MonitorRange,
  MonitorSnapshot,
} from '@/api/channelMonitorV2'
import { userGroupsAPI } from '@/api/groups'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { extractApiErrorMessage } from '@/utils/apiError'
import { providerGradient, useChannelMonitorFormat } from '@/composables/useChannelMonitorFormat'
import {
  availabilityOf,
  buildCards,
  cacheRateOf,
  formatPercent,
  groupByProvider,
  providerTint,
} from '@/otok/monitorCards'

const RANGES: MonitorRange[] = ['90m', '24h', '7d', '30d']
const MIN_REFRESH_SECONDS = 10

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const { providerLabel } = useChannelMonitorFormat()
const isAdmin = computed(() => authStore.isAdmin)

const range = ref<MonitorRange>('90m')
const snapshot = ref<MonitorSnapshot | null>(null)
const matrix = ref<MonitorMatrixResponse | null>(null)
const dimensions = ref<MonitorDimensions | null>(null)
const groupRates = ref<Record<number, number>>({})
const userRates = ref<Record<number, number>>({})
const loading = ref(false)
const loaded = ref(false)
const countdown = ref(0)

let controller: AbortController | null = null
let sequence = 0
let timer: number | undefined

const bootstrapActive = computed(() => !!snapshot.value?.coverage.bootstrap?.active)
const bootstrapPercent = computed(() =>
  Math.round(Math.min(100, Math.max(0, snapshot.value?.coverage.bootstrap?.progress_percent ?? 0)))
)

/** 回填期间加快轮询，让进度条能动起来；平时跟随后台配置的聚合周期。 */
const refreshSeconds = computed(() => {
  if (bootstrapActive.value) return MIN_REFRESH_SECONDS
  return Math.max(MIN_REFRESH_SECONDS, snapshot.value?.config?.refresh_interval_seconds || 60)
})

const sections = computed(() =>
  groupByProvider(
    buildCards({
      range: range.value,
      coverage: matrix.value?.coverage,
      rows: matrix.value?.items ?? [],
      dimensionGroups: dimensions.value?.groups ?? [],
      groupRates: groupRates.value,
      userRates: userRates.value,
    })
  )
)

const summary = computed(() => {
  const metrics = snapshot.value?.metrics
  const availability = availabilityOf(metrics)
  if (availability == null) return ''
  return t('otok.channelCards.summary', {
    availability: formatPercent(availability),
    cache: formatPercent(cacheRateOf(metrics)),
  })
})

function currentFilter(): MonitorFilter {
  return { range: range.value, platforms: [], groupIds: [], models: [] }
}

function isCanceled(error: unknown): boolean {
  const e = error as { name?: string; code?: string }
  return e?.name === 'AbortError' || e?.name === 'CanceledError' || e?.code === 'ERR_CANCELED'
}

/** 倍率变化很少，只在首次加载和手动刷新时拉取；失败不影响卡片展示。 */
async function loadRates() {
  const [groups, rates] = await Promise.all([
    userGroupsAPI.getAvailable().catch(() => []),
    userGroupsAPI.getUserGroupRates().catch(() => ({})),
  ])
  groupRates.value = Object.fromEntries(groups.map((g) => [g.id, g.rate_multiplier]))
  userRates.value = rates
}

async function reload(withRates = false) {
  controller?.abort()
  const request = new AbortController()
  controller = request
  const id = ++sequence
  loading.value = true
  try {
    const filter = currentFilter()
    const [nextSnapshot, nextMatrix, nextDimensions] = await Promise.all([
      monitorAPI.getSnapshot(filter, isAdmin.value, request.signal),
      monitorAPI.getMatrix(filter, 'platform_group', isAdmin.value, request.signal),
      monitorAPI.getDimensions(filter, isAdmin.value, request.signal),
      withRates ? loadRates() : Promise.resolve(),
    ])
    if (id !== sequence) return
    snapshot.value = nextSnapshot
    matrix.value = nextMatrix
    dimensions.value = nextDimensions
    loaded.value = true
  } catch (error) {
    if (!isCanceled(error)) {
      appStore.showError(extractApiErrorMessage(error, t('otok.channelCards.loadFailed')))
    }
  } finally {
    if (id === sequence) {
      loading.value = false
      countdown.value = refreshSeconds.value
    }
  }
}

function setRange(value: MonitorRange) {
  if (range.value === value) return
  range.value = value
  void reload()
}

function tick() {
  if (loading.value || document.hidden) return
  if (countdown.value <= 1) {
    void reload()
    return
  }
  countdown.value -= 1
}

function formatTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString(undefined, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}

onMounted(() => {
  void reload(true)
  timer = window.setInterval(tick, 1000)
})

onBeforeUnmount(() => {
  controller?.abort()
  if (timer !== undefined) window.clearInterval(timer)
})
</script>
