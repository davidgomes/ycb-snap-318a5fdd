import type { TestSpecification, Vitest } from 'vitest/node'
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'pathe'
import { afterEach, describe, expect, test } from 'vitest'
import { BaseSequencer } from '../../../packages/vitest/src/node/sequencers/BaseSequencer'
import { readDurationHistory, readFileDurations, writeDurationHistory } from '../../../packages/vitest/src/node/sequencers/duration-history'
import { smoothDuration } from '../../../packages/vitest/src/node/sequencers/duration-smoothing'

const roots: string[] = []

afterEach(() => {
  roots.splice(0).forEach(root => rmSync(root, { recursive: true, force: true }))
})

function createRoot(history?: unknown) {
  const root = mkdtempSync(join(tmpdir(), 'vitest-duration-'))
  roots.push(root)
  if (history !== undefined) {
    writeFileSync(join(root, 'duration-history.json'), typeof history === 'string' ? history : JSON.stringify(history))
  }
  return root
}

function createSequencer(root: string, shard: { index: number; count: number }, sequence: Record<string, unknown>) {
  const warnings: string[] = []
  const ctx = {
    config: {
      root,
      shard,
      sequence: {
        groupOrder: 0,
        shardStrategy: 'hash',
        durationHistoryPath: 'duration-history.json',
        durationHistoryTTL: 0,
        durationSmoothing: 'latest',
        durationFallbackStrategy: 'hash',
        shardAffinityRules: [],
        rebalanceThreshold: 0,
        isolateSlowThreshold: 0,
        ...sequence,
      },
    },
    logger: {
      warn: (message: string) => warnings.push(message),
    },
  } as unknown as Vitest
  return { sequencer: new BaseSequencer(ctx), warnings }
}

function specs(root: string, names: string[]) {
  return names.map(name => ({ moduleId: join(root, name) } as TestSpecification))
}

async function shardAll(root: string, names: string[], count: number, sequence: Record<string, unknown>) {
  const result: string[][] = []
  const warnings: string[] = []
  for (let index = 1; index <= count; index++) {
    const { sequencer, warnings: shardWarnings } = createSequencer(root, { index, count }, sequence)
    const files = await sequencer.shard(specs(root, names))
    result.push(files.map(file => file.moduleId.slice(root.length + 1)).sort())
    warnings.push(...shardWarnings)
  }
  return { shards: result, warnings }
}

const files = ['a.test.ts', 'b.test.ts', 'c.test.ts', 'd.test.ts', 'e.test.ts', 'f.test.ts']
const history = {
  'a.test.ts': { duration: 1000, recordedAt: 1 },
  'b.test.ts': { duration: 900, recordedAt: 1 },
  'c.test.ts': { duration: 300, recordedAt: 1 },
  'd.test.ts': { duration: 200, recordedAt: 1 },
  'e.test.ts': { duration: 100, recordedAt: 1 },
}

describe('duration smoothing', () => {
  const observations = [10, 40, 20, 30].map((duration, i) => ({ duration, recordedAt: [4, 1, 3, 2][i] }))

  test.each([
    ['latest', 10],
    ['average', 25],
    ['p95', 40],
    ['median', 25],
  ] as const)('%s', (strategy, expected) => {
    expect(smoothDuration(observations, strategy)).toBe(expected)
  })

  test('median of odd count and floor of even count', () => {
    expect(smoothDuration([{ duration: 3, recordedAt: 0 }, { duration: 1, recordedAt: 0 }, { duration: 2, recordedAt: 0 }], 'median')).toBe(2)
    expect(smoothDuration([{ duration: 1, recordedAt: 0 }, { duration: 2, recordedAt: 0 }], 'median')).toBe(1)
  })

  test('average is rounded', () => {
    expect(smoothDuration([{ duration: 1, recordedAt: 0 }, { duration: 2, recordedAt: 0 }], 'average')).toBe(2)
  })

  test('empty observations', () => {
    expect(smoothDuration([], 'latest')).toBeUndefined()
  })
})

