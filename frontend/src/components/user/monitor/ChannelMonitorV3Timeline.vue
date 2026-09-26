<template>
  <div class="channel-signal-timeline mt-4 border-t border-white/70 pt-3 dark:border-dark-700/60">
    <div class="channel-signal-timeline__legend mb-2 flex justify-between text-[10px] font-semibold uppercase tracking-widest text-gray-400">
      <span>{{ t('monitorCommon.history60pts', { n: length }) }}</span>
      <span class="tabular-nums">{{ t('monitorCommon.nextUpdateIn', { n: countdownSeconds }) }}</span>
    </div>

    <div class="v3-timeline-bars" @mouseleave="clearHoveredBar">
      <div
        v-for="(bar, index) in displayBars"
        :key="bar.key"
        class="v3-bar-slot"
        @mouseenter="setHoveredBar(index, $event)"
      >
        <button
          type="button"
          class="v3-bar-hitbox"
          :class="{
            'is-active': hoveredBarIndex === index,
            'is-neighbor': barDistance(index) === 1,
            'is-pressed': hoveredBarIndex !== null && barDistance(index) > 0,
          }"
          :aria-label="bar.title || '-'"
          @focus="setHoveredBar(index, $event)"
          @blur="clearHoveredBar"
        >
          <span class="v3-bar-visual" :style="barMotionStyle(index)" aria-hidden="true">
            <span
              class="v3-soft-glass-bar"
              :class="bar.colorClass"
              :style="{ height: `${bar.heightPct}%`, animationDelay: `${index * 18}ms` }"
            />
          </span>
        </button>
      </div>
    </div>

    <Teleport to="body">
      <Transition name="v3-timeline-tooltip">
        <div
          v-if="hoveredBar?.title"
          ref="tooltipEl"
          class="v3-timeline-tooltip"
          role="tooltip"
          data-testid="channel-timeline-tooltip"
          :style="tooltipStyle"
        >
          {{ hoveredBar.title }}
        </div>
      </Transition>
    </Teleport>

    <div class="channel-signal-timeline__axis mt-1 flex justify-between text-[9px] uppercase tracking-widest text-gray-400">
      <span>{{ t('monitorCommon.past') }}</span>
      <span>{{ t('monitorCommon.now') }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { MonitorMatrixBucket } from '@/api/channelMonitorV2'
import {
  SIGNAL_STATE_STYLE,
  channelSignalState,
  formatMonitorMs,
  formatMonitorPercent,
  metricHasTraffic,
  type SignalTtftThresholds,
} from '@/features/channel-monitor-v2/monitorFormat'

const props = withDefaults(defineProps<{
  buckets?: MonitorMatrixBucket[]
  countdownSeconds: number
  length?: number
  ttftThresholds?: SignalTtftThresholds | null
}>(), {
  buckets: () => [],
  length: 18,
  ttftThresholds: null,
})

const { t, locale } = useI18n()
const hoveredBarIndex = ref<number | null>(null)
// 被指向色块的视口坐标（中心 x、顶部 y）；提示框位置与箭头都由它推算
const tooltipAnchor = ref({ center: 0, top: 0 })
const tooltipEl = ref<HTMLElement | null>(null)
const tooltipWidth = ref(280)
const TOOLTIP_GUTTER = 16
const TOOLTIP_ARROW_INSET = 12

function setHoveredBar(index: number, event?: Event) {
  hoveredBarIndex.value = index
  const target = event?.currentTarget
  if (!(target instanceof HTMLElement)) return
  const rect = target.getBoundingClientRect()
  tooltipAnchor.value = { center: rect.left + rect.width / 2, top: rect.top - 8 }
}

function clearHoveredBar() {
  hoveredBarIndex.value = null
}

function barDistance(index: number) {
  return hoveredBarIndex.value === null ? 0 : Math.abs(index - hoveredBarIndex.value)
}

function barMotionStyle(index: number) {
  const distance = barDistance(index)
  if (hoveredBarIndex.value === null) {
    return {
      '--bar-scale': '1',
      '--bar-opacity': '1',
      '--bar-lift': '0px',
    }
  }

  // Keep the hit target fixed. Only the visual layer responds, so its
  // transformed bounds can never move the pointer between neighboring bars.
  const pressure = Math.exp(-distance / 2.8)
  const scaleY = distance === 0 ? 1.1 : 1 - 0.06 * pressure
  const opacity = distance === 0 ? 1 : 0.8 + (0.2 * (1 - pressure))
  return {
    // Keep every slot's horizontal footprint fixed so edge bars cannot expand
    // the page or move the pointer between neighboring hit targets.
    '--bar-scale-x': '1',
    '--bar-scale-y': scaleY.toFixed(3),
    '--bar-opacity': opacity.toFixed(3),
    '--bar-lift': distance === 0 ? '-1px' : '0px',
  }
}

interface TimelineBar {
  key: string
  colorClass: string
  heightPct: number
  title: string
}

function formatBucketTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return new Intl.DateTimeFormat(locale.value || undefined, {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
  }).format(date)
}

