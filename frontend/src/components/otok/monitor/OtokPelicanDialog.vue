<template>
  <BaseDialog
    :show="show"
    :title="t('otok.pelican.dialogTitle', { name: groupName })"
    width="full"
    close-on-click-outside
    @close="emit('close')"
  >
    <div class="space-y-4" data-testid="otok-pelican-dialog">
      <p class="text-xs leading-relaxed text-gray-500 dark:text-gray-400">
        {{ t('otok.pelican.subtitle', { keep: keepRounds, interval: intervalMinutes }) }}
      </p>

      <div v-if="loading && rounds.length === 0" class="grid gap-4 lg:grid-cols-[280px_minmax(0,1fr)]">
        <div class="space-y-3">
          <div v-for="i in 4" :key="i" class="h-20 animate-pulse rounded-xl bg-gray-100 dark:bg-dark-700"></div>
        </div>
        <div class="h-80 animate-pulse rounded-xl bg-gray-100 dark:bg-dark-700"></div>
      </div>

      <div
        v-else-if="rounds.length === 0"
        class="rounded-xl border border-dashed border-gray-200 py-12 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400"
        data-testid="otok-pelican-empty"
      >
        {{ t('otok.pelican.empty') }}
      </div>

      <div v-else class="grid gap-4 lg:grid-cols-[280px_minmax(0,1fr)]">
        <!-- 左侧：轮次列表（新的在前） -->
        <ol class="flex max-h-[30vh] flex-col gap-2 overflow-y-auto pr-1 lg:max-h-[70vh]" data-testid="otok-pelican-rounds">
          <li v-for="round in rounds" :key="round.id">
            <button
              type="button"
              class="w-full rounded-xl border px-4 py-3 text-left transition-colors"
              :class="
                round.id === selectedRoundId
                  ? 'border-primary-400 bg-primary-50/60 dark:border-primary-500/60 dark:bg-primary-500/10'
                  : 'border-gray-200 hover:border-gray-300 hover:bg-gray-50 dark:border-dark-700 dark:hover:border-dark-600 dark:hover:bg-dark-800'
              "
              :data-testid="`otok-pelican-round-${round.id}`"
              @click="selectRound(round)"
            >
              <div class="font-mono text-sm font-semibold tabular-nums text-gray-900 dark:text-gray-100">
                {{ formatOtokPelicanTime(round.started_at) }}
              </div>
              <div class="mt-1 text-xs font-medium" :class="ROUND_STATE_CLASS[otokPelicanRoundState(round)]">
                {{ t(`otok.pelican.roundStates.${otokPelicanRoundState(round)}`) }}
                <span v-if="round.trigger === 'manual'" class="ml-1 text-gray-400">· {{ t('otok.pelican.manual') }}</span>
              </div>
              <div
                v-for="run in round.runs"
                :key="run.id"
                class="mt-0.5 truncate font-mono text-xs text-gray-500 dark:text-gray-400"
              >
                {{ run.model }} · {{ runDurationLabel(run) }}
              </div>
            </button>
          </li>
        </ol>

        <!-- 右侧：选中轮次的详情 -->
        <section v-if="selectedRound" class="min-w-0 space-y-3 border-gray-100 dark:border-dark-700 lg:border-l lg:pl-4">
          <div class="text-xs text-gray-500 dark:text-gray-400">
            {{ t('otok.pelican.startedAt') }}：{{ formatOtokPelicanTime(selectedRound.started_at) }}
            · {{ t('otok.pelican.finishedAt') }}：{{ formatOtokPelicanTime(selectedRound.finished_at) }}
          </div>

          <div class="flex flex-wrap gap-2 border-b border-gray-100 pb-3 dark:border-dark-700" role="tablist">
            <button
              v-for="run in selectedRound.runs"
              :key="run.id"
              type="button"
              role="tab"
              class="rounded-lg border px-3 py-1.5 font-mono text-sm transition-colors"
              :class="
                run.id === selectedRunId
                  ? 'border-primary-400 bg-primary-50 text-primary-700 dark:border-primary-500/60 dark:bg-primary-500/10 dark:text-primary-300'
                  : 'border-gray-200 text-gray-600 hover:bg-gray-50 dark:border-dark-700 dark:text-gray-300 dark:hover:bg-dark-800'
              "
              :aria-selected="run.id === selectedRunId"
              @click="selectRun(run.id)"
            >
              {{ run.model }}
            </button>
          </div>

          <div v-if="selectedSummary" class="space-y-3">
            <div class="flex flex-wrap items-center gap-2">
              <button
                type="button"
                class="btn btn-secondary btn-sm"
                :disabled="!detail?.output_html"
                data-testid="otok-pelican-toggle-preview"
                @click="previewOn = !previewOn"
              >
                {{ previewOn ? t('otok.pelican.stopPreview') : t('otok.pelican.preview') }}
              </button>
              <button
                type="button"
                class="btn btn-ghost btn-sm"
                :disabled="!detail?.output_text"
                data-testid="otok-pelican-toggle-source"
                @click="sourceOn = !sourceOn"
              >
                {{ sourceOn ? t('otok.pelican.hideSource') : t('otok.pelican.viewSource') }}
              </button>
              <span class="ml-auto font-mono text-xs tabular-nums text-gray-500 dark:text-gray-400">
                {{ runMeta }}
              </span>
            </div>

            <!-- 失败 / 进行中 / 无 HTML 的提示 -->
            <div
              v-if="selectedSummary.status === 'failed'"
              class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700 dark:bg-red-500/10 dark:text-red-300"
              data-testid="otok-pelican-error"
            >
              {{ t(`otok.pelican.errorCodes.${errorCodeKey(selectedSummary.error_code)}`) }}
              <span v-if="selectedSummary.error_message" class="block break-all opacity-80">{{ selectedSummary.error_message }}</span>
            </div>
            <div
              v-else-if="isOtokPelicanRunPending(selectedSummary)"
              class="rounded-lg bg-blue-50 px-3 py-2 text-xs text-blue-700 dark:bg-blue-500/10 dark:text-blue-300"
            >
              {{ t('otok.pelican.generating') }}
            </div>
            <div
              v-else-if="detail && !detail.output_html"
              class="rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:bg-amber-500/10 dark:text-amber-300"
            >
              {{ t('otok.pelican.noHtml') }}
            </div>

            <div v-if="detailLoading" class="aspect-[3/2] w-full animate-pulse rounded-xl bg-gray-100 dark:bg-dark-700"></div>

            <!-- 预览：sandbox 不给任何权限，脚本、表单、同源访问都被禁止 -->
            <div
              v-else-if="previewOn && previewDoc"
              ref="frameBoxRef"
              class="relative w-full overflow-hidden rounded-xl border border-gray-200 bg-white dark:border-dark-700"
              :style="{ height: `${frameHeight}px` }"
            >
              <iframe
                :key="detail?.id"
                :srcdoc="previewDoc"
                sandbox=""
                referrerpolicy="no-referrer"
                loading="lazy"
                :title="t('otok.pelican.previewTitle', { model: selectedSummary.model })"
                class="absolute left-0 top-0 origin-top-left border-0"
                :style="{
                  width: `${OTOK_PELICAN_PREVIEW_WIDTH}px`,
                  height: `${OTOK_PELICAN_PREVIEW_HEIGHT}px`,
                  transform: `scale(${frameScale})`,
                }"
                data-testid="otok-pelican-iframe"
              ></iframe>
            </div>

            <!-- 源码：纯文本插值，不解析 HTML -->
            <pre
              v-if="sourceOn && detail?.output_text"
              class="max-h-[50vh] overflow-auto whitespace-pre-wrap break-all rounded-xl bg-gray-900 p-4 font-mono text-xs leading-relaxed text-gray-100"
              data-testid="otok-pelican-source"
            >{{ detail.output_text }}</pre>

            <p class="text-[11px] text-gray-400">{{ t('otok.pelican.sandboxHint') }}</p>
          </div>
        </section>
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import type {
  OtokPelicanRound,
  OtokPelicanRunDetail,
  OtokPelicanRunSummary,
  OtokPelicanSource,
} from '@/api/otok/pelican'
import {
  OTOK_PELICAN_POLL_MS,
  OTOK_PELICAN_PREVIEW_HEIGHT,
  OTOK_PELICAN_PREVIEW_WIDTH,
  buildOtokPelicanPreviewDoc,
  defaultOtokPelicanRun,
  formatOtokPelicanDuration,
  formatOtokPelicanTime,
  hasOtokPelicanPending,
  isOtokPelicanRunPending,
  otokPelicanRoundState,
  type OtokPelicanRoundState,
} from '@/otok/pelican'

