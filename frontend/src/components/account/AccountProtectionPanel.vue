<template>
  <div class="rounded-lg border border-gray-200 p-4 dark:border-dark-600" data-testid="account-protection-panel">
    <div class="flex items-start justify-between gap-3">
      <div class="min-w-0">
        <label class="input-label mb-0">
          {{ t('admin.accounts.antiDegrade') }}
          <span
            v-if="protectionOn"
            data-testid="anti-degrade-status"
            class="ml-2 rounded-full bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-700 dark:bg-dark-600 dark:text-gray-200"
          >{{ statusLabel }}</span>
        </label>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.antiDegradeDesc') }}</p>
        <p v-if="protectionOn && currentMode" class="mt-1 text-xs font-medium text-primary-700 dark:text-primary-300">
          {{ t('admin.accounts.antiDegradeActiveMode') }}：{{ modeLabel(currentMode) }}
        </p>
      </div>
      <button
        type="button"
        role="switch"
        data-testid="anti-degrade-toggle"
        :aria-checked="protectionOn"
        :aria-label="t('admin.accounts.antiDegrade')"
        :disabled="disabled || busy"
        :class="[
          'relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out disabled:opacity-50',
          protectionOn ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'
        ]"
        @click="handleToggle"
      >
        <span
          :class="[
            'pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
            protectionOn ? 'translate-x-5' : 'translate-x-0'
          ]"
        />
      </button>
    </div>

    <div v-if="visibleStrategies.length" class="mt-3 flex flex-wrap items-center gap-2" data-testid="anti-degrade-strategies">
      <button
        v-for="strategy in visibleStrategies"
        v-show="!strategy.diagnostic_only || showDiagnostics || currentMode === strategy.id"
        :key="strategy.id"
        type="button"
        :class="['btn btn-sm', currentMode === strategy.id ? 'btn-primary' : 'btn-secondary']"
        :disabled="disabled || busy"
        :aria-pressed="currentMode === strategy.id"
        :data-testid="`anti-degrade-mode-${strategy.id}`"
        :title="strategy.description"
        @click="openPreview(strategy.id)"
      >
        {{ strategy.name }}<span v-if="strategy.id === DEFAULT_ANTI_DEGRADE_MODE"> · {{ t('admin.accounts.antiDegradeDefaultTag') }}</span>
      </button>
      <button v-if="hasDiagnostics" type="button" class="text-xs text-gray-500 underline" @click="showDiagnostics = !showDiagnostics">
        {{ showDiagnostics ? t('admin.accounts.antiDegradeHideDiagnostics') : t('admin.accounts.antiDegradeShowDiagnostics') }}
      </button>
      <button
        v-if="protectionOn"
        type="button"
        class="btn btn-secondary btn-sm"
        data-testid="anti-degrade-revert"
        :disabled="disabled || busy"
        @click="disableConfirm = true"
      >{{ t('admin.accounts.antiDegradeRevert') }}</button>
    </div>

    <div class="mt-3">
      <label class="input-label">{{ t('admin.accounts.requestIntegrity') }}</label>
      <select
        class="input"
        data-testid="request-integrity-select"
        :value="requestIntegrityMode"
        :disabled="disabled"
        @change="emit('update:requestIntegrityMode', ($event.target as HTMLSelectElement).value)"
      >
        <option value="default">{{ t('admin.accounts.requestIntegrityDefault') }}</option>
        <option value="off">{{ t('admin.accounts.requestIntegrityOff') }}</option>
        <option value="observe">{{ t('admin.accounts.requestIntegrityObserve') }}</option>
        <option value="enforce">{{ t('admin.accounts.requestIntegrityEnforce') }}</option>
      </select>
      <p class="input-hint">{{ t('admin.accounts.requestIntegrityHint') }}</p>
    </div>
    <p class="mt-3 text-xs text-gray-500">{{ t('admin.accounts.antiDegradeImmediateHint') }}</p>

    <ConfirmDialog
      :show="disableConfirm"
      :title="t('admin.accounts.antiDegradeDisableTitle')"
      :message="t('admin.accounts.antiDegradeDisableMessage')"
      :confirm-text="t('admin.accounts.antiDegradeRevert')"
      :cancel-text="t('common.cancel')"
      danger
      @cancel="disableConfirm = false"
      @confirm="confirmRevert"
    />

    <BaseDialog :show="previewDialog" :title="modeLabel(selectedMode)" width="normal" @close="previewDialog = false">
      <div v-if="preview" class="space-y-2">
        <p class="text-sm text-gray-600 dark:text-gray-300">{{ selectedDescription }}</p>
        <div data-testid="anti-degrade-preview-state" class="space-y-1 rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-700">
          <div>{{ t('admin.accounts.antiDegradeConfiguration') }}: {{ statusLabel }}</div>
          <div>{{ t('admin.accounts.antiDegradeActiveMode') }}: {{ modeLabel(preview.active_mode) }}</div>
          <div>{{ t('admin.accounts.antiDegradePolicyVersion') }}: {{ policyVersionLabel(preview) }}</div>
          <div>{{ t('admin.accounts.antiDegradeIdentity') }}: {{ preview.identity_ready === true ? t('admin.accounts.antiDegradeIdentityReady') : t('admin.accounts.antiDegradeIdentityMissing') }}</div>
          <div>{{ t('admin.accounts.antiDegradeTLSProfile') }}: {{ preview.tls_profile || t('admin.accounts.antiDegradeNotConfigured') }}</div>
          <template v-if="preview.runtime">
            <div>{{ t('admin.accounts.antiDegradeEffectiveTLS') }}：{{ preview.runtime.effective_tls }}</div>
            <div>{{ t('admin.accounts.antiDegradeEffectiveConcurrency') }}：{{ preview.runtime.concurrency }}</div>
            <p v-if="preview.runtime.tls_reason" class="text-amber-700 dark:text-amber-300">{{ preview.runtime.tls_reason }}</p>
            <p class="text-xs text-gray-500">{{ preview.runtime.observed ? t('admin.accounts.antiDegradeObserved') : t('admin.accounts.antiDegradeComputed') }}</p>
          </template>
        </div>
        <ul v-if="preview.issues?.length" data-testid="anti-degrade-issues" class="list-inside list-disc text-sm text-amber-700 dark:text-amber-300">
          <li v-for="issue in preview.issues" :key="issue">{{ issue }}</li>
        </ul>
        <p v-if="preview.reason" class="text-sm text-gray-500 dark:text-gray-400">{{ reasonLabel(preview.reason) }}</p>
        <div v-for="chg in visibleChanges" :key="chg.key" class="rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-700">
          <p class="font-medium text-gray-900 dark:text-white">{{ changeLabel(chg.key) }}</p>
          <p class="mt-1 text-gray-900 dark:text-white">
            <span class="text-gray-400">{{ fmtValue(chg.from) }}</span>
            <span class="mx-1">→</span>
            <span class="font-medium">{{ fmtValue(chg.to) }}</span>
          </p>
        </div>
        <p v-if="preview.enabled" class="text-xs text-amber-600 dark:text-amber-400">{{ t('admin.accounts.antiDegradeRevertHint') }}</p>
      </div>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="previewDialog = false">{{ t('common.cancel') }}</button>
          <button
            v-if="preview?.eligible"
            type="button"
            class="btn btn-primary"
            data-testid="anti-degrade-apply"
            :disabled="busy"
            @click="applySelected"
          >{{ t('admin.accounts.antiDegradeApply') }}</button>
        </div>
      </template>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Account } from '@/types'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { AntiDegradeMode, AntiDegradePreview, AntiDegradeStrategyProfile } from '@/api/admin/accounts'
