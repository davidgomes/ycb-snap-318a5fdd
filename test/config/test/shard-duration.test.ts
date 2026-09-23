import type { TestUserConfig } from 'vitest/node'
import { describe, expect, test } from 'vitest'
import { runInlineTests } from '../../test-utils'

function testFile(name: string) {
  return `import { test } from 'vitest'\ntest('${name}', () => {})\n`
}

const files = {
  'a.test.ts': testFile('a'),
  'b.test.ts': testFile('b'),
  'c.test.ts': testFile('c'),
  'nested/d.test.ts': testFile('d'),
}

describe('config validation', () => {
  test.each([
    [{ shardStrategy: 'size' }, '"sequence.shardStrategy" must be one of "hash", "time", "round-robin", "affinity", received \'size\'.'],
    [{ balanceShardsByTime: 'yes' }, '"sequence.balanceShardsByTime" must be a boolean, received \'yes\'.'],
    [{ recordFileDurations: 1 }, '"sequence.recordFileDurations" must be a boolean, received 1.'],
    [{ durationBasedSorting: 'true' }, '"sequence.durationBasedSorting" must be a boolean, received \'true\'.'],
    [{ durationHistoryTTL: -1 }, '"sequence.durationHistoryTTL" must be a finite number greater than or equal to 0, received -1.'],
    [{ durationHistoryTTL: Infinity }, '"sequence.durationHistoryTTL" must be a finite number greater than or equal to 0, received Infinity.'],
    [{ durationHistoryPath: '' }, '"sequence.durationHistoryPath" must be a non-empty string without leading or trailing whitespace, received \'\'.'],
    [{ durationHistoryPath: ' history.json' }, '"sequence.durationHistoryPath" must be a non-empty string without leading or trailing whitespace, received \' history.json\'.'],
    [{ durationHistoryMaxRuns: 0 }, '"sequence.durationHistoryMaxRuns" must be an integer greater than or equal to 1, received 0.'],
    [{ durationHistoryMaxRuns: 1.5 }, '"sequence.durationHistoryMaxRuns" must be an integer greater than or equal to 1, received 1.5.'],
    [{ durationSmoothing: 'mean' }, '"sequence.durationSmoothing" must be one of "latest", "average", "p95", "median", received \'mean\'.'],
    [{ shardAffinityRules: {} }, '"sequence.shardAffinityRules" must be an array, received {}.'],
    [{ shardAffinityRules: [{ pattern: 1, shardIndex: 0 }] }, '"sequence.shardAffinityRules[0].pattern" must be a non-empty string, received 1.'],
    [{ shardAffinityRules: [{ pattern: '*.ts', shardIndex: -1 }] }, '"sequence.shardAffinityRules[0].shardIndex" must be an integer greater than or equal to 0, received -1.'],
    [{ rebalanceThreshold: 1.5 }, '"sequence.rebalanceThreshold" must be a number between 0 and 1, received 1.5.'],
    [{ isolateSlowThreshold: -10 }, '"sequence.isolateSlowThreshold" must be a number greater than or equal to 0, received -10.'],
    [{ durationFallbackStrategy: 'random' }, '"sequence.durationFallbackStrategy" must be one of "hash", "equal-split", received \'random\'.'],
  ])('throws on invalid %o', async (sequence, error) => {
    const { stderr } = await runInlineTests(files, { sequence: sequence as TestUserConfig['sequence'] })
    expect(stderr).toMatch(`Error: ${error}`)
  })

  test('resolves defaults and serializes options to the worker config', async () => {
    const { ctx } = await runInlineTests(files)
    const expected = {
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
    }
    expect(ctx!.config.sequence).toMatchObject(expected)
    expect(ctx!.projects[0].serializedConfig.sequence).toMatchObject(expected)
  })

  test.each([
    [{ balanceShardsByTime: true }, { shardStrategy: 'time', balanceShardsByTime: true }],
    [{ balanceShardsByTime: true, shardStrategy: 'round-robin' }, { shardStrategy: 'round-robin', balanceShardsByTime: false }],
    [{ shardStrategy: 'time' }, { shardStrategy: 'time', balanceShardsByTime: false }],
  ] as const)('resolves balanceShardsByTime %o', async (sequence, expected) => {
    const { ctx } = await runInlineTests(files, { sequence })
    expect(ctx!.config.sequence).toMatchObject(expected)
    expect(ctx!.projects[0].serializedConfig.sequence).toMatchObject(expected)
  })
})

