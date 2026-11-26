import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { OtokQualityItems, OtokQualityRound, OtokQualityRunSummary } from '@/api/otok/quality'

const getItems = vi.hoisted(() => vi.fn())

vi.mock('@/api/otok/quality', () => ({
  otokQualityAPI: { getItems },
}))

import {
  OTOK_QUALITY_SAMPLE_PROMPT,
  __resetOtokQualityItems,
  defaultOtokQualityRun,
  detectOtokQualityProtocol,
  ensureOtokQualityItems,
  estimateOtokQualityDailyRequests,
  formatOtokQualityDuration,
  hasOtokQualityPending,
  isValidOtokQualityExtraBody,
  otokQualityApplyItems,
  otokQualityErrorCodeKey,
  otokQualityItems,
  otokQualityMenuFlag,
  otokQualityRoundState,
  parseOtokQualityModels,
  refreshOtokQualityItems,
} from '../quality'

function run(overrides: Partial<OtokQualityRunSummary> = {}): OtokQualityRunSummary {
  return {
    id: 1,
    model: 'gpt-6-astra',
    status: 'succeeded',
    attempt: 1,
    duration_ms: 248_300,
    first_token_ms: 90_000,
    output_tokens: 3000,
    reasoning_tokens: 1800,
    has_html: true,
    error_code: '',
    started_at: '2026-10-20T14:48:30Z',
    finished_at: '2026-10-20T14:52:39Z',
    ...overrides,
  }
}

function round(runs: OtokQualityRunSummary[], finished = true): OtokQualityRound {
  return {
    id: 1,
    item_id: 3,
    trigger: 'schedule',
    prompt: 'p',
    started_at: '2026-10-20T14:48:30Z',
    finished_at: finished ? '2026-10-20T14:52:39Z' : null,
    runs,
  }
}

function items(n: number): OtokQualityItems {
  return {
    items: Array.from({ length: n }, (_, i) => ({ id: i + 1, name: `条目 ${i + 1}`, models: ['m'] })),
    interval_minutes: 60,
    keep_rounds: 10,
  }
}

describe('otok quality 纯逻辑', () => {
  it('归纳轮次状态', () => {
    expect(otokQualityRoundState(round([run(), run({ id: 2 })]))).toBe('succeeded')
    expect(otokQualityRoundState(round([run(), run({ id: 2, status: 'failed' })]))).toBe('partial')
    expect(otokQualityRoundState(round([run({ status: 'failed' })]))).toBe('failed')
    expect(otokQualityRoundState(round([run(), run({ id: 2, status: 'running' })], false))).toBe('running')
    expect(otokQualityRoundState(round([run({ status: 'queued' })], false))).toBe('running')
    expect(otokQualityRoundState(round([], false))).toBe('running')
    expect(otokQualityRoundState(round([], true))).toBe('failed')
    expect(hasOtokQualityPending([round([run()]), round([run({ status: 'queued' })], false)])).toBe(true)
    expect(hasOtokQualityPending([round([run()])])).toBe(false)
  })

  it('格式化耗时', () => {
    expect(formatOtokQualityDuration(248_300)).toBe('248.3s')
    expect(formatOtokQualityDuration(0)).toBe('0.0s')
    expect(formatOtokQualityDuration(null)).toBe('—')
    expect(formatOtokQualityDuration(-1)).toBe('—')
  })

  it('默认选中第一个有 HTML 的模型', () => {
    expect(defaultOtokQualityRun(null)).toBeNull()
    expect(defaultOtokQualityRun(round([run({ id: 1, has_html: false, status: 'failed' }), run({ id: 2 })]))?.id).toBe(2)
    expect(defaultOtokQualityRun(round([run({ id: 3, has_html: false })]))?.id).toBe(3)
  })

  it('解析模型输入', () => {
    expect(parseOtokQualityModels(' gpt-6-astra, gpt-6.1-sol，gpt-6-astra\ngpt-5.5 ')).toEqual(['gpt-6-astra', 'gpt-6.1-sol', 'gpt-5.5'])
    expect(parseOtokQualityModels('')).toEqual([])
  })

  it('按 URL 路径识别协议', () => {
    expect(detectOtokQualityProtocol('http://127.0.0.1:8080/v1/responses')).toBe('responses')
    expect(detectOtokQualityProtocol('https://api.example.com/v1/responses/')).toBe('responses')
    expect(detectOtokQualityProtocol('https://api.example.com/v1/chat/completions?x=1')).toBe('chat')
    expect(detectOtokQualityProtocol('http://127.0.0.1:8080/v1/messages')).toBe('messages')
    expect(detectOtokQualityProtocol('http://127.0.0.1:8080/v1/messages/count_tokens')).toBeNull()
    expect(detectOtokQualityProtocol('https://api.example.com/v1')).toBeNull()
    expect(detectOtokQualityProtocol('ftp://api.example.com/v1/responses')).toBeNull()
    expect(detectOtokQualityProtocol('https://u:p@api.example.com/v1/responses')).toBeNull()
    expect(detectOtokQualityProtocol('https://api.example.com/v1/responses#x')).toBeNull()
    expect(detectOtokQualityProtocol('/v1/responses')).toBeNull()
    expect(detectOtokQualityProtocol('')).toBeNull()
  })

  it('估算每日请求数：启用条目的模型数 × 每天轮数', () => {
    const cfg = {
      enabled: true,
      interval_minutes: 10,
      items: [
        { enabled: true, models: ['a'] },
        { enabled: true, models: ['a', 'b'] },
        { enabled: false, models: ['a', 'b', 'c'] },
      ],
    }
    expect(estimateOtokQualityDailyRequests(cfg)).toBe(144 * 3)
    expect(estimateOtokQualityDailyRequests({ ...cfg, interval_minutes: 60 })).toBe(24 * 3)
    expect(estimateOtokQualityDailyRequests({ ...cfg, enabled: false })).toBe(0)
  })

  it('附加请求参数只接受 JSON 对象', () => {
    expect(isValidOtokQualityExtraBody('')).toBe(true)
    expect(isValidOtokQualityExtraBody('  ')).toBe(true)
    expect(isValidOtokQualityExtraBody('{"max_tokens": 1}')).toBe(true)
    expect(isValidOtokQualityExtraBody('[1]')).toBe(false)
    expect(isValidOtokQualityExtraBody('null')).toBe(false)
    expect(isValidOtokQualityExtraBody('{bad')).toBe(false)
  })

  it('错误类型映射到文案 key', () => {
    expect(otokQualityErrorCodeKey('timeout')).toBe('timeout')
    expect(otokQualityErrorCodeKey('key_error')).toBe('key_error')
    expect(otokQualityErrorCodeKey('http_3xx')).toBe('http_3xx')
    expect(otokQualityErrorCodeKey('unexpected_response')).toBe('unexpected_response')
    expect(otokQualityErrorCodeKey('http_502')).toBe('http_5xx')
    expect(otokQualityErrorCodeKey('http_401')).toBe('http_4xx')
    expect(otokQualityErrorCodeKey('provision_failed')).toBe('unknown')
    expect(otokQualityErrorCodeKey('')).toBe('unknown')
  })

  it('示例提示词要求只用内联 SVG 和 CSS', () => {
    expect(OTOK_QUALITY_SAMPLE_PROMPT).toContain('内联 SVG')
    expect(OTOK_QUALITY_SAMPLE_PROMPT.length).toBeLessThanOrEqual(8000)
  })
})