describe('duration history', () => {
  test('returns null for missing and corrupt files', async () => {
    expect(await readDurationHistory(join(createRoot(), 'duration-history.json'), 0)).toBeNull()
    expect(await readDurationHistory(join(createRoot('{ not json'), 'duration-history.json'), 0)).toBeNull()
    expect(await readDurationHistory(join(createRoot('[]'), 'duration-history.json'), 0)).toBeNull()
  })

  test('reads single, multi and legacy formats', async () => {
    const root = createRoot({
      'single.test.ts': { duration: 10, recordedAt: 5 },
      'multi.test.ts': { observations: [{ duration: 1, recordedAt: 1 }, { duration: 2, recordedAt: 2 }] },
      'legacy.test.ts': 5000,
    })
    const history = await readDurationHistory(join(root, 'duration-history.json'), 0)
    expect(Object.fromEntries(history!)).toEqual({
      'single.test.ts': [{ duration: 10, recordedAt: 5 }],
      'multi.test.ts': [{ duration: 1, recordedAt: 1 }, { duration: 2, recordedAt: 2 }],
      'legacy.test.ts': [{ duration: 5000, recordedAt: 0 }],
    })
  })

  test('drops expired observations, but keeps observations without timestamp', async () => {
    const now = Date.now()
    const root = createRoot({
      'expired.test.ts': { duration: 10, recordedAt: now - 10_000 },
      'fresh.test.ts': { observations: [{ duration: 1, recordedAt: now - 10_000 }, { duration: 2, recordedAt: now }] },
      'legacy.test.ts': 5000,
    })
    const durations = await readFileDurations(join(root, 'duration-history.json'), { ttl: 1000, smoothing: 'average' })
    expect(Object.fromEntries(durations!)).toEqual({
      'fresh.test.ts': 2,
      'legacy.test.ts': 5000,
    })
  })

  test('writes single entries and preserves other files', async () => {
    const root = createRoot({ 'other.test.ts': 5000, 'a.test.ts': { duration: 1, recordedAt: 1 } })
    const filepath = join(root, 'duration-history.json')
    await writeDurationHistory(filepath, new Map([['a.test.ts', 20]]), 1, 100)
    expect(JSON.parse(readFileSync(filepath, 'utf-8'))).toEqual({
      'other.test.ts': 5000,
      'a.test.ts': { duration: 20, recordedAt: 100 },
    })
  })

  test('caps observations with maxRuns', async () => {
    const root = createRoot({
      'a.test.ts': { observations: [{ duration: 1, recordedAt: 1 }, { duration: 2, recordedAt: 2 }] },
      'b.test.ts': 7,
    })
    const filepath = join(root, 'duration-history.json')
    await writeDurationHistory(filepath, new Map([['a.test.ts', 3], ['b.test.ts', 8]]), 2, 100)
    expect(JSON.parse(readFileSync(filepath, 'utf-8'))).toEqual({
      'a.test.ts': { observations: [{ duration: 2, recordedAt: 2 }, { duration: 3, recordedAt: 100 }] },
      'b.test.ts': { observations: [{ duration: 7, recordedAt: 0 }, { duration: 8, recordedAt: 100 }] },
    })
  })

  test('creates parent directories', async () => {
    const root = createRoot()
    const filepath = join(root, 'nested/dir/history.json')
    await writeDurationHistory(filepath, new Map([['a.test.ts', 1]]), 1, 100)
    expect(JSON.parse(readFileSync(filepath, 'utf-8'))).toEqual({ 'a.test.ts': { duration: 1, recordedAt: 100 } })
  })
})