const displayBars = computed<TimelineBar[]>(() => {
  const real = [...props.buckets]
    .sort((a, b) => Date.parse(a.bucket_start) - Date.parse(b.bucket_start))
    .slice(-props.length)
  // Missing slots sit at the right (NOW) end: the window is bucket-aligned so the
  // trailing slot is the current partial bucket, which is the one that can be
  // empty right after a refresh. Padding at the front would wrongly show the gap
  // as old history and shift every real bar one slot to the past.
  const bars: TimelineBar[] = []

  for (const bucket of real) {
    const style = SIGNAL_STATE_STYLE[channelSignalState(bucket.metrics, props.ttftThresholds)]
    if (!metricHasTraffic(bucket.metrics)) {
      bars.push({ key: bucket.bucket_start, colorClass: style.barClass, heightPct: style.heightPct, title: t('channelMonitorV3.timelineNoTraffic', { time: formatBucketTime(bucket.bucket_start) }) })
      continue
    }
    bars.push({
      key: bucket.bucket_start,
      colorClass: style.barClass,
      heightPct: style.heightPct,
      title: t('channelMonitorV3.timelineTooltip', {
        time: formatBucketTime(bucket.bucket_start),
        availability: formatMonitorPercent(1 - bucket.metrics.error_rate, locale.value || 'zh-CN'),
        cache: formatMonitorPercent(bucket.metrics.cache_rate, locale.value || 'zh-CN'),
        ttft: formatMonitorMs(bucket.metrics.ttft.p50_ms),
      }),
    })
  }

  const missing = Math.max(0, props.length - real.length)
  for (let index = 0; index < missing; index += 1) {
    bars.push({ key: `empty-${index}`, colorClass: SIGNAL_STATE_STYLE.unknown.barClass, heightPct: SIGNAL_STATE_STYLE.unknown.heightPct, title: '' })
  }
  return bars
})

const hoveredBar = computed(() => hoveredBarIndex.value === null ? null : displayBars.value[hoveredBarIndex.value] ?? null)

// 提示框渲染后量实际宽度：贴边平移时箭头要按真实宽度换算，不能假设固定宽度或固定居中
watch(() => hoveredBar.value?.title, async () => {
  await nextTick()
  if (tooltipEl.value) tooltipWidth.value = tooltipEl.value.offsetWidth
})

const tooltipStyle = computed(() => {
  const viewportWidth = typeof document === 'undefined' ? Infinity : document.documentElement.clientWidth
  const width = tooltipWidth.value
  const { center, top } = tooltipAnchor.value
  // 优先以色块为中心，放不下时贴边，但不越过左右留白
  const left = Math.max(TOOLTIP_GUTTER, Math.min(center - width / 2, viewportWidth - TOOLTIP_GUTTER - width))
  const arrow = Math.max(TOOLTIP_ARROW_INSET, Math.min(center - left, width - TOOLTIP_ARROW_INSET))
  return {
    '--tooltip-left': `${left}px`,
    '--tooltip-top': `${top}px`,
    '--tooltip-arrow-left': `${arrow}px`,
  }
})
</script>