describe('otok quality 菜单显隐', () => {
  beforeEach(() => {
    __resetOtokQualityItems()
    getItems.mockReset()
  })

  it('首次取值只发一次请求并返回 false，有条目后为 true', async () => {
    let resolve!: (v: OtokQualityItems) => void
    getItems.mockReturnValue(new Promise<OtokQualityItems>((r) => (resolve = r)))

    expect(otokQualityMenuFlag()).toBe(false)
    expect(otokQualityMenuFlag()).toBe(false)
    expect(getItems).toHaveBeenCalledTimes(1)

    resolve(items(2))
    await ensureOtokQualityItems()
    expect(otokQualityMenuFlag()).toBe(true)
    expect(getItems).toHaveBeenCalledTimes(1)
  })

  it('返回空列表后再取值不重发请求，仍为 false', async () => {
    getItems.mockResolvedValue(items(0))
    expect(otokQualityMenuFlag()).toBe(false)
    await ensureOtokQualityItems()
    expect(otokQualityItems.value?.items).toEqual([])
    expect(otokQualityMenuFlag()).toBe(false)
    expect(otokQualityMenuFlag()).toBe(false)
    expect(getItems).toHaveBeenCalledTimes(1)
  })

  it('请求失败不写缓存，下次取值重新请求', async () => {
    getItems.mockRejectedValueOnce(new Error('boom'))
    expect(otokQualityMenuFlag()).toBe(false)
    await ensureOtokQualityItems()
    expect(otokQualityItems.value).toBeNull()
    expect(getItems).toHaveBeenCalledTimes(1)

    getItems.mockResolvedValueOnce(items(1))
    expect(otokQualityMenuFlag()).toBe(false)
    expect(getItems).toHaveBeenCalledTimes(2)
    await ensureOtokQualityItems()
    expect(otokQualityMenuFlag()).toBe(true)
  })

  it('写回和强制刷新都会更新菜单', async () => {
    otokQualityApplyItems(items(1))
    expect(otokQualityMenuFlag()).toBe(true)
    expect(getItems).not.toHaveBeenCalled()

    getItems.mockResolvedValue(items(0))
    await refreshOtokQualityItems()
    expect(getItems).toHaveBeenCalledTimes(1)
    expect(otokQualityMenuFlag()).toBe(false)
  })
})
