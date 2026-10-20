<template>
  <AppLayout>
    <div class="mx-auto max-w-6xl space-y-4">
      <!-- 说明 + 内部账号状态 -->
      <div class="card p-6">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div class="min-w-0">
            <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('otok.nav.pelicanSettings') }}</h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('otok.pelicanAdmin.description') }}</p>
          </div>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" @click="loadAll">
            {{ t('common.refresh') }}
          </button>
        </div>

        <dl v-if="status" class="mt-4 grid grid-cols-2 gap-3 text-sm sm:grid-cols-4" data-testid="otok-pelican-status">
          <div class="rounded-lg bg-gray-50 px-3 py-2 dark:bg-dark-800">
            <dt class="text-xs text-gray-500">{{ t('otok.pelicanAdmin.probeUser') }}</dt>
            <dd class="mt-0.5 truncate font-mono text-gray-900 dark:text-gray-100">
              {{ status.user_id ? `#${status.user_id} ${status.user_email}` : t('otok.pelicanAdmin.probeUserPending') }}
            </dd>
          </div>
          <div class="rounded-lg bg-gray-50 px-3 py-2 dark:bg-dark-800">
            <dt class="text-xs text-gray-500">{{ t('otok.pelicanAdmin.probeBalance') }}</dt>
            <dd class="mt-0.5 font-mono text-gray-900 dark:text-gray-100">
              {{ status.user_id ? `$${status.balance.toFixed(2)}` : '—' }}
              <span v-if="status.user_status && status.user_status !== 'active'" class="ml-1 text-red-600">{{ status.user_status }}</span>
            </dd>
          </div>
          <div class="rounded-lg bg-gray-50 px-3 py-2 dark:bg-dark-800">
            <dt class="text-xs text-gray-500">{{ t('otok.pelicanAdmin.baseUrl') }}</dt>
            <dd class="mt-0.5 truncate font-mono text-gray-900 dark:text-gray-100">{{ status.base_url }}</dd>
          </div>
          <div class="rounded-lg bg-gray-50 px-3 py-2 dark:bg-dark-800">
            <dt class="text-xs text-gray-500">{{ t('otok.pelicanAdmin.dailyEstimate') }}</dt>
            <dd class="mt-0.5 font-mono text-gray-900 dark:text-gray-100">{{ dailyRequests }}</dd>
          </div>
        </dl>
        <p v-if="status?.disabled" class="mt-3 rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:bg-amber-500/10 dark:text-amber-300">
          {{ t('otok.pelicanAdmin.disabledByEnv') }}
        </p>
        <p v-if="status?.error" class="mt-3 rounded-lg bg-red-50 px-3 py-2 text-xs text-red-700 dark:bg-red-500/10 dark:text-red-300">
          {{ status.error }}
        </p>
        <p class="mt-3 text-xs text-gray-500 dark:text-gray-400">{{ t('otok.pelicanAdmin.costHint') }}</p>
      </div>

      <div v-if="loading" class="card space-y-4 p-6">
        <div v-for="i in 5" :key="i" class="h-10 animate-pulse rounded-lg bg-gray-100 dark:bg-dark-700"></div>
      </div>

      <form v-else class="space-y-4" @submit.prevent="save">
        <!-- 全局设置 -->
        <div class="card space-y-5 p-6">
          <div class="flex items-center justify-between gap-4">
            <div>
              <div class="font-medium text-gray-900 dark:text-white">{{ t('otok.pelicanAdmin.enabled') }}</div>
              <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('otok.pelicanAdmin.enabledHint') }}</p>
            </div>
            <Toggle v-model="form.enabled" data-testid="otok-pelican-enabled" />
          </div>

          <div class="grid grid-cols-2 gap-4 md:grid-cols-5">
            <div>
              <label class="input-label" for="otok-pelican-interval">{{ t('otok.pelicanAdmin.interval') }}</label>
              <input id="otok-pelican-interval" v-model.number="form.interval_minutes" type="number" min="5" max="1440" class="input" />
            </div>
            <div>
              <label class="input-label" for="otok-pelican-effort">{{ t('otok.pelicanAdmin.effort') }}</label>
              <select id="otok-pelican-effort" v-model="form.reasoning_effort" class="input">
                <option v-for="e in EFFORTS" :key="e" :value="e">{{ t(`otok.pelicanAdmin.efforts.${e}`) }}</option>
              </select>
            </div>
            <div>
              <label class="input-label" for="otok-pelican-timeout">{{ t('otok.pelicanAdmin.timeout') }}</label>
              <input id="otok-pelican-timeout" v-model.number="form.timeout_seconds" type="number" min="60" max="900" class="input" />
            </div>
            <div>
              <label class="input-label" for="otok-pelican-concurrency">{{ t('otok.pelicanAdmin.concurrency') }}</label>
              <input id="otok-pelican-concurrency" v-model.number="form.concurrency" type="number" min="1" max="4" class="input" />
            </div>
            <div>
              <label class="input-label" for="otok-pelican-keep">{{ t('otok.pelicanAdmin.keepRounds') }}</label>
              <input id="otok-pelican-keep" v-model.number="form.keep_rounds" type="number" min="1" max="50" class="input" />
            </div>
          </div>

          <div>
            <label class="input-label" for="otok-pelican-models">{{ t('otok.pelicanAdmin.defaultModels') }}</label>
            <input id="otok-pelican-models" v-model="defaultModelsText" type="text" class="input font-mono" autocomplete="off" />
            <p class="input-hint">{{ t('otok.pelicanAdmin.modelsHint') }}</p>
          </div>

          <div>
            <div class="flex items-center justify-between">
              <label class="input-label" for="otok-pelican-prompt">{{ t('otok.pelicanAdmin.prompt') }}</label>
              <button type="button" class="text-xs text-primary-600 hover:underline dark:text-primary-400" @click="form.prompt = ''">
                {{ t('otok.pelicanAdmin.resetPrompt') }}
              </button>
            </div>
            <textarea id="otok-pelican-prompt" v-model="form.prompt" rows="7" class="input font-mono text-xs leading-relaxed"></textarea>
            <p class="input-hint">{{ t('otok.pelicanAdmin.promptHint') }}</p>
          </div>
        </div>

        <!-- 分组 -->
        <div class="card overflow-hidden">
          <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
            <div class="font-medium text-gray-900 dark:text-white">{{ t('otok.pelicanAdmin.groups') }}</div>
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('otok.pelicanAdmin.groupsHint') }}</p>
          </div>
          <div v-if="rows.length === 0" class="px-6 py-8 text-center text-sm text-gray-500">{{ t('otok.pelicanAdmin.noGroups') }}</div>
          <div v-else class="divide-y divide-gray-100 dark:divide-dark-700">
            <div
              v-for="row in rows"
              :key="row.group.id"
              class="grid gap-3 px-6 py-4 md:grid-cols-[minmax(0,220px)_auto_minmax(0,1fr)_auto] md:items-start"
              :data-testid="`otok-pelican-group-${row.group.id}`"
            >
              <div class="min-w-0">
                <div class="truncate font-medium text-gray-900 dark:text-white">{{ row.group.name }}</div>
                <div class="mt-0.5 text-xs text-gray-500">
                  #{{ row.group.id }}
                  <span v-if="row.group.status !== 'active'" class="ml-1 text-amber-600">{{ t('otok.pelicanAdmin.inactive') }}</span>
                  <span v-if="row.group.subscription_type === 'subscription'" class="ml-1 text-amber-600">{{ t('otok.pelicanAdmin.subscription') }}</span>
                </div>
                <div class="mt-1 text-xs text-gray-500">
                  <span :class="groupStatus(row.group.id)?.has_key ? 'text-emerald-600' : 'text-gray-400'">
                    {{ groupStatus(row.group.id)?.has_key ? t('otok.pelicanAdmin.keyReady') : t('otok.pelicanAdmin.keyPending') }}
                  </span>
                  <span v-if="groupStatus(row.group.id)?.last_started_at">
                    · {{ t('otok.pelicanAdmin.lastRun', { time: formatOtokPelicanTime(groupStatus(row.group.id)?.last_started_at) }) }}
                  </span>
                  <span v-if="groupStatus(row.group.id)?.running" class="text-blue-600"> · {{ t('otok.pelicanAdmin.running') }}</span>
                </div>
                <div v-if="groupStatus(row.group.id)?.last_error" class="mt-1 break-all text-xs text-red-600">
                  {{ groupStatus(row.group.id)?.last_error }}
                </div>
              </div>

              <div class="flex items-center gap-2 md:pt-1">
                <Toggle v-model="row.enabled" :data-testid="`otok-pelican-group-toggle-${row.group.id}`" />
                <span class="text-xs text-gray-500">{{ row.enabled ? t('otok.pelicanAdmin.groupOn') : t('otok.pelicanAdmin.groupOff') }}</span>
              </div>

              <div class="min-w-0">
                <input
                  v-model="row.modelsText"
                  type="text"
                  class="input font-mono text-xs"
                  :placeholder="t('otok.pelicanAdmin.groupModelsPlaceholder', { models: defaultModelsText })"
                  autocomplete="off"
                />
                <div v-if="row.candidates.length" class="mt-1.5 flex flex-wrap gap-1">
                  <button
                    v-for="m in row.candidates"
                    :key="m"
                    type="button"
                    class="rounded-md border px-1.5 py-0.5 font-mono text-[11px] transition-colors"
                    :class="
                      rowModels(row).includes(m)
                        ? 'border-primary-300 bg-primary-50 text-primary-700 dark:border-primary-500/50 dark:bg-primary-500/10 dark:text-primary-300'
                        : 'border-gray-200 text-gray-500 hover:bg-gray-50 dark:border-dark-600 dark:text-gray-400 dark:hover:bg-dark-800'
                    "
                    @click="toggleRowModel(row, m)"
                  >
                    {{ m }}
                  </button>
                </div>
              </div>

              <div class="flex gap-2 md:justify-end">
                <button
                  type="button"
                  class="btn btn-secondary btn-sm"
                  :disabled="runningNow.has(row.group.id) || row.group.status !== 'active'"
                  @click="runNow(row)"
                >
                  {{ t('otok.pelicanAdmin.runNow') }}
                </button>
                <button type="button" class="btn btn-ghost btn-sm" @click="viewGroup = row.group">
                  {{ t('otok.pelicanAdmin.viewResults') }}
                </button>
              </div>
            </div>
          </div>
        </div>

        <div class="flex justify-end">
          <button type="submit" class="btn btn-primary" :disabled="saving">
            {{ saving ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </form>
    </div>

    <OtokPelicanDialog
      :show="viewGroup != null"
      :group-id="viewGroup?.id ?? null"
      :group-name="viewGroup?.name ?? ''"
      :source="otokAdminPelicanAPI"
      :interval-minutes="form.interval_minutes"
      :keep-rounds="form.keep_rounds"
      @close="viewGroup = null"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Toggle from '@/components/common/Toggle.vue'
import OtokPelicanDialog from '@/components/otok/monitor/OtokPelicanDialog.vue'
import { adminAPI } from '@/api/admin'
import {
  otokAdminPelicanAPI,
  type OtokPelicanConfig,
  type OtokPelicanGroupConfig,
  type OtokPelicanProbeStatus,
} from '@/api/otok/pelican'
import {
  OTOK_PELICAN_MODEL_RE,
  estimateOtokPelicanDailyRequests,
  formatOtokPelicanTime,
  parseOtokPelicanModels,
} from '@/otok/pelican'
import { useAppStore } from '@/stores/app'
import type { AdminGroup } from '@/types'
import { extractI18nErrorMessage } from '@/utils/apiError'

interface Row {
  group: AdminGroup
  enabled: boolean
  modelsText: string
  candidates: string[]
}

const EFFORTS = ['none', 'minimal', 'low', 'medium', 'high', 'xhigh'] as const

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(true)
const saving = ref(false)
const status = ref<OtokPelicanProbeStatus | null>(null)
const rows = ref<Row[]>([])
const viewGroup = ref<AdminGroup | null>(null)
const runningNow = ref(new Set<number>())
const defaultModelsText = ref('')
// 配置里有、但已不在 OpenAI 分组列表里的分组（被删或改了平台），保存时原样保留并强制关闭。
let orphanGroups: OtokPelicanGroupConfig[] = []

const form = reactive<OtokPelicanConfig>({
  enabled: false,
  interval_minutes: 10,
  reasoning_effort: 'medium',
  prompt: '',
  timeout_seconds: 480,
  concurrency: 2,
  keep_rounds: 10,
  default_models: [],
  groups: [],
})

function rowModels(row: Row): string[] {
  return parseOtokPelicanModels(row.modelsText)
}

function toggleRowModel(row: Row, model: string) {
  const list = rowModels(row)
  const i = list.indexOf(model)
  if (i >= 0) list.splice(i, 1)
  else list.push(model)
  row.modelsText = list.join(', ')
}

function buildConfig(): OtokPelicanConfig {
  return {
    ...form,
    default_models: parseOtokPelicanModels(defaultModelsText.value),
    groups: [
      ...rows.value.map((r) => ({ group_id: r.group.id, enabled: r.enabled, models: rowModels(r) })),
      ...orphanGroups.map((g) => ({ ...g, enabled: false })),
    ],
  }
}

const dailyRequests = computed(() => estimateOtokPelicanDailyRequests(buildConfig()).toLocaleString())

function groupStatus(groupId: number) {
  return status.value?.groups.find((g) => g.group_id === groupId)
}

function applyConfig(cfg: OtokPelicanConfig, groups: AdminGroup[]) {
  Object.assign(form, cfg)
  defaultModelsText.value = cfg.default_models.join(', ')
  const byId = new Map(cfg.groups.map((g) => [g.group_id, g]))
  rows.value = groups.map((g) => {
    const c = byId.get(g.id)
    const allow = g.model_allowlist?.models ?? []
    return { group: g, enabled: c?.enabled ?? false, modelsText: (c?.models ?? []).join(', '), candidates: allow }
  })
  const ids = new Set(groups.map((g) => g.id))
  orphanGroups = cfg.groups.filter((g) => !ids.has(g.group_id))
}

async function loadStatus() {
  try {
    status.value = await otokAdminPelicanAPI.getStatus()
  } catch {
    status.value = null
  }
}

async function loadAll() {
  loading.value = true
  try {
    const [cfg, groups] = await Promise.all([otokAdminPelicanAPI.getConfig(), adminAPI.groups.getAll('openai')])
    applyConfig(cfg, groups)
    await loadStatus()
  } catch (err) {
    appStore.showError(extractI18nErrorMessage(err, t, 'otok.pelicanAdmin.errors', t('otok.pelicanAdmin.loadFailed')))
  } finally {
    loading.value = false
  }
}

function localError(cfg: OtokPelicanConfig): string | null {
  const all = [...cfg.default_models, ...cfg.groups.flatMap((g) => g.models)]
  const bad = all.find((m) => !OTOK_PELICAN_MODEL_RE.test(m))
  if (bad) return t('otok.pelicanAdmin.invalidModel', { model: bad })
  if (cfg.default_models.length === 0) return t('otok.pelicanAdmin.errors.OTOK_PELICAN_NO_MODELS')
  return null
}

async function save() {
  const cfg = buildConfig()
  const msg = localError(cfg)
  if (msg) {
    appStore.showError(msg)
    return
  }
  saving.value = true
  try {
    const saved = await otokAdminPelicanAPI.updateConfig(cfg)
    applyConfig(saved, rows.value.map((r) => r.group))
    appStore.showSuccess(t('otok.pelicanAdmin.saved'))
    void loadStatus()
  } catch (err) {
    appStore.showError(extractI18nErrorMessage(err, t, 'otok.pelicanAdmin.errors', t('common.error')))
  } finally {
    saving.value = false
  }
}

async function runNow(row: Row) {
  runningNow.value.add(row.group.id)
  try {
    await otokAdminPelicanAPI.runNow(row.group.id)
    appStore.showSuccess(t('otok.pelicanAdmin.runNowStarted', { name: row.group.name }))
    void loadStatus()
  } catch (err) {
    appStore.showError(extractI18nErrorMessage(err, t, 'otok.pelicanAdmin.errors', t('common.error')))
  } finally {
    runningNow.value.delete(row.group.id)
  }
}

onMounted(loadAll)
</script>
