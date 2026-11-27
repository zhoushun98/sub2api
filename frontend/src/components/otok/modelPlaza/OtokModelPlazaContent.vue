<template>
  <!-- otok: 模型广场按平台展示（替换上游按分组的 ModelPlazaContent，props 与之相同） -->
  <div class="space-y-5">
    <div v-if="!embedded">
      <h1 class="text-2xl font-bold tracking-tight text-gray-900 dark:text-white sm:text-3xl">{{ t('modelPlaza.title') }}</h1>
      <p class="mt-1.5 text-sm text-gray-500 dark:text-dark-400">{{ t('otok.modelPlaza.description') }}</p>
    </div>

    <!-- 全局价格说明(管理员配置,Markdown) -->
    <div
      v-if="descriptionHtml"
      class="plaza-description rounded-2xl border border-gray-100 bg-white px-5 py-4 text-sm shadow-card dark:border-dark-700/50 dark:bg-dark-800/50"
      v-html="descriptionHtml"
    ></div>

    <div v-if="loading" class="flex min-h-[240px] items-center justify-center">
      <div class="h-8 w-8 animate-spin rounded-full border-2 border-primary-600/25 border-t-primary-600 dark:border-primary-400/25 dark:border-t-primary-400"></div>
    </div>
    <div
      v-else-if="error"
      class="rounded-2xl border border-red-200 bg-red-50 px-5 py-8 text-center text-sm text-red-600 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-300"
    >
      {{ t('modelPlaza.loadFailed') }}
    </div>
    <template v-else>
      <!-- 筛选:平台 + 模型名 -->
      <div class="space-y-3">
        <div class="flex items-start gap-2">
          <span class="w-10 shrink-0 pt-2 text-xs font-semibold uppercase tracking-wider text-gray-400 dark:text-dark-500">
            {{ t('modelPlaza.filters.platformLabel') }}
          </span>
          <div class="flex flex-wrap items-center gap-2">
            <button
              type="button"
              class="rounded-lg px-3 py-1.5 text-sm font-medium transition"
              :class="allChipClass(selectedPlatform === 'all')"
              data-test="platform-all"
              @click="selectedPlatform = 'all'"
            >
              {{ t('modelPlaza.filters.all') }}
            </button>
            <button
              v-for="p in platforms"
              :key="p.platform"
              type="button"
              class="inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-sm font-medium transition"
              :class="selectedPlatform === p.platform ? 'chip-tinted-active' : 'chip-tinted'"
              :style="{ '--chip-accent': platformAccentColor(p.platform) }"
              :data-test="`platform-${p.platform}`"
              @click="selectedPlatform = p.platform"
            >
              <PlatformIcon :platform="p.platform as GroupPlatform" size="xs" />
              {{ platformLabel(p.platform) }}
            </button>
          </div>
        </div>
        <div class="flex flex-wrap items-start gap-2">
          <span class="w-10 shrink-0 pt-2 text-xs font-semibold uppercase tracking-wider text-gray-400 dark:text-dark-500">
            {{ t('modelPlaza.filters.modelLabel') }}
          </span>
          <div class="relative w-full sm:w-72">
            <Icon name="search" size="sm" class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400 dark:text-dark-500" />
            <input
              v-model="searchQuery"
              type="text"
              :placeholder="t('modelPlaza.filters.searchPlaceholder')"
              class="input rounded-lg py-1.5 pl-9 pr-9"
            />
            <button
              v-if="searchQuery"
              type="button"
              class="absolute right-2.5 top-1/2 -translate-y-1/2 text-gray-400 transition-colors hover:text-gray-600 dark:text-dark-500 dark:hover:text-gray-300"
              @click="searchQuery = ''"
            >
              <Icon name="x" size="xs" class="h-3.5 w-3.5" />
            </button>
          </div>
        </div>
      </div>

      <p class="flex items-center gap-1.5 text-xs text-gray-500 dark:text-dark-400">
        <Icon name="infoCircle" size="xs" class="h-3.5 w-3.5 shrink-0" />
        {{ t('otok.modelPlaza.priceNote') }}
      </p>

      <div v-if="visiblePlatforms.length > 0" class="space-y-5">
        <section
          v-for="p in visiblePlatforms"
          :key="p.platform"
          class="overflow-hidden rounded-2xl border border-gray-100 bg-white shadow-card dark:border-dark-700/50 dark:bg-dark-800/50"
          :data-test="`section-${p.platform}`"
        >
          <header class="flex items-center gap-2 border-b border-gray-100 px-5 py-3 dark:border-dark-700/60">
            <PlatformIcon :platform="p.platform as GroupPlatform" size="sm" />
            <span class="font-semibold text-gray-900 dark:text-white">{{ platformLabel(p.platform) }}</span>
            <span class="text-xs text-gray-400 dark:text-dark-500">{{ t('otok.modelPlaza.modelCount', { count: p.modelCount }) }}</span>
          </header>
          <div class="overflow-x-auto">
            <table class="w-full min-w-[760px] table-auto border-collapse text-sm tabular-nums">
              <colgroup>
                <col class="w-[34%]" />
                <col class="w-[15%]" />
                <col class="w-[15%]" />
                <col class="w-[21%]" />
                <col class="w-[15%]" />
              </colgroup>
              <thead>
                <tr class="border-b border-gray-200 text-left text-xs font-medium text-gray-500 dark:border-dark-700 dark:text-dark-400">
                  <th class="py-2.5 pl-5 pr-4 font-medium">{{ t('modelPlaza.table.model') }}</th>
                  <th class="px-3 py-2.5 font-medium">{{ t('modelPlaza.table.input') }}</th>
                  <th class="px-3 py-2.5 font-medium">{{ t('modelPlaza.table.output') }}</th>
                  <th class="px-3 py-2.5 font-medium">{{ t('otok.modelPlaza.cacheWrite') }}</th>
                  <th class="py-2.5 pl-3 pr-5 font-medium">{{ t('otok.modelPlaza.cacheRead') }}</th>
                </tr>
              </thead>
              <tbody>
                <!-- 同一模型版本的时段行紧跟基础行，中间不画分隔线 -->
                <tr
                  v-for="(m, idx) in p.rows"
                  :key="m.key"
                  class="transition-colors hover:bg-gray-50/70 dark:hover:bg-dark-800/50"
                  :class="idx < p.rows.length - 1 && p.rows[idx + 1].first ? 'border-b border-gray-100 dark:border-dark-800' : ''"
                  :data-test="m.first ? `model-${m.name}` : `model-${m.name}-tier`"
                >
                  <td class="py-2.5 pl-5 pr-4 align-middle">
                    <!-- 时段行:不重复模型名，只显示时段 -->
                    <div v-if="m.timeTier" class="flex flex-wrap items-center gap-1.5 pl-3">
                      <span
                        class="whitespace-nowrap rounded-md bg-amber-50 px-1.5 py-0.5 text-[11px] font-medium text-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
                        :title="timeTierHint(m.timeTier)"
                      >
                        {{ timeTierLabel(m.timeTier) }}
                      </span>
                    </div>
                    <div v-else class="flex flex-wrap items-center gap-1.5">
                      <span class="inline-flex min-w-0 items-center gap-1">
                        <span class="min-w-0 break-words font-medium text-gray-900 dark:text-white">{{ m.name }}</span>
                        <button
                          type="button"
                          class="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:text-dark-300 dark:hover:bg-dark-700 dark:hover:text-primary-400"
                          :title="t('modelPlaza.table.copyModelId', { model: m.name })"
                          :aria-label="t('modelPlaza.table.copyModelId', { model: m.name })"
                          @click="copyToClipboard(m.name)"
                        >
                          <Icon name="copy" size="sm" aria-hidden="true" />
                        </button>
                      </span>
                      <span
                        v-if="m.groups.length"
                        class="whitespace-nowrap rounded-md bg-primary-50 px-1.5 py-0.5 text-[10px] font-medium text-primary-700 dark:bg-primary-900/20 dark:text-primary-300"
                        data-test="groups-badge"
                      >
                        {{ t('otok.modelPlaza.groupsBadge', { names: m.groups.join('、') }) }}
                      </span>
                      <span
                        v-for="badge in badges(m)"
                        :key="badge.text"
                        class="whitespace-nowrap rounded-md px-1.5 py-0.5 text-[10px] font-medium"
                        :class="badge.warn ? 'bg-amber-50 text-amber-700 dark:bg-amber-900/20 dark:text-amber-300' : 'bg-gray-100 text-gray-500 dark:bg-dark-700/70 dark:text-dark-300'"
                        :title="badge.title"
                      >
                        {{ badge.text }}
                      </span>
                    </div>
                  </td>

                  <!-- 无价格 -->
                  <td v-if="!m.price" colspan="4" class="px-3 py-2.5 text-xs text-gray-400 dark:text-dark-500">
                    {{ t('modelPlaza.detail.noPricing') }}
                  </td>

                  <!-- 按次 / 按图片计费 -->
                  <td v-else-if="m.price.billingMode !== 'token'" colspan="4" class="px-3 py-2.5 align-middle">
                    <div v-if="m.price.requestTiers.length" class="flex flex-wrap items-center gap-1.5">
                      <span
                        v-for="(iv, ivIdx) in m.price.requestTiers"
                        :key="ivIdx"
                        class="inline-flex items-center gap-1 rounded-md bg-gray-100 px-2 py-0.5 font-mono text-xs text-gray-800 dark:bg-dark-700/60 dark:text-gray-200"
                      >
                        <span class="font-sans text-gray-400 dark:text-dark-500">{{ tierLabel(iv) }}</span>
                        {{ perUnit(iv.per_request_price) }}<span class="font-sans text-gray-400 dark:text-dark-500">{{ unitSuffix(m) }}</span>
                      </span>
                    </div>
                    <span v-else class="font-mono font-semibold text-gray-900 dark:text-gray-50">
                      {{ perUnit(m.price.perRequest) }}<span class="ml-1 font-sans text-xs font-normal text-gray-400 dark:text-dark-500">{{ unitSuffix(m) }}</span>
                    </span>
                  </td>

                  <!-- token 计费:有上下文分档时每档一行,档位标签放在输入列 -->
                  <template v-else>
                    <td class="px-3 py-2.5 align-middle font-mono font-semibold text-gray-900 dark:text-gray-50">
                      <template v-if="m.price.intervals.length">
                        <div v-for="(iv, ivIdx) in m.price.intervals" :key="ivIdx" class="whitespace-nowrap text-xs leading-5">
                          <span class="mr-1 font-sans font-normal text-gray-400 dark:text-dark-500" :title="t('modelPlaza.table.tierHint')">{{ tierLabel(iv) }}</span>
                          {{ perMillion(iv.input_price) }}
                        </div>
                      </template>
                      <template v-else>{{ perMillion(m.price.input) }}</template>
                    </td>
                    <td class="px-3 py-2.5 align-middle font-mono font-semibold text-gray-900 dark:text-gray-50">
                      <template v-if="m.price.intervals.length">
                        <div v-for="(iv, ivIdx) in m.price.intervals" :key="ivIdx" class="whitespace-nowrap text-xs leading-5">
                          {{ perMillion(iv.output_price) }}
                        </div>
                      </template>
                      <template v-else>{{ perMillion(m.price.output) }}</template>
                    </td>
                    <td class="px-3 py-2.5 align-middle font-mono text-xs text-gray-800 dark:text-gray-200">
                      <template v-if="m.price.intervals.length">
                        <div v-for="(iv, ivIdx) in m.price.intervals" :key="ivIdx" class="whitespace-nowrap leading-5">
                          {{ cacheWriteText(iv.cache_write_price, iv.cache_write_1h_price, iv.cache_write_12h_price) }}
                        </div>
                      </template>
                      <template v-else>{{ cacheWriteText(m.price.cacheWrite, m.price.cacheWrite1h, m.price.cacheWrite12h) }}</template>
                    </td>
                    <td class="py-2.5 pl-3 pr-5 align-middle font-mono text-xs text-gray-800 dark:text-gray-200">
                      <template v-if="m.price.intervals.length">
                        <div v-for="(iv, ivIdx) in m.price.intervals" :key="ivIdx" class="whitespace-nowrap leading-5">
                          {{ perMillion(iv.cache_read_price) }}
                        </div>
                      </template>
                      <template v-else>{{ perMillion(m.price.cacheRead) }}</template>
                    </td>
                  </template>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
      </div>
      <div
        v-else
        class="rounded-2xl border border-dashed border-gray-300 px-5 py-12 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-dark-400"
      >
        {{ searchQuery.trim() ? t('modelPlaza.noSearchResult') : t('otok.modelPlaza.empty') }}
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import Icon from '@/components/icons/Icon.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import { useClipboard } from '@/composables/useClipboard'
import { formatScaled } from '@/utils/pricing'
import { platformAccentColor, platformLabel } from '@/utils/platformColors'
import type { GroupPlatform } from '@/types'
import type { UserPricingInterval } from '@/api/channels'
import type { ModelPlazaResponse } from '@/api/modelPlaza'
import {
  otokFilterPlazaPlatforms,
  otokPlazaPlatforms,
  type OtokPlazaRow,
  type OtokPlazaTimeTier
} from '@/otok/modelPlaza'