import { DEFAULT_ANTI_DEGRADE_MODE } from '@/utils/accountProtection'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'

const props = defineProps<{
  account: Account | null
  disabled?: boolean
  requestIntegrityMode: string
}>()
const emit = defineEmits<{
  updated: [account: Account]
  'update:requestIntegrityMode': [value: string]
}>()

const { t } = useI18n()
const appStore = useAppStore()

const busy = ref(false)
const previewDialog = ref(false)
const disableConfirm = ref(false)
const preview = ref<AntiDegradePreview | null>(null)
const selectedMode = ref<AntiDegradeMode>(DEFAULT_ANTI_DEGRADE_MODE)
const strategies = ref<AntiDegradeStrategyProfile[]>([])
const showDiagnostics = ref(false)
// 最近一次保护操作返回的账号；父组件的账号快照可能滞后，以它为准展示状态。
const latest = ref<Account | null>(null)
let generation = 0

const fallbackStrategies: AntiDegradeStrategyProfile[] = [
  { id: 'legacy', name: '初代兼容', description: '', category: '常用', identity_mode: 'session', tls_profile: 'nodejs24', max_concurrency: 16, risk: '中', apply_supported: true },
  { id: 'mode1', name: '兼容架构 v3', description: '', category: '常用', identity_mode: 'device', tls_profile: 'standard', max_concurrency: 16, risk: '中', apply_supported: true },
  { id: 'minimal_compat', name: '最小兼容', description: '', category: '常用', identity_mode: 'device', tls_profile: 'standard', max_concurrency: 8, risk: '低', apply_supported: true },
  { id: 'session_standard', name: '会话兼容', description: '', category: '诊断', identity_mode: 'session', tls_profile: 'standard', max_concurrency: 8, risk: '中', apply_supported: true, diagnostic_only: true },
  { id: 'tls_node24', name: 'Node.js 24 对照', description: '', category: '诊断', identity_mode: 'device', tls_profile: 'nodejs24', max_concurrency: 8, risk: '中', apply_supported: true, diagnostic_only: true },
  { id: 'low_concurrency', name: '低并发稳定', description: '', category: '诊断', identity_mode: 'session', tls_profile: 'standard', max_concurrency: 4, risk: '低', apply_supported: true, diagnostic_only: true },
  { id: 'mode2', name: '完整收敛', description: '', category: '诊断', identity_mode: 'full', tls_profile: 'nodejs22', max_concurrency: 8, risk: '高', apply_supported: true, diagnostic_only: true }
]

