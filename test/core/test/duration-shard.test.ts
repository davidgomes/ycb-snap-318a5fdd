import type { TestProject, Vitest } from 'vitest/node'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, test, vi } from 'vitest'
import { BaseSequencer } from '../../../packages/vitest/src/node/sequencers/BaseSequencer'
import { recordFileDurations } from '../../../packages/vitest/src/node/sequencers/duration-history'
import { smoothDuration } from '../../../packages/vitest/src/node/sequencers/duration-smoothing'
import { TestSpecification } from '../../../packages/vitest/src/node/test-specification'

const dirs: string[] = []

afterEach(async () => {
  await Promise.all(dirs.splice(0).map(dir => rm(dir, { recursive: true, force: true })))
})

function buildCtx(root: string, sequence: Record<string, unknown> = {}, shard = { index: 1, count: 2 }): Vitest {
  return {
    config: {
      root,
      shard,
      sequence: {
        groupOrder: 0,
        durationHistoryPath: 'duration-history.json',
        durationHistoryTTL: 0,
        durationSmoothing: 'latest',
        shardStrategy: 'hash',
        shardAffinityRules: [],
        isolateSlowThreshold: 0,
        rebalanceThreshold: 0,
        durationFallbackStrategy: 'hash',
        ...sequence,
      },
    },
    logger: { warn: vi.fn() },
    cache: {
      getFileTestResults: vi.fn(),
      getFileStats: vi.fn(),
    },
    state: { getFiles: vi.fn(() => []) },
  } as unknown as Vitest
}

function spec(root: string, name: string): TestSpecification {
  const project = {
    name: 'test',
    config: { root, sequence: { groupOrder: 0 } },
  } as TestProject
  return new TestSpecification(project, join(root, name), 'forks')
}

async function tempRoot(): Promise<string> {
  const dir = await mkdtemp(join(tmpdir(), 'vitest-duration-'))
  dirs.push(dir)
  return dir
}

describe('duration smoothing', () => {
  const observations = [
    { duration: 10, recordedAt: 1 },
    { duration: 20, recordedAt: 3 },
    { duration: 30, recordedAt: 2 },
    { duration: 40, recordedAt: 4 },
  ]

  test('latest uses the highest recordedAt', () => {
    expect(smoothDuration(observations, 'latest')).toBe(40)
  })

  test('average rounds the mean', () => {
    expect(smoothDuration(observations, 'average')).toBe(25)
  })

  test('p95 uses ceil(0.95 * n) - 1', () => {
    expect(smoothDuration(observations, 'p95')).toBe(40)
    expect(smoothDuration([{ duration: 5, recordedAt: 1 }, { duration: 9, recordedAt: 2 }], 'p95')).toBe(9)
  })

  test('median averages the two middle values for an even count', () => {
    expect(smoothDuration(observations, 'median')).toBe(25)
    expect(smoothDuration([{ duration: 4, recordedAt: 1 }, { duration: 9, recordedAt: 2 }, { duration: 1, recordedAt: 3 }], 'median')).toBe(4)
  })
})

