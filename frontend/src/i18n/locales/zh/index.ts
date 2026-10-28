import adminAccessKeys from './adminAccessKeys'
import landing from './landing'
import common from './common'
import dashboard from './dashboard'
import channelMonitorV2 from './channelMonitorV2'
import batchImage from './batchImage'
import admin from './admin'
import misc from './misc'
import otok from './otok' // otok
import { otokMergeLocale } from '../../../otok/i18nMerge' // otok

export default otokMergeLocale({ // otok: 深合并 fork 文案，避免浅展开覆盖 payment 等对象
  adminAccessKeys,
  ...landing,
  ...common,
  ...dashboard,
  ...channelMonitorV2,
  ...batchImage,
  admin,
  ...misc,
}, otok) // otok
