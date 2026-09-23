import type { TestProject, Vitest } from 'vitest/node'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, resolve } from 'pathe'
import { afterEach, describe, expect, test, vi } from 'vitest'
import { resolveSequenceOptions } from '../../../packages/vitest/src/node/config/resolveSequenceOptions'
import { BaseSequencer } from '../../../packages/vitest/src/node/sequencers/BaseSequencer'
import {
  parseDurationHistory,
  readDurationHistory,
  recordFileDurations,
  relativeModulePath,
  writeDurationHistory,
} from '../../../packages/vitest/src/node/sequencers/duration-history'
import { smoothDuration } from '../../../packages/vitest/src/node/sequencers/duration-smoothing'
import { TestSpecification } from '../../../packages/vitest/src/node/test-specification'

const tempDirs: string[] = []

afterEach(async () => {
  await Promise.all(tempDirs.splice(0).map(dir => rm(dir, { recursive: true, force: true })))
})

async function createRoot(): Promise<string> {
  const root = await mkdtemp(resolve(tmpdir(), 'vitest-duration-'))
  tempDirs.push(root)
  return root
}

function buildProject(root: string): TestProject {
  return {
    name: 'test',
    config: {
      root,
      isolate: true,
      sequence: { groupOrder: 0 },
    },
  } as any as TestProject
}

function specs(root: string, files: string[]): TestSpecification[] {
  const project = buildProject(root)
  return files.map(file => new TestSpecification(project, resolve(root, file), 'forks'))
}

function buildCtx(root: string, config: Record<string, any> = {}, logger = { warn: vi.fn() }): Vitest {
  return {
    config: {
      root,
      sequence: { groupOrder: 0 },
      ...config,
    },
    cache: {
      getFileTestResults: vi.fn(),
      getFileStats: vi.fn(),
    },
    logger,
  } as unknown as Vitest
}

async function shardNames(root: string, files: string[], config: Record<string, any>): Promise<string[][]> {
  const testFiles = specs(root, files)
  const count = config.shard.count as number
  const names: string[][] = []
  for (let index = 1; index <= count; index++) {
    const sequencer = new BaseSequencer(buildCtx(root, {
      ...config,
      shard: { index, count },
    }, config.logger))
    const shard = await sequencer.shard(testFiles)
    names.push(shard.map(spec => relativeModulePath(root, spec.moduleId)))
  }
  return names
}

describe('duration smoothing', () => {
  const observations = [
    { duration: 10, recordedAt: 1 },
    { duration: 30, recordedAt: 3 },
    { duration: 20, recordedAt: 2 },
    { duration: 40, recordedAt: 4 },
  ]

  test('latest uses the highest recordedAt', () => {
    expect(smoothDuration(observations, 'latest')).toBe(40)
    expect(smoothDuration([
      { duration: 5, recordedAt: 2 },
      { duration: 9, recordedAt: 2 },
    ], 'latest')).toBe(9)
  })

  test('average rounds the mean', () => {
    expect(smoothDuration(observations, 'average')).toBe(25)
    expect(smoothDuration([
      { duration: 1, recordedAt: 1 },
      { duration: 2, recordedAt: 2 },
    ], 'average')).toBe(2)
  })

  test('p95 uses the nearest-rank index', () => {
    expect(smoothDuration(observations, 'p95')).toBe(40)
    expect(smoothDuration([
      { duration: 1, recordedAt: 1 },
    ], 'p95')).toBe(1)
    const values = Array.from({ length: 10 }, (_, index) => ({
      duration: index + 1,
      recordedAt: index + 1,
    }))
    expect(smoothDuration(values, 'p95')).toBe(10)
  })

  test('median averages the two middle values for an even count', () => {
    expect(smoothDuration(observations, 'median')).toBe(25)
    expect(smoothDuration([
      { duration: 1, recordedAt: 1 },
      { duration: 4, recordedAt: 2 },
    ], 'median')).toBe(2)
    expect(smoothDuration([
      { duration: 1, recordedAt: 1 },
      { duration: 2, recordedAt: 2 },
      { duration: 9, recordedAt: 3 },
    ], 'median')).toBe(2)
  })
})