const props = defineProps<{
  response: ModelPlazaResponse | null
  loading: boolean
  error?: boolean
  /** 后台内嵌形态(AppLayout 内):隐藏页头。 */
  embedded?: boolean
}>()

const { t } = useI18n()
const { copyToClipboard } = useClipboard()

const selectedPlatform = ref('all')
const searchQuery = ref('')

const descriptionHtml = computed(() => {
  const md = props.response?.description?.trim()
  if (!md) return ''
  return DOMPurify.sanitize(marked.parse(md) as string)
})

const platforms = computed(() => otokPlazaPlatforms(props.response?.groups ?? []))

/** 数据刷新后选中的平台可能不复存在,重置为全部。 */
watch(platforms, (list) => {
  if (selectedPlatform.value !== 'all' && !list.some((p) => p.platform === selectedPlatform.value)) {
    selectedPlatform.value = 'all'
  }
})

const visiblePlatforms = computed(() =>
  otokFilterPlazaPlatforms(platforms.value, selectedPlatform.value, searchQuery.value)
)

const PER_MILLION = 1_000_000
/** 价格统一保底 2 位小数,更长的有效小数原样保留。 */
const MIN_DECIMALS = 2

function perMillion(v: number | null | undefined): string {
  return v == null ? '-' : formatScaled(v, PER_MILLION, MIN_DECIMALS)
}

