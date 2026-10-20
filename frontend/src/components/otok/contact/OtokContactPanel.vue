<template>
  <div class="space-y-4 sm:space-y-6" data-testid="otok-contact-panel">
    <!-- 手机上也左右并排，二维码随卡片宽度缩放，整个弹窗一屏放得下 -->
    <div
      v-if="sections.wechat || sections.qq"
      class="grid gap-3 sm:gap-4"
      :class="sections.wechat && sections.qq ? 'grid-cols-2' : 'mx-auto max-w-xs grid-cols-1'"
    >
      <!-- 微信客服 -->
      <section v-if="sections.wechat" class="otok-contact-card" data-testid="otok-contact-wechat">
        <h3 class="otok-contact-card-title">{{ t('otok.contact.wechatTitle') }}</h3>
        <div class="otok-contact-qr">
          <img v-if="wechatSrc" :src="wechatSrc" :alt="t('otok.contact.wechatTitle')" class="h-full w-full object-contain" />
          <div v-else class="h-full w-full animate-pulse rounded-lg bg-gray-200"></div>
        </div>
        <p class="otok-contact-card-hint">{{ t('otok.contact.wechatHint') }}</p>
      </section>

      <!-- QQ 交流群 -->
      <section v-if="sections.qq" class="otok-contact-card" data-testid="otok-contact-qq">
        <h3 class="otok-contact-card-title">{{ t('otok.contact.qqTitle') }}</h3>
        <div v-if="hasQqGroupQr" class="otok-contact-qr">
          <img v-if="qqSrc" :src="qqSrc" :alt="t('otok.contact.qqTitle')" class="h-full w-full object-contain" />
          <div v-else class="h-full w-full animate-pulse rounded-lg bg-gray-200"></div>
        </div>
        <button
          v-if="qqGroup"
          type="button"
          class="mx-auto inline-flex items-center gap-1 whitespace-nowrap rounded-lg px-1.5 py-1 text-gray-700 sm:gap-1.5 sm:px-2.5 transition-colors hover:bg-gray-200/70 dark:text-gray-200 dark:hover:bg-dark-700"
          :class="hasQqGroupQr ? 'mt-2 text-xs sm:mt-3 sm:text-sm' : 'my-auto text-base sm:text-lg'"
          :title="t('otok.contact.copy')"
          data-testid="otok-contact-qq-group"
          @click="copy(qqGroup, t('otok.contact.groupCopied'))"
        >
          {{ t('otok.contact.qqGroupLabel') }}<span class="font-mono font-semibold tabular-nums">{{ qqGroup }}</span>
          <Icon name="copy" size="sm" class="text-gray-400" />
        </button>
        <p class="otok-contact-card-hint">{{ t(hasQqGroupQr ? 'otok.contact.qqHint' : 'otok.contact.qqSearchHint') }}</p>
      </section>
    </div>

    <p v-if="sections.wechat || sections.qq" class="text-center text-xs text-gray-400 sm:hidden">{{ t('otok.contact.mobileHint') }}</p>

    <!-- 邮件联系 -->
    <div v-if="sections.email" class="text-center" data-testid="otok-contact-email">
      <p class="text-xs font-semibold tracking-[0.2em] text-primary-600 dark:text-primary-400">{{ t('otok.contact.emailTitle') }}</p>
      <div class="mt-1.5 inline-flex max-w-full items-center gap-2">
        <a
          :href="`mailto:${email}`"
          class="truncate text-lg font-semibold text-gray-900 hover:text-primary-600 dark:text-white dark:hover:text-primary-400 sm:text-2xl"
        >{{ email }}</a>
        <button
          type="button"
          class="flex-shrink-0 rounded-md p-1 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-dark-700 dark:hover:text-gray-200"
          :title="t('otok.contact.copy')"
          :aria-label="t('otok.contact.copy')"
          data-testid="otok-contact-email-copy"
          @click="copy(email, t('otok.contact.emailCopied'))"
        >
          <Icon name="copy" size="sm" />
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { useClipboard } from '@/composables/useClipboard'
import { otokContactSections } from '@/otok/contact'
import { sanitizeUrl } from '@/utils/url'

const props = defineProps<{
  email: string
  qqGroup: string
  hasWechatQr: boolean
  hasQqGroupQr: boolean
  /** 二维码 data URL；还没拉到时为空，显示占位。 */
  wechatQr?: string
  qqGroupQr?: string
}>()

const { t } = useI18n()
const { copyToClipboard } = useClipboard()

const sections = computed(() =>
  otokContactSections({
    email: props.email,
    qq_group: props.qqGroup,
    has_wechat_qr: props.hasWechatQr,
    has_qq_group_qr: props.hasQqGroupQr,
  }),
)

const wechatSrc = computed(() => sanitizeUrl(props.wechatQr || '', { allowDataUrl: true }))
const qqSrc = computed(() => sanitizeUrl(props.qqGroupQr || '', { allowDataUrl: true }))

function copy(text: string, message: string) {
  void copyToClipboard(text, message)
}
</script>

<style scoped>
.otok-contact-card {
  @apply flex min-w-0 flex-col rounded-2xl border border-gray-100 bg-gray-50 p-3 text-center dark:border-dark-700 dark:bg-dark-800/80 sm:p-5;
}

.otok-contact-card-title {
  @apply mb-2.5 text-sm font-semibold text-gray-900 dark:text-white sm:mb-4 sm:text-base;
}

/* 二维码固定白底，深色模式下也方便扫码；宽度跟随卡片，桌面最大 11rem */
.otok-contact-qr {
  @apply mx-auto aspect-square w-full max-w-[11rem] rounded-xl bg-white p-1.5 shadow-sm ring-1 ring-black/5 sm:p-2.5;
}

.otok-contact-card-hint {
  @apply mt-auto pt-2.5 text-xs font-medium text-primary-600 dark:text-primary-400 sm:pt-4 sm:text-sm;
}
</style>
