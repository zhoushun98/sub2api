<template>
  <template v-if="summary?.enabled">
    <button
      type="button"
      class="sidebar-link mb-1 w-full"
      :class="{ 'sidebar-link-collapsed': collapsed }"
      :title="collapsed ? label : undefined"
      data-testid="otok-contact-menu"
      @click="open"
    >
      <Icon name="chat" class="h-5 w-5 flex-shrink-0" />
      <span class="sidebar-label" :class="{ 'sidebar-label-collapsed': collapsed }" :aria-hidden="collapsed ? 'true' : 'false'">{{ label }}</span>
    </button>

    <BaseDialog :show="show" :title="t('otok.contact.title')" width="normal" close-on-click-outside @close="show = false">
      <OtokContactPanel
        :email="summary.email"
        :qq-group="summary.qq_group"
        :has-wechat-qr="summary.has_wechat_qr"
        :has-qq-group-qr="summary.has_qq_group_qr"
        :wechat-qr="qrcodes?.wechat_qr"
        :qq-group-qr="qrcodes?.qq_group_qr"
      />
    </BaseDialog>
  </template>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import {
  ensureOtokContactQRCodes,
  ensureOtokContactSummary,
  otokContactQRCodes,
  otokContactSummary,
} from '@/otok/contact'
import OtokContactPanel from './OtokContactPanel.vue'

const { t } = useI18n()
const appStore = useAppStore()

const show = ref(false)
const summary = otokContactSummary
const qrcodes = otokContactQRCodes
const collapsed = computed(() => appStore.sidebarCollapsed)
const label = computed(() => t('otok.nav.contactUs'))

onMounted(() => {
  void ensureOtokContactSummary()
})

function open() {
  // 移动端先收起抽屉；弹窗挂在 body 上，不受侧边栏影响
  if (appStore.mobileOpen) appStore.setMobileOpen(false)
  show.value = true
  if (summary.value?.has_wechat_qr || summary.value?.has_qq_group_qr) {
    void ensureOtokContactQRCodes()
  }
}
</script>
