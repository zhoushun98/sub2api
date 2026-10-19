/**
 * otok: 把 fork 文案深合并进上游语言包。上游 index.ts 用对象展开平铺各模块，
 * 直接展开会整块覆盖同名对象（如 payment），所以必须逐层合并。
 */
type Messages = Record<string, unknown>

function isPlainObject(value: unknown): value is Messages {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

export function otokMergeLocale<T extends Messages>(base: T, extra: Messages): T {
  const out: Messages = { ...base }
  for (const [key, value] of Object.entries(extra)) {
    const current = out[key]
    out[key] = isPlainObject(current) && isPlainObject(value) ? otokMergeLocale(current, value) : value
  }
  return out as T
}
