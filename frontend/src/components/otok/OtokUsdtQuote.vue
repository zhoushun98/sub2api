<template>
  <div v-if="visible" data-test="otok-usdt-quote" class="card p-4">
    <template v-if="quote && quote.enabled && estimate > 0">
      <p class="text-sm font-semibold text-gray-900 dark:text-white">
        {{ t('otok.usdt.quoteEstimate', { amount: formatUsdt(estimate) }) }}
      </p>
      <p class="mt-1 text-xs leading-relaxed text-gray-500 dark:text-gray-400">
        {{ t('otok.usdt.quoteDetail', { rate: quote.exchange_rate, fee: quote.fee_rate }) }}
      </p>
    </template>
    <p v-else-if="failed" class="text-xs text-gray-500 dark:text-gray-400">{{ t('otok.usdt.quoteUnavailable') }}</p>
  </div>
</template>

<script setup lang="ts">
// otok: 下单前的 USDT 应付预估。报价参数只取一次，金额变化时本地按与后端相同的公式计算。
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { otokUsdtAPI, type OtokUsdtQuote } from '@/api/otok/usdt'
import { formatUsdt, otokEstimateUsdt } from '@/otok/payment'

const props = defineProps<{
  /** 站内计价金额（元），即实付基数，不含上游全局手续费。 */
  amount: number
}>()

const { t } = useI18n()

const quote = ref<OtokUsdtQuote | null>(null)
const failed = ref(false)

const estimate = computed(() => {
  if (!quote.value?.enabled) return 0
  return otokEstimateUsdt(props.amount, Number(quote.value.exchange_rate), Number(quote.value.fee_rate))
})

const visible = computed(() => props.amount > 0 && (failed.value || estimate.value > 0))

onMounted(async () => {
  try {
    quote.value = (await otokUsdtAPI.getQuote()).data
  } catch {
    failed.value = true
  }
})
</script>