<style scoped>
.v3-soft-glass-bar {
	display: block;
	width: 100%;
	min-height: 3px;
	border-radius: 3px;
  transform-origin: bottom;
  animation: v3-soft-glass-rise 0.7s cubic-bezier(0.22, 1, 0.36, 1) both;
}

.v3-timeline-bars {
	display: flex;
	position: relative;
	height: 20px;
	width: 100%;
	gap: 4px;
	isolation: isolate;
}

.v3-bar-slot {
	position: relative;
	display: flex;
	align-items: flex-end;
	min-width: 0;
	height: 100%;
	flex: 1 1 0%;
}

.v3-bar-hitbox {
	position: relative;
	display: flex;
	align-items: flex-end;
	width: 100%;
	height: 100%;
	min-width: 0;
	padding: 0;
	border: 0;
	background: transparent;
	cursor: crosshair;
	appearance: none;
}

.v3-bar-visual {
	display: flex;
	align-items: flex-end;
	width: 100%;
	height: 100%;
	transform: translateY(var(--bar-lift, 0px)) scaleX(var(--bar-scale-x, 1)) scaleY(var(--bar-scale-y, 1));
	transform-origin: center bottom;
	opacity: var(--bar-opacity, 1);
	pointer-events: none;
	will-change: transform, opacity;
	transition: transform 240ms cubic-bezier(0.22, 1, 0.36, 1), opacity 220ms ease;
}

.v3-bar-hitbox:focus-visible {
	outline: 2px solid rgb(59 130 246 / 0.6);
	outline-offset: 2px;
	border-radius: 4px;
}

.v3-bar-hitbox.is-active {
	z-index: 3;
}

.v3-bar-hitbox.is-active .v3-soft-glass-bar {
	filter: saturate(1.12) brightness(1.05);
	box-shadow: 0 4px 10px rgb(15 118 110 / 0.24);
}

.v3-bar-hitbox.is-pressed {
	z-index: 2;
}

.v3-timeline-tooltip {
	position: fixed;
	left: var(--tooltip-left, 50%);
	top: var(--tooltip-top, 0px);
	z-index: 50;
	width: max-content;
	max-width: min(280px, calc(100vw - 32px));
	transform: translateY(-100%);
	border: 1px solid rgb(255 255 255 / 0.84);
	border-radius: 9px;
	background: rgb(15 23 42 / 0.92);
	padding: 6px 9px;
	color: rgb(248 250 252);
	font-size: 10px;
	font-weight: 600;
	line-height: 1.35;
	letter-spacing: 0;
	white-space: normal;
	box-shadow: 0 10px 24px rgb(15 23 42 / 0.2);
	pointer-events: none;
}

.v3-timeline-tooltip::after {
	position: absolute;
	left: var(--tooltip-arrow-left, 50%);
	bottom: -4px;
	width: 7px;
	height: 7px;
	transform: translateX(-50%) rotate(45deg);
	border-right: 1px solid rgb(255 255 255 / 0.84);
	border-bottom: 1px solid rgb(255 255 255 / 0.84);
	background: rgb(15 23 42 / 0.92);
	content: '';
}

.v3-timeline-tooltip-enter-active,
.v3-timeline-tooltip-leave-active {
	transition: opacity 100ms ease, transform 120ms cubic-bezier(0.22, 1, 0.36, 1);
}

.v3-timeline-tooltip-enter-from,
.v3-timeline-tooltip-leave-to {
	opacity: 0;
	transform: translateY(calc(-100% + 3px)) scale(0.96);
}

@keyframes v3-soft-glass-rise {
  from {
    transform: scaleY(0.15);
    opacity: 0.3;
  }
  to {
    transform: scaleY(1);
    opacity: 1;
  }
}

@media (prefers-reduced-motion: reduce) {
  .v3-soft-glass-bar {
    animation: none;
  }

  .v3-bar-hitbox,
  .v3-timeline-tooltip-enter-active,
  .v3-timeline-tooltip-leave-active {
    transition: none;
  }
}
</style>
