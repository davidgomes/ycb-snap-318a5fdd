import type { TestProject, Vitest } from 'vitest/node'
import { mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'pathe'
import { describe, expect, test, vi } from 'vitest'
import { BaseSequencer } from '../../../packages/vitest/src/node/sequencers/BaseSequencer'
import { readDurationHistory, resolveDurationSequence, writeFileDurations } from '../../../packages/vitest/src/node/sequencers/duration-history'
import { smoothDuration } from '../../../packages/vitest/src/node/sequencers/duration-smoothing'
import { TestSpecification } from '../../../packages/vitest/src/node/test-specification'

function buildCtx(config?: Partial<Vitest['config']> & { logger?: Vitest['logger'] }) {
  const { logger, ...rest } = config ?? {}
  return {
    config: {
      sequence: { groupOrder: 0 },
      ...rest,
    },
    cache: {
      getFileTestResults: vi.fn(),
      getFileStats: vi.fn(),
    },
    logger,
  } as unknown as Vitest
}

function buildWorkspace(root: string) {
  return {
    name: 'test',
    config: {
      root,
      isolate: true,
      sequence: { groupOrder: 0 },
    },
  } as any as TestProject
}

async function withHistory(entries: Record<string, unknown>) {
  const root = join(tmpdir(), `vitest-duration-${Date.now()}-${Math.random().toString(16).slice(2)}`)
  await mkdir(root, { recursive: true })
  await writeFile(join(root, 'duration-history.json'), JSON.stringify(entries))
  return root
}

function specs(root: string, files: string[]) {
  const workspace = buildWorkspace(root)
  return files.map(file => new TestSpecification(workspace, join(root, file), 'forks'))
}

describe('duration smoothing', () => {
  test('latest, average, p95, and median', () => {
    const observations = [
      { duration: 10, recordedAt: 1 },
      { duration: 20, recordedAt: 3 },
      { duration: 30, recordedAt: 2 },
      { duration: 40, recordedAt: 4 },
    ]
    expect(smoothDuration(observations, 'latest')).toBe(40)
    expect(smoothDuration(observations, 'average')).toBe(25)
    expect(smoothDuration(observations, 'median')).toBe(25)
    expect(smoothDuration(observations, 'p95')).toBe(40)
    expect(smoothDuration([
      { duration: 10, recordedAt: 1 },
      { duration: 13, recordedAt: 2 },
    ], 'median')).toBe(11)
  })
})

describe('duration history', () => {
  test('migrates legacy numbers and drops expired observations', async () => {
    const root = await withHistory({
      'test/a.test.ts': 5000,
      'test/b.test.ts': { duration: 20, recordedAt: 0 },
      'test/c.test.ts': {
        observations: [
          { duration: 5, recordedAt: 10 },
          { duration: 9, recordedAt: 5_000 },
        ],
      },
      'test/d.test.ts': { duration: 7, recordedAt: 100 },
    })
    const history = await readDurationHistory(root, { ttl: 1_000, now: 5_500 })
    expect(history).not.toBeNull()
    expect(history!.get('test/a.test.ts')).toEqual([{ duration: 5000, recordedAt: 0 }])
    expect(history!.get('test/b.test.ts')).toEqual([{ duration: 20, recordedAt: 0 }])
    expect(history!.get('test/c.test.ts')).toEqual([{ duration: 9, recordedAt: 5_000 }])
    expect(history!.has('test/d.test.ts')).toBe(false)
    await rm(root, { recursive: true, force: true })
  })

  test('missing and corrupt history are null', async () => {
    const root = join(tmpdir(), `vitest-duration-missing-${Date.now()}`)
    await mkdir(root, { recursive: true })
    expect(await readDurationHistory(root)).toBeNull()
    await writeFile(join(root, 'duration-history.json'), '{')
    expect(await readDurationHistory(root)).toBeNull()
    await rm(root, { recursive: true, force: true })
  })

  test('caps written observations and preserves unrelated files', async () => {
    const root = await withHistory({
      'test/keep.test.ts': 12,
      'test/a.test.ts': {
        observations: [
          { duration: 1, recordedAt: 1 },
          { duration: 2, recordedAt: 2 },
        ],
      },
    })
    await writeFileDurations({
      root,
      historyPath: 'nested/history.json',
      maxRuns: 1,
      now: 50,
      files: [{ filepath: join(root, 'test/a.test.ts'), duration: 8.6 }],
    })
    const single = JSON.parse(await readFile(join(root, 'nested/history.json'), 'utf8'))
    expect(single['test/a.test.ts']).toEqual({ duration: 9, recordedAt: 50 })

    await writeFileDurations({
      root,
      historyPath: 'duration-history.json',
      maxRuns: 1,
      now: 40,
      files: [{ filepath: join(root, 'test/new.test.ts'), duration: 3 }],
    })
    const preserved = JSON.parse(await readFile(join(root, 'duration-history.json'), 'utf8'))
    expect(preserved['test/keep.test.ts']).toBe(12)

    await writeFileDurations({
      root,
      historyPath: 'duration-history.json',
      maxRuns: 2,
      now: 80,
      files: [{ filepath: join(root, 'test/a.test.ts'), duration: 4 }],
    })
    const multi = JSON.parse(await readFile(join(root, 'duration-history.json'), 'utf8'))
    expect(multi['test/keep.test.ts']).toBe(12)
    expect(multi['test/a.test.ts']).toEqual({
      observations: [
        { duration: 2, recordedAt: 2 },
        { duration: 4, recordedAt: 80 },
      ],
    })
    await rm(root, { recursive: true, force: true })
  })
})

describe('sequence duration config', () => {
  test('defaults and balanceShardsByTime', () => {
    const sequence: Record<string, unknown> = {}
    resolveDurationSequence(sequence as any)
    expect(sequence).toMatchObject({
      shardStrategy: 'hash',
      balanceShardsByTime: false,
      recordFileDurations: false,
      durationBasedSorting: false,
      durationHistoryTTL: 0,
      durationHistoryPath: 'duration-history.json',
      durationHistoryMaxRuns: 1,
      durationSmoothing: 'latest',
      shardAffinityRules: [],
      rebalanceThreshold: 0,
      isolateSlowThreshold: 0,
      durationFallbackStrategy: 'hash',
    })

    const balanced: Record<string, unknown> = { balanceShardsByTime: true }
    resolveDurationSequence(balanced as any)
    expect(balanced.shardStrategy).toBe('time')
    expect(balanced.balanceShardsByTime).toBe(true)

    const overridden: Record<string, unknown> = { balanceShardsByTime: true, shardStrategy: 'round-robin' }
    resolveDurationSequence(overridden as any)
    expect(overridden.shardStrategy).toBe('round-robin')
    expect(overridden.balanceShardsByTime).toBe(false)
  })

  test('rejects invalid values', () => {
    expect(() => resolveDurationSequence({ shardStrategy: 'nope' } as any)).toThrow(/shardStrategy/)
    expect(() => resolveDurationSequence({ durationHistoryTTL: -1 } as any)).toThrow(/durationHistoryTTL/)
    expect(() => resolveDurationSequence({ durationHistoryTTL: Number.POSITIVE_INFINITY } as any)).toThrow(/durationHistoryTTL/)
    expect(() => resolveDurationSequence({ durationHistoryPath: ' pad.json' } as any)).toThrow(/durationHistoryPath/)
    expect(() => resolveDurationSequence({ durationHistoryPath: '' } as any)).toThrow(/durationHistoryPath/)
    expect(() => resolveDurationSequence({ durationHistoryMaxRuns: 1.5 } as any)).toThrow(/durationHistoryMaxRuns/)
    expect(() => resolveDurationSequence({ durationHistoryMaxRuns: 0 } as any)).toThrow(/durationHistoryMaxRuns/)
    expect(() => resolveDurationSequence({ durationSmoothing: 'mean' } as any)).toThrow(/durationSmoothing/)
    expect(() => resolveDurationSequence({ rebalanceThreshold: 1.1 } as any)).toThrow(/rebalanceThreshold/)
    expect(() => resolveDurationSequence({ isolateSlowThreshold: -5 } as any)).toThrow(/isolateSlowThreshold/)
    expect(() => resolveDurationSequence({ durationFallbackStrategy: 'time' } as any)).toThrow(/durationFallbackStrategy/)
    expect(() => resolveDurationSequence({ balanceShardsByTime: 'yes' } as any)).toThrow(/balanceShardsByTime/)
    expect(() => resolveDurationSequence({ recordFileDurations: 1 } as any)).toThrow(/recordFileDurations/)
    expect(() => resolveDurationSequence({ durationBasedSorting: 'true' } as any)).toThrow(/durationBasedSorting/)
    expect(() => resolveDurationSequence({
      shardAffinityRules: [{ pattern: '**/*.ts', shardIndex: -1 }],
    } as any)).toThrow(/shardAffinityRules/)
  })
})

describe('duration sharding', () => {
  test('time strategy packs longest files first', async () => {
    const root = await withHistory({
      'a.test.ts': { duration: 10, recordedAt: 1 },
      'b.test.ts': { duration: 8, recordedAt: 1 },
      'c.test.ts': { duration: 6, recordedAt: 1 },
      'd.test.ts': { duration: 4, recordedAt: 1 },
      'e.test.ts': { duration: 2, recordedAt: 1 },
    })
    const files = specs(root, ['a.test.ts', 'b.test.ts', 'c.test.ts', 'd.test.ts', 'e.test.ts'])
    const shards = []
    for (const index of [1, 2, 3]) {
      const sequencer = new BaseSequencer(buildCtx({
        root,
        shard: { index, count: 3 },
        sequence: { shardStrategy: 'time', groupOrder: 0 } as any,
      }))
      shards.push((await sequencer.shard(files)).map(file => file.moduleId.split('/').at(-1)))
    }
    expect(shards).toEqual([
      ['a.test.ts'],
      ['b.test.ts', 'e.test.ts'],
      ['c.test.ts', 'd.test.ts'],
    ])
    await rm(root, { recursive: true, force: true })
  })

  test('falls back to equal-split when history is missing', async () => {
    const root = join(tmpdir(), `vitest-duration-fallback-${Date.now()}`)
    await mkdir(root, { recursive: true })
    const files = specs(root, ['b.test.ts', 'a.test.ts', 'c.test.ts'])
    const sequencer = new BaseSequencer(buildCtx({
      root,
      shard: { index: 1, count: 2 },
      sequence: {
        shardStrategy: 'time',
        durationFallbackStrategy: 'equal-split',
        groupOrder: 0,
      } as any,
    }))
    const shard = (await sequencer.shard(files)).map(file => file.moduleId.split('/').at(-1))
    expect(shard).toEqual(['a.test.ts', 'c.test.ts'])
    await rm(root, { recursive: true, force: true })
  })

  test('round-robin bounces across shards', async () => {
    const root = await withHistory({
      'a.test.ts': { duration: 50, recordedAt: 1 },
      'b.test.ts': { duration: 40, recordedAt: 1 },
      'c.test.ts': { duration: 30, recordedAt: 1 },
      'd.test.ts': { duration: 20, recordedAt: 1 },
      'e.test.ts': { duration: 10, recordedAt: 1 },
    })
    const files = specs(root, ['a.test.ts', 'b.test.ts', 'c.test.ts', 'd.test.ts', 'e.test.ts'])
    const shards = []
    for (const index of [1, 2]) {
      const sequencer = new BaseSequencer(buildCtx({
        root,
        shard: { index, count: 2 },
        sequence: { shardStrategy: 'round-robin', groupOrder: 0 } as any,
      }))
      shards.push((await sequencer.shard(files)).map(file => file.moduleId.split('/').at(-1)))
    }
    expect(shards).toEqual([
      ['a.test.ts', 'd.test.ts', 'e.test.ts'],
      ['b.test.ts', 'c.test.ts'],
    ])
    await rm(root, { recursive: true, force: true })
  })

  test('affinity pins matches and packs the rest by time', async () => {
    const root = await withHistory({
      'slow.test.ts': { duration: 30, recordedAt: 1 },
      'also-slow.test.ts': { duration: 20, recordedAt: 1 },
      'unit/a.test.ts': { duration: 5, recordedAt: 1 },
      'unit/b.test.ts': { duration: 1, recordedAt: 1 },
    })
    const files = specs(root, ['slow.test.ts', 'also-slow.test.ts', 'unit/a.test.ts', 'unit/b.test.ts'])
    const shards = []
    for (const index of [1, 2]) {
      const sequencer = new BaseSequencer(buildCtx({
        root,
        shard: { index, count: 2 },
        sequence: {
          shardStrategy: 'affinity',
          shardAffinityRules: [{ pattern: 'unit/**', shardIndex: 5 }],
          groupOrder: 0,
        } as any,
      }))
      shards.push((await sequencer.shard(files)).map(file => file.moduleId.split('/').at(-1)))
    }
    expect(shards[0]).toEqual(['slow.test.ts'])
    expect(shards[1]).toEqual(['a.test.ts', 'b.test.ts', 'also-slow.test.ts'])
    await rm(root, { recursive: true, force: true })
  })

  test('warns when shard loads fall below the rebalance threshold', async () => {
    const root = await withHistory({
      'a.test.ts': { duration: 100, recordedAt: 1 },
      'b.test.ts': { duration: 1, recordedAt: 1 },
    })
    const files = specs(root, ['a.test.ts', 'b.test.ts'])
    const warn = vi.fn()
    const sequencer = new BaseSequencer(buildCtx({
      root,
      shard: { index: 1, count: 2 },
      logger: { warn } as any,
      sequence: {
        shardStrategy: 'hash',
        rebalanceThreshold: 0.5,
        groupOrder: 0,
      } as any,
    }))
    await sequencer.shard(files)
    expect(warn).toHaveBeenCalled()
    expect(String(warn.mock.calls[0]![0])).toContain('ratio=')
    expect(String(warn.mock.calls[0]![0])).toContain('threshold=0.50')
    await rm(root, { recursive: true, force: true })
  })

  test('sorts by duration with unknown files last', async () => {
    const root = await withHistory({
      'a.test.ts': { duration: 5, recordedAt: 1 },
      'b.test.ts': { duration: 20, recordedAt: 1 },
    })
    const files = specs(root, ['c.test.ts', 'a.test.ts', 'b.test.ts'])
    const sequencer = new BaseSequencer(buildCtx({
      root,
      sequence: { durationBasedSorting: true, groupOrder: 0 } as any,
    }))
    const sorted = (await sequencer.sort(files)).map(file => file.moduleId.split('/').at(-1))
    expect(sorted).toEqual(['b.test.ts', 'a.test.ts', 'c.test.ts'])
    await rm(root, { recursive: true, force: true })
  })
})
