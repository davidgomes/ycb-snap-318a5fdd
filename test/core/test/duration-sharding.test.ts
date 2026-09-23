import type { TestProject, Vitest } from 'vitest/node'
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'pathe'
import { afterEach, describe, expect, test } from 'vitest'
import { BaseSequencer } from '../../../packages/vitest/src/node/sequencers/BaseSequencer'
import {
  loadFileDurations,
  parseDurationHistory,
  writeDurationHistory,
} from '../../../packages/vitest/src/node/sequencers/duration-history'
import { smoothDuration } from '../../../packages/vitest/src/node/sequencers/duration-smoothing'
import { TestSpecification } from '../../../packages/vitest/src/node/test-specification'

const roots: string[] = []

afterEach(() => {
  roots.splice(0).forEach(root => rmSync(root, { recursive: true, force: true }))
})

function createRoot(history?: unknown) {
  const root = mkdtempSync(join(tmpdir(), 'vitest-duration-'))
  roots.push(root)
  if (history !== undefined) {
    writeFileSync(
      join(root, 'duration-history.json'),
      typeof history === 'string' ? history : JSON.stringify(history),
    )
  }
  return root
}

function createSequencer(root: string, index: number, count: number, sequence: Record<string, unknown> = {}) {
  const warnings: string[] = []
  const ctx = {
    config: {
      root,
      shard: { index, count },
      sequence: { groupOrder: 0, ...sequence },
    },
    logger: { warn: (message: string) => warnings.push(message) },
    cache: {
      getFileTestResults: () => undefined,
      getFileStats: () => undefined,
    },
  } as unknown as Vitest
  return { sequencer: new BaseSequencer(ctx), warnings }
}

function specs(root: string, files: string[]) {
  const project = {
    name: 'test',
    config: { root, sequence: { groupOrder: 0 } },
  } as any as TestProject
  return files.map(file => new TestSpecification(project, join(root, file), 'forks'))
}

async function allShards(root: string, files: string[], count: number, sequence: Record<string, unknown> = {}) {
  const result: string[][] = []
  const warnings: string[] = []
  for (let index = 1; index <= count; index++) {
    const { sequencer, warnings: shardWarnings } = createSequencer(root, index, count, sequence)
    const shard = await sequencer.shard(specs(root, files))
    result.push(shard.map(spec => spec.moduleId.slice(root.length + 1)))
    warnings.push(...shardWarnings)
  }
  return { shards: result, warnings }
}

const durations = {
  'a.test.ts': { duration: 100, recordedAt: 1 },
  'b.test.ts': { duration: 90, recordedAt: 1 },
  'c.test.ts': { duration: 50, recordedAt: 1 },
  'd.test.ts': { duration: 40, recordedAt: 1 },
  'e.test.ts': { duration: 20, recordedAt: 1 },
  'f.test.ts': { duration: 10, recordedAt: 1 },
}
const files = Object.keys(durations)

describe('duration history', () => {
  test('parses single, multi and legacy entries', () => {
    expect(parseDurationHistory({
      'single.ts': { duration: 10, recordedAt: 5 },
      'multi.ts': { observations: [{ duration: 1, recordedAt: 1 }, { duration: 2, recordedAt: 2 }] },
      'legacy.ts': 5000,
    })).toEqual({
      'single.ts': [{ duration: 10, recordedAt: 5 }],
      'multi.ts': [{ duration: 1, recordedAt: 1 }, { duration: 2, recordedAt: 2 }],
      'legacy.ts': [{ duration: 5000, recordedAt: 0 }],
    })
  })

  test('returns null for missing or corrupt history', () => {
    expect(loadFileDurations(createRoot(), {})).toBeNull()
    expect(loadFileDurations(createRoot('{not json'), {})).toBeNull()
    expect(loadFileDurations(createRoot('[1, 2]'), {})).toBeNull()
  })

  test('drops expired observations but keeps recordedAt 0', () => {
    const now = 10_000
    const root = createRoot({
      'expired.ts': { duration: 10, recordedAt: 1000 },
      'fresh.ts': { observations: [{ duration: 10, recordedAt: 1000 }, { duration: 20, recordedAt: 9500 }] },
      'legacy.ts': 30,
    })
    const result = loadFileDurations(root, { durationHistoryTTL: 1000, durationSmoothing: 'average' }, now)
    expect(Object.fromEntries(result!)).toEqual({ 'fresh.ts': 20, 'legacy.ts': 30 })
  })

  test('writes capped observations and preserves other entries', () => {
    const root = createRoot({
      'other.ts': 5000,
      'a.ts': { observations: [
        { duration: 1, recordedAt: 1 },
        { duration: 2, recordedAt: 2 },
        { duration: 3, recordedAt: 3 },
      ] },
    })
    const path = join(root, 'duration-history.json')
    writeDurationHistory(path, new Map([['a.ts', 4.6], ['nested/b.ts', 7.2]]), 2, 10)
    expect(JSON.parse(readFileSync(path, 'utf-8'))).toEqual({
      'other.ts': 5000,
      'a.ts': { observations: [{ duration: 5, recordedAt: 10 }, { duration: 3, recordedAt: 3 }] },
      'nested/b.ts': { observations: [{ duration: 7, recordedAt: 10 }] },
    })

    writeDurationHistory(path, new Map([['a.ts', 8]]), 1, 20)
    expect(JSON.parse(readFileSync(path, 'utf-8'))['a.ts']).toEqual({ duration: 8, recordedAt: 20 })
  })

  test('writes to nested directories', () => {
    const root = createRoot()
    const path = join(root, 'nested/dir/history.json')
    writeDurationHistory(path, new Map([['a.ts', 1]]), 1, 1)
    expect(JSON.parse(readFileSync(path, 'utf-8'))).toEqual({ 'a.ts': { duration: 1, recordedAt: 1 } })
  })
})

