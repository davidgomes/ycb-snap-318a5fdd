import type { TestProject, Vitest } from 'vitest/node'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'pathe'
import { resolveConfig as viteResolveConfig } from 'vite'
import { describe, expect, test, vi } from 'vitest'
import { resolveConfig } from '../../../packages/vitest/src/node/config/resolveConfig'
import { BaseSequencer } from '../../../packages/vitest/src/node/sequencers/BaseSequencer'
import {
  readDurationHistory,
  recordFileDurations,
  toDurationHistoryKey,
} from '../../../packages/vitest/src/node/sequencers/duration-history'
import { smoothDuration } from '../../../packages/vitest/src/node/sequencers/duration-smoothing'
import { TestSpecification } from '../../../packages/vitest/src/node/test-specification'

// @ts-expect-error test-only global used by resolveConfig
globalThis.__VITEST_GENERATE_UI_TOKEN__ = true

async function withRoot(run: (root: string) => Promise<void>) {
  const root = await mkdtemp(join(tmpdir(), 'vitest-duration-'))
  try {
    await run(root)
  }
  finally {
    await rm(root, { recursive: true, force: true })
  }
}

function buildCtx(root: string, index: number, count: number, sequence: Record<string, unknown> = {}) {
  const warn = vi.fn()
  const ctx = {
    config: {
      root,
      shard: { index, count },
      sequence: {
        groupOrder: 0,
        ...sequence,
      },
    },
    logger: { warn },
    cache: {
      getFileTestResults: vi.fn(),
      getFileStats: vi.fn(),
    },
  } as unknown as Vitest
  return { ctx, warn }
}

function specs(root: string, files: string[]) {
  const project = {
    name: 'test',
    config: {
      root,
      isolate: true,
      sequence: { groupOrder: 0 },
    },
  } as TestProject
  return files.map(file => new TestSpecification(project, join(root, file), 'forks'))
}

async function shardAll(root: string, files: string[], count: number, sequence: Record<string, unknown> = {}) {
  const modules = specs(root, files)
  const shards: string[][] = []
  let warn: ReturnType<typeof vi.fn> | undefined
  for (let index = 1; index <= count; index++) {
    const built = buildCtx(root, index, count, sequence)
    warn = built.warn
    const sequencer = new BaseSequencer(built.ctx)
    const shard = await sequencer.shard(modules)
    shards.push(shard.map(spec => toDurationHistoryKey(root, spec.moduleId)))
  }
  return { shards, warn: warn! }
}

