<template>
  <article class="channel-signal-card group relative z-0 glass-card flex min-h-[286px] flex-col overflow-visible rounded-[24px] p-5 text-left hover:z-20">
    <header class="channel-signal-card__header flex items-start gap-3">
      <span class="channel-signal-card__provider grid h-9 w-9 shrink-0 place-items-center rounded-xl ring-1 ring-black/5 dark:ring-white/10" :class="providerGradient(row.platform)">
        <ProviderIcon :provider="row.platform" :size="20" />
      </span>
      <div class="min-w-0 flex-1">
        <div class="channel-signal-card__name truncate text-base font-semibold text-gray-900 dark:text-gray-100">{{ groupLabel }}</div>
        <div class="channel-signal-card__meta mt-1 flex min-w-0 flex-wrap items-center gap-1.5">
          <span v-if="showPlatformBadge" class="rounded-md px-1.5 py-0.5 text-[10px] font-medium" :class="providerBadgeClass(row.platform)">{{ providerLabel(row.platform) }}</span>
          <span class="rounded-md bg-primary-50 px-1.5 py-0.5 font-mono text-[10px] font-medium text-primary-700 dark:bg-dark-700 dark:text-gray-300">{{ t('channelMonitorV3.userRate') }} {{ formattedUserRate }}</span>
        </div>
      </div>
      <span
        data-testid="channel-status-badge"
        :data-state="signalState"
        class="channel-signal-card__status shrink-0 rounded-full px-2.5 py-1 text-xs font-semibold"
        :class="SIGNAL_STATE_STYLE[signalState].badgeClass"
      >{{ t(`channelMonitorV3.${signalState}`) }}</span>
    </header>

    <div class="channel-signal-card__metrics mt-5 grid grid-cols-3 gap-2">
      <div class="channel-signal-card__metric rounded-2xl border border-slate-200/80 bg-slate-50/85 p-3 dark:border-dark-700/50 dark:bg-dark-900/40">
        <div class="text-[10px] font-semibold uppercase tracking-wider text-gray-400">{{ t('channelMonitorV3.cacheRate') }}</div>
        <div class="channel-signal-card__value mt-1.5 font-mono text-lg font-bold tabular-nums text-gray-900 dark:text-gray-100">{{ cacheRate }}</div>
      </div>
      <div class="channel-signal-card__metric rounded-2xl border border-slate-200/80 bg-slate-50/85 p-3 dark:border-dark-700/50 dark:bg-dark-900/40">
        <div class="text-[10px] font-semibold uppercase tracking-wider text-gray-400">{{ t('channelMonitorV3.successRate') }}</div>
        <div class="channel-signal-card__value mt-1.5 font-mono text-lg font-bold tabular-nums" :class="availabilityClass">{{ successRate }}</div>
      </div>
      <div class="channel-signal-card__metric rounded-2xl border border-slate-200/80 bg-slate-50/85 p-3 dark:border-dark-700/50 dark:bg-dark-900/40" :title="ttftHint" data-testid="channel-status-ttft">
        <div class="text-[10px] font-semibold uppercase tracking-wider text-gray-400">{{ t('channelMonitorV3.ttft') }}</div>
        <div class="channel-signal-card__value mt-1.5 font-mono text-lg font-bold tabular-nums" :class="[ttftClass, ttftHint ? 'cursor-help' : '']">{{ ttft }}</div>
      </div>
    </div>

    <ChannelMonitorV3Timeline
      class="channel-signal-card__timeline mt-auto"
      :buckets="row.buckets"
      :countdown-seconds="countdownSeconds"
      :length="timelineLength"
      :ttft-thresholds="ttftThresholds"
    />
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { MonitorMatrixRow } from '@/api/channelMonitorV2'
import {
  SIGNAL_STATE_STYLE,
  availabilitySignalState,
  channelSignalState,
  formatMonitorMs,
  formatMonitorPercent,
  metricHasTraffic,
  ttftSignalState,
  type SignalTtftThresholds,
} from '@/features/channel-monitor-v2/monitorFormat'
import { providerGradient, useChannelMonitorFormat } from '@/composables/useChannelMonitorFormat'
import ProviderIcon from './ProviderIcon.vue'
import ChannelMonitorV3Timeline from './ChannelMonitorV3Timeline.vue'

const props = withDefaults(defineProps<{
  row: MonitorMatrixRow
  countdownSeconds: number
  timelineLength: number
  userRateMultiplier?: number | null
  showPlatformBadge?: boolean
  ttftThresholds?: SignalTtftThresholds | null
}>(), {
  showPlatformBadge: true,
  ttftThresholds: null,
})
const { t } = useI18n()
const { providerLabel, providerBadgeClass } = useChannelMonitorFormat()

const groupLabel = computed(() => props.row.group_name || t('channelMonitorV3.unknownGroup'))
const formattedUserRate = computed(() => {
  const value = props.userRateMultiplier
  return typeof value === 'number' && Number.isFinite(value) ? `${value.toFixed(2)}x` : '-'
})
// 卡片指标按所选时间范围（90m / 24h / 7d / 30d）整体汇总，与顶部范围切换一致；
// 低流量分组若只看最新 5 分钟桶，几次请求就会让缓存率、首 Token 大幅跳动。逐段变化交给底部色块。
const metrics = computed(() => props.row.metrics)
const hasTraffic = computed(() => metricHasTraffic(metrics.value))
const signalState = computed(() => channelSignalState(metrics.value, props.ttftThresholds))
const cacheRate = computed(() => hasTraffic.value ? formatMonitorPercent(metrics.value.cache_rate) : '-')
const availabilityPercent = computed(() => (1 - metrics.value.error_rate) * 100)
const successRate = computed(() => hasTraffic.value ? formatMonitorPercent(availabilityPercent.value / 100) : '-')
const availabilityClass = computed(() => SIGNAL_STATE_STYLE[hasTraffic.value ? availabilitySignalState(availabilityPercent.value) : 'unknown'].textClass)
const ttft = computed(() => hasTraffic.value ? formatMonitorMs(metrics.value.ttft.p50_ms) : '-')
const ttftClass = computed(() => SIGNAL_STATE_STYLE[hasTraffic.value ? ttftSignalState(metrics.value.ttft.p50_ms, props.ttftThresholds) : 'unknown'].textClass)
// 有请求却没有首 Token：首字时间只在流式请求里记录，该时段全是非流式请求
const ttftHint = computed(() => hasTraffic.value && metrics.value.ttft.p50_ms == null ? t('channelMonitorV3.ttftNoStream') : undefined)
</script>