describe('duration smoothing', () => {
  const observations = [
    { duration: 40, recordedAt: 1 },
    { duration: 10, recordedAt: 3 },
    { duration: 25, recordedAt: 2 },
    { duration: 100, recordedAt: 0 },
  ]

  test.each([
    ['latest', 10],
    ['average', 44],
    ['p95', 100],
    ['median', 32],
  ] as const)('%s', (strategy, expected) => {
    expect(smoothDuration(observations, strategy)).toBe(expected)
  })

  test('median with odd count', () => {
    expect(smoothDuration(observations.slice(0, 3), 'median')).toBe(25)
  })
})

describe('duration-aware sharding', () => {
  test('time strategy uses LPT bin packing', async () => {
    const root = createRoot(durations)
    const { shards } = await allShards(root, files, 2, { shardStrategy: 'time' })
    expect(shards).toEqual([
      ['a.test.ts', 'd.test.ts', 'e.test.ts'],
      ['b.test.ts', 'c.test.ts', 'f.test.ts'],
    ])
  })

  test('round-robin strategy bounces between shards', async () => {
    const root = createRoot(durations)
    const { shards } = await allShards(root, files, 3, { shardStrategy: 'round-robin' })
    expect(shards).toEqual([
      ['a.test.ts', 'f.test.ts'],
      ['b.test.ts', 'e.test.ts'],
      ['c.test.ts', 'd.test.ts'],
    ])
  })

  test('affinity strategy pins files and packs the rest', async () => {
    const root = createRoot(durations)
    const { shards } = await allShards(root, files, 2, {
      shardStrategy: 'affinity',
      shardAffinityRules: [
        { pattern: '{a,b}.test.ts', shardIndex: 5 },
        { pattern: 'b.test.ts', shardIndex: 0 },
      ],
    })
    expect(shards).toEqual([
      ['c.test.ts', 'd.test.ts', 'e.test.ts', 'f.test.ts'],
      ['a.test.ts', 'b.test.ts'],
    ])
  })

  test('affinity strategy falls back to time if nothing matches', async () => {
    const root = createRoot(durations)
    const { shards } = await allShards(root, files, 2, {
      shardStrategy: 'affinity',
      shardAffinityRules: [{ pattern: 'unknown/**', shardIndex: 1 }],
    })
    expect(shards).toEqual([
      ['a.test.ts', 'd.test.ts', 'e.test.ts'],
      ['b.test.ts', 'c.test.ts', 'f.test.ts'],
    ])
  })

  test('equal-split fallback without history', async () => {
    const root = createRoot()
    const { shards } = await allShards(root, ['c.ts', 'a.ts', 'b.ts', 'd.ts', 'e.ts'], 2, {
      shardStrategy: 'time',
      durationFallbackStrategy: 'equal-split',
    })
    expect(shards).toEqual([
      ['a.ts', 'c.ts', 'e.ts'],
      ['b.ts', 'd.ts'],
    ])
  })

  test('hash fallback without history matches hash strategy', async () => {
    const root = createRoot()
    const hashed = await allShards(root, files, 3)
    const fallback = await allShards(root, files, 3, { shardStrategy: 'round-robin' })
    expect(fallback.shards).toEqual(hashed.shards)
  })

  test('isolates slow files into their own shards', async () => {
    const root = createRoot(durations)
    const { shards } = await allShards(root, files, 4, { shardStrategy: 'time', isolateSlowThreshold: 60 })
    expect(shards).toEqual([
      ['a.test.ts'],
      ['b.test.ts'],
      ['c.test.ts', 'f.test.ts'],
      ['d.test.ts', 'e.test.ts'],
    ])
  })

  test('last shard receives extra slow files and the rest', async () => {
    const root = createRoot(durations)
    const { shards } = await allShards(root, files, 2, { shardStrategy: 'time', isolateSlowThreshold: 30 })
    expect(shards).toEqual([
      ['a.test.ts'],
      ['b.test.ts', 'c.test.ts', 'd.test.ts', 'e.test.ts', 'f.test.ts'],
    ])
  })

  test('warns when shards are unbalanced', async () => {
    const root = createRoot(durations)
    const { warnings } = await allShards(root, files, 2, {
      shardStrategy: 'time',
      isolateSlowThreshold: 30,
      rebalanceThreshold: 0.5,
    })
    expect(warnings[0]).toMatchInlineSnapshot(`"Shards are unbalanced: ratio=0.48 is below threshold=0.50 (min load 100ms, max load 210ms). Consider recording file durations or adjusting the shard strategy."`)
  })

  test('sorts files by duration', async () => {
    const root = createRoot({ 'b.ts': 10, 'c.ts': 30 })
    const { sequencer } = createSequencer(root, 1, 1, { durationBasedSorting: true })
    const sorted = await sequencer.sort(specs(root, ['a.ts', 'b.ts', 'c.ts']))
    expect(sorted.map(spec => spec.moduleId.slice(root.length + 1))).toEqual(['c.ts', 'b.ts', 'a.ts'])
  })
})
