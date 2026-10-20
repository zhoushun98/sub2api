<template>
  <div class="mt-4 border-t border-gray-100 pt-3 dark:border-dark-700/60">
    <div class="mb-2 flex justify-between text-[10px] font-semibold uppercase tracking-widest text-gray-400">
      <span>{{ t('otok.channelCards.recent', { n: bars.length }) }}</span>
      <span class="tabular-nums">{{ t('otok.channelCards.nextRefresh', { n: countdownSeconds }) }}</span>
    </div>

    <!-- 高度 + 颜色双重编码：绿=满高，黄=中，红=矮，无数据=最矮；底部对齐 -->
    <div class="otok-bars" @mouseleave="clearActive">
      <div
        v-for="(bar, idx) in bars"
        :key="bar.start"
        class="otok-bar-slot"
        @mouseenter="setActive(idx, $event)"
      >
        <button
          type="button"
          class="otok-bar-hitbox"
          :class="{ 'is-active': active === idx }"
          :aria-label="tooltip(bar)"
          data-testid="otok-monitor-bar"
          @focus="setActive(idx, $event)"
          @blur="clearActive"
        >
          <!-- 命中区固定不动，只有视觉层做位移/缩放，避免指针在相邻色条间来回跳 -->
          <span class="otok-bar-visual" :style="motionStyle(idx)" aria-hidden="true">
            <span
              class="otok-bar"
              :class="[BAR_COLOR[bar.state], { 'is-pending': bar.pending }]"
              :style="{ height: `${BAR_HEIGHT[bar.state]}%`, animationDelay: `${idx * 18}ms` }"
            ></span>
          </span>
        </button>
      </div>
    </div>

    <div class="mt-1.5 flex justify-between text-[9px] uppercase tracking-widest text-gray-400">
      <span>{{ t('monitorCommon.past') }}</span>
      <span>{{ t('monitorCommon.now') }}</span>
    </div>

    <!-- 详情气泡挂到 body，用 fixed 定位，不会被相邻卡片遮挡 -->
    <Teleport to="body">
      <Transition name="otok-bar-tip">
        <div
          v-if="activeBar"
          class="otok-bar-tip"
          role="tooltip"
          data-testid="otok-monitor-bar-tooltip"
          :style="tipStyle"
        >
          {{ tooltip(activeBar) }}
        </div>
      </Transition>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  formatPercent,
  formatTtft,
  type OtokCardState,
  type OtokMonitorBar,
} from '@/otok/monitorCards'

const props = defineProps<{
  bars: OtokMonitorBar[]
  countdownSeconds: number
}>()

const { t } = useI18n()

const BAR_COLOR: Record<OtokCardState, string> = {
  healthy: 'bg-emerald-500',
  warning: 'bg-amber-500',
  critical: 'bg-red-500',
  unknown: 'bg-gray-300 dark:bg-dark-600',
}

const BAR_HEIGHT: Record<OtokCardState, number> = {
  healthy: 100,
  warning: 65,
  critical: 35,
  unknown: 15,
}

const VIEWPORT_GUTTER = 16
const TIP_MAX_WIDTH = 280

const active = ref<number | null>(null)
const tipPos = ref({ left: 0, top: 0, shift: '-50%', arrow: '50%' })
const activeBar = computed(() => (active.value == null ? null : props.bars[active.value] ?? null))

function setActive(idx: number, event?: Event) {
  active.value = idx
  const target = event?.currentTarget
  if (!(target instanceof HTMLElement) || typeof window === 'undefined') return
  const rect = target.getBoundingClientRect()
  const center = rect.left + rect.width / 2
  const width = Math.min(TIP_MAX_WIDTH, window.innerWidth - VIEWPORT_GUTTER * 2)
  const top = rect.top - 10
  // 靠近视口边缘时气泡贴边，箭头仍指向当前色条。
  if (center + width / 2 > window.innerWidth - VIEWPORT_GUTTER) {
    const right = window.innerWidth - VIEWPORT_GUTTER
    tipPos.value = { left: right, top, shift: '-100%', arrow: `calc(100% - ${right - center}px)` }
  } else if (center - width / 2 < VIEWPORT_GUTTER) {
    tipPos.value = { left: VIEWPORT_GUTTER, top, shift: '0%', arrow: `${center - VIEWPORT_GUTTER}px` }
  } else {
    tipPos.value = { left: center, top, shift: '-50%', arrow: '50%' }
  }
}

function clearActive() {
  active.value = null
}

/** 悬停条放大上抬；其余按距离衰减地下压、变暗，越近越明显。 */
function motionStyle(idx: number) {
  if (active.value == null) return undefined
  const distance = Math.abs(idx - active.value)
  const pressure = Math.exp(-distance / 2.8)
  return {
    '--bar-scale-y': distance === 0 ? '1.1' : (1 - 0.06 * pressure).toFixed(3),
    '--bar-opacity': distance === 0 ? '1' : (0.8 + 0.2 * (1 - pressure)).toFixed(3),
    '--bar-lift': distance === 0 ? '-1px' : '0px',
  }
}