describe('duration history', () => {
  test('returns null when the file is missing or corrupt', async () => {
    await withRoot(async (root) => {
      const missing = join(root, 'duration-history.json')
      expect(await readDurationHistory(missing)).toBeNull()
      await writeFile(missing, '{', 'utf8')
      expect(await readDurationHistory(missing)).toBeNull()
      await writeFile(missing, '[]', 'utf8')
      expect(await readDurationHistory(missing)).toBeNull()
      await writeFile(missing, JSON.stringify({ 'test/a.ts': { duration: 'slow' } }), 'utf8')
      expect(await readDurationHistory(missing)).toBeNull()
    })
  })

  test('reads legacy, single, and multi entries and applies ttl', async () => {
    await withRoot(async (root) => {
      const file = join(root, 'history.json')
      const now = 10_000
      await writeFile(file, JSON.stringify({
        'test/legacy.ts': 5000,
        'test/single.ts': { duration: 12, recordedAt: 9500 },
        'test/multi.ts': {
          observations: [
            { duration: 1, recordedAt: 0 },
            { duration: 2, recordedAt: 8999 },
            { duration: 3, recordedAt: 9000 },
            { duration: 4, recordedAt: 9500 },
          ],
        },
        'test/expired.ts': { duration: 9, recordedAt: 1000 },
      }), 'utf8')

      const history = await readDurationHistory(file, 1000, now)
      expect(history).not.toBeNull()
      expect(history!.get('test/legacy.ts')).toEqual([{ duration: 5000, recordedAt: 0 }])
      expect(history!.get('test/single.ts')).toEqual([{ duration: 12, recordedAt: 9500 }])
      expect(history!.get('test/multi.ts')).toEqual([
        { duration: 1, recordedAt: 0 },
        { duration: 3, recordedAt: 9000 },
        { duration: 4, recordedAt: 9500 },
      ])
      expect(history!.has('test/expired.ts')).toBe(false)
    })
  })

  test('records rounded durations, caps runs, and preserves other files', async () => {
    await withRoot(async (root) => {
      const historyPath = 'nested/duration-history.json'
      await mkdir(join(root, 'nested'), { recursive: true })
      await writeFile(join(root, historyPath), JSON.stringify({
        'test/keep.ts': 40,
        'test/update.ts': {
          observations: [
            { duration: 10, recordedAt: 1 },
            { duration: 20, recordedAt: 2 },
            { duration: 30, recordedAt: 3 },
          ],
        },
      }), 'utf8')

      await recordFileDurations({
        config: {
          root,
          sequence: {
            recordFileDurations: true,
            durationHistoryPath: historyPath,
            durationHistoryMaxRuns: 2,
          },
        },
        state: {
          getFiles: () => [
            {
              filepath: join(root, 'test/update.ts'),
              result: { state: 'pass', duration: 10.6 },
            },
            {
              filepath: join(root, 'test/new.ts'),
              result: { state: 'pass', duration: 4.2 },
            },
            {
              filepath: join(root, 'test/skipped.ts'),
              local: true,
              result: { state: 'pass', duration: 99 },
            },
          ] as any,
        },
      }, 50)

      const written = JSON.parse(await readFile(join(root, historyPath), 'utf8'))
      expect(written['test/keep.ts']).toBe(40)
      expect(written['test/update.ts']).toEqual({
        observations: [
          { duration: 30, recordedAt: 3 },
          { duration: 11, recordedAt: 50 },
        ],
      })
      expect(written['test/new.ts']).toEqual({
        observations: [{ duration: 4, recordedAt: 50 }],
      })
      expect(written['test/skipped.ts']).toBeUndefined()

      await recordFileDurations({
        config: {
          root,
          sequence: {
            recordFileDurations: true,
            durationHistoryPath: historyPath,
            durationHistoryMaxRuns: 1,
          },
        },
        state: {
          getFiles: () => [
            {
              filepath: join(root, 'test/new.ts'),
              result: { state: 'pass', duration: 8 },
            },
          ] as any,
        },
      }, 80)

      const single = JSON.parse(await readFile(join(root, historyPath), 'utf8'))
      expect(single['test/new.ts']).toEqual({ duration: 8, recordedAt: 80 })
      expect(single['test/keep.ts']).toBe(40)
    })
  })
})

describe('duration smoothing', () => {
  const observations = [
    { duration: 10, recordedAt: 1 },
    { duration: 40, recordedAt: 4 },
    { duration: 20, recordedAt: 2 },
    { duration: 30, recordedAt: 3 },
  ]

  test('latest, average, p95, and median', () => {
    expect(smoothDuration(observations, 'latest')).toBe(40)
    expect(smoothDuration(observations, 'average')).toBe(25)
    expect(smoothDuration(observations, 'p95')).toBe(40)
    expect(smoothDuration(observations, 'median')).toBe(25)
    expect(smoothDuration([
      { duration: 10, recordedAt: 1 },
      { duration: 11, recordedAt: 2 },
    ], 'median')).toBe(10)
    expect(smoothDuration([
      { duration: 5, recordedAt: 1 },
    ], 'p95')).toBe(5)
    expect(smoothDuration([], 'latest')).toBe(0)
  })
})