function perUnit(v: number | null | undefined): string {
  return v == null ? '-' : formatScaled(v, 1, MIN_DECIMALS)
}

/**
 * 缓存写入:5m 价,另有 1h / 12h 价时括号附注。0 视为不收这项费用(DeepSeek、GLM 的渠道价填 0),
 * 与缺失一样显示 "-"。
 */
function cacheWriteText(base: number | null | undefined, h1: number | null | undefined, h12: number | null | undefined): string {
  const priced = (v: number | null | undefined) => v != null && v !== 0
  if (!priced(base) && !priced(h1) && !priced(h12)) return '-'
  let s = perMillion(base)
  if (h1 != null) s += ` (1h ${perMillion(h1)})`
  if (h12 != null) s += ` (12h ${perMillion(h12)})`
  return s
}

function unitSuffix(m: OtokPlazaRow): string {
  return m.price?.billingMode === 'image' ? t('modelPlaza.table.perUnitImage') : t('modelPlaza.table.perUnitRequest')
}

/** 档位标签:有上限为「≤上限」,末档为「>下限」。不用 tier_label(库里多是 standard / long_context 这类内部名)。 */
function tierLabel(iv: UserPricingInterval): string {
  return iv.max_tokens == null ? `>${tokenCount(iv.min_tokens)}` : `≤${tokenCount(iv.max_tokens)}`
}