const tipStyle = computed(() => ({
  '--tip-left': `${tipPos.value.left}px`,
  '--tip-top': `${tipPos.value.top}px`,
  '--tip-shift': tipPos.value.shift,
  '--tip-arrow': tipPos.value.arrow,
}))

function formatSlot(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString(undefined, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  })
}

function tooltip(bar: OtokMonitorBar): string {
  const time = formatSlot(bar.start)
  if (bar.pending) return `${time} · ${t('otok.channelCards.pending')}`
  if (bar.state === 'unknown') return `${time} · ${t('otok.channelCards.noData')}`
  return t('otok.channelCards.barTooltip', {
    time,
    availability: formatPercent(bar.availability),
    cache: formatPercent(bar.cacheRate),
    ttft: formatTtft(bar.ttftMs),
  })
}
</script>

<style scoped>
.otok-bars {
  position: relative;
  display: flex;
  width: 100%;
  height: 20px;
  gap: 4px;
  isolation: isolate;
}

.otok-bar-slot {
  display: flex;
  flex: 1 1 0%;
  align-items: flex-end;
  min-width: 0;
  height: 100%;
}

.otok-bar-hitbox {
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

.otok-bar-hitbox:focus-visible {
  outline: 2px solid rgb(59 130 246 / 0.6);
  outline-offset: 2px;
  border-radius: 4px;
}

.otok-bar-hitbox.is-active {
  z-index: 2;
}

.otok-bar-visual {
  display: flex;
  align-items: flex-end;
  width: 100%;
  height: 100%;
  transform: translateY(var(--bar-lift, 0px)) scaleY(var(--bar-scale-y, 1));
  transform-origin: center bottom;
  opacity: var(--bar-opacity, 1);
  pointer-events: none;
  will-change: transform, opacity;
  transition: transform 240ms cubic-bezier(0.22, 1, 0.36, 1), opacity 220ms ease;
}

.otok-bar {
  display: block;
  width: 100%;
  min-height: 3px;
  border-radius: 3px;
  transform-origin: bottom;
  animation: otok-bar-rise 0.7s cubic-bezier(0.22, 1, 0.36, 1) both;
  transition: filter 200ms ease, box-shadow 200ms ease;
}

/* 统计中：沿用上一格颜色，半透明呼吸；入场动画结束后再开始呼吸 */
.otok-bar.is-pending {
  animation:
    otok-bar-rise 0.7s cubic-bezier(0.22, 1, 0.36, 1) both,
    otok-bar-pending 1.6s ease-in-out 0.7s infinite;
}

.otok-bar-hitbox.is-active .otok-bar {
  filter: saturate(1.12) brightness(1.05);
  box-shadow: 0 4px 10px rgb(15 118 110 / 0.24);
}

.otok-bar-tip {
  position: fixed;
  left: var(--tip-left);
  top: var(--tip-top);
  z-index: 60;
  width: max-content;
  max-width: min(280px, calc(100vw - 32px));
  transform: translateX(var(--tip-shift)) translateY(-100%);
  border: 1px solid rgb(255 255 255 / 0.84);
  border-radius: 9px;
  background: rgb(15 23 42 / 0.92);
  padding: 6px 9px;
  color: rgb(248 250 252);
  font-size: 11px;
  font-weight: 600;
  line-height: 1.4;
  white-space: normal;
  box-shadow: 0 10px 24px rgb(15 23 42 / 0.2);
  pointer-events: none;
}

.otok-bar-tip::after {
  position: absolute;
  left: var(--tip-arrow);
  bottom: -4px;
  width: 7px;
  height: 7px;
  transform: translateX(-50%) rotate(45deg);
  border-right: 1px solid rgb(255 255 255 / 0.84);
  border-bottom: 1px solid rgb(255 255 255 / 0.84);
  background: rgb(15 23 42 / 0.92);
  content: '';
}

.otok-bar-tip-enter-active,
.otok-bar-tip-leave-active {
  transition: opacity 100ms ease, transform 120ms cubic-bezier(0.22, 1, 0.36, 1);
}

.otok-bar-tip-enter-from,
.otok-bar-tip-leave-to {
  opacity: 0;
  transform: translateX(var(--tip-shift)) translateY(calc(-100% + 3px)) scale(0.96);
}

@keyframes otok-bar-pending {
  0%,
  100% {
    opacity: 0.55;
  }
  50% {
    opacity: 0.25;
  }
}

@keyframes otok-bar-rise {
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
  .otok-bar {
    animation: none;
  }

  .otok-bar.is-pending {
    opacity: 0.4;
  }

  .otok-bar-visual,
  .otok-bar-tip-enter-active,
  .otok-bar-tip-leave-active {
    transition: none;
  }
}
</style>
