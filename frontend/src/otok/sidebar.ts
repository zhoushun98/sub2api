/**
 * otok: 给上游 AppSidebar.vue 用的出口，钩子只需一行 import。
 */
import { h } from 'vue'
import Icon from '@/components/icons/Icon.vue'

export { default as OtokContactMenuItem } from '@/components/otok/contact/OtokContactMenuItem.vue'

/** 管理端「联系方式」菜单图标，写法同 AppSidebar 里的内联图标组件。 */
export const OtokContactIcon = {
  render: () => h(Icon, { name: 'chat' }),
}