const props = withDefaults(
  defineProps<{
    show: boolean
    groupId: number | null
    groupName: string
    source: OtokPelicanSource
    intervalMinutes?: number
    keepRounds?: number
  }>(),
  { intervalMinutes: 10, keepRounds: 10 },
)

const emit = defineEmits<{ (e: 'close'): void }>()

const { t } = useI18n()
const appStore = useAppStore()

const ROUND_STATE_CLASS: Record<OtokPelicanRoundState, string> = {
  running: 'text-blue-600 dark:text-blue-400',
  succeeded: 'text-emerald-600 dark:text-emerald-400',
  partial: 'text-amber-600 dark:text-amber-400',
  failed: 'text-red-600 dark:text-red-400',
}

const KNOWN_ERROR_CODES = new Set([
  'timeout',
  'interrupted',
  'shutdown',
  'provision_failed',
  'stream_interrupted',
  'response_failed',
  'response_incomplete',
  'stream_error',
  'empty_output',
  'network_error',
  'connection_refused',
  'http_429',
])

const rounds = ref<OtokPelicanRound[]>([])
const loading = ref(false)
const selectedRoundId = ref<number | null>(null)
const selectedRunId = ref<number | null>(null)
const detail = ref<OtokPelicanRunDetail | null>(null)
const detailLoading = ref(false)
const previewOn = ref(true)
const sourceOn = ref(false)
const frameBoxRef = ref<HTMLElement | null>(null)
const frameWidth = ref(OTOK_PELICAN_PREVIEW_WIDTH)

