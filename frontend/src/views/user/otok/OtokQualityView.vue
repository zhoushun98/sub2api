<template>
  <AppLayout>
    <div class="space-y-6 pb-12">
      <section class="card !rounded-3xl !border-0 p-0 shadow-sm ring-1 ring-gray-900/5 dark:!bg-dark-800 dark:ring-dark-700">
        <header class="flex flex-wrap items-start justify-between gap-4 px-5 py-4 sm:px-6">
          <div class="min-w-0 flex-1">
            <h1 class="flex items-center gap-2 text-xl font-black text-gray-900 dark:text-white">
              <span class="inline-flex h-8 w-8 items-center justify-center rounded-xl bg-violet-50 text-violet-500 dark:bg-violet-900/30 dark:text-violet-400">
                <Icon name="beaker" size="sm" />
              </span>
              {{ t('otok.quality.title') }}
            </h1>
            <p class="mt-1.5 max-w-4xl text-xs leading-relaxed text-gray-500 dark:text-gray-400">
              {{ t('otok.quality.description', { keep: info?.keep_rounds ?? 10, interval: info?.interval_minutes ?? 60 }) }}
            </p>
          </div>
          <button
            class="btn btn-secondary btn-icon flex h-8 w-8 items-center justify-center rounded-lg"
            type="button"
            :title="t('common.refresh')"
            :disabled="loading"
            data-testid="otok-quality-refresh"
            @click="refresh"
          >
            <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
          </button>
        </header>

        <!-- 条目标签：单行横向滚动，名称过长截断 -->
        <div
          v-if="items.length > 0"
          class="flex gap-2 overflow-x-auto border-t border-gray-100 px-4 py-3 dark:border-dark-700 sm:px-5"
          role="tablist"
          data-testid="otok-quality-items"
        >
          <button
            v-for="item in items"
            :key="item.id"
            type="button"
            role="tab"
            class="max-w-[16rem] shrink-0 truncate rounded-lg border px-3 py-1.5 text-sm font-medium transition-colors"
            :class="
              item.id === selectedId
                ? 'border-primary-400 bg-primary-50 text-primary-700 dark:border-primary-500/60 dark:bg-primary-500/10 dark:text-primary-300'
                : 'border-gray-200 text-gray-600 hover:bg-gray-50 dark:border-dark-700 dark:text-gray-300 dark:hover:bg-dark-700'
            "
            :aria-selected="item.id === selectedId"
            :title="item.name"
            @click="selectItem(item.id)"
          >
            {{ item.name }}
          </button>
        </div>
      </section>

      <div v-if="loading && !loaded" class="h-80 animate-pulse rounded-3xl bg-gray-100 dark:bg-dark-800/60"></div>

      <EmptyState
        v-else-if="items.length === 0"
        :title="t('otok.quality.empty')"
        :description="t('otok.quality.emptyHint')"
      />

      <section
        v-else-if="selectedId != null"
        class="card !rounded-3xl !border-0 p-4 shadow-sm ring-1 ring-gray-900/5 dark:!bg-dark-800 dark:ring-dark-700 sm:p-5"
      >
        <OtokQualityPanel ref="panelRef" :item-id="selectedId" :source="otokQualityAPI" />
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import OtokQualityPanel from '@/components/otok/quality/OtokQualityPanel.vue'
import { otokQualityAPI, type OtokQualityItems } from '@/api/otok/quality'
import { otokQualityApplyItems } from '@/otok/quality'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const appStore = useAppStore()

const info = ref<OtokQualityItems | null>(null)
const loading = ref(false)
const loaded = ref(false)
const panelRef = ref<InstanceType<typeof OtokQualityPanel> | null>(null)

const items = computed(() => info.value?.items ?? [])

function queryItemId(): number | null {
  const raw = route.query.item
  const value = Array.isArray(raw) ? raw[0] : raw
  const id = Number(value)
  return Number.isInteger(id) && id > 0 ? id : null
}

/** 地址里的条目不存在（被删或停用）时回落到第一个。 */
const selectedId = computed<number | null>(() => {
  const id = queryItemId()
  if (id != null && items.value.some((i) => i.id === id)) return id
  return items.value[0]?.id ?? null
})

function selectItem(id: number) {
  if (id === queryItemId()) return
  void router.replace({ query: { ...route.query, item: String(id) } })
}

async function loadItems() {
  loading.value = true
  try {
    const data = await otokQualityAPI.getItems()
    info.value = data
    otokQualityApplyItems(data)
  } catch (err) {
    appStore.showError(extractApiErrorMessage(err, t('otok.quality.loadItemsFailed')))
  } finally {
    loading.value = false
    loaded.value = true
  }
}

async function refresh() {
  const before = selectedId.value
  await loadItems()
  // 条目没变时面板不会因 itemId 变化而自动重载，这里手动刷新一次。
  if (before != null && before === selectedId.value) panelRef.value?.reload()
}

// 地址里的条目无效时，把实际显示的条目写回地址，刷新页面后保持一致。
watch(selectedId, (id) => {
  if (id != null && id !== queryItemId()) {
    void router.replace({ query: { ...route.query, item: String(id) } })
  }
})

onMounted(loadItems)
</script>