describe('duration history', () => {
  test('reads single, multi, and legacy entries', () => {
    const history = parseDurationHistory(JSON.stringify({
      'test/a.ts': { duration: 1234, recordedAt: 1700000000 },
      'test/b.ts': {
        observations: [
          { duration: 10, recordedAt: 1 },
          { duration: 20, recordedAt: 2 },
        ],
      },
      'test/c.ts': 5000,
      './test/d.ts': { duration: 7, recordedAt: 4 },
    }))

    expect(history?.get('test/a.ts')).toEqual([{ duration: 1234, recordedAt: 1700000000 }])
    expect(history?.get('test/b.ts')).toEqual([
      { duration: 10, recordedAt: 1 },
      { duration: 20, recordedAt: 2 },
    ])
    expect(history?.get('test/c.ts')).toEqual([{ duration: 5000, recordedAt: 0 }])
    expect(history?.get('test/d.ts')).toEqual([{ duration: 7, recordedAt: 4 }])
  })

  test('returns null for missing and corrupt history', async () => {
    const root = await createRoot()
    expect(await readDurationHistory(resolve(root, 'missing.json'))).toBeNull()
    expect(parseDurationHistory('{')).toBeNull()
    expect(parseDurationHistory('[]')).toBeNull()
    expect(parseDurationHistory('null')).toBeNull()
    expect(parseDurationHistory(JSON.stringify({ 'test/a.ts': 'bad' }))).toBeNull()
  })

  test('drops expired observations and keeps recordedAt 0', () => {
    const history = parseDurationHistory(JSON.stringify({
      'test/expired.ts': { duration: 10, recordedAt: 8000 },
      'test/fresh.ts': { duration: 15, recordedAt: 9500 },
      'test/legacy.ts': 5000,
      'test/mixed.ts': {
        observations: [
          { duration: 1, recordedAt: 0 },
          { duration: 2, recordedAt: 1000 },
          { duration: 3, recordedAt: 9900 },
        ],
      },
    }), { ttl: 1000, now: 10_000 })

    expect(history?.has('test/expired.ts')).toBe(false)
    expect(history?.get('test/fresh.ts')).toEqual([{ duration: 15, recordedAt: 9500 }])
    expect(history?.get('test/legacy.ts')).toEqual([{ duration: 5000, recordedAt: 0 }])
    expect(history?.get('test/mixed.ts')).toEqual([
      { duration: 1, recordedAt: 0 },
      { duration: 3, recordedAt: 9900 },
    ])
  })

  test('writes one entry or capped observations and preserves other files', async () => {
    const root = await createRoot()
    const filePath = resolve(root, 'nested/duration-history.json')
    await mkdir(dirname(filePath), { recursive: true })
    await writeFile(filePath, JSON.stringify({
      'test/keep.ts': { duration: 4, recordedAt: 1 },
      'test/old.ts': {
        observations: [
          { duration: 1, recordedAt: 1 },
          { duration: 2, recordedAt: 2 },
          { duration: 3, recordedAt: 3 },
        ],
      },
    }))

    const existing = await readDurationHistory(filePath)
    existing!.set('test/old.ts', [
      ...(existing!.get('test/old.ts') ?? []),
      { duration: 9, recordedAt: 4 },
    ])
    await writeDurationHistory(filePath, existing!, 2)

    expect(JSON.parse(await readFile(filePath, 'utf-8'))).toEqual({
      'test/keep.ts': {
        observations: [{ duration: 4, recordedAt: 1 }],
      },
      'test/old.ts': {
        observations: [
          { duration: 3, recordedAt: 3 },
          { duration: 9, recordedAt: 4 },
        ],
      },
    })

    await writeDurationHistory(filePath, new Map([
      ['test/keep.ts', [{ duration: 4, recordedAt: 1 }, { duration: 8, recordedAt: 5 }]],
      ['test/other.ts', [{ duration: 6, recordedAt: 2 }]],
    ]), 1)

    expect(JSON.parse(await readFile(filePath, 'utf-8'))).toEqual({
      'test/keep.ts': { duration: 8, recordedAt: 5 },
      'test/other.ts': { duration: 6, recordedAt: 2 },
    })
  })

  test('records rounded durations and creates parent directories', async () => {
    const root = await createRoot()
    const historyPath = 'reports/duration-history.json'
    await recordFileDurations({
      config: {
        root,
        sequence: {
          recordFileDurations: true,
          durationHistoryPath: historyPath,
          durationHistoryMaxRuns: 1,
          durationHistoryTTL: 0,
        },
      },
      state: {
        getFiles: () => [
          { filepath: resolve(root, 'test/a.test.ts'), result: { duration: 10.6 } },
          { filepath: resolve(root, 'test/b.test.ts'), result: { duration: 3.2 } },
          { filepath: resolve(root, 'test/skipped.test.ts'), result: {} },
        ],
      },
    })

    const saved = JSON.parse(await readFile(resolve(root, historyPath), 'utf-8'))
    expect(saved['test/a.test.ts'].duration).toBe(11)
    expect(saved['test/b.test.ts'].duration).toBe(3)
    expect(saved['test/skipped.test.ts']).toBeUndefined()
    expect(saved['test/a.test.ts'].recordedAt).toBeGreaterThan(0)

    await recordFileDurations({
      config: {
        root,
        sequence: {
          recordFileDurations: true,
          durationHistoryPath: historyPath,
          durationHistoryMaxRuns: 1,
          durationHistoryTTL: 0,
        },
      },
      state: {
        getFiles: () => [
          { filepath: resolve(root, 'test/b.test.ts'), result: { duration: 8.2 } },
        ],
      },
    })

    const merged = JSON.parse(await readFile(resolve(root, historyPath), 'utf-8'))
    expect(merged['test/a.test.ts'].duration).toBe(11)
    expect(merged['test/b.test.ts'].duration).toBe(8)
  })

  test('does not write when recording is disabled', async () => {
    const root = await createRoot()
    await recordFileDurations({
      config: {
        root,
        sequence: { recordFileDurations: false },
      },
      state: {
        getFiles: () => [
          { filepath: resolve(root, 'test/a.test.ts'), result: { duration: 10 } },
        ],
      },
    })
    await expect(readDurationHistory(resolve(root, 'duration-history.json'))).resolves.toBeNull()
  })
})

