<template>
  <article class="channel-signal-card group relative z-0 glass-card flex min-h-[286px] flex-col overflow-visible rounded-[24px] p-5 text-left hover:z-20">
    <header class="channel-signal-card__header flex items-start gap-3">
      <span class="channel-signal-card__provider grid h-11 w-11 shrink-0 place-items-center rounded-2xl text-gray-800 ring-1 ring-black/5 dark:text-gray-100 dark:ring-white/10" :class="providerGradient(row.platform)">
        <ProviderIcon :provider="row.platform" :size="22" />
      </span>
      <div class="min-w-0 flex-1">
        <div class="channel-signal-card__name truncate font-semibold leading-snug text-gray-900 dark:text-gray-50">{{ groupLabel }}</div>
        <div class="channel-signal-card__meta mt-1.5 flex min-w-0 flex-wrap items-center gap-1.5">
          <span v-if="showPlatformBadge" class="rounded-md px-2 py-0.5 text-[11px] font-medium" :class="providerBadgeClass(row.platform)">{{ providerLabel(row.platform) }}</span>
          <span class="rounded-md bg-primary-50 px-2 py-0.5 font-mono text-[11px] font-medium text-primary-700 dark:bg-dark-700 dark:text-gray-300">{{ t('channelMonitorV3.userRate') }} {{ formattedUserRate }}</span>
        </div>
      </div>
      <span
        data-testid="channel-status-badge"
        :data-state="signalState"
        class="channel-signal-card__status shrink-0 rounded-full px-3 py-1 text-sm font-semibold"
        :class="SIGNAL_STATE_STYLE[signalState].badgeClass"
      >{{ t(`channelMonitorV3.${signalState}`) }}</span>
    </header>

    <div class="channel-signal-card__metrics mt-5 grid grid-cols-3 gap-2.5">
      <div class="channel-signal-card__metric rounded-2xl border border-slate-200/80 bg-slate-50/85 px-3.5 py-3 dark:border-dark-700/50 dark:bg-dark-900/40">
        <div class="text-[11px] font-semibold uppercase tracking-wider text-gray-400">{{ t('channelMonitorV3.cacheRate') }}</div>
        <div class="channel-signal-card__value mt-2 font-mono font-bold tabular-nums text-gray-900 dark:text-gray-50">{{ cacheRate }}</div>
      </div>
      <div class="channel-signal-card__metric rounded-2xl border border-slate-200/80 bg-slate-50/85 px-3.5 py-3 dark:border-dark-700/50 dark:bg-dark-900/40">
        <div class="text-[11px] font-semibold uppercase tracking-wider text-gray-400">{{ t('channelMonitorV3.successRate') }}</div>
        <div class="channel-signal-card__value mt-2 font-mono font-bold tabular-nums" :class="availabilityClass">{{ successRate }}</div>
      </div>
      <div class="channel-signal-card__metric rounded-2xl border border-slate-200/80 bg-slate-50/85 px-3.5 py-3 dark:border-dark-700/50 dark:bg-dark-900/40">
        <div class="text-[11px] font-semibold uppercase tracking-wider text-gray-400">{{ t('channelMonitorV3.ttft') }}</div>
        <div class="channel-signal-card__value mt-2 font-mono font-bold tabular-nums" :class="ttftClass">{{ ttft }}</div>
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
// 卡片展示最新一个有请求的时间桶；刚跨桶时最新桶可能为空，不能拿它显示 0% / 100%
const latestMetrics = computed(() => [...props.row.buckets]
  .filter(bucket => bucket.bucket_start && metricHasTraffic(bucket.metrics))
  .sort((a, b) => Date.parse(a.bucket_start) - Date.parse(b.bucket_start))
  .at(-1)?.metrics ?? props.row.metrics)
const hasTraffic = computed(() => metricHasTraffic(latestMetrics.value))
const signalState = computed(() => channelSignalState(latestMetrics.value, props.ttftThresholds))
const cacheRate = computed(() => hasTraffic.value ? formatMonitorPercent(latestMetrics.value.cache_rate) : '-')
const availabilityPercent = computed(() => (1 - latestMetrics.value.error_rate) * 100)
const successRate = computed(() => hasTraffic.value ? formatMonitorPercent(availabilityPercent.value / 100) : '-')
const availabilityClass = computed(() => SIGNAL_STATE_STYLE[hasTraffic.value ? availabilitySignalState(availabilityPercent.value) : 'unknown'].textClass)
const ttft = computed(() => hasTraffic.value ? formatMonitorMs(latestMetrics.value.ttft.p50_ms) : '-')
const ttftClass = computed(() => SIGNAL_STATE_STYLE[hasTraffic.value ? ttftSignalState(latestMetrics.value.ttft.p50_ms, props.ttftThresholds) : 'unknown'].textClass)
</script>

<style scoped>
/* 按卡片内容宽度分档放大字号：宽卡片对齐参考排版，窄卡片保证「100.0%」不溢出（等宽字宽约 0.6em） */
.channel-signal-card {
  container-type: inline-size;
}

.channel-signal-card__name {
  font-size: 1.125rem;
  line-height: 1.75rem;
}

.channel-signal-card__value {
  font-size: 1.125rem;
  line-height: 1.75rem;
}

@container (min-width: 320px) {
  .channel-signal-card__value {
    font-size: 1.25rem;
  }
}

@container (min-width: 368px) {
  .channel-signal-card__name {
    font-size: 1.25rem;
  }

  .channel-signal-card__value {
    font-size: 1.5rem;
    line-height: 2rem;
  }
}
</style>