describe('duration based sharding', () => {
  test('time strategy uses longest processing time first', async () => {
    const root = createRoot(history)
    const { shards } = await shardAll(root, files, 3, { shardStrategy: 'time' })
    expect(shards).toEqual([
      ['a.test.ts'],
      ['b.test.ts'],
      ['c.test.ts', 'd.test.ts', 'e.test.ts', 'f.test.ts'],
    ])
  })

  test('round-robin strategy bounces between shards', async () => {
    const root = createRoot(history)
    const { shards } = await shardAll(root, files, 3, { shardStrategy: 'round-robin' })
    expect(shards).toEqual([
      ['a.test.ts', 'f.test.ts'],
      ['b.test.ts', 'e.test.ts'],
      ['c.test.ts', 'd.test.ts'],
    ])
  })

  test('affinity strategy pins matched files and balances the rest', async () => {
    const root = createRoot(history)
    const { shards } = await shardAll(root, files, 3, {
      shardStrategy: 'affinity',
      shardAffinityRules: [
        { pattern: '**/e.test.ts', shardIndex: 0 },
        { pattern: 'f.test.ts', shardIndex: 10 },
        { pattern: 'e.test.ts', shardIndex: 1 },
      ],
    })
    expect(shards).toEqual([
      ['c.test.ts', 'd.test.ts', 'e.test.ts'],
      ['a.test.ts'],
      ['b.test.ts', 'f.test.ts'],
    ])
  })

  test('affinity strategy without matches behaves like time strategy', async () => {
    const root = createRoot(history)
    const affinity = await shardAll(root, files, 3, {
      shardStrategy: 'affinity',
      shardAffinityRules: [{ pattern: 'unknown/**', shardIndex: 0 }],
    })
    const time = await shardAll(root, files, 3, { shardStrategy: 'time' })
    expect(affinity.shards).toEqual(time.shards)
  })

  test('equal-split fallback when history is missing', async () => {
    const root = createRoot()
    const { shards } = await shardAll(root, files, 2, { shardStrategy: 'time', durationFallbackStrategy: 'equal-split' })
    expect(shards).toEqual([
      ['a.test.ts', 'c.test.ts', 'e.test.ts'],
      ['b.test.ts', 'd.test.ts', 'f.test.ts'],
    ])
  })

  test('hash fallback when history is corrupt', async () => {
    const root = createRoot('not json')
    const fallback = await shardAll(root, files, 2, { shardStrategy: 'time' })
    const hash = await shardAll(root, files, 2, { shardStrategy: 'hash' })
    expect(fallback.shards).toEqual(hash.shards)
  })

  test('isolates slow files into their own shards', async () => {
    const root = createRoot(history)
    const { shards } = await shardAll(root, files, 3, { shardStrategy: 'time', isolateSlowThreshold: 500 })
    expect(shards).toEqual([
      ['a.test.ts'],
      ['b.test.ts'],
      ['c.test.ts', 'd.test.ts', 'e.test.ts', 'f.test.ts'],
    ])
  })

  test('last shard gets extra slow files and the remaining files', async () => {
    const root = createRoot(history)
    const { shards } = await shardAll(root, files, 2, { shardStrategy: 'round-robin', isolateSlowThreshold: 250 })
    expect(shards).toEqual([
      ['a.test.ts'],
      ['b.test.ts', 'c.test.ts', 'd.test.ts', 'e.test.ts', 'f.test.ts'],
    ])
  })

  test('warns when shards are not balanced', async () => {
    const root = createRoot(history)
    const { warnings } = await shardAll(root, files, 3, { shardStrategy: 'round-robin', rebalanceThreshold: 0.9 })
    expect(warnings).toHaveLength(3)
    expect(warnings[0]).toMatch(/ratio=0\.50 .*threshold=0\.90/)
  })

  test('does not warn when shards are balanced enough', async () => {
    const root = createRoot(history)
    const { warnings } = await shardAll(root, files, 3, { shardStrategy: 'round-robin', rebalanceThreshold: 0.4 })
    expect(warnings).toEqual([])
  })
})

describe('duration based sorting', () => {
  test('sorts by duration and puts unknown files last', async () => {
    const root = createRoot(history)
    const project = { name: '', config: { sequence: { groupOrder: 0 } } }
    const ctx = {
      config: { root, sequence: { durationBasedSorting: true } },
      cache: { getFileTestResults: () => undefined, getFileStats: () => undefined },
    } as unknown as Vitest
    const input = ['f.test.ts', 'c.test.ts', 'a.test.ts', 'e.test.ts']
      .map(name => ({ project, moduleId: join(root, name) } as unknown as TestSpecification))
    const sorted = await new BaseSequencer(ctx).sort(input)
    expect(sorted.map(spec => spec.moduleId.slice(root.length + 1))).toEqual([
      'a.test.ts',
      'c.test.ts',
      'e.test.ts',
      'f.test.ts',
    ])
  })
})