const extraOf = (value: unknown): Record<string, unknown> =>
  value && typeof value === 'object' ? (value as Record<string, unknown>) : {}
const current = computed<Account | null>(() => {
  if (!props.account) return null
  return latest.value && latest.value.id === props.account.id ? latest.value : props.account
})
const knownStrategies = computed(() => (strategies.value.length ? strategies.value : fallbackStrategies))
const visibleStrategies = computed(() => {
  const a = current.value
  if (!a || !['oauth', 'setup-token'].includes(a.type) || a.parent_account_id) return []
  return knownStrategies.value
    .filter(s => s.id !== 'native_baseline' && s.apply_supported)
    .filter(s => (s.id === 'legacy' ? ['openai', 'anthropic'].includes(a.platform) : a.platform === 'openai'))
    .sort((x, y) => Number(y.id === DEFAULT_ANTI_DEGRADE_MODE) - Number(x.id === DEFAULT_ANTI_DEGRADE_MODE))
})
const hasDiagnostics = computed(() => visibleStrategies.value.some(s => s.diagnostic_only))
const protectionOn = computed(() => {
  const a = current.value
  const marker = extraOf(a?.extra).anti_degrade as Record<string, unknown> | undefined
  return a?.anti_degradation === true || marker?.enabled === true
})
const currentMode = computed<AntiDegradeMode | ''>(() => {
  const a = current.value
  const known = (id: unknown): id is AntiDegradeMode => typeof id === 'string' && knownStrategies.value.some(s => s.id === id)
  if (known(a?.protection_mode)) return a!.protection_mode as AntiDegradeMode
  const marker = extraOf(a?.extra).anti_degrade as Record<string, unknown> | undefined
  if (known(marker?.mode)) return marker!.mode as AntiDegradeMode
  if (protectionOn.value && a?.protection_scope === 'legacy') return 'legacy'
  if (protectionOn.value && a?.protection_scope === 'codex_v3') return 'mode1'
  return ''
})
const modeLabel = (mode?: string) => {
  if (mode === 'mode1') return t('admin.accounts.antiDegradeMode1')
  if (mode === 'mode2') return t('admin.accounts.antiDegradeMode2')
  if (mode === 'legacy') return t('admin.accounts.antiDegradeModeLegacy')
  return knownStrategies.value.find(s => s.id === mode)?.name || t('admin.accounts.antiDegradeNotConfigured')
}
const selectedDescription = computed(() => {
  if (selectedMode.value === 'mode1') return t('admin.accounts.antiDegradeMode1Desc')
  if (selectedMode.value === 'mode2') return t('admin.accounts.antiDegradeMode2Desc')
  if (selectedMode.value === 'legacy') return t('admin.accounts.antiDegradeModeLegacyDesc')
  const s = knownStrategies.value.find(item => item.id === selectedMode.value)
  return s?.description || s?.name || ''
})
const statusLabel = computed(() => {
  const p = preview.value
  if (!p) {
    if (protectionOn.value) {
      const mode = currentMode.value
      return mode ? `${t('admin.accounts.antiDegradeEnabled')} · ${modeLabel(mode)}` : t('admin.accounts.antiDegradeEnabled')
    }
    return current.value?.protection_scope === 'generic_v1' ? t('admin.accounts.antiDegradeGenericScope') : t('admin.accounts.antiDegradeNotConfigured')
  }
  if (p.issues?.length) return t('admin.accounts.antiDegradeConfigurationIssue')
  if (!p.enabled) return t('admin.accounts.antiDegradeNotConfigured')
  if (!p.active_mode || p.identity_ready == null || p.tls_profile == null) return t('admin.accounts.antiDegradeUnverified')
  if (p.active_mode === 'mode1' && (![2, 3].includes(p.policy_version ?? 0) || !p.identity_ready || !p.tls_profile)) {
    return t('admin.accounts.antiDegradeConfigurationIssue')
  }
  return t('admin.accounts.antiDegradeEnabled')
})
const visibleChanges = computed(() => (preview.value?.changes || []).filter(c => !/seed|identity_secret/i.test(c.key)))
const fmtValue = (v: unknown): string => {
  if (v === undefined || v === null || v === '') return '-'
  if (typeof v === 'boolean') return v ? t('common.enabled') : t('common.disabled')
  if (v === 'off') return t('admin.accounts.openai.codexFingerprintOff')
  if (v === 'device') return t('admin.accounts.openai.codexFingerprintDevice')
  if (v === 'session') return t('admin.accounts.openai.codexFingerprintSession')
  if (v === 'full') return t('admin.accounts.openai.codexFingerprintFull')
  return String(v)
}
const reasonLabel = (reason?: string): string => {
  if (reason === 'account not found') return t('admin.accounts.antiDegradeAccountNotFound')
  if (reason === 'already enabled, can revert') return t('admin.accounts.antiDegradeAlreadyEnabled')
  if (reason === 'platform has no fingerprint convergence; only generic items apply') return t('admin.accounts.antiDegradeGenericOnly')
  if (!reason || reason === 'nothing to change') return t('admin.accounts.antiDegradeNoChange')
  return reason
}
const changeLabel = (key: string): string => {
  if (key === 'strategy') return t('admin.accounts.antiDegradeChangeStrategy')
  if (key === 'extra.codex_fingerprint_mode') return t('admin.accounts.antiDegradeChangeFingerprint')
  if (key === 'extra.enable_tls_fingerprint' || key === 'extra.tls_fingerprint_builtin') return t('admin.accounts.antiDegradeChangeTLS')
  if (key === 'transport') return t('admin.accounts.antiDegradeTLSProfile')
  if (key === 'policy_version') return t('admin.accounts.antiDegradePolicyVersion')
  if (key === 'concurrency') return t('admin.accounts.antiDegradeChangeConcurrency')
  return key
}
const policyVersionLabel = (p: AntiDegradePreview | null): string => {
  if (!p) return '-'
  if (p.active_mode === 'legacy' || selectedMode.value === 'legacy') return t('admin.accounts.antiDegradeLegacyVersion')
  return p.policy_version == null || p.policy_version === 0 ? '-' : String(p.policy_version)
}

