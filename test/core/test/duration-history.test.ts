import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'pathe'
import { describe, expect, onTestFinished, test } from 'vitest'
import {
  getDurationHistoryKey,
  loadFileDurations,
  readDurationHistory,
} from '../../../packages/vitest/src/node/sequencers/duration-history'
import { smoothDuration } from '../../../packages/vitest/src/node/sequencers/duration-smoothing'

function createRoot(history?: unknown) {
  const root = mkdtempSync(join(tmpdir(), 'vitest-duration-history-'))
  onTestFinished(() => rmSync(root, { recursive: true, force: true }))
  if (history !== undefined) {
    writeFileSync(
      join(root, 'duration-history.json'),
      typeof history === 'string' ? history : JSON.stringify(history),
    )
  }
  return root
}

describe('smoothDuration', () => {
  const observations = [
    { duration: 10, recordedAt: 1 },
    { duration: 45, recordedAt: 2 },
    { duration: 20, recordedAt: 4 },
    { duration: 31, recordedAt: 3 },
  ]

  test.each([
    { strategy: 'latest', expected: 20 },
    { strategy: 'average', expected: 27 },
    { strategy: 'p95', expected: 45 },
    { strategy: 'median', expected: 25 },
  ] as const)('$strategy', ({ strategy, expected }) => {
    expect(smoothDuration(observations, strategy)).toBe(expected)
  })

  test('median of odd number of observations', () => {
    expect(smoothDuration(observations.slice(0, 3), 'median')).toBe(20)
  })

  test('p95 picks the value at ceil(0.95 * n) - 1', () => {
    const many = Array.from({ length: 20 }, (_, index) => ({ duration: 20 - index, recordedAt: index }))
    expect(smoothDuration(many, 'p95')).toBe(19)
  })

  test('returns undefined without observations', () => {
    expect(smoothDuration([], 'latest')).toBeUndefined()
  })
})

describe('duration history', () => {
  test('keys are slash-normalized paths relative to the root', () => {
    expect(getDurationHistoryKey('/root', '/root/test/a.test.ts')).toBe('test/a.test.ts')
    expect(getDurationHistoryKey('C:\\root', 'C:\\root\\test\\a.test.ts')).toBe('test/a.test.ts')
  })

  test('reads single, multi and legacy entries', async () => {
    const root = createRoot({
      'test/single.test.ts': { duration: 1234, recordedAt: 1700000000 },
      'test/multi.test.ts': {
        observations: [
          { duration: 10, recordedAt: 1 },
          { duration: 20, recordedAt: 2 },
        ],
      },
      'test/legacy.test.ts': 5000,
      'test/invalid.test.ts': 'invalid',
    })
    const history = await readDurationHistory({ root, sequence: {} })
    expect(Object.fromEntries(history!)).toEqual({
      'test/single.test.ts': [{ duration: 1234, recordedAt: 1700000000 }],
      'test/multi.test.ts': [
        { duration: 10, recordedAt: 1 },
        { duration: 20, recordedAt: 2 },
      ],
      'test/legacy.test.ts': [{ duration: 5000, recordedAt: 0 }],
    })
  })

  test.each([
    { name: 'missing', content: undefined },
    { name: 'invalid JSON', content: '{ "a.test.ts": ' },
    { name: 'not an object', content: '[1, 2, 3]' },
  ])('returns null if history is $name', async ({ content }) => {
    const root = createRoot(content)
    expect(await readDurationHistory({ root, sequence: {} })).toBeNull()
    expect(await loadFileDurations({ root, sequence: {} })).toBeNull()
  })

  test('reads history from durationHistoryPath', async () => {
    const root = createRoot()
    writeFileSync(join(root, 'custom.json'), JSON.stringify({ 'a.test.ts': 10 }))
    const durations = await loadFileDurations({ root, sequence: { durationHistoryPath: 'custom.json' } })
    expect(Object.fromEntries(durations!)).toEqual({ 'a.test.ts': 10 })
  })

  test('drops expired observations, but never legacy ones', async () => {
    const now = Date.now()
    const root = createRoot({
      'fresh.test.ts': { duration: 10, recordedAt: now - 500 },
      'expired.test.ts': { duration: 20, recordedAt: now - 5000 },
      'legacy.test.ts': 30,
      'mixed.test.ts': {
        observations: [
          { duration: 100, recordedAt: now - 5000 },
          { duration: 40, recordedAt: now - 100 },
          { duration: 60, recordedAt: now - 200 },
        ],
      },
    })
    const durations = await loadFileDurations({
      root,
      sequence: { durationHistoryTTL: 1000, durationSmoothing: 'average' },
    })
    expect(Object.fromEntries(durations!)).toEqual({
      'fresh.test.ts': 10,
      'legacy.test.ts': 30,
      'mixed.test.ts': 50,
    })
  })

  test('uses all non-expired observations for smoothing', async () => {
    const root = createRoot({
      'a.test.ts': {
        observations: [
          { duration: 10, recordedAt: 3 },
          { duration: 30, recordedAt: 1 },
          { duration: 20, recordedAt: 2 },
        ],
      },
    })
    const latest = await loadFileDurations({ root, sequence: { durationHistoryMaxRuns: 1 } })
    const median = await loadFileDurations({ root, sequence: { durationSmoothing: 'median' } })
    expect(latest!.get('a.test.ts')).toBe(10)
    expect(median!.get('a.test.ts')).toBe(20)
  })
})