describe('duration-aware sharding', () => {
  test('keeps the hash distribution when history is missing', async () => {
    await withRoot(async (root) => {
      const files = ['a.test.ts', 'b.test.ts', 'c.test.ts', 'd.test.ts']
      const hashed = await shardAll(root, files, 3, { shardStrategy: 'hash' })
      const timed = await shardAll(root, files, 3, {
        shardStrategy: 'time',
        durationFallbackStrategy: 'hash',
      })
      expect(timed.shards).toEqual(hashed.shards)
      expect(hashed.shards.flat().sort()).toEqual(files.sort())
    })
  })

  test('equal-split fallback sorts by path and stripes shards', async () => {
    await withRoot(async (root) => {
      const { shards } = await shardAll(root, ['c.test.ts', 'a.test.ts', 'b.test.ts'], 2, {
        shardStrategy: 'time',
        durationFallbackStrategy: 'equal-split',
      })
      expect(shards).toEqual([
        ['a.test.ts', 'c.test.ts'],
        ['b.test.ts'],
      ])
    })
  })

  test('time strategy packs the longest files onto the lightest shard', async () => {
    await withRoot(async (root) => {
      await writeFile(join(root, 'duration-history.json'), JSON.stringify({
        'a.test.ts': { duration: 10, recordedAt: 1 },
        'b.test.ts': { duration: 8, recordedAt: 1 },
        'c.test.ts': { duration: 6, recordedAt: 1 },
        'd.test.ts': { duration: 4, recordedAt: 1 },
        'e.test.ts': { duration: 2, recordedAt: 1 },
      }), 'utf8')
      const { shards } = await shardAll(root, [
        'e.test.ts',
        'a.test.ts',
        'd.test.ts',
        'c.test.ts',
        'b.test.ts',
      ], 3, { shardStrategy: 'time' })
      expect(shards).toEqual([
        ['a.test.ts'],
        ['b.test.ts', 'e.test.ts'],
        ['c.test.ts', 'd.test.ts'],
      ])
    })
  })

  test('empty history is not a missing history', async () => {
    await withRoot(async (root) => {
      await writeFile(join(root, 'duration-history.json'), '{}\n', 'utf8')
      const { shards } = await shardAll(root, ['b.test.ts', 'a.test.ts'], 2, {
        shardStrategy: 'time',
      })
      expect(shards).toEqual([
        ['b.test.ts', 'a.test.ts'],
        [],
      ])
    })
  })

  test('round-robin bounces at the boundary shards', async () => {
    await withRoot(async (root) => {
      await writeFile(join(root, 'duration-history.json'), JSON.stringify({
        'a.test.ts': 7,
        'b.test.ts': 6,
        'c.test.ts': 5,
        'd.test.ts': 4,
        'e.test.ts': 3,
        'f.test.ts': 2,
        'g.test.ts': 1,
      }), 'utf8')
      const { shards } = await shardAll(root, [
        'g.test.ts',
        'a.test.ts',
        'c.test.ts',
        'b.test.ts',
        'e.test.ts',
        'd.test.ts',
        'f.test.ts',
      ], 3, { shardStrategy: 'round-robin' })
      expect(shards).toEqual([
        ['a.test.ts', 'f.test.ts', 'g.test.ts'],
        ['b.test.ts', 'e.test.ts'],
        ['c.test.ts', 'd.test.ts'],
      ])
    })
  })

  test('affinity pins the first glob match and packs the rest by time', async () => {
    await withRoot(async (root) => {
      await writeFile(join(root, 'duration-history.json'), JSON.stringify({
        'pinned/a.test.ts': 100,
        'b.test.ts': 10,
        'c.test.ts': 10,
        'other/d.test.ts': 4,
      }), 'utf8')
      const { shards } = await shardAll(root, [
        'b.test.ts',
        'pinned/a.test.ts',
        'other/d.test.ts',
        'c.test.ts',
      ], 2, {
        shardStrategy: 'affinity',
        shardAffinityRules: [
          { pattern: 'pinned/**', shardIndex: 0 },
          { pattern: '**/*', shardIndex: 5 },
        ],
      })
      expect(shards[0]).toEqual(['pinned/a.test.ts'])
      expect(shards[1]).toEqual(['b.test.ts', 'other/d.test.ts', 'c.test.ts'])
    })
  })

  test('affinity with no matches falls back to time', async () => {
    await withRoot(async (root) => {
      await writeFile(join(root, 'duration-history.json'), JSON.stringify({
        'a.test.ts': 10,
        'b.test.ts': 8,
        'c.test.ts': 6,
        'd.test.ts': 4,
        'e.test.ts': 2,
      }), 'utf8')
      const affinity = await shardAll(root, [
        'e.test.ts',
        'a.test.ts',
        'd.test.ts',
        'c.test.ts',
        'b.test.ts',
      ], 3, {
        shardStrategy: 'affinity',
        shardAffinityRules: [{ pattern: 'integration/**', shardIndex: 1 }],
      })
      const timed = await shardAll(root, [
        'e.test.ts',
        'a.test.ts',
        'd.test.ts',
        'c.test.ts',
        'b.test.ts',
      ], 3, { shardStrategy: 'time' })
      expect(affinity.shards).toEqual(timed.shards)
    })
  })

  test('isolateSlowThreshold gives each shard one slow file', async () => {
    await withRoot(async (root) => {
      await writeFile(join(root, 'duration-history.json'), JSON.stringify({
        'a.test.ts': 100,
        'b.test.ts': 80,
        'c.test.ts': 70,
        'd.test.ts': 10,
        'e.test.ts': 5,
      }), 'utf8')
      const saturated = await shardAll(root, [
        'd.test.ts',
        'c.test.ts',
        'a.test.ts',
        'b.test.ts',
      ], 2, {
        shardStrategy: 'time',
        isolateSlowThreshold: 50,
      })
      expect(saturated.shards).toEqual([
        ['a.test.ts'],
        ['b.test.ts', 'c.test.ts', 'd.test.ts'],
      ])

      const partial = await shardAll(root, [
        'e.test.ts',
        'd.test.ts',
        'a.test.ts',
      ], 3, {
        shardStrategy: 'time',
        isolateSlowThreshold: 50,
      })
      expect(partial.shards).toEqual([
        ['a.test.ts'],
        ['d.test.ts'],
        ['e.test.ts'],
      ])
    })
  })

  test('warns when shard load ratio is below the threshold', async () => {
    await withRoot(async (root) => {
      await writeFile(join(root, 'duration-history.json'), JSON.stringify({
        'heavy/a.test.ts': 1000,
        'heavy/b.test.ts': 1000,
        'light/c.test.ts': 10,
      }), 'utf8')
      const { warn } = await shardAll(root, [
        'heavy/a.test.ts',
        'heavy/b.test.ts',
        'light/c.test.ts',
      ], 2, {
        shardStrategy: 'affinity',
        shardAffinityRules: [
          { pattern: 'heavy/**', shardIndex: 0 },
          { pattern: 'light/**', shardIndex: 1 },
        ],
        rebalanceThreshold: 0.5,
      })
      expect(warn).toHaveBeenCalled()
      const message = String(warn.mock.calls.at(-1)?.[0])
      expect(message).toContain('ratio=0.01')
      expect(message).toContain('threshold=0.50')
    })
  })

  test('sorts by smoothed duration and leaves unknown files last', async () => {
    await withRoot(async (root) => {
      await writeFile(join(root, 'duration-history.json'), JSON.stringify({
        'a.test.ts': {
          observations: [
            { duration: 10, recordedAt: 1 },
            { duration: 30, recordedAt: 2 },
          ],
        },
        'b.test.ts': { duration: 5, recordedAt: 1 },
        'c.test.ts': 0,
      }), 'utf8')
      const { ctx } = buildCtx(root, 1, 1, {
        durationBasedSorting: true,
        durationSmoothing: 'latest',
      })
      const sequencer = new BaseSequencer(ctx)
      const files = specs(root, ['missing.test.ts', 'b.test.ts', 'c.test.ts', 'a.test.ts'])
      const sorted = await sequencer.sort(files)
      expect(sorted.map(spec => toDurationHistoryKey(root, spec.moduleId))).toEqual([
        'a.test.ts',
        'b.test.ts',
        'c.test.ts',
        'missing.test.ts',
      ])
    })
  })
})