describe('duration history sharding', () => {
  test('migrates legacy numeric entries and keeps recordedAt 0 past a ttl', async () => {
    const root = await tempRoot()
    await writeFile(join(root, 'duration-history.json'), JSON.stringify({
      'test/a.test.ts': 5000,
      'test/b.test.ts': { duration: 10, recordedAt: 1 },
    }))
    const ctx = buildCtx(root, {
      shardStrategy: 'time',
      durationHistoryTTL: 1000,
      durationSmoothing: 'latest',
    }, { index: 1, count: 2 })
    const sequencer = new BaseSequencer(ctx)
    const files = [spec(root, 'test/a.test.ts'), spec(root, 'test/b.test.ts')]
    const shard = await sequencer.shard(files)
    expect(shard.map(file => file.moduleId.endsWith('a.test.ts'))).toEqual([true])
  })

  test('round-robin bounces and repeats the boundary shard', async () => {
    const root = await tempRoot()
    await writeFile(join(root, 'duration-history.json'), JSON.stringify({
      'a.test.ts': { duration: 50, recordedAt: 5 },
      'b.test.ts': { duration: 40, recordedAt: 5 },
      'c.test.ts': { duration: 30, recordedAt: 5 },
      'd.test.ts': { duration: 20, recordedAt: 5 },
    }))
    const ctx = buildCtx(root, { shardStrategy: 'round-robin' }, { index: 1, count: 3 })
    const names = async (index: number) => {
      ctx.config.shard = { index, count: 3 }
      const shard = await new BaseSequencer(ctx).shard(['a', 'b', 'c', 'd'].map(name => spec(root, `${name}.test.ts`)))
      return shard.map(file => file.moduleId.split('/').pop())
    }
    expect(await names(1)).toEqual(['a.test.ts'])
    expect(await names(2)).toEqual(['b.test.ts'])
    expect(await names(3)).toEqual(['c.test.ts', 'd.test.ts'])
  })

  test('affinity pins the first glob match and packs the rest by time', async () => {
    const root = await tempRoot()
    await writeFile(join(root, 'duration-history.json'), JSON.stringify({
      'slow.test.ts': { duration: 100, recordedAt: 5 },
      'unit/a.test.ts': { duration: 10, recordedAt: 5 },
      'unit/b.test.ts': { duration: 80, recordedAt: 5 },
    }))
    const ctx = buildCtx(root, {
      shardStrategy: 'affinity',
      shardAffinityRules: [{ pattern: 'unit/**', shardIndex: 0 }],
    }, { index: 2, count: 2 })
    const shard = await new BaseSequencer(ctx).shard([
      spec(root, 'slow.test.ts'),
      spec(root, 'unit/a.test.ts'),
      spec(root, 'unit/b.test.ts'),
    ])
    expect(shard.map(file => file.moduleId.split('/').pop())).toEqual(['slow.test.ts'])
  })

  test('affinity falls back to time when nothing matches', async () => {
    const root = await tempRoot()
    await writeFile(join(root, 'duration-history.json'), JSON.stringify({
      'a.test.ts': { duration: 5, recordedAt: 1 },
      'b.test.ts': { duration: 50, recordedAt: 1 },
    }))
    const ctx = buildCtx(root, {
      shardStrategy: 'affinity',
      shardAffinityRules: [{ pattern: 'nope/**', shardIndex: 0 }],
    }, { index: 1, count: 2 })
    const shard = await new BaseSequencer(ctx).shard([spec(root, 'a.test.ts'), spec(root, 'b.test.ts')])
    expect(shard.map(file => file.moduleId.endsWith('b.test.ts'))).toEqual([true])
  })

  test('missing history uses equal-split fallback', async () => {
    const root = await tempRoot()
    const ctx = buildCtx(root, {
      shardStrategy: 'time',
      durationFallbackStrategy: 'equal-split',
    }, { index: 2, count: 2 })
    const shard = await new BaseSequencer(ctx).shard([
      spec(root, 'b.test.ts'),
      spec(root, 'a.test.ts'),
    ])
    expect(shard.map(file => file.moduleId.endsWith('b.test.ts'))).toEqual([true])
  })

  test('warns when shard load ratio is under the threshold', async () => {
    const root = await tempRoot()
    await writeFile(join(root, 'duration-history.json'), JSON.stringify({
      'a.test.ts': { duration: 100, recordedAt: 1 },
      'b.test.ts': { duration: 1, recordedAt: 1 },
    }))
    const ctx = buildCtx(root, {
      shardStrategy: 'time',
      rebalanceThreshold: 0.5,
    })
    await new BaseSequencer(ctx).shard([spec(root, 'a.test.ts'), spec(root, 'b.test.ts')])
    expect(ctx.logger.warn).toHaveBeenCalledOnce()
    expect(String(vi.mocked(ctx.logger.warn).mock.calls[0]?.[0])).toContain('ratio=0.01')
    expect(String(vi.mocked(ctx.logger.warn).mock.calls[0]?.[0])).toContain('threshold=0.50')
  })

  test('sorts known durations first and leaves unknown files last', async () => {
    const root = await tempRoot()
    await writeFile(join(root, 'duration-history.json'), JSON.stringify({
      'short.test.ts': { duration: 5, recordedAt: 1 },
      'long.test.ts': { duration: 50, recordedAt: 1 },
    }))
    const ctx = buildCtx(root, { durationBasedSorting: true })
    const sorted = await new BaseSequencer(ctx).sort([
      spec(root, 'missing.test.ts'),
      spec(root, 'short.test.ts'),
      spec(root, 'long.test.ts'),
    ])
    expect(sorted.map(file => file.moduleId.split('/').pop())).toEqual([
      'long.test.ts',
      'short.test.ts',
      'missing.test.ts',
    ])
  })

  test('records rounded durations without dropping other files', async () => {
    const root = await tempRoot()
    const nested = join(root, 'nested')
    await mkdir(nested)
    const ctx = buildCtx(root, {
      recordFileDurations: true,
      durationHistoryPath: 'nested/history.json',
      durationHistoryMaxRuns: 2,
    })
    await writeFile(join(nested, 'history.json'), JSON.stringify({
      'keep.test.ts': { duration: 8, recordedAt: 0 },
    }))
    vi.mocked(ctx.state.getFiles).mockReturnValue([
      { filepath: join(root, 'ran.test.ts'), result: { duration: 12.6 } },
    ] as any)
    await recordFileDurations(ctx)
    const written = JSON.parse(await readFile(join(nested, 'history.json'), 'utf-8'))
    expect(written['keep.test.ts']).toEqual({ observations: [{ duration: 8, recordedAt: 0 }] })
    expect(written['ran.test.ts'].observations[0].duration).toBe(13)
  })
})
