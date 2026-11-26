<template>
  <div class="space-y-4" data-testid="otok-quality-panel">
    <div v-if="loading && rounds.length === 0" class="grid gap-4 xl:grid-cols-[280px_minmax(0,1fr)]">
      <div class="space-y-3">
        <div v-for="i in 4" :key="i" class="h-20 animate-pulse rounded-xl bg-gray-100 dark:bg-dark-700"></div>
      </div>
      <div class="h-80 animate-pulse rounded-xl bg-gray-100 dark:bg-dark-700"></div>
    </div>

    <div
      v-else-if="rounds.length === 0"
      class="rounded-xl border border-dashed border-gray-200 py-12 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400"
      data-testid="otok-quality-empty"
    >
      {{ t('otok.quality.noRounds') }}
    </div>

    <div v-else class="grid gap-4 xl:grid-cols-[280px_minmax(0,1fr)]">
      <!-- 左侧：轮次列表（新的在前） -->
      <ol class="flex max-h-[30vh] flex-col gap-2 overflow-y-auto pr-1 xl:max-h-[75vh]" data-testid="otok-quality-rounds">
        <li v-for="round in rounds" :key="round.id">
          <button
            type="button"
            class="w-full rounded-xl border px-4 py-3 text-left transition-colors"
            :class="
              round.id === selectedRoundId
                ? 'border-primary-400 bg-primary-50/60 dark:border-primary-500/60 dark:bg-primary-500/10'
                : 'border-gray-200 hover:border-gray-300 hover:bg-gray-50 dark:border-dark-700 dark:hover:border-dark-600 dark:hover:bg-dark-800'
            "
            :data-testid="`otok-quality-round-${round.id}`"
            @click="selectRound(round)"
          >
            <div class="font-mono text-sm font-semibold tabular-nums text-gray-900 dark:text-gray-100">
              {{ formatOtokQualityTime(round.started_at) }}
            </div>
            <div class="mt-1 text-xs font-medium" :class="ROUND_STATE_CLASS[otokQualityRoundState(round)]">
              {{ t(`otok.quality.roundStates.${otokQualityRoundState(round)}`) }}
              <span v-if="round.trigger === 'manual'" class="ml-1 text-gray-400">· {{ t('otok.quality.manual') }}</span>
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
      <section v-if="selectedRound" class="min-w-0 space-y-3 border-gray-100 dark:border-dark-700 xl:border-l xl:pl-4">
        <div class="text-xs text-gray-500 dark:text-gray-400">
          {{ t('otok.quality.startedAt') }}：{{ formatOtokQualityTime(selectedRound.started_at) }}
          · {{ t('otok.quality.finishedAt') }}：{{ formatOtokQualityTime(selectedRound.finished_at) }}
        </div>

        <details
          v-if="selectedRound.prompt"
          class="group rounded-lg border border-gray-200 bg-gray-50/60 dark:border-dark-700 dark:bg-dark-800/60"
          data-testid="otok-quality-prompt"
        >
          <summary class="cursor-pointer select-none px-3 py-2 text-xs font-medium text-gray-600 dark:text-gray-300">
            {{ t('otok.quality.roundPrompt') }}
          </summary>
          <pre class="max-h-60 overflow-auto whitespace-pre-wrap break-words border-t border-gray-200 px-3 py-2 text-xs leading-relaxed text-gray-700 dark:border-dark-700 dark:text-gray-200">{{ selectedRound.prompt }}</pre>
        </details>

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
            <template v-if="detail?.output_html">
              <button
                type="button"
                class="btn btn-secondary btn-sm"
                data-testid="otok-quality-toggle-preview"
                @click="previewOn = !previewOn"
              >
                {{ previewOn ? t('otok.quality.stopPreview') : t('otok.quality.preview') }}
              </button>
              <button
                type="button"
                class="btn btn-ghost btn-sm"
                :disabled="!detail?.output_text"
                data-testid="otok-quality-toggle-source"
                @click="sourceOn = !sourceOn"
              >
                {{ sourceOn ? t('otok.quality.hideSource') : t('otok.quality.viewSource') }}
              </button>
            </template>
            <span class="ml-auto font-mono text-xs tabular-nums text-gray-500 dark:text-gray-400">
              {{ runMeta }}
            </span>
          </div>

          <div
            v-if="admin && adminMeta"
            class="font-mono text-[11px] text-gray-400"
            data-testid="otok-quality-admin-meta"
          >
            {{ adminMeta }}
          </div>

          <!-- 失败 / 进行中的提示 -->
          <div
            v-if="selectedSummary.status === 'failed'"
            class="rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700 dark:bg-red-500/10 dark:text-red-300"
            data-testid="otok-quality-error"
          >
            {{ t(`otok.quality.errorCodes.${otokQualityErrorCodeKey(selectedSummary.error_code)}`) }}
            <span
              v-if="admin && errorMessage"
              class="mt-1 block whitespace-pre-wrap break-all font-mono opacity-80"
              data-testid="otok-quality-error-detail"
            >{{ errorMessage }}</span>
          </div>
          <div
            v-else-if="isOtokQualityRunPending(selectedSummary)"
            class="rounded-lg bg-blue-50 px-3 py-2 text-xs text-blue-700 dark:bg-blue-500/10 dark:text-blue-300"
          >
            {{ t('otok.quality.generating') }}
          </div>

          <div v-if="detailLoading" class="aspect-[3/2] w-full animate-pulse rounded-xl bg-gray-100 dark:bg-dark-700"></div>

          <template v-else-if="detail">
            <!-- 预览：sandbox 不给任何权限，脚本、表单、同源访问都被禁止 -->
            <div
              v-if="previewOn && previewDoc"
              ref="frameBoxRef"
              class="relative w-full overflow-hidden rounded-xl border border-gray-200 bg-white dark:border-dark-700"
              :style="{ height: `${frameHeight}px` }"
            >
              <iframe
                :key="detail.id"
                :srcdoc="previewDoc"
                sandbox=""
                referrerpolicy="no-referrer"
                loading="lazy"
                :title="t('otok.quality.previewTitle', { model: selectedSummary.model })"
                class="absolute left-0 top-0 origin-top-left border-0"
                :style="{
                  width: `${OTOK_QUALITY_PREVIEW_WIDTH}px`,
                  height: `${OTOK_QUALITY_PREVIEW_HEIGHT}px`,
                  transform: `scale(${frameScale})`,
                }"
                data-testid="otok-quality-iframe"
              ></iframe>
            </div>

            <!-- 原文：纯文本插值，不解析 HTML -->
            <pre
              v-if="detail.output_html && sourceOn && detail.output_text"
              class="max-h-[50vh] overflow-auto whitespace-pre-wrap break-all rounded-xl bg-gray-900 p-4 font-mono text-xs leading-relaxed text-gray-100"
              data-testid="otok-quality-source"
            >{{ detail.output_text }}</pre>

            <!-- 没有可预览的 HTML：直接显示输出文本 -->
            <pre
              v-if="!detail.output_html && detail.output_text"
              class="max-h-[60vh] overflow-auto whitespace-pre-wrap break-words rounded-xl border border-gray-200 bg-gray-50 p-4 text-sm leading-relaxed text-gray-800 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-100"
              data-testid="otok-quality-text"
            >{{ detail.output_text }}</pre>
          </template>

          <p v-if="detail?.output_html" class="text-[11px] text-gray-400">{{ t('otok.quality.sandboxHint') }}</p>
        </div>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import type {
  OtokQualityRound,
  OtokQualityRunDetail,
  OtokQualityRunSummary,
  OtokQualitySource,
} from '@/api/otok/quality'
import {
  OTOK_QUALITY_POLL_MS,
  OTOK_QUALITY_PREVIEW_HEIGHT,
  OTOK_QUALITY_PREVIEW_WIDTH,
  buildOtokQualityPreviewDoc,
  defaultOtokQualityRun,
  formatOtokQualityDuration,
  formatOtokQualityTime,
  hasOtokQualityPending,
  isOtokQualityRunPending,
  otokQualityErrorCodeKey,
  otokQualityRoundState,
  type OtokQualityRoundState,
} from '@/otok/quality'