describe('sequence duration config', () => {
  async function resolveSequence(sequence?: Record<string, unknown>) {
    const viteConfig = await viteResolveConfig({ configFile: false }, 'serve')
    return resolveConfig({
      logger: undefined,
      mode: 'test',
      _cliOptions: {},
    } as any, { sequence } as any, viteConfig).sequence
  }

  test('defaults and balanceShardsByTime resolution', async () => {
    const defaults = await resolveSequence()
    expect(defaults.shardStrategy).toBe('hash')
    expect(defaults.balanceShardsByTime).toBe(false)
    expect(defaults.recordFileDurations).toBe(false)
    expect(defaults.durationBasedSorting).toBe(false)
    expect(defaults.durationHistoryTTL).toBe(0)
    expect(defaults.durationHistoryPath).toBe('duration-history.json')
    expect(defaults.durationHistoryMaxRuns).toBe(1)
    expect(defaults.durationSmoothing).toBe('latest')
    expect(defaults.shardAffinityRules).toEqual([])
    expect(defaults.rebalanceThreshold).toBe(0)
    expect(defaults.isolateSlowThreshold).toBe(0)
    expect(defaults.durationFallbackStrategy).toBe('hash')

    const balanced = await resolveSequence({ balanceShardsByTime: true })
    expect(balanced.shardStrategy).toBe('time')
    expect(balanced.balanceShardsByTime).toBe(true)

    const overridden = await resolveSequence({
      balanceShardsByTime: true,
      shardStrategy: 'hash',
    })
    expect(overridden.shardStrategy).toBe('hash')
    expect(overridden.balanceShardsByTime).toBe(false)

    const explicit = await resolveSequence({
      balanceShardsByTime: true,
      shardStrategy: 'time',
    })
    expect(explicit.shardStrategy).toBe('time')
    expect(explicit.balanceShardsByTime).toBe(true)
  })

  test('rejects invalid sequence duration options', async () => {
    await expect(resolveSequence({ shardStrategy: 'random' })).rejects.toThrow(/shardStrategy/)
    await expect(resolveSequence({ balanceShardsByTime: 'yes' })).rejects.toThrow(/balanceShardsByTime/)
    await expect(resolveSequence({ durationHistoryTTL: Number.NaN })).rejects.toThrow(/durationHistoryTTL/)
    await expect(resolveSequence({ durationHistoryTTL: -1 })).rejects.toThrow(/durationHistoryTTL/)
    await expect(resolveSequence({ durationHistoryPath: '  history.json' })).rejects.toThrow(/durationHistoryPath/)
    await expect(resolveSequence({ durationHistoryPath: '' })).rejects.toThrow(/durationHistoryPath/)
    await expect(resolveSequence({ durationHistoryMaxRuns: 1.5 })).rejects.toThrow(/durationHistoryMaxRuns/)
    await expect(resolveSequence({ durationHistoryMaxRuns: 0 })).rejects.toThrow(/durationHistoryMaxRuns/)
    await expect(resolveSequence({ durationSmoothing: 'mean' })).rejects.toThrow(/durationSmoothing/)
    await expect(resolveSequence({
      shardAffinityRules: [{ pattern: 'a', shardIndex: -1 }],
    })).rejects.toThrow(/shardIndex/)
    await expect(resolveSequence({ shardAffinityRules: 'slow/**' })).rejects.toThrow(/shardAffinityRules/)
    await expect(resolveSequence({ rebalanceThreshold: 1.1 })).rejects.toThrow(/rebalanceThreshold/)
    await expect(resolveSequence({ isolateSlowThreshold: -5 })).rejects.toThrow(/isolateSlowThreshold/)
    await expect(resolveSequence({ durationFallbackStrategy: 'time' })).rejects.toThrow(/durationFallbackStrategy/)
    await expect(resolveSequence({ recordFileDurations: 1 })).rejects.toThrow(/recordFileDurations/)
    await expect(resolveSequence({ durationBasedSorting: 'yes' })).rejects.toThrow(/durationBasedSorting/)
  })
})
