<template>
  <h2 class="channel-signal-platform mb-4 flex items-center gap-3 px-1">
    <span
      class="grid h-11 w-11 shrink-0 place-items-center rounded-2xl text-gray-800 ring-1 ring-black/5 dark:text-gray-100 dark:ring-white/10"
      :class="providerGradient(platform)"
    >
      <ProviderIcon :provider="platform" :size="22" />
    </span>
    <span class="text-xl font-extrabold tracking-tight text-gray-900 dark:text-white">{{ providerLabel(platform) }}</span>
    <span
      data-testid="channel-status-platform-count"
      class="inline-flex h-7 min-w-[1.75rem] items-center justify-center rounded-full border border-gray-200 bg-white px-2 text-sm font-semibold tabular-nums text-gray-600 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300"
    >{{ count }}</span>
  </h2>
</template>

<script setup lang="ts">
import { providerGradient, useChannelMonitorFormat } from '@/composables/useChannelMonitorFormat'
import ProviderIcon from './ProviderIcon.vue'

defineProps<{
  platform: string
  count: number
}>()

const { providerLabel } = useChannelMonitorFormat()
</script>