// 已结束的记录内容不会再变，按 id 缓存；进行中的不缓存。
const detailCache = new Map<number, OtokPelicanRunDetail>()
let pollTimer: number | undefined
let resizeObserver: ResizeObserver | null = null
let loadSeq = 0

const selectedRound = computed(() => rounds.value.find((r) => r.id === selectedRoundId.value) ?? null)
const selectedSummary = computed<OtokPelicanRunSummary | null>(
  () => selectedRound.value?.runs.find((r) => r.id === selectedRunId.value) ?? null,
)
const previewDoc = computed(() => buildOtokPelicanPreviewDoc(detail.value?.output_html ?? ''))
const frameScale = computed(() => Math.min(1, frameWidth.value / OTOK_PELICAN_PREVIEW_WIDTH))
const frameHeight = computed(() => Math.round(OTOK_PELICAN_PREVIEW_HEIGHT * frameScale.value))

const runMeta = computed(() => {
  const s = selectedSummary.value
  if (!s) return ''
  const parts = [formatOtokPelicanDuration(s.duration_ms)]
  if (s.output_tokens != null) parts.push(t('otok.pelican.outputTokens', { n: s.output_tokens }))
  if (s.reasoning_tokens != null) parts.push(t('otok.pelican.reasoningTokens', { n: s.reasoning_tokens }))
  if (s.attempt > 1) parts.push(t('otok.pelican.retried'))
  return parts.join(' · ')
})

