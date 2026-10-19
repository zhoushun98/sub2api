<template>
  <ChannelStatusV1View v-if="isV1" />
  <OtokChannelStatusCardsView v-else /><!-- otok: V2 模式改用卡片页 -->
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { isChannelMonitorV1Mode } from '@/utils/featureFlags'
import ChannelStatusV1View from './ChannelStatusV1View.vue'
import OtokChannelStatusCardsView from './otok/OtokChannelStatusCardsView.vue' // otok

const isV1 = computed(() => isChannelMonitorV1Mode())
</script>
