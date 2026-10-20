<template>
  <AppLayout>
    <div class="mx-auto max-w-3xl space-y-4">
      <div class="card p-6">
        <div class="mb-6">
          <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('otok.nav.contactSettings') }}</h2>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('otok.contact.admin.description') }}</p>
        </div>

        <div v-if="loading" class="space-y-4">
          <div v-for="i in 4" :key="i" class="h-10 animate-pulse rounded-lg bg-gray-100 dark:bg-dark-700"></div>
        </div>

        <form v-else class="space-y-6" @submit.prevent="save">
          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div>
              <label class="input-label" for="otok-contact-email">{{ t('otok.contact.admin.email') }}</label>
              <input
                id="otok-contact-email"
                v-model="form.email"
                type="email"
                class="input"
                :placeholder="t('otok.contact.admin.emailPlaceholder')"
                autocomplete="off"
              />
            </div>
            <div>
              <label class="input-label" for="otok-contact-qq">{{ t('otok.contact.admin.qqGroup') }}</label>
              <input
                id="otok-contact-qq"
                v-model="form.qq_group"
                type="text"
                inputmode="numeric"
                class="input"
                :placeholder="t('otok.contact.admin.qqGroupPlaceholder')"
                autocomplete="off"
              />
            </div>
          </div>

          <div class="grid grid-cols-1 gap-6 sm:grid-cols-2">
            <div>
              <label class="input-label">{{ t('otok.contact.admin.wechatQr') }}</label>
              <ImageUpload v-model="form.wechat_qr" :max-size="OTOK_CONTACT_MAX_QR_BYTES" :hint="t('otok.contact.admin.qrHint')" />
            </div>
            <div>
              <label class="input-label">{{ t('otok.contact.admin.qqGroupQr') }}</label>
              <ImageUpload v-model="form.qq_group_qr" :max-size="OTOK_CONTACT_MAX_QR_BYTES" :hint="t('otok.contact.admin.qrHint')" />
            </div>
          </div>

          <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('otok.contact.admin.emptyHint') }}</p>

          <div class="flex justify-end gap-3 border-t border-gray-100 pt-4 dark:border-dark-700">
            <button type="button" class="btn btn-secondary" :disabled="!previewSections.any" @click="previewOpen = true">
              {{ t('otok.contact.admin.preview') }}
            </button>
            <button type="submit" class="btn btn-primary" :disabled="saving">
              {{ saving ? t('common.saving') : t('common.save') }}
            </button>
          </div>
        </form>
      </div>
    </div>

    <BaseDialog :show="previewOpen" :title="t('otok.contact.title')" width="normal" close-on-click-outside @close="previewOpen = false">
      <OtokContactPanel
        :email="form.email.trim()"
        :qq-group="form.qq_group.trim()"
        :has-wechat-qr="!!form.wechat_qr"
        :has-qq-group-qr="!!form.qq_group_qr"
        :wechat-qr="form.wechat_qr"
        :qq-group-qr="form.qq_group_qr"
      />
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ImageUpload from '@/components/common/ImageUpload.vue'
import OtokContactPanel from '@/components/otok/contact/OtokContactPanel.vue'
import { useAppStore } from '@/stores/app'
import { otokAdminContactAPI, type OtokContactConfig } from '@/api/otok/contact'
import {
  OTOK_CONTACT_MAX_QR_BYTES,
  isAllowedOtokQrDataUrl,
  isValidOtokContactEmail,
  isValidOtokQQGroup,
  otokContactApplySaved,
  otokContactSections,
} from '@/otok/contact'
import { extractI18nErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(true)
const saving = ref(false)
const previewOpen = ref(false)
const form = reactive<OtokContactConfig>({ email: '', qq_group: '', wechat_qr: '', qq_group_qr: '' })

const previewSections = computed(() =>
  otokContactSections({
    email: form.email.trim(),
    qq_group: form.qq_group.trim(),
    has_wechat_qr: !!form.wechat_qr,
    has_qq_group_qr: !!form.qq_group_qr,
  }),
)

onMounted(async () => {
  try {
    Object.assign(form, (await otokAdminContactAPI.get()).data)
  } catch (err) {
    appStore.showError(extractI18nErrorMessage(err, t, 'otok.contact.errors', t('otok.contact.loadFailed')))
  } finally {
    loading.value = false
  }
})

/** 提交前先在本地拦一遍，规则与后端一致。 */
function localError(): string | null {
  if (!isValidOtokContactEmail(form.email)) return t('otok.contact.errors.OTOK_CONTACT_INVALID_EMAIL')
  if (!isValidOtokQQGroup(form.qq_group)) return t('otok.contact.errors.OTOK_CONTACT_INVALID_QQ_GROUP')
  if (!isAllowedOtokQrDataUrl(form.wechat_qr)) return t('otok.contact.errors.OTOK_CONTACT_INVALID_WECHAT_QR')
  if (!isAllowedOtokQrDataUrl(form.qq_group_qr)) return t('otok.contact.errors.OTOK_CONTACT_INVALID_QQ_GROUP_QR')
  return null
}

async function save() {
  const msg = localError()
  if (msg) {
    appStore.showError(msg)
    return
  }
  saving.value = true
  try {
    const { config, summary } = (await otokAdminContactAPI.update({ ...form })).data
    Object.assign(form, config)
    otokContactApplySaved(summary)
    appStore.showSuccess(t('otok.contact.admin.saved'))
  } catch (err) {
    appStore.showError(extractI18nErrorMessage(err, t, 'otok.contact.errors', t('common.error')))
  } finally {
    saving.value = false
  }
}
</script>