const sync = (updated: Account) => {
  latest.value = updated
  preview.value = null
  previewDialog.value = false
  emit('updated', updated)
}
const fail = (error: unknown) => {
  appStore.showError((error as { message?: string })?.message || t('admin.accounts.antiDegradeFailed'))
}
const run = async (task: (id: number) => Promise<void>) => {
  if (!props.account || busy.value || props.disabled) return
  const id = props.account.id
  const gen = ++generation
  busy.value = true
  try {
    await task(id)
  } catch (error) {
    if (gen === generation) fail(error)
  } finally {
    if (gen === generation) busy.value = false
  }
}
const openPreview = (mode: AntiDegradeMode) => run(async id => {
  selectedMode.value = mode
  preview.value = await adminAPI.accounts.previewAntiDegrade(id, mode)
  previewDialog.value = true
})
const applySelected = () => run(async id => {
  sync(await adminAPI.accounts.applyAntiDegrade(id, selectedMode.value))
  if (protectionOn.value) appStore.showSuccess(t('admin.accounts.antiDegradeApplied'))
  else appStore.showInfo(t('admin.accounts.antiDegradeNotConfigured'))
})
const handleToggle = () => {
  if (protectionOn.value) {
    disableConfirm.value = true
    return
  }
  void run(async id => {
    sync(await adminAPI.accounts.setProtection(id, true))
  })
}
const confirmRevert = () => {
  disableConfirm.value = false
  void run(async id => {
    sync(await adminAPI.accounts.revertAntiDegrade(id, true))
    if (!protectionOn.value) appStore.showSuccess(t('admin.accounts.antiDegradeReverted'))
    else appStore.showInfo(t('admin.accounts.antiDegradeUnverified'))
  })
}

watch(
  () => props.account?.id,
  async (id, previousID) => {
    generation++
    busy.value = false
    disableConfirm.value = false
    previewDialog.value = false
    preview.value = null
    selectedMode.value = DEFAULT_ANTI_DEGRADE_MODE
    if (id !== previousID) latest.value = null
    if (id && strategies.value.length === 0) {
      try {
        strategies.value = await adminAPI.accounts.listAntiDegradeStrategies()
      } catch {
        // 旧后端没有策略接口时使用内置回退列表。
      }
    }
  },
  { immediate: true }
)
</script>