describe('recordFileDurations', () => {
  test('writes durations and preserves other entries', async () => {
    const { fs, stderr } = await runInlineTests({
      ...files,
      'duration-history.json': JSON.stringify({
        'removed.test.ts': { duration: 5, recordedAt: 1 },
        'legacy.test.ts': 5000,
      }),
    }, { sequence: { recordFileDurations: true } })

    expect(stderr).toBe('')
    const history = JSON.parse(fs.readFile('duration-history.json'))
    expect(Object.keys(history).sort()).toEqual([
      'a.test.ts',
      'b.test.ts',
      'c.test.ts',
      'legacy.test.ts',
      'nested/d.test.ts',
      'removed.test.ts',
    ])
    expect(history['removed.test.ts']).toEqual({ duration: 5, recordedAt: 1 })
    expect(history['legacy.test.ts']).toBe(5000)
    expect(history['nested/d.test.ts']).toEqual({
      duration: expect.any(Number),
      recordedAt: expect.any(Number),
    })
    expect(Number.isInteger(history['nested/d.test.ts'].duration)).toBe(true)
    expect(history['nested/d.test.ts'].recordedAt).toBeGreaterThan(Date.now() - 60_000)
  })

  test('creates parent directories of durationHistoryPath', async () => {
    const { fs } = await runInlineTests(files, {
      sequence: { recordFileDurations: true, durationHistoryPath: 'reports/durations/history.json' },
    })
    const history = JSON.parse(fs.readFile('reports/durations/history.json'))
    expect(Object.keys(history).sort()).toEqual(['a.test.ts', 'b.test.ts', 'c.test.ts', 'nested/d.test.ts'])
  })

  test('keeps durationHistoryMaxRuns most recent observations', async () => {
    const { fs } = await runInlineTests({
      ...files,
      'duration-history.json': JSON.stringify({
        'a.test.ts': {
          observations: [
            { duration: 3, recordedAt: 3 },
            { duration: 1, recordedAt: 1 },
            { duration: 2, recordedAt: 2 },
          ],
        },
        'b.test.ts': 100,
      }),
    }, { sequence: { recordFileDurations: true, durationHistoryMaxRuns: 3 } })

    const history = JSON.parse(fs.readFile('duration-history.json'))
    expect(history['a.test.ts'].observations).toHaveLength(3)
    expect(history['a.test.ts'].observations.slice(0, 2)).toEqual([
      { duration: 2, recordedAt: 2 },
      { duration: 3, recordedAt: 3 },
    ])
    expect(history['b.test.ts'].observations).toHaveLength(2)
    expect(history['b.test.ts'].observations[0]).toEqual({ duration: 100, recordedAt: 0 })
    expect(history['c.test.ts'].observations).toHaveLength(1)
  })
})

describe('duration-aware sharding', () => {
  const history = JSON.stringify({
    'a.test.ts': 100,
    'b.test.ts': 90,
    'c.test.ts': 20,
    'nested/d.test.ts': 10,
  })

  async function runShard(shard: string, sequence: TestUserConfig['sequence']) {
    const result = await runInlineTests({ ...files, 'duration-history.json': history }, { shard, sequence })
    return {
      ...result,
      paths: result.results.map(testModule => testModule.relativeModuleId).sort(),
    }
  }

  test('time strategy distributes files by recorded durations', async () => {
    const first = await runShard('1/2', { shardStrategy: 'time' })
    const second = await runShard('2/2', { shardStrategy: 'time' })
    expect(first.paths).toEqual(['a.test.ts', 'nested/d.test.ts'])
    expect(second.paths).toEqual(['b.test.ts', 'c.test.ts'])
  })

  test('warns when shards are unbalanced', async () => {
    const { stderr, paths } = await runShard('1/3', { shardStrategy: 'time', rebalanceThreshold: 0.5 })
    expect(paths).toEqual(['a.test.ts'])
    expect(stderr).toMatch('ratio=0.30')
    expect(stderr).toMatch('threshold=0.50')
  })
})
