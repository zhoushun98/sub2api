<template>
  <button
    type="button"
    class="inline-flex items-center gap-1 rounded-full px-2 py-1 text-xs font-medium transition-colors disabled:opacity-50"
    :class="enabled ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300' : 'bg-amber-50 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'"
    :title="scopeDescription"
    :disabled="busy || !authStore.isAdmin"
    :aria-label="enabled ? t('admin.accounts.protectionToggleOff') : t('admin.accounts.protectionToggleOn')"
    @click.stop="toggle"
  >
    <Icon :name="enabled ? 'shield' : 'exclamationTriangle'" size="sm" />
    {{ account.protection_scope === 'generic_v1' ? t('admin.accounts.protectionGenericLabel') : t('admin.accounts.protectionLabel') }}
    {{ enabled ? t('admin.accounts.protectionOn') : t('admin.accounts.protectionOff') }}
  </button>
  <ConfirmDialog
    :show="confirming"
    :title="t('admin.accounts.antiDegradeDisableTitle')"
    :message="t('admin.accounts.protectionToggleConfirmMessage')"
    :confirm-text="t('admin.accounts.protectionToggleConfirm')"
    :cancel-text="t('common.cancel')"
    danger
    @cancel="confirming = false"
    @confirm="save(false, true)"
  />
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Account, AccountListItem } from '@/types'
import { setProtection } from '@/api/admin/accounts'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import Icon from '@/components/icons/Icon.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'

const props = defineProps<{ account: AccountListItem }>()
const emit = defineEmits<{ updated: [account: Account] }>()
const { t } = useI18n()
const authStore = useAuthStore()
const appStore = useAppStore()
const busy = ref(false)
const confirming = ref(false)
const enabled = computed(() =>
  props.account.anti_degradation ?? (props.account.extra?.anti_degrade as { enabled?: boolean } | undefined)?.enabled === true
)
const scopeDescription = computed(() => {
  if (!enabled.value) return t('admin.accounts.protectionScopeDisabled')
  if (props.account.protection_scope === 'generic_v1') return t('admin.accounts.protectionScopeGeneric')
  if (props.account.protection_scope === 'codex_v3') return t('admin.accounts.protectionScopeCodex')
  return t('admin.accounts.protectionScopeSaved')
})
function toggle() {
  if (enabled.value) confirming.value = true
  else void save(true)
}
async function save(value: boolean, confirmDisable = false) {
  if (busy.value || !authStore.isAdmin) return
  const id = props.account.id
  busy.value = true
  confirming.value = false
  try {
    const updated = await setProtection(id, value, confirmDisable)
    emit('updated', updated)
    appStore.showSuccess(value ? t('admin.accounts.protectionEnabledToast') : t('admin.accounts.protectionDisabledToast'))
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.accounts.protectionUpdateFailed'))
  } finally {
    busy.value = false
  }
}
</script>