function tokenCount(n: number): string {
  const trim = (v: number) => String(Math.round(v * 100) / 100)
  if (n >= 1_000_000) return `${trim(n / 1_000_000)}M`
  if (n >= 1_000) return `${trim(n / 1_000)}K`
  return String(n)
}

interface Badge {
  text: string
  title?: string
  warn?: boolean
}

/** 模型名旁的徽章:非 token 计费方式、思考等级倍率；配了分时倍率的基础行标「其他时段」。 */
function badges(m: OtokPlazaRow): Badge[] {
  const list: Badge[] = []
  const mode = m.price?.billingMode
  if (mode === 'image') list.push({ text: t('modelPlaza.table.perImage') })
  else if (mode && mode !== 'token') list.push({ text: t('modelPlaza.table.perRequest') })
  for (const [effort, multiplier] of Object.entries(m.model.pricing?.reasoning_effort_multipliers ?? {})) {
    if (!(multiplier > 0) || multiplier === 1) continue
    list.push({
      text: t('modelPlaza.table.reasoningMultiplierBadge', { effort, multiplier }),
      title: t('modelPlaza.table.reasoningMultiplierHint', { effort, multiplier }),
      warn: true
    })
  }
  if (m.hasTimeTiers) {
    const tp = m.model.time_pricing
    list.push({
      text: t('otok.modelPlaza.otherHours'),
      title: t(tp?.weekdays_only ? 'otok.modelPlaza.otherHoursHintWeekdays' : 'otok.modelPlaza.otherHoursHint')
    })
  }
  return list
}