const props = withDefaults(
  defineProps<{
    itemId: number | null
    source: OtokQualitySource
    /** 管理端：额外显示错误原文、HTTP 状态码、上游返回的模型名和协议。 */
    admin?: boolean
  }>(),
  { admin: false },
)

const { t } = useI18n()
const appStore = useAppStore()

const ROUND_STATE_CLASS: Record<OtokQualityRoundState, string> = {
  running: 'text-blue-600 dark:text-blue-400',
  succeeded: 'text-emerald-600 dark:text-emerald-400',
  partial: 'text-amber-600 dark:text-amber-400',
  failed: 'text-red-600 dark:text-red-400',
}

const PROTOCOL_LABEL: Record<string, string> = {
  responses: 'Responses',
  chat: 'Chat Completions',
  messages: 'Messages',
}

const rounds = ref<OtokQualityRound[]>([])
const loading = ref(false)
const selectedRoundId = ref<number | null>(null)
const selectedRunId = ref<number | null>(null)
const detail = ref<OtokQualityRunDetail | null>(null)
const detailLoading = ref(false)
const previewOn = ref(true)
const sourceOn = ref(false)
const frameBoxRef = ref<HTMLElement | null>(null)
const frameWidth = ref(OTOK_QUALITY_PREVIEW_WIDTH)