function runDurationLabel(run: OtokPelicanRunSummary): string {
  if (isOtokPelicanRunPending(run)) return t(`otok.pelican.runStates.${run.status}`)
  if (run.status === 'failed') return `${t('otok.pelican.runStates.failed')} · ${formatOtokPelicanDuration(run.duration_ms)}`
  return formatOtokPelicanDuration(run.duration_ms)
}

function errorCodeKey(code: string): string {
  if (KNOWN_ERROR_CODES.has(code)) return code
  if (/^http_5\d\d$/.test(code)) return 'http_5xx'
  if (/^http_4\d\d$/.test(code)) return 'http_4xx'
  return 'unknown'
}

async function loadRounds(silent = false) {
  if (props.groupId == null) return
  const seq = ++loadSeq
  if (!silent) loading.value = true
  try {
    const list = await props.source.listRounds(props.groupId)
    if (seq !== loadSeq) return
    rounds.value = list
    const current = list.find((r) => r.id === selectedRoundId.value)
    if (!current) {
      selectRound(list[0] ?? null)
    } else if (selectedSummary.value && !isOtokPelicanRunPending(selectedSummary.value) && detail.value?.status !== selectedSummary.value.status) {
      // 选中的记录刚完成：重新拉取内容
      void loadDetail(selectedSummary.value.id)
    }
  } catch (err) {
    if (!silent) appStore.showError(extractApiErrorMessage(err, t('otok.pelican.loadFailed')))
  } finally {
    if (seq === loadSeq) loading.value = false
    schedulePoll()
  }
}

function schedulePoll() {
  window.clearTimeout(pollTimer)
  pollTimer = undefined
  if (props.show && hasOtokPelicanPending(rounds.value)) {
    pollTimer = window.setTimeout(() => void loadRounds(true), OTOK_PELICAN_POLL_MS)
  }
}

function selectRound(round: OtokPelicanRound | null) {
  selectedRoundId.value = round?.id ?? null
  const run = defaultOtokPelicanRun(round)
  selectRun(run?.id ?? null)
}

function selectRun(runId: number | null) {
  if (runId === selectedRunId.value && detail.value?.id === runId) return
  selectedRunId.value = runId
  sourceOn.value = false
  detail.value = null
  if (runId != null) void loadDetail(runId)
}

async function loadDetail(runId: number) {
  const cached = detailCache.get(runId)
  if (cached) {
    detail.value = cached
    return
  }
  detailLoading.value = true
  try {
    const d = await props.source.getRun(runId)
    if (!isOtokPelicanRunPending(d)) detailCache.set(runId, d)
    if (selectedRunId.value === runId) detail.value = d
  } catch (err) {
    appStore.showError(extractApiErrorMessage(err, t('otok.pelican.loadFailed')))
  } finally {
    if (selectedRunId.value === runId) detailLoading.value = false
  }
}

function reset() {
  window.clearTimeout(pollTimer)
  pollTimer = undefined
  loadSeq++
  rounds.value = []
  selectedRoundId.value = null
  selectedRunId.value = null
  detail.value = null
  detailLoading.value = false
  previewOn.value = true
  sourceOn.value = false
  detailCache.clear()
}

// 预览按 1200×800 渲染，再等比缩放到容器宽度。
watch(frameBoxRef, (el) => {
  resizeObserver?.disconnect()
  resizeObserver = null
  if (!el) return
  frameWidth.value = el.clientWidth || OTOK_PELICAN_PREVIEW_WIDTH
  if (typeof ResizeObserver !== 'undefined') {
    resizeObserver = new ResizeObserver((entries) => {
      const w = entries[0]?.contentRect.width
      if (w) frameWidth.value = w
    })
    resizeObserver.observe(el)
  }
})

watch(
  () => [props.show, props.groupId] as const,
  async ([show]) => {
    reset()
    if (show) {
      await nextTick()
      void loadRounds()
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  window.clearTimeout(pollTimer)
  resizeObserver?.disconnect()
})
</script>
