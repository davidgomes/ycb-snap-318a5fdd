import type { ResolvedConfig, TestProject, Vitest } from 'vitest/node'
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, relative } from 'pathe'
import { describe, expect, onTestFinished, test, vi } from 'vitest'
import { BaseSequencer } from '../../../packages/vitest/src/node/sequencers/BaseSequencer'
import { RandomSequencer } from '../../../packages/vitest/src/node/sequencers/RandomSequencer'
import { TestSpecification } from '../../../packages/vitest/src/node/test-specification'

function buildCtx(config?: Partial<Vitest['config']>) {
  return {
    config: {
      sequence: { groupOrder: 0 },
      ...config,
    },
    cache: {
      getFileTestResults: vi.fn(),
      getFileStats: vi.fn(),
    },
  } as unknown as Vitest
}

function buildWorkspace() {
  return {
    name: 'test',
    config: {
      root: import.meta.dirname,
      sequence: { groupOrder: 0 },
    },
  } as any as TestProject
}

const workspace = buildWorkspace()

function workspaced(files: string[]) {
  return files.map(file => new TestSpecification(workspace, file, 'forks'))
}

describe('base sequencer', () => {
  test('sorting when no info is available', async () => {
    const sequencer = new BaseSequencer(buildCtx())
    const files = workspaced(['a', 'b', 'c'])
    const sorted = await sequencer.sort(files)
    expect(sorted).toStrictEqual(files)
  })

  test('prioritize unknown files', async () => {
    const ctx = buildCtx()
    vi.spyOn(ctx.cache, 'getFileStats').mockImplementation((file) => {
      if (file === 'test:b') {
        return { size: 2 }
      }
    })
    const sequencer = new BaseSequencer(ctx)
    const files = workspaced(['b', 'a', 'c'])
    const sorted = await sequencer.sort(files)
    expect(sorted).toStrictEqual(workspaced(['a', 'c', 'b']))
  })

  test('sort by size, larger first', async () => {
    const ctx = buildCtx()
    vi.spyOn(ctx.cache, 'getFileStats').mockImplementation((file) => {
      if (file === 'test:a') {
        return { size: 1 }
      }
      if (file === 'test:b') {
        return { size: 2 }
      }
      if (file === 'test:c') {
        return { size: 3 }
      }
    })
    const sequencer = new BaseSequencer(ctx)
    const files = workspaced(['b', 'a', 'c'])
    const sorted = await sequencer.sort(files)
    expect(sorted).toStrictEqual(workspaced(['c', 'b', 'a']))
  })

  test('sort by results, failed first', async () => {
    const ctx = buildCtx()
    vi.spyOn(ctx.cache, 'getFileTestResults').mockImplementation((file) => {
      if (file === 'test:a') {
        return { failed: false, duration: 1 }
      }
      if (file === 'test:b') {
        return { failed: true, duration: 1 }
      }
      if (file === 'test:c') {
        return { failed: true, duration: 1 }
      }
    })
    const sequencer = new BaseSequencer(ctx)
    const files = workspaced(['b', 'a', 'c'])
    const sorted = await sequencer.sort(files)
    expect(sorted).toStrictEqual(workspaced(['b', 'c', 'a']))
  })

  test('sort by results, long first', async () => {
    const ctx = buildCtx()
    vi.spyOn(ctx.cache, 'getFileTestResults').mockImplementation((file) => {
      if (file === 'test:a') {
        return { failed: true, duration: 1 }
      }
      if (file === 'test:b') {
        return { failed: true, duration: 2 }
      }
      if (file === 'test:c') {
        return { failed: true, duration: 3 }
      }
    })
    const sequencer = new BaseSequencer(ctx)
    const files = workspaced(['b', 'a', 'c'])
    const sorted = await sequencer.sort(files)
    expect(sorted).toStrictEqual(workspaced(['c', 'b', 'a']))
  })

  test('sort by results, long and failed first', async () => {
    const ctx = buildCtx()
    vi.spyOn(ctx.cache, 'getFileTestResults').mockImplementation((file) => {
      if (file === 'test:a') {
        return { failed: false, duration: 1 }
      }
      if (file === 'test:b') {
        return { failed: false, duration: 6 }
      }
      if (file === 'test:c') {
        return { failed: true, duration: 3 }
      }
    })
    const sequencer = new BaseSequencer(ctx)
    const files = workspaced(['b', 'a', 'c'])
    const sorted = await sequencer.sort(files)
    expect(sorted).toStrictEqual(workspaced(['c', 'b', 'a']))
  })

  test.each([
    { files: 4, count: 3, expected: [2, 1, 1] },
    { files: 5, count: 4, expected: [2, 1, 1, 1] },
    { files: 9, count: 4, expected: [3, 2, 2, 2] },
  ])('shard x/$count distributes $files files as $expected', async ({ count, files, expected }) => {
    const specs = Array.from({ length: files }, (_, id) => ({ moduleId: `file-${id}.test.ts` } as TestSpecification))
    const slices = []

    for (const index of Array.from({ length: count }).keys()) {
      const ctx = buildCtx({ root: '/example/root', shard: { index: 1 + index, count } })
      const sequencer = new BaseSequencer(ctx)
      const shard = await sequencer.shard(specs)

      slices.push(shard.length)
    }

    expect(slices).toEqual(expected)

    const sum = slices.reduce((total, current) => total + current, 0)
    expect(sum).toBe(files)
  })
})

