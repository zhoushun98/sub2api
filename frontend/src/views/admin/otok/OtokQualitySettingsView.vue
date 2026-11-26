<template>
  <AppLayout>
    <div class="mx-auto max-w-6xl space-y-4">
      <!-- 说明 -->
      <div class="card p-6">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div class="min-w-0 flex-1">
            <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('otok.nav.qualitySettings') }}</h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('otok.qualityAdmin.description') }}</p>
          </div>
          <div v-if="loaded" class="rounded-lg bg-gray-50 px-3 py-2 text-right dark:bg-dark-800" data-testid="otok-quality-daily">
            <div class="text-xs text-gray-500">{{ t('otok.qualityAdmin.dailyEstimate') }}</div>
            <div class="mt-0.5 font-mono text-lg font-semibold tabular-nums text-gray-900 dark:text-gray-100">{{ dailyRequests }}</div>
          </div>
        </div>

        <div class="mt-4 rounded-lg border border-gray-100 bg-gray-50/60 px-4 py-3 dark:border-dark-700 dark:bg-dark-800/60">
          <div class="text-xs font-medium text-gray-700 dark:text-gray-200">{{ t('otok.qualityAdmin.notesTitle') }}</div>
          <ul class="mt-1.5 list-disc space-y-1 pl-4 text-xs leading-relaxed text-gray-500 dark:text-gray-400">
            <li>{{ t('otok.qualityAdmin.notes.local', LOCAL_URLS) }}</li>
            <li>{{ t('otok.qualityAdmin.notes.billing') }}</li>
            <li>{{ t('otok.qualityAdmin.notes.visibility') }}</li>
            <li>{{ t('otok.qualityAdmin.notes.audit') }}</li>
            <li>{{ t('otok.qualityAdmin.notes.sandbox') }}</li>
          </ul>
        </div>

        <p
          v-if="status?.disabled"
          class="mt-3 rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:bg-amber-500/10 dark:text-amber-300"
        >
          {{ t('otok.qualityAdmin.disabledByEnv') }}
        </p>
        <p
          v-if="loaded && form.invalid"
          class="mt-3 rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700 dark:bg-red-500/10 dark:text-red-300"
          data-testid="otok-quality-invalid"
        >
          {{ t('otok.qualityAdmin.invalidConfig') }}
        </p>
      </div>

      <div v-if="loading" class="card space-y-4 p-6">
        <div v-for="i in 5" :key="i" class="h-10 animate-pulse rounded-lg bg-gray-100 dark:bg-dark-700"></div>
      </div>

      <!-- 加载失败时不渲染表单，避免用默认值覆盖已存配置 -->
      <div v-else-if="!loaded" class="card flex flex-col items-center gap-3 p-10 text-sm text-gray-500 dark:text-gray-400">
        {{ t('otok.qualityAdmin.loadFailed') }}
        <button type="button" class="btn btn-secondary btn-sm" @click="loadAll">{{ t('otok.qualityAdmin.reload') }}</button>
      </div>

      <form v-else class="space-y-4" @submit.prevent="save">
        <!-- 全局设置 -->
        <div class="card space-y-5 p-6">
          <div class="flex items-center justify-between gap-4">
            <div>
              <div class="font-medium text-gray-900 dark:text-white">{{ t('otok.qualityAdmin.enabled') }}</div>
              <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('otok.qualityAdmin.enabledHint') }}</p>
            </div>
            <Toggle v-model="form.enabled" data-testid="otok-quality-enabled" />
          </div>

          <div class="grid grid-cols-2 gap-4 md:grid-cols-4">
            <div>
              <label class="input-label" for="otok-quality-interval">{{ t('otok.qualityAdmin.interval') }}</label>
              <input id="otok-quality-interval" v-model.number="form.interval_minutes" type="number" min="5" max="1440" class="input" />
            </div>
            <div>
              <label class="input-label" for="otok-quality-timeout">{{ t('otok.qualityAdmin.timeout') }}</label>
              <input id="otok-quality-timeout" v-model.number="form.timeout_seconds" type="number" min="60" max="900" class="input" />
            </div>
            <div>
              <label class="input-label" for="otok-quality-concurrency">{{ t('otok.qualityAdmin.concurrency') }}</label>
              <input id="otok-quality-concurrency" v-model.number="form.concurrency" type="number" min="1" max="16" class="input" />
            </div>
            <div>
              <label class="input-label" for="otok-quality-keep">{{ t('otok.qualityAdmin.keepRounds') }}</label>
              <input id="otok-quality-keep" v-model.number="form.keep_rounds" type="number" min="1" max="50" class="input" />
            </div>
          </div>
          <p class="-mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('otok.qualityAdmin.timeoutHint') }}</p>
        </div>

        <!-- 条目 -->
        <div class="card overflow-hidden">
          <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-6 py-4 dark:border-dark-700">
            <div>
              <div class="font-medium text-gray-900 dark:text-white">{{ t('otok.qualityAdmin.items') }}</div>
              <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('otok.qualityAdmin.itemsHint') }}</p>
            </div>
            <button
              type="button"
              class="btn btn-secondary btn-sm"
              :disabled="items.length >= OTOK_QUALITY_MAX_ITEMS"
              data-testid="otok-quality-add"
              @click="addItem"
            >
              {{ t('otok.qualityAdmin.addItem') }}
            </button>
          </div>

          <div v-if="items.length === 0" class="px-6 py-10 text-center text-sm text-gray-500">{{ t('otok.qualityAdmin.noItems') }}</div>

          <div v-else class="divide-y divide-gray-100 dark:divide-dark-700">
            <div v-for="(item, index) in items" :key="item.uid" class="space-y-4 px-6 py-5" :data-testid="`otok-quality-item-${index}`">
              <!-- 条目头：名称、状态、排序和删除 -->
              <div class="flex flex-wrap items-center gap-2">
                <span class="font-mono text-xs text-gray-400">#{{ index + 1 }}</span>
                <span class="min-w-0 max-w-xs truncate font-medium text-gray-900 dark:text-white">
                  {{ item.name.trim() || t('otok.qualityAdmin.newItem') }}
                </span>
                <span v-if="item.id === 0" class="rounded bg-amber-50 px-1.5 py-0.5 text-[11px] text-amber-700 dark:bg-amber-500/10 dark:text-amber-300">
                  {{ t('otok.qualityAdmin.unsavedItem') }}
                </span>
                <span v-if="!item.enabled" class="rounded bg-gray-100 px-1.5 py-0.5 text-[11px] text-gray-500 dark:bg-dark-700 dark:text-gray-400">
                  {{ t('otok.qualityAdmin.itemDisabled') }}
                </span>
                <span v-if="item.id !== 0" class="text-xs text-gray-500">
                  <template v-if="itemStatus(item.id)?.last_started_at">
                    {{ t('otok.qualityAdmin.lastRun', { time: formatOtokQualityTime(itemStatus(item.id)?.last_started_at) }) }}
                  </template>
                  <template v-else>{{ t('otok.qualityAdmin.neverRun') }}</template>
                  <span v-if="itemStatus(item.id)?.running" class="text-blue-600 dark:text-blue-400"> · {{ t('otok.qualityAdmin.running') }}</span>
                </span>
                <div class="ml-auto flex gap-1">
                  <button type="button" class="btn btn-ghost btn-sm" :disabled="index === 0" :title="t('otok.qualityAdmin.moveUp')" @click="move(index, -1)">
                    <Icon name="arrowUp" size="sm" />
                  </button>
                  <button
                    type="button"
                    class="btn btn-ghost btn-sm"
                    :disabled="index === items.length - 1"
                    :title="t('otok.qualityAdmin.moveDown')"
                    @click="move(index, 1)"
                  >
                    <Icon name="arrowDown" size="sm" />
                  </button>
                  <button
                    type="button"
                    class="btn btn-ghost btn-sm text-red-600 dark:text-red-400"
                    :data-testid="`otok-quality-remove-${index}`"
                    @click="askRemove(item)"
                  >
                    {{ t('otok.qualityAdmin.remove') }}
                  </button>
                </div>
              </div>

              <div class="grid gap-4 md:grid-cols-[minmax(0,1fr)_auto]">
                <div>
                  <label class="input-label" :for="`otok-quality-name-${item.uid}`">{{ t('otok.qualityAdmin.itemName') }}</label>
                  <input
                    :id="`otok-quality-name-${item.uid}`"
                    v-model="item.name"
                    type="text"
                    class="input"
                    :maxlength="OTOK_QUALITY_MAX_NAME"
                    :placeholder="t('otok.qualityAdmin.itemNamePlaceholder')"
                    autocomplete="off"
                  />
                </div>
                <div class="flex items-center gap-2 md:pt-7">
                  <Toggle v-model="item.enabled" />
                  <span class="text-xs text-gray-500">{{ t('otok.qualityAdmin.itemEnabled') }}</span>
                </div>
              </div>

              <div>
                <div class="flex flex-wrap items-center justify-between gap-2">
                  <label class="input-label" :for="`otok-quality-url-${item.uid}`">{{ t('otok.qualityAdmin.url') }}</label>
                  <span
                    v-if="protocolOf(item)"
                    class="rounded bg-emerald-50 px-1.5 py-0.5 font-mono text-[11px] text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-300"
                    :data-testid="`otok-quality-protocol-${index}`"
                  >
                    {{ PROTOCOL_LABEL[protocolOf(item)!] }}
                  </span>
                  <span v-else-if="item.url.trim()" class="text-[11px] text-red-600 dark:text-red-400" :data-testid="`otok-quality-protocol-${index}`">
                    {{ t('otok.qualityAdmin.protocolUnknown') }}
                  </span>
                </div>
                <input
                  :id="`otok-quality-url-${item.uid}`"
                  v-model="item.url"
                  type="text"
                  class="input font-mono text-xs"
                  :class="item.url.trim() && !protocolOf(item) ? 'border-red-400 dark:border-red-500' : ''"
                  :placeholder="t('otok.qualityAdmin.urlPlaceholder')"
                  autocomplete="off"
                  spellcheck="false"
                />
              </div>

              <div class="grid gap-4 md:grid-cols-3">
                <div>
                  <label class="input-label" :for="`otok-quality-key-${item.uid}`">{{ t('otok.qualityAdmin.apiKey') }}</label>
                  <input
                    :id="`otok-quality-key-${item.uid}`"
                    v-model="item.apiKey"
                    type="password"
                    class="input font-mono text-xs"
                    :placeholder="keyPlaceholder(item)"
                    autocomplete="new-password"
                    spellcheck="false"
                  />
                  <p v-if="item.apiKeyDecryptFailed && !item.apiKey" class="mt-1 text-xs text-red-600 dark:text-red-400">
                    {{ t('otok.qualityAdmin.apiKeyDecryptFailed') }}
                  </p>
                </div>
                <div>
                  <label class="input-label" :for="`otok-quality-models-${item.uid}`">{{ t('otok.qualityAdmin.models') }}</label>
                  <input
                    :id="`otok-quality-models-${item.uid}`"
                    v-model="item.modelsText"
                    type="text"
                    class="input font-mono text-xs"
                    autocomplete="off"
                    spellcheck="false"
                  />
                  <p class="input-hint">{{ t('otok.qualityAdmin.modelsHint') }}</p>
                </div>
                <div>
                  <label class="input-label" :for="`otok-quality-effort-${item.uid}`">{{ t('otok.qualityAdmin.effort') }}</label>
                  <select
                    :id="`otok-quality-effort-${item.uid}`"
                    v-model="item.effort"
                    class="input"
                    :disabled="protocolOf(item) === 'messages'"
                  >
                    <option v-for="e in OTOK_QUALITY_EFFORTS" :key="e" :value="e">{{ t(`otok.qualityAdmin.efforts.${e || 'omit'}`) }}</option>
                  </select>
                  <p v-if="protocolOf(item) === 'messages'" class="input-hint">{{ t('otok.qualityAdmin.effortMessagesHint') }}</p>
                </div>
              </div>

              <div>
                <div class="flex items-center justify-between">
                  <label class="input-label" :for="`otok-quality-prompt-${item.uid}`">{{ t('otok.qualityAdmin.prompt') }}</label>
                  <button
                    type="button"
                    class="text-xs text-primary-600 hover:underline dark:text-primary-400"
                    :data-testid="`otok-quality-sample-${index}`"
                    @click="item.prompt = OTOK_QUALITY_SAMPLE_PROMPT"
                  >
                    {{ t('otok.qualityAdmin.fillSample') }}
                  </button>
                </div>
                <textarea
                  :id="`otok-quality-prompt-${item.uid}`"
                  v-model="item.prompt"
                  rows="6"
                  class="input font-mono text-xs leading-relaxed"
                ></textarea>
                <p class="input-hint">{{ t('otok.qualityAdmin.promptHint') }}</p>
              </div>

              <div>
                <label class="input-label" :for="`otok-quality-extra-${item.uid}`">{{ t('otok.qualityAdmin.extraBody') }}</label>
                <textarea
                  :id="`otok-quality-extra-${item.uid}`"
                  v-model="item.extraBody"
                  rows="3"
                  class="input font-mono text-xs leading-relaxed"
                  :class="!isValidOtokQualityExtraBody(item.extraBody) ? 'border-red-400 dark:border-red-500' : ''"
                  spellcheck="false"
                ></textarea>
                <p v-if="!isValidOtokQualityExtraBody(item.extraBody)" class="mt-1 text-xs text-red-600 dark:text-red-400">
                  {{ t('otok.qualityAdmin.extraBodyInvalid') }}
                </p>
                <p class="input-hint">
                  {{ t('otok.qualityAdmin.extraBodyHint') }}
                  <template v-if="protocolOf(item)">
                    {{ t(`otok.qualityAdmin.extraBodyExample.${protocolOf(item)}`, { example: EXTRA_BODY_EXAMPLE[protocolOf(item)!] }) }}
                  </template>
                </p>
              </div>

              <div class="flex flex-wrap items-center justify-end gap-2">
                <span v-if="runNowBlocked(item)" class="text-xs text-gray-400">{{ t('otok.qualityAdmin.runNowNeedSave') }}</span>
                <button
                  type="button"
                  class="btn btn-secondary btn-sm"
                  :disabled="runNowBlocked(item) || runningNow.has(item.id)"
                  :title="runNowBlocked(item) ? t('otok.qualityAdmin.runNowNeedSave') : undefined"
                  :data-testid="`otok-quality-run-${index}`"
                  @click="runNow(item)"
                >
                  {{ t('otok.qualityAdmin.runNow') }}
                </button>
                <button
                  type="button"
                  class="btn btn-ghost btn-sm"
                  :disabled="item.id === 0"
                  :data-testid="`otok-quality-view-${index}`"
                  @click="viewItem = { id: item.id, name: item.name }"
                >
                  {{ t('otok.qualityAdmin.viewResults') }}
                </button>
              </div>
            </div>
          </div>
        </div>

        <div class="flex items-center justify-end gap-3">
          <span v-if="dirty" class="text-xs text-amber-600 dark:text-amber-400">{{ t('otok.qualityAdmin.unsaved') }}</span>
          <button type="submit" class="btn btn-primary" :disabled="saving" data-testid="otok-quality-save">
            {{ saving ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </form>
    </div>

    <BaseDialog
      :show="viewItem != null"
      :title="t('otok.qualityAdmin.resultsTitle', { name: viewItem?.name ?? '' })"
      width="full"
      close-on-click-outside
      @close="viewItem = null"
    >
      <OtokQualityPanel v-if="viewItem" :item-id="viewItem.id" :source="otokAdminQualityAPI" admin />
    </BaseDialog>

    <ConfirmDialog
      :show="pendingRemove != null"
      :title="t('otok.qualityAdmin.removeTitle')"
      :message="t('otok.qualityAdmin.removeConfirm', { name: pendingRemove?.name.trim() || t('otok.qualityAdmin.newItem') })"
      :confirm-text="t('otok.qualityAdmin.remove')"
      danger
      @confirm="confirmRemove"
      @cancel="pendingRemove = null"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import OtokQualityPanel from '@/components/otok/quality/OtokQualityPanel.vue'
import {
  otokAdminQualityAPI,
  type OtokQualityConfig,
  type OtokQualityConfigUpdate,
  type OtokQualityProtocol,
  type OtokQualityStatus,
} from '@/api/otok/quality'
import {
  OTOK_QUALITY_EFFORTS,
  OTOK_QUALITY_MAX_API_KEY,
  OTOK_QUALITY_MAX_EXTRA_BODY_BYTES,
  OTOK_QUALITY_MAX_ITEMS,
  OTOK_QUALITY_MAX_MODELS,
  OTOK_QUALITY_MAX_NAME,
  OTOK_QUALITY_MAX_PROMPT,
  OTOK_QUALITY_MODEL_RE,
  OTOK_QUALITY_SAMPLE_PROMPT,
  detectOtokQualityProtocol,
  estimateOtokQualityDailyRequests,
  formatOtokQualityTime,
  isValidOtokQualityExtraBody,
  parseOtokQualityModels,
  refreshOtokQualityItems,
} from '@/otok/quality'
import { useAppStore } from '@/stores/app'
import { extractI18nErrorMessage } from '@/utils/apiError'

interface ItemForm {
  /** 只在页面内区分行，新条目保存前 id 都是 0。 */
  uid: number
  id: number
  name: string
  enabled: boolean
  url: string
  apiKey: string
  hasApiKey: boolean
  apiKeyMasked: string
  apiKeyDecryptFailed: boolean
  modelsText: string
  effort: string
  prompt: string
  extraBody: string
}

const LOCAL_URLS = {
  responses: 'http://127.0.0.1:8080/v1/responses',
  chat: 'http://127.0.0.1:8080/v1/chat/completions',
  messages: 'http://127.0.0.1:8080/v1/messages',
}

const PROTOCOL_LABEL: Record<OtokQualityProtocol, string> = {
  responses: 'Responses',
  chat: 'Chat Completions',
  messages: 'Messages',
}

// JSON 示例放在代码里：花括号在 i18n 文案里会被当成占位符。
const EXTRA_BODY_EXAMPLE: Record<OtokQualityProtocol, string> = {
  responses: '{"max_output_tokens": 16000}',
  chat: '{"max_tokens": 16000}',
  messages: '{"max_tokens": 32000}',
}

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(true)
const loaded = ref(false)
const saving = ref(false)
const status = ref<OtokQualityStatus | null>(null)
const items = ref<ItemForm[]>([])
const viewItem = ref<{ id: number; name: string } | null>(null)
const pendingRemove = ref<ItemForm | null>(null)
const runningNow = ref(new Set<number>())
const savedSnapshot = ref('')
let nextUid = 1

const form = reactive({
  version: 0,
  invalid: false,
  enabled: false,
  interval_minutes: 60,
  timeout_seconds: 480,
  concurrency: 2,
  keep_rounds: 10,
})

function protocolOf(item: ItemForm): OtokQualityProtocol | null {
  return detectOtokQualityProtocol(item.url)
}

function keyPlaceholder(item: ItemForm): string {
  if (!item.hasApiKey) return t('otok.qualityAdmin.apiKeyRequired')
  if (item.apiKeyMasked) return t('otok.qualityAdmin.apiKeyKeep', { masked: item.apiKeyMasked })
  return t('otok.qualityAdmin.apiKeyKeepNoMask')
}

function buildPayload(): OtokQualityConfigUpdate {
  return {
    version: form.version,
    enabled: form.enabled,
    interval_minutes: form.interval_minutes,
    timeout_seconds: form.timeout_seconds,
    concurrency: form.concurrency,
    keep_rounds: form.keep_rounds,
    items: items.value.map((i) => ({
      id: i.id,
      name: i.name.trim(),
      enabled: i.enabled,
      url: i.url.trim(),
      api_key: i.apiKey.trim(),
      models: parseOtokQualityModels(i.modelsText),
      reasoning_effort: i.effort,
      prompt: i.prompt,
      extra_body: i.extraBody.trim(),
    })),
  }
}

const dirty = computed(() => loaded.value && JSON.stringify(buildPayload()) !== savedSnapshot.value)

const dailyRequests = computed(() =>
  estimateOtokQualityDailyRequests({
    enabled: form.enabled,
    interval_minutes: form.interval_minutes,
    items: items.value.map((i) => ({ enabled: i.enabled, models: parseOtokQualityModels(i.modelsText) })),
  }).toLocaleString(),
)

function runNowBlocked(item: ItemForm): boolean {
  return item.id === 0 || dirty.value
}

function itemStatus(id: number) {
  return status.value?.items.find((s) => s.item_id === id)
}

/** 用后端返回值整体替换表单：回填 id 和 version，清空 key 输入框。 */
function applyConfig(cfg: OtokQualityConfig) {
  form.version = cfg.version
  form.invalid = !!cfg.invalid
  form.enabled = cfg.enabled
  form.interval_minutes = cfg.interval_minutes
  form.timeout_seconds = cfg.timeout_seconds
  form.concurrency = cfg.concurrency
  form.keep_rounds = cfg.keep_rounds
  items.value = (cfg.items ?? []).map((i) => ({
    uid: nextUid++,
    id: i.id,
    name: i.name,
    enabled: i.enabled,
    url: i.url,
    apiKey: '',
    hasApiKey: i.has_api_key,
    apiKeyMasked: i.api_key_masked,
    apiKeyDecryptFailed: i.api_key_decrypt_failed,
    modelsText: (i.models ?? []).join(', '),
    effort: i.reasoning_effort ?? '',
    prompt: i.prompt,
    extraBody: i.extra_body ?? '',
  }))
  savedSnapshot.value = JSON.stringify(buildPayload())
}

function addItem() {
  if (items.value.length >= OTOK_QUALITY_MAX_ITEMS) return
  items.value.push({
    uid: nextUid++,
    id: 0,
    name: '',
    enabled: true,
    url: '',
    apiKey: '',
    hasApiKey: false,
    apiKeyMasked: '',
    apiKeyDecryptFailed: false,
    modelsText: '',
    effort: 'medium',
    prompt: '',
    extraBody: '',
  })
}

function move(index: number, delta: number) {
  const target = index + delta
  if (target < 0 || target >= items.value.length) return
  const list = items.value.slice()
  ;[list[index], list[target]] = [list[target], list[index]]
  items.value = list
}

function askRemove(item: ItemForm) {
  // 还没保存过的条目没有历史结果，直接移除。
  if (item.id === 0) {
    items.value = items.value.filter((i) => i.uid !== item.uid)
    return
  }
  pendingRemove.value = item
}

function confirmRemove() {
  const target = pendingRemove.value
  pendingRemove.value = null
  if (target) items.value = items.value.filter((i) => i.uid !== target.uid)
}

async function loadStatus() {
  try {
    status.value = await otokAdminQualityAPI.getStatus()
  } catch {
    status.value = null
  }
}

async function loadAll() {
  loading.value = true
  try {
    applyConfig(await otokAdminQualityAPI.getConfig())
    loaded.value = true
    void loadStatus()
  } catch (err) {
    loaded.value = false
    appStore.showError(extractI18nErrorMessage(err, t, 'otok.qualityAdmin.errors', t('otok.qualityAdmin.loadFailed')))
  } finally {
    loading.value = false
  }
}

function inRange(v: number, min: number, max: number): boolean {
  return Number.isInteger(v) && v >= min && v <= max
}

function localError(cfg: OtokQualityConfigUpdate): string | null {
  const err = (code: string) => t(`otok.qualityAdmin.errors.OTOK_QUALITY_${code}`)
  if (!inRange(cfg.interval_minutes, 5, 1440)) return err('INVALID_INTERVAL')
  if (!inRange(cfg.timeout_seconds, 60, 900)) return err('INVALID_TIMEOUT')
  if (!inRange(cfg.concurrency, 1, 16)) return err('INVALID_CONCURRENCY')
  if (!inRange(cfg.keep_rounds, 1, 50)) return err('INVALID_KEEP_ROUNDS')
  if (cfg.items.length > OTOK_QUALITY_MAX_ITEMS) return err('TOO_MANY_ITEMS')
  for (const [index, item] of cfg.items.entries()) {
    const label = item.name || `#${index + 1}`
    const itemErr = (message: string) => t('otok.qualityAdmin.itemError', { name: label, message })
    if (!item.name || [...item.name].length > OTOK_QUALITY_MAX_NAME) return itemErr(err('INVALID_NAME'))
    if (!detectOtokQualityProtocol(item.url)) return itemErr(err('INVALID_URL'))
    if (item.id === 0 && !item.api_key) return itemErr(err('API_KEY_REQUIRED'))
    if (item.api_key && (item.api_key.length > OTOK_QUALITY_MAX_API_KEY || /[\s\p{Cc}]/u.test(item.api_key))) {
      return itemErr(err('INVALID_API_KEY'))
    }
    if (item.models.length === 0) return itemErr(err('NO_MODELS'))
    if (item.models.length > OTOK_QUALITY_MAX_MODELS) return itemErr(err('TOO_MANY_MODELS'))
    const bad = item.models.find((m) => !OTOK_QUALITY_MODEL_RE.test(m))
    if (bad) return itemErr(t('otok.qualityAdmin.invalidModel', { model: bad }))
    if (!item.prompt.trim()) return itemErr(err('PROMPT_REQUIRED'))
    if ([...item.prompt.trim()].length > OTOK_QUALITY_MAX_PROMPT) return itemErr(err('PROMPT_TOO_LONG'))
    if (
      !isValidOtokQualityExtraBody(item.extra_body) ||
      new TextEncoder().encode(item.extra_body).length > OTOK_QUALITY_MAX_EXTRA_BODY_BYTES
    ) {
      return itemErr(err('INVALID_EXTRA_BODY'))
    }
  }
  return null
}

async function save() {
  const payload = buildPayload()
  const msg = localError(payload)
  if (msg) {
    appStore.showError(msg)
    return
  }
  saving.value = true
  try {
    applyConfig(await otokAdminQualityAPI.updateConfig(payload))
    appStore.showSuccess(t('otok.qualityAdmin.saved'))
    void refreshOtokQualityItems()
    void loadStatus()
  } catch (err) {
    appStore.showError(extractI18nErrorMessage(err, t, 'otok.qualityAdmin.errors', t('common.error')))
  } finally {
    saving.value = false
  }
}

async function runNow(item: ItemForm) {
  if (runNowBlocked(item)) return
  const id = item.id
  runningNow.value.add(id)
  try {
    await otokAdminQualityAPI.runNow(id)
    appStore.showSuccess(t('otok.qualityAdmin.runNowStarted', { name: item.name }))
    void loadStatus()
  } catch (err) {
    appStore.showError(extractI18nErrorMessage(err, t, 'otok.qualityAdmin.errors', t('common.error')))
  } finally {
    runningNow.value.delete(id)
  }
}

onMounted(loadAll)
</script>