describe('sequence option validation', () => {
  test('applies defaults', () => {
    expect(resolveSequenceOptions({})).toEqual({
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
  })

  test('balanceShardsByTime selects time only when strategy is unset', () => {
    expect(resolveSequenceOptions({ balanceShardsByTime: true })).toMatchObject({
      shardStrategy: 'time',
      balanceShardsByTime: true,
    })
    expect(resolveSequenceOptions({
      balanceShardsByTime: true,
      shardStrategy: 'time',
    })).toMatchObject({
      shardStrategy: 'time',
      balanceShardsByTime: true,
    })
    expect(resolveSequenceOptions({
      balanceShardsByTime: true,
      shardStrategy: 'round-robin',
    })).toMatchObject({
      shardStrategy: 'round-robin',
      balanceShardsByTime: false,
    })
    expect(resolveSequenceOptions({
      balanceShardsByTime: true,
      shardStrategy: 'hash',
    })).toMatchObject({
      shardStrategy: 'hash',
      balanceShardsByTime: false,
    })
  })

  test('throws for invalid values', () => {
    expect(() => resolveSequenceOptions({ shardStrategy: 'nope' })).toThrowError(
      'Invalid sequence.shardStrategy: expected "hash", "time", "round-robin", "affinity", received "nope"',
    )
    expect(() => resolveSequenceOptions({ balanceShardsByTime: 'true' })).toThrowError(
      'Invalid sequence.balanceShardsByTime: expected a boolean, received "true"',
    )
    expect(() => resolveSequenceOptions({ recordFileDurations: 1 })).toThrowError(
      'Invalid sequence.recordFileDurations: expected a boolean, received 1',
    )
    expect(() => resolveSequenceOptions({ durationBasedSorting: null })).toThrowError(
      'Invalid sequence.durationBasedSorting: expected a boolean, received null',
    )
    expect(() => resolveSequenceOptions({ durationHistoryTTL: Number.POSITIVE_INFINITY })).toThrowError(
      'Invalid sequence.durationHistoryTTL: expected a finite number greater than or equal to 0, received Infinity',
    )
    expect(() => resolveSequenceOptions({ durationHistoryTTL: -1 })).toThrowError(
      'Invalid sequence.durationHistoryTTL: expected a finite number greater than or equal to 0, received -1',
    )
    expect(() => resolveSequenceOptions({ durationHistoryPath: ' history.json' })).toThrowError(
      'Invalid sequence.durationHistoryPath: expected a non-empty string with no leading or trailing whitespace',
    )
    expect(() => resolveSequenceOptions({ durationHistoryPath: '' })).toThrowError(
      'Invalid sequence.durationHistoryPath: expected a non-empty string with no leading or trailing whitespace',
    )
    expect(() => resolveSequenceOptions({ durationHistoryMaxRuns: 1.5 })).toThrowError(
      'Invalid sequence.durationHistoryMaxRuns: expected an integer greater than or equal to 1, received 1.5',
    )
    expect(() => resolveSequenceOptions({ durationHistoryMaxRuns: 0 })).toThrowError(
      'Invalid sequence.durationHistoryMaxRuns: expected an integer greater than or equal to 1, received 0',
    )
    expect(() => resolveSequenceOptions({ durationSmoothing: 'mean' })).toThrowError(
      'Invalid sequence.durationSmoothing: expected "latest", "average", "p95", "median", received "mean"',
    )
    expect(() => resolveSequenceOptions({ shardAffinityRules: {} })).toThrowError(
      'Invalid sequence.shardAffinityRules: expected an array, received {}',
    )
    expect(() => resolveSequenceOptions({
      shardAffinityRules: [{ pattern: '', shardIndex: 0 }],
    })).toThrowError(
      'Invalid sequence.shardAffinityRules[0].pattern: expected a non-empty string',
    )
    expect(() => resolveSequenceOptions({
      shardAffinityRules: [{ pattern: '**/*.ts', shardIndex: -1 }],
    })).toThrowError(
      'Invalid sequence.shardAffinityRules[0].shardIndex: expected an integer greater than or equal to 0, received -1',
    )
    expect(() => resolveSequenceOptions({
      shardAffinityRules: [{ pattern: '**/*.ts', shardIndex: 1.2 }],
    })).toThrowError(
      'Invalid sequence.shardAffinityRules[0].shardIndex: expected an integer greater than or equal to 0, received 1.2',
    )
    expect(() => resolveSequenceOptions({ rebalanceThreshold: 1.1 })).toThrowError(
      'Invalid sequence.rebalanceThreshold: expected a number between 0 and 1, received 1.1',
    )
    expect(() => resolveSequenceOptions({ isolateSlowThreshold: -0.1 })).toThrowError(
      'Invalid sequence.isolateSlowThreshold: expected a finite number greater than or equal to 0, received -0.1',
    )
    expect(() => resolveSequenceOptions({ durationFallbackStrategy: 'time' })).toThrowError(
      'Invalid sequence.durationFallbackStrategy: expected "hash", "equal-split", received "time"',
    )
  })

  test('accepts boundary numeric values', () => {
    expect(resolveSequenceOptions({
      durationHistoryTTL: 0,
      durationHistoryMaxRuns: 1,
      rebalanceThreshold: 0,
      isolateSlowThreshold: 0,
      durationHistoryPath: 'dir/history.json',
      shardAffinityRules: [{ pattern: 'slow/**', shardIndex: 0 }],
    })).toMatchObject({
      durationHistoryTTL: 0,
      durationHistoryMaxRuns: 1,
      rebalanceThreshold: 0,
      isolateSlowThreshold: 0,
      durationHistoryPath: 'dir/history.json',
      shardAffinityRules: [{ pattern: 'slow/**', shardIndex: 0 }],
    })
    expect(resolveSequenceOptions({ rebalanceThreshold: 1 }).rebalanceThreshold).toBe(1)
  })
})

describe('duration-aware sharding', () => {
  test('time strategy packs the longest files first', async () => {
    const root = await createRoot()
    await writeFile(resolve(root, 'duration-history.json'), JSON.stringify({
      'a.test.ts': { duration: 5, recordedAt: 1 },
      'b.test.ts': { duration: 4, recordedAt: 1 },
      'c.test.ts': { duration: 3, recordedAt: 1 },
      'd.test.ts': { duration: 2, recordedAt: 1 },
    }))

    expect(await shardNames(root, ['a.test.ts', 'b.test.ts', 'c.test.ts', 'd.test.ts'], {
      shard: { count: 2 },
      sequence: { shardStrategy: 'time' },
    })).toEqual([
      ['a.test.ts', 'd.test.ts'],
      ['b.test.ts', 'c.test.ts'],
    ])
  })

  test('round-robin bounces and repeats boundary shards', async () => {
    const root = await createRoot()
    await writeFile(resolve(root, 'duration-history.json'), JSON.stringify({
      'a.test.ts': { duration: 8, recordedAt: 1 },
      'b.test.ts': { duration: 7, recordedAt: 1 },
      'c.test.ts': { duration: 6, recordedAt: 1 },
      'd.test.ts': { duration: 5, recordedAt: 1 },
      'e.test.ts': { duration: 4, recordedAt: 1 },
      'f.test.ts': { duration: 3, recordedAt: 1 },
      'g.test.ts': { duration: 2, recordedAt: 1 },
      'h.test.ts': { duration: 1, recordedAt: 1 },
    }))

    expect(await shardNames(root, ['h.test.ts', 'a.test.ts', 'c.test.ts', 'b.test.ts', 'e.test.ts', 'd.test.ts', 'g.test.ts', 'f.test.ts'], {
      shard: { count: 4 },
      sequence: { shardStrategy: 'round-robin' },
    })).toEqual([
      ['a.test.ts', 'h.test.ts'],
      ['b.test.ts', 'g.test.ts'],
      ['c.test.ts', 'f.test.ts'],
      ['d.test.ts', 'e.test.ts'],
    ])
  })

  test('equal-split fallback sorts by path', async () => {
    const root = await createRoot()
    expect(await shardNames(root, ['c.test.ts', 'a.test.ts', 'e.test.ts', 'b.test.ts', 'd.test.ts'], {
      shard: { count: 2 },
      sequence: {
        shardStrategy: 'time',
        durationFallbackStrategy: 'equal-split',
      },
    })).toEqual([
      ['a.test.ts', 'c.test.ts', 'e.test.ts'],
      ['b.test.ts', 'd.test.ts'],
    ])
  })

  test('hash fallback reuses the hash distribution when history is missing', async () => {
    const root = await createRoot()
    const files = ['a.test.ts', 'b.test.ts', 'c.test.ts', 'd.test.ts', 'e.test.ts']
    const hashed = await shardNames(root, files, {
      shard: { count: 2 },
      sequence: { shardStrategy: 'hash' },
    })
    const fallback = await shardNames(root, files, {
      shard: { count: 2 },
      sequence: { shardStrategy: 'time', durationFallbackStrategy: 'hash' },
    })
    expect(fallback).toEqual(hashed)
  })

  test('affinity pins matches and packs the rest by time', async () => {
    const root = await createRoot()
    await writeFile(resolve(root, 'duration-history.json'), JSON.stringify({
      'slow/a.test.ts': { duration: 10, recordedAt: 1 },
      'fast/b.test.ts': { duration: 100, recordedAt: 1 },
      'fast/c.test.ts': { duration: 1, recordedAt: 1 },
    }))

    expect(await shardNames(root, ['fast/c.test.ts', 'slow/a.test.ts', 'fast/b.test.ts'], {
      shard: { count: 2 },
      sequence: {
        shardStrategy: 'affinity',
        shardAffinityRules: [{ pattern: 'slow/**', shardIndex: 0 }],
      },
    })).toEqual([
      ['slow/a.test.ts', 'fast/c.test.ts'],
      ['fast/b.test.ts'],
    ])
  })

  test('affinity clamps shard indexes and falls back to time when nothing matches', async () => {
    const root = await createRoot()
    await writeFile(resolve(root, 'duration-history.json'), JSON.stringify({
      'a.test.ts': { duration: 5, recordedAt: 1 },
      'b.test.ts': { duration: 4, recordedAt: 1 },
      'c.test.ts': { duration: 3, recordedAt: 1 },
      'd.test.ts': { duration: 2, recordedAt: 1 },
    }))

    expect(await shardNames(root, ['a.test.ts', 'b.test.ts'], {
      shard: { count: 2 },
      sequence: {
        shardStrategy: 'affinity',
        shardAffinityRules: [{ pattern: '**/*.test.ts', shardIndex: 9 }],
      },
    })).toEqual([
      [],
      ['a.test.ts', 'b.test.ts'],
    ])

    const time = await shardNames(root, ['a.test.ts', 'b.test.ts', 'c.test.ts', 'd.test.ts'], {
      shard: { count: 2 },
      sequence: { shardStrategy: 'time' },
    })
    const unmatched = await shardNames(root, ['a.test.ts', 'b.test.ts', 'c.test.ts', 'd.test.ts'], {
      shard: { count: 2 },
      sequence: {
        shardStrategy: 'affinity',
        shardAffinityRules: [{ pattern: 'none/**', shardIndex: 0 }],
      },
    })
    expect(unmatched).toEqual(time)
  })

  test('first affinity rule wins', async () => {
    const root = await createRoot()
    await writeFile(resolve(root, 'duration-history.json'), JSON.stringify({
      'slow/a.test.ts': { duration: 5, recordedAt: 1 },
      'fast/b.test.ts': { duration: 1, recordedAt: 1 },
    }))

    expect(await shardNames(root, ['slow/a.test.ts', 'fast/b.test.ts'], {
      shard: { count: 3 },
      sequence: {
        shardStrategy: 'affinity',
        shardAffinityRules: [
          { pattern: 'slow/**', shardIndex: 0 },
          { pattern: '**', shardIndex: 2 },
        ],
      },
    })).toEqual([
      ['slow/a.test.ts'],
      [],
      ['fast/b.test.ts'],
    ])
  })

  test('isolates slow files and gives overflow to the last shard', async () => {
    const root = await createRoot()
    await writeFile(resolve(root, 'duration-history.json'), JSON.stringify({
      'a.test.ts': { duration: 100, recordedAt: 1 },
      'b.test.ts': { duration: 80, recordedAt: 1 },
      'c.test.ts': { duration: 10, recordedAt: 1 },
      'd.test.ts': { duration: 5, recordedAt: 1 },
    }))

    expect(await shardNames(root, ['d.test.ts', 'b.test.ts', 'c.test.ts', 'a.test.ts'], {
      shard: { count: 2 },
      sequence: {
        shardStrategy: 'time',
        isolateSlowThreshold: 50,
      },
    })).toEqual([
      ['a.test.ts'],
      ['b.test.ts', 'd.test.ts', 'c.test.ts'],
    ])
  })

  test('distributes remaining files across shards that do not have a slow file', async () => {
    const root = await createRoot()
    await writeFile(resolve(root, 'duration-history.json'), JSON.stringify({
      'a.test.ts': { duration: 100, recordedAt: 1 },
      'b.test.ts': { duration: 10, recordedAt: 1 },
      'c.test.ts': { duration: 9, recordedAt: 1 },
      'd.test.ts': { duration: 8, recordedAt: 1 },
    }))

    expect(await shardNames(root, ['a.test.ts', 'b.test.ts', 'c.test.ts', 'd.test.ts'], {
      shard: { count: 3 },
      sequence: {
        shardStrategy: 'time',
        isolateSlowThreshold: 50,
      },
    })).toEqual([
      ['a.test.ts'],
      ['b.test.ts'],
      ['c.test.ts', 'd.test.ts'],
    ])
  })

  test('warns when shard load ratio is below the threshold', async () => {
    const root = await createRoot()
    await writeFile(resolve(root, 'duration-history.json'), JSON.stringify({
      'a.test.ts': { duration: 40, recordedAt: 1 },
      'b.test.ts': { duration: 10, recordedAt: 1 },
    }))
    const logger = { warn: vi.fn() }
    const sequencer = new BaseSequencer(buildCtx(root, {
      shard: { index: 1, count: 2 },
      sequence: {
        shardStrategy: 'time',
        rebalanceThreshold: 0.5,
      },
    }, logger))

    await sequencer.shard(specs(root, ['a.test.ts', 'b.test.ts']))
    expect(logger.warn).toHaveBeenCalledTimes(1)
    expect(logger.warn.mock.calls[0][0]).toBe('Shard load imbalance: ratio=0.25 threshold=0.50')
  })

  test('sorts known durations first and leaves absent files last', async () => {
    const root = await createRoot()
    await writeFile(resolve(root, 'duration-history.json'), JSON.stringify({
      'a.test.ts': { duration: 5, recordedAt: 1 },
      'b.test.ts': { duration: 20, recordedAt: 1 },
    }))
    const sequencer = new BaseSequencer(buildCtx(root, {
      sequence: { groupOrder: 0, durationBasedSorting: true },
    }))
    const sorted = await sequencer.sort(specs(root, ['c.test.ts', 'a.test.ts', 'b.test.ts']))
    expect(sorted.map(spec => relativeModulePath(root, spec.moduleId))).toEqual([
      'b.test.ts',
      'a.test.ts',
      'c.test.ts',
    ])
  })

  test('uses smoothing when several observations exist', async () => {
    const root = await createRoot()
    await writeFile(resolve(root, 'duration-history.json'), JSON.stringify({
      'a.test.ts': {
        observations: [
          { duration: 10, recordedAt: 1 },
          { duration: 30, recordedAt: 2 },
        ],
      },
      'b.test.ts': {
        observations: [
          { duration: 10, recordedAt: 1 },
          { duration: 10, recordedAt: 2 },
        ],
      },
    }))

    expect(await shardNames(root, ['a.test.ts', 'b.test.ts'], {
      shard: { count: 2 },
      sequence: {
        shardStrategy: 'time',
        durationSmoothing: 'average',
        durationHistoryMaxRuns: 2,
      },
    })).toEqual([
      ['a.test.ts'],
      ['b.test.ts'],
    ])
  })
})