// 已结束的记录内容不会再变，按 id 缓存；进行中的不缓存。
const detailCache = new Map<number, OtokQualityRunDetail>()
let pollTimer: number | undefined
let resizeObserver: ResizeObserver | null = null
let loadSeq = 0
let unmounted = false

const selectedRound = computed(() => rounds.value.find((r) => r.id === selectedRoundId.value) ?? null)
const selectedSummary = computed<OtokQualityRunSummary | null>(
  () => selectedRound.value?.runs.find((r) => r.id === selectedRunId.value) ?? null,
)
const previewDoc = computed(() => buildOtokQualityPreviewDoc(detail.value?.output_html ?? ''))
const frameScale = computed(() => Math.min(1, frameWidth.value / OTOK_QUALITY_PREVIEW_WIDTH))
const frameHeight = computed(() => Math.round(OTOK_QUALITY_PREVIEW_HEIGHT * frameScale.value))

const errorMessage = computed(() => detail.value?.error_message || selectedSummary.value?.error_message || '')

const runMeta = computed(() => {
  const s = selectedSummary.value
  if (!s) return ''
  const parts = [formatOtokQualityDuration(s.duration_ms)]
  if (s.first_token_ms != null) parts.push(t('otok.quality.firstToken', { time: formatOtokQualityDuration(s.first_token_ms) }))
  if (detail.value?.id === s.id && detail.value.input_tokens != null) {
    parts.push(t('otok.quality.inputTokens', { n: detail.value.input_tokens }))
  }
  if (s.output_tokens != null) parts.push(t('otok.quality.outputTokens', { n: s.output_tokens }))
  if (s.reasoning_tokens != null) parts.push(t('otok.quality.reasoningTokens', { n: s.reasoning_tokens }))
  if (s.attempt > 1) parts.push(t('otok.quality.retried'))
  return parts.join(' · ')
})

const adminMeta = computed(() => {
  const d = detail.value?.id === selectedRunId.value ? detail.value : null
  const s = d ?? selectedSummary.value
  if (!s) return ''
  const parts: string[] = []
  if (s.protocol) parts.push(PROTOCOL_LABEL[s.protocol] ?? s.protocol)
  if (s.http_status != null) parts.push(`HTTP ${s.http_status}`)
  if (s.response_model) parts.push(t('otok.quality.responseModel', { model: s.response_model }))
  if (s.error_code) parts.push(s.error_code)
  return parts.join(' · ')
})