function createRoot(history?: Record<string, unknown> | string) {
  const root = mkdtempSync(join(tmpdir(), 'vitest-sequencer-'))
  onTestFinished(() => rmSync(root, { recursive: true, force: true }))
  if (history !== undefined) {
    writeFileSync(
      join(root, 'duration-history.json'),
      typeof history === 'string' ? history : JSON.stringify(history),
    )
  }
  return root
}

async function shardAll(
  root: string,
  files: string[],
  count: number,
  sequence: Partial<ResolvedConfig['sequence']>,
) {
  const warnings: string[] = []
  const specs = files.map(file => ({ moduleId: join(root, file) } as TestSpecification))
  const shards: string[][] = []
  for (let index = 1; index <= count; index++) {
    const ctx = {
      config: { root, shard: { index, count }, sequence: { groupOrder: 0, ...sequence } },
      logger: { warn: (message: string) => warnings.push(message) },
    } as unknown as Vitest
    const shard = await new BaseSequencer(ctx).shard(specs)
    shards.push(shard.map(spec => relative(root, spec.moduleId)))
  }
  return { shards, warnings }
}

describe('duration-aware sharding', () => {
  const files = ['a.test.ts', 'b.test.ts', 'c.test.ts', 'd.test.ts', 'e.test.ts']

  test('time strategy assigns slowest files to the least loaded shard', async () => {
    const root = createRoot({
      'a.test.ts': { duration: 100, recordedAt: 1 },
      'b.test.ts': { duration: 80, recordedAt: 1 },
      'c.test.ts': { duration: 60, recordedAt: 1 },
      'd.test.ts': { duration: 40, recordedAt: 1 },
      'e.test.ts': { duration: 20, recordedAt: 1 },
    })
    const { shards } = await shardAll(root, files, 2, { shardStrategy: 'time' })
    expect(shards).toEqual([
      ['a.test.ts', 'd.test.ts', 'e.test.ts'],
      ['b.test.ts', 'c.test.ts'],
    ])
  })

  test('balanceShardsByTime uses time strategy when shardStrategy is not set', async () => {
    const root = createRoot({ 'a.test.ts': 100, 'b.test.ts': 80, 'c.test.ts': 60, 'd.test.ts': 40, 'e.test.ts': 20 })
    const { shards } = await shardAll(root, files, 2, { balanceShardsByTime: true })
    expect(shards).toEqual([
      ['a.test.ts', 'd.test.ts', 'e.test.ts'],
      ['b.test.ts', 'c.test.ts'],
    ])
  })

  test('files missing from history have zero duration', async () => {
    const root = createRoot({ 'a.test.ts': 100, 'b.test.ts': 50 })
    const { shards } = await shardAll(root, ['a.test.ts', 'b.test.ts', 'c.test.ts'], 2, { shardStrategy: 'time' })
    expect(shards).toEqual([
      ['a.test.ts'],
      ['b.test.ts', 'c.test.ts'],
    ])
  })

  test('round-robin strategy bounces between the first and the last shard', async () => {
    const root = createRoot({
      'a.test.ts': 50,
      'b.test.ts': 40,
      'c.test.ts': 30,
      'd.test.ts': 20,
      // same duration, sorted by path
      'f.test.ts': 10,
      'e.test.ts': 10,
    })
    const { shards } = await shardAll(root, [...files, 'f.test.ts'], 3, { shardStrategy: 'round-robin' })
    expect(shards).toEqual([
      ['a.test.ts', 'f.test.ts'],
      ['b.test.ts', 'e.test.ts'],
      ['c.test.ts', 'd.test.ts'],
    ])
  })

  test('affinity strategy pins matching files and balances the rest', async () => {
    const root = createRoot({
      'slow/x.test.ts': 100,
      'a.test.ts': 60,
      'b.test.ts': 50,
      'c.test.ts': 10,
      'y.pinned.test.ts': 5,
    })
    const { shards } = await shardAll(
      root,
      ['a.test.ts', 'b.test.ts', 'c.test.ts', 'slow/x.test.ts', 'y.pinned.test.ts'],
      2,
      {
        shardStrategy: 'affinity',
        shardAffinityRules: [
          { pattern: 'slow/**', shardIndex: 1 },
          // clamped to the last shard
          { pattern: '**/*.pinned.test.ts', shardIndex: 5 },
          { pattern: 'slow/x.test.ts', shardIndex: 0 },
        ],
      },
    )
    expect(shards).toEqual([
      ['a.test.ts', 'b.test.ts'],
      ['slow/x.test.ts', 'y.pinned.test.ts', 'c.test.ts'],
    ])
  })

  test('affinity strategy falls back to time strategy if no rule matches', async () => {
    const root = createRoot({ 'a.test.ts': 100, 'b.test.ts': 80, 'c.test.ts': 60, 'd.test.ts': 40, 'e.test.ts': 20 })
    const affinity = await shardAll(root, files, 2, {
      shardStrategy: 'affinity',
      shardAffinityRules: [{ pattern: 'unknown/**', shardIndex: 0 }],
    })
    const time = await shardAll(root, files, 2, { shardStrategy: 'time' })
    expect(affinity.shards).toEqual(time.shards)
  })

  test.each([
    { name: 'missing', history: undefined },
    { name: 'corrupt', history: '{ not json' },
  ])('uses equal-split fallback if history is $name', async ({ history }) => {
    const root = createRoot(history)
    const { shards } = await shardAll(root, ['e.test.ts', 'c.test.ts', 'a.test.ts', 'b.test.ts', 'd.test.ts'], 2, {
      shardStrategy: 'time',
      durationFallbackStrategy: 'equal-split',
    })
    expect(shards).toEqual([
      ['a.test.ts', 'c.test.ts', 'e.test.ts'],
      ['b.test.ts', 'd.test.ts'],
    ])
  })

  test('uses hash fallback if history is missing', async () => {
    const root = createRoot()
    const time = await shardAll(root, files, 2, { shardStrategy: 'round-robin' })
    const hash = await shardAll(root, files, 2, { shardStrategy: 'hash' })
    expect(time.shards).toEqual(hash.shards)
    expect(time.shards.flat().sort()).toEqual(files)
  })

  test('isolates slow files into their own shards', async () => {
    const root = createRoot({ 'a.test.ts': 500, 'b.test.ts': 400, 'c.test.ts': 50, 'd.test.ts': 40, 'e.test.ts': 30 })
    const { shards } = await shardAll(root, files, 3, { shardStrategy: 'time', isolateSlowThreshold: 100 })
    expect(shards).toEqual([
      ['a.test.ts'],
      ['b.test.ts'],
      ['c.test.ts', 'd.test.ts', 'e.test.ts'],
    ])
  })

  test('puts extra slow files and the rest into the last shard', async () => {
    const root = createRoot({ 'a.test.ts': 500, 'b.test.ts': 400, 'c.test.ts': 300, 'd.test.ts': 40, 'e.test.ts': 30 })
    const { shards } = await shardAll(root, files, 2, { shardStrategy: 'time', isolateSlowThreshold: 100 })
    expect(shards).toEqual([
      ['a.test.ts'],
      ['b.test.ts', 'c.test.ts', 'd.test.ts', 'e.test.ts'],
    ])
  })

  test('warns if shards are unbalanced', async () => {
    const root = createRoot({ 'a.test.ts': 100, 'b.test.ts': 10 })
    const unbalanced = await shardAll(root, ['a.test.ts', 'b.test.ts'], 2, {
      shardStrategy: 'time',
      rebalanceThreshold: 0.5,
    })
    expect(unbalanced.warnings).toHaveLength(2)
    expect(unbalanced.warnings[0]).toMatch('ratio=0.10')
    expect(unbalanced.warnings[0]).toMatch('threshold=0.50')

    const balanced = await shardAll(root, ['a.test.ts', 'b.test.ts'], 2, {
      shardStrategy: 'time',
      rebalanceThreshold: 0.05,
    })
    expect(balanced.warnings).toEqual([])
  })

  test('sorts files by recorded duration', async () => {
    const root = createRoot({ 'b.test.ts': 10, 'c.test.ts': { observations: [{ duration: 30, recordedAt: 1 }] } })
    const ctx = {
      config: { root, sequence: { groupOrder: 0, durationBasedSorting: true } },
    } as unknown as Vitest
    const specs = ['a.test.ts', 'b.test.ts', 'c.test.ts'].map(file => ({ moduleId: join(root, file) } as TestSpecification))
    const sorted = await new BaseSequencer(ctx).sort(specs)
    expect(sorted.map(spec => relative(root, spec.moduleId))).toEqual(['c.test.ts', 'b.test.ts', 'a.test.ts'])
  })
})

describe('random sequencer', () => {
  test('sorting is the same when seed is defined', async () => {
    const ctx = buildCtx()
    ctx.config.sequence.seed = 101
    const sequencer = new RandomSequencer(ctx)
    const files = workspaced(['b', 'a', 'c'])
    const sorted = await sequencer.sort(files)
    expect(sorted).toStrictEqual(workspaced(['a', 'c', 'b']))
  })
})
