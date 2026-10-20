<template>
  <article
    class="flex w-full flex-col rounded-2xl border border-gray-200/80 bg-white/70 p-5 shadow-card backdrop-blur-xl transition-all duration-300 hover:-translate-y-0.5 hover:shadow-card-hover dark:border-dark-700/70 dark:bg-dark-800/60"
    data-testid="otok-monitor-card"
  >
    <!-- 头部：图标 + 分组名 + 状态徽章 -->
    <div class="flex items-start gap-3">
      <span
        class="grid h-10 w-10 flex-shrink-0 place-items-center rounded-xl ring-1 ring-black/5 dark:ring-white/10"
        :class="[providerGradient(card.provider), providerTint(card.provider)]"
      >
        <ProviderIcon :provider="card.provider as Provider" :size="20" />
      </span>
      <div class="min-w-0 flex-1">
        <div class="truncate text-base font-semibold text-gray-900 dark:text-gray-100" :title="card.name">
          {{ card.name }}
        </div>
        <div class="mt-1 flex min-w-0 flex-wrap items-center gap-1.5">
          <span
            class="inline-flex items-center rounded-md px-1.5 py-0.5 text-[10px] font-medium"
            :class="providerBadgeClass(card.provider)"
          >
            {{ providerLabel(card.provider) }}
          </span>
          <span
            v-if="card.rate != null"
            class="inline-flex items-center rounded-md bg-gray-100 px-1.5 py-0.5 font-mono text-[10px] font-medium text-gray-600 dark:bg-dark-700 dark:text-gray-300"
          >
            {{ t('otok.channelCards.userRate', { rate: formatRate(card.rate) }) }}
          </span>
        </div>
      </div>
      <span
        class="flex-shrink-0 rounded-full px-2.5 py-1 text-xs font-semibold"
        :class="STATE_BADGE[card.state]"
        data-testid="otok-monitor-state"
      >
        {{ t(`otok.channelCards.states.${card.state}`) }}
      </span>
    </div>

    <!-- 指标：缓存率 / 可用率 / 首 Token -->
    <div class="mt-4 grid grid-cols-3 gap-2">
      <div
        v-for="metric in metrics"
        :key="metric.key"
        class="rounded-xl border border-gray-100 bg-gray-50/70 px-3 py-2.5 dark:border-dark-700/60 dark:bg-dark-900/40"
      >
        <div class="truncate text-[10px] font-semibold uppercase tracking-wider text-gray-400">
          {{ metric.label }}
        </div>
        <div class="mt-1 font-mono text-lg font-bold tabular-nums" :class="VALUE_COLOR[metric.state]">
          {{ metric.value }}
        </div>
      </div>
    </div>

    <!-- 降智检测（鹈鹕测试）入口：只有启用了的分组才显示 -->
    <div v-if="pelicanEnabled" class="mt-3 flex justify-end">
      <button
        type="button"
        class="rounded-lg border border-gray-200 bg-white/60 px-3 py-1.5 text-xs font-semibold text-gray-700 transition-colors hover:border-primary-300 hover:text-primary-600 dark:border-dark-600 dark:bg-dark-900/40 dark:text-gray-200 dark:hover:border-primary-500/60 dark:hover:text-primary-300"
        data-testid="otok-monitor-pelican"
        @click="emit('pelican')"
      >
        {{ t('otok.pelican.button') }}
      </button>
    </div>

    <div class="flex-1"></div>

    <OtokMonitorBars :bars="card.bars" :countdown-seconds="countdownSeconds" />
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Provider } from '@/api/admin/channelMonitor'
import ProviderIcon from '@/components/user/monitor/ProviderIcon.vue'
import { providerGradient, useChannelMonitorFormat } from '@/composables/useChannelMonitorFormat'
import {
  formatPercent,
  formatRate,
  formatTtft,
  providerTint,
  type OtokCardState,
  type OtokMonitorCard,
} from '@/otok/monitorCards'
import OtokMonitorBars from './OtokMonitorBars.vue'

const props = defineProps<{
  card: OtokMonitorCard
  countdownSeconds: number
  pelicanEnabled?: boolean
}>()

const emit = defineEmits<{ (e: 'pelican'): void }>()

const { t } = useI18n()
const { providerLabel, providerBadgeClass } = useChannelMonitorFormat()

const STATE_BADGE: Record<OtokCardState, string> = {
  healthy: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300',
  warning: 'bg-amber-100 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300',
  critical: 'bg-red-100 text-red-700 dark:bg-red-500/15 dark:text-red-300',
  unknown: 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-400',
}

const VALUE_COLOR: Record<OtokCardState, string> = {
  healthy: 'text-emerald-600 dark:text-emerald-400',
  warning: 'text-amber-600 dark:text-amber-400',
  critical: 'text-red-600 dark:text-red-400',
  unknown: 'text-gray-900 dark:text-gray-100',
}

const metrics = computed(() => [
  {
    key: 'cache',
    label: t('otok.channelCards.cacheRate'),
    value: formatPercent(props.card.cacheRate),
    // 缓存率只展示，不参与状态判定。
    state: 'unknown' as OtokCardState,
  },
  {
    key: 'availability',
    label: t('otok.channelCards.availability'),
    value: formatPercent(props.card.availability),
    state: props.card.availabilityState,
  },
  {
    key: 'ttft',
    label: t('otok.channelCards.ttft'),
    value: formatTtft(props.card.ttftMs),
    state: props.card.ttftState,
  },
])
</script>