function runDurationLabel(run: OtokQualityRunSummary): string {
  if (isOtokQualityRunPending(run)) return t(`otok.quality.runStates.${run.status}`)
  if (run.status === 'failed') return `${t('otok.quality.runStates.failed')} · ${formatOtokQualityDuration(run.duration_ms)}`
  return formatOtokQualityDuration(run.duration_ms)
}

async function loadRounds(silent = false) {
  if (props.itemId == null) return
  const itemId = props.itemId
  const seq = ++loadSeq
  if (!silent) loading.value = true
  try {
    const list = await props.source.listRounds(itemId)
    if (seq !== loadSeq) return
    rounds.value = list
    const current = list.find((r) => r.id === selectedRoundId.value)
    if (!current) {
      selectRound(list[0] ?? null)
    } else if (
      selectedSummary.value &&
      !isOtokQualityRunPending(selectedSummary.value) &&
      detail.value?.status !== selectedSummary.value.status
    ) {
      // 选中的记录刚完成：重新拉取内容
      void loadDetail(selectedSummary.value.id)
    }
  } catch (err) {
    if (!silent && seq === loadSeq) appStore.showError(extractApiErrorMessage(err, t('otok.quality.loadFailed')))
  } finally {
    if (seq === loadSeq) {
      loading.value = false
      schedulePoll()
    }
  }
}

function schedulePoll() {
  window.clearTimeout(pollTimer)
  pollTimer = undefined
  if (!unmounted && props.itemId != null && hasOtokQualityPending(rounds.value)) {
    pollTimer = window.setTimeout(() => void loadRounds(true), OTOK_QUALITY_POLL_MS)
  }
}

function selectRound(round: OtokQualityRound | null) {
  selectedRoundId.value = round?.id ?? null
  const run = defaultOtokQualityRun(round)
  selectRun(run?.id ?? null)
}

function selectRun(runId: number | null) {
  if (runId === selectedRunId.value && detail.value?.id === runId) return
  selectedRunId.value = runId
  sourceOn.value = false
  detail.value = null
  detailLoading.value = false
  if (runId != null) void loadDetail(runId)
}

async function loadDetail(runId: number) {
  const cached = detailCache.get(runId)
  if (cached) {
    detail.value = cached
    detailLoading.value = false
    return
  }
  detailLoading.value = true
  try {
    const d = await props.source.getRun(runId)
    if (!isOtokQualityRunPending(d)) detailCache.set(runId, d)
    if (selectedRunId.value === runId) detail.value = d
  } catch (err) {
    appStore.showError(extractApiErrorMessage(err, t('otok.quality.loadFailed')))
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
  loading.value = false
  previewOn.value = true
  sourceOn.value = false
  detailCache.clear()
}

/** 供页面上的「刷新」按钮调用：保留当前选中项，重新拉轮次列表。 */
function reload() {
  void loadRounds()
}

defineExpose({ reload })

// 预览按 1200×800 渲染，再等比缩放到容器宽度。
watch(frameBoxRef, (el) => {
  resizeObserver?.disconnect()
  resizeObserver = null
  if (!el) return
  frameWidth.value = el.clientWidth || OTOK_QUALITY_PREVIEW_WIDTH
  if (typeof ResizeObserver !== 'undefined') {
    resizeObserver = new ResizeObserver((entries) => {
      const w = entries[0]?.contentRect.width
      if (w) frameWidth.value = w
    })
    resizeObserver.observe(el)
  }
})

watch(
  () => props.itemId,
  async (id) => {
    reset()
    if (id != null) {
      await nextTick()
      void loadRounds()
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  unmounted = true
  window.clearTimeout(pollTimer)
  resizeObserver?.disconnect()
})
</script>