/** 时段行标签:「工作日 09:00–12:00、14:00–18:00 ×2」。 */
function timeTierLabel(tier: OtokPlazaTimeTier): string {
  return t(tier.weekdaysOnly ? 'otok.modelPlaza.timeTierWeekdays' : 'otok.modelPlaza.timeTier', {
    windows: tier.windows.join('、'),
    multiplier: tier.multiplier
  })
}

function timeTierHint(tier: OtokPlazaTimeTier): string {
  return t(tier.weekdaysOnly ? 'modelPlaza.table.timePricingRowHintWeekdays' : 'modelPlaza.table.timePricingRowHint', {
    timezone: tier.timezone
  })
}

function allChipClass(active: boolean): string {
  return active
    ? 'bg-gradient-to-r from-primary-500 to-primary-600 text-white shadow-sm shadow-primary-500/30'
    : 'bg-white text-gray-600 ring-1 ring-inset ring-gray-200 hover:bg-gray-50 hover:text-gray-900 hover:ring-gray-300 dark:bg-dark-800/60 dark:text-dark-300 dark:ring-dark-700 dark:hover:bg-dark-800 dark:hover:text-white'
}
</script>

<style scoped>
/* 平台 chip 配色从 --chip-accent(平台主色)派生,与上游 PlazaFilterBar 一致 */
.chip-tinted {
  color: var(--chip-accent);
  color: color-mix(in srgb, var(--chip-accent) 78%, black);
  background-color: color-mix(in srgb, var(--chip-accent) 9%, transparent);
  box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--chip-accent) 25%, transparent);
}

.chip-tinted:hover {
  background-color: color-mix(in srgb, var(--chip-accent) 16%, transparent);
}

.dark .chip-tinted {
  color: color-mix(in srgb, var(--chip-accent) 72%, white);
  background-color: color-mix(in srgb, var(--chip-accent) 12%, transparent);
  box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--chip-accent) 30%, transparent);
}

.dark .chip-tinted:hover {
  background-color: color-mix(in srgb, var(--chip-accent) 18%, transparent);
}

.chip-tinted-active {
  color: #fff;
  background-color: var(--chip-accent);
  background-color: color-mix(in srgb, var(--chip-accent) 85%, black);
  box-shadow: 0 1px 2px 0 color-mix(in srgb, var(--chip-accent) 35%, transparent);
}

.chip-tinted-active:hover {
  background-color: color-mix(in srgb, var(--chip-accent) 75%, black);
}

.dark .chip-tinted-active {
  background-color: color-mix(in srgb, var(--chip-accent) 80%, transparent);
}

.dark .chip-tinted-active:hover {
  background-color: var(--chip-accent);
}

.plaza-description {
  line-height: 1.7;
  overflow-wrap: anywhere;
}

.plaza-description :deep(h1),
.plaza-description :deep(h2),
.plaza-description :deep(h3) {
  @apply mb-2 mt-3 font-semibold text-gray-900 first:mt-0 dark:text-white;
}

.plaza-description :deep(p) {
  @apply mb-2 text-gray-700 last:mb-0 dark:text-dark-200;
}

.plaza-description :deep(a) {
  @apply text-primary-600 underline underline-offset-4 hover:text-primary-700 dark:text-primary-300;
}

.plaza-description :deep(ul) {
  @apply mb-2 list-disc pl-5;
}

.plaza-description :deep(ol) {
  @apply mb-2 list-decimal pl-5;
}

.plaza-description :deep(li) {
  @apply mb-0.5 text-gray-700 dark:text-dark-200;
}

.plaza-description :deep(code) {
  @apply rounded bg-gray-100 px-1.5 py-0.5 font-mono text-xs dark:bg-dark-800;
}

.plaza-description :deep(blockquote) {
  @apply my-2 border-l-4 border-gray-300 pl-3 text-gray-600 dark:border-dark-600 dark:text-dark-300;
}
</style>
