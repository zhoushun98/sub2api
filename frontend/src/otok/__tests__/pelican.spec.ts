import { describe, expect, it } from 'vitest'
import type { OtokPelicanConfig, OtokPelicanRound, OtokPelicanRunSummary } from '@/api/otok/pelican'
import {
  buildOtokPelicanPreviewDoc,
  defaultOtokPelicanRun,
  estimateOtokPelicanDailyRequests,
  formatOtokPelicanDuration,
  hasOtokPelicanPending,
  otokPelicanRoundState,
  parseOtokPelicanModels,
} from '../pelican'

function run(overrides: Partial<OtokPelicanRunSummary> = {}): OtokPelicanRunSummary {
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
    error_message: '',
    started_at: '2026-10-20T14:48:30Z',
    finished_at: '2026-10-20T14:52:39Z',
    ...overrides,
  }
}

function round(runs: OtokPelicanRunSummary[], finished = true): OtokPelicanRound {
  return { id: 1, group_id: 127, trigger: 'schedule', started_at: '2026-10-20T14:48:30Z', finished_at: finished ? '2026-10-20T14:52:39Z' : null, runs }
}

describe('otok pelican', () => {
  it('归纳轮次状态', () => {
    expect(otokPelicanRoundState(round([run(), run({ id: 2 })]))).toBe('succeeded')
    expect(otokPelicanRoundState(round([run(), run({ id: 2, status: 'failed' })]))).toBe('partial')
    expect(otokPelicanRoundState(round([run({ status: 'failed' })]))).toBe('failed')
    expect(otokPelicanRoundState(round([run(), run({ id: 2, status: 'running' })], false))).toBe('running')
    expect(otokPelicanRoundState(round([run({ status: 'queued' })], false))).toBe('running')
    expect(otokPelicanRoundState(round([], false))).toBe('running')
    expect(hasOtokPelicanPending([round([run()]), round([run({ status: 'queued' })], false)])).toBe(true)
    expect(hasOtokPelicanPending([round([run()])])).toBe(false)
  })

  it('格式化耗时', () => {
    expect(formatOtokPelicanDuration(248_300)).toBe('248.3s')
    expect(formatOtokPelicanDuration(0)).toBe('0.0s')
    expect(formatOtokPelicanDuration(null)).toBe('—')
    expect(formatOtokPelicanDuration(-1)).toBe('—')
  })

  it('预览文档：完整文档原样返回，片段补骨架', () => {
    const doc = '<!DOCTYPE html><html><body>x</body></html>'
    expect(buildOtokPelicanPreviewDoc(doc)).toBe(doc)
    expect(buildOtokPelicanPreviewDoc('  ')).toBe('')
    expect(buildOtokPelicanPreviewDoc('<svg></svg>')).toBe('<!DOCTYPE html><html><head><meta charset="utf-8"></head><body><svg></svg></body></html>')
  })

  it('默认选中第一个有 HTML 的模型', () => {
    expect(defaultOtokPelicanRun(null)).toBeNull()
    const r = round([run({ id: 1, has_html: false, status: 'failed' }), run({ id: 2 })])
    expect(defaultOtokPelicanRun(r)?.id).toBe(2)
    expect(defaultOtokPelicanRun(round([run({ id: 3, has_html: false })]))?.id).toBe(3)
  })

  it('解析模型输入', () => {
    expect(parseOtokPelicanModels(' gpt-6-astra, gpt-6.1-sol，gpt-6-astra\ngpt-5.5 ')).toEqual(['gpt-6-astra', 'gpt-6.1-sol', 'gpt-5.5'])
    expect(parseOtokPelicanModels('')).toEqual([])
  })

  it('估算每日请求数', () => {
    const cfg: OtokPelicanConfig = {
      enabled: true,
      interval_minutes: 10,
      reasoning_effort: 'medium',
      prompt: '',
      timeout_seconds: 480,
      concurrency: 2,
      keep_rounds: 10,
      default_models: ['gpt-6-astra'],
      groups: [
        { group_id: 127, enabled: true, models: [] },
        { group_id: 273, enabled: true, models: ['a', 'b'] },
        { group_id: 147, enabled: false, models: [] },
      ],
    }
    expect(estimateOtokPelicanDailyRequests(cfg)).toBe(144 * 3)
    expect(estimateOtokPelicanDailyRequests({ ...cfg, enabled: false })).toBe(0)
  })
})
