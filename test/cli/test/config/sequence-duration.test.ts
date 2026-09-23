import type { InlineConfig } from 'vitest/node'
import { runInlineTests, runVitest } from '#test-utils'
import { describe, expect, test } from 'vitest'
import { createVitest } from 'vitest/node'

function resolve(sequence: InlineConfig['sequence']) {
  return runVitest({
    sequence,
    include: [],
    standalone: true,
    watch: true,
  })
}

describe('config validation', () => {
  test.each([
    [{ shardStrategy: 'fastest' }, '"sequence.shardStrategy" must be one of "hash", "time", "round-robin", "affinity", received: "fastest"'],
    [{ balanceShardsByTime: 'yes' }, '"sequence.balanceShardsByTime" must be a boolean, received: "yes"'],
    [{ recordFileDurations: 1 }, '"sequence.recordFileDurations" must be a boolean, received: 1'],
    [{ durationBasedSorting: null }, '"sequence.durationBasedSorting" must be a boolean, received: null'],
    [{ durationHistoryTTL: -1 }, '"sequence.durationHistoryTTL" must be a finite number greater than or equal to 0, received: -1'],
    [{ durationHistoryTTL: Infinity }, '"sequence.durationHistoryTTL" must be a finite number greater than or equal to 0, received: null'],
    [{ durationHistoryPath: '' }, '"sequence.durationHistoryPath" must be a non-empty string without leading or trailing whitespace, received: ""'],
    [{ durationHistoryPath: ' history.json' }, '"sequence.durationHistoryPath" must be a non-empty string without leading or trailing whitespace, received: " history.json"'],
    [{ durationHistoryMaxRuns: 0 }, '"sequence.durationHistoryMaxRuns" must be an integer greater than or equal to 1, received: 0'],
    [{ durationHistoryMaxRuns: 1.5 }, '"sequence.durationHistoryMaxRuns" must be an integer greater than or equal to 1, received: 1.5'],
    [{ durationSmoothing: 'max' }, '"sequence.durationSmoothing" must be one of "latest", "average", "p95", "median", received: "max"'],
    [{ shardAffinityRules: {} }, '"sequence.shardAffinityRules" must be an array, received: {}'],
    [{ shardAffinityRules: [{ pattern: '*.test.ts', shardIndex: -1 }] }, '"sequence.shardAffinityRules[0]" must be an object with a string "pattern" and a non-negative integer "shardIndex", received: {"pattern":"*.test.ts","shardIndex":-1}'],
    [{ rebalanceThreshold: 1.1 }, '"sequence.rebalanceThreshold" must be a number between 0 and 1, received: 1.1'],
    [{ isolateSlowThreshold: -5 }, '"sequence.isolateSlowThreshold" must be a number greater than or equal to 0, received: -5'],
    [{ durationFallbackStrategy: 'random' }, '"sequence.durationFallbackStrategy" must be one of "hash", "equal-split", received: "random"'],
  ])('throws on invalid %o', async (sequence, message) => {
    await expect(createVitest('test', { sequence: sequence as any, watch: false })).rejects.toThrow(message)
  })
})

describe('config resolution', () => {
  test('defaults', async () => {
    const { ctx } = await resolve({})
    const { sequencer: _sequencer, seed: _seed, ...sequence } = ctx!.config.sequence
    expect(sequence).toMatchInlineSnapshot(`
      {
        "balanceShardsByTime": false,
        "durationBasedSorting": false,
        "durationFallbackStrategy": "hash",
        "durationHistoryMaxRuns": 1,
        "durationHistoryPath": "duration-history.json",
        "durationHistoryTTL": 0,
        "durationSmoothing": "latest",
        "groupOrder": 0,
        "hooks": "stack",
        "isolateSlowThreshold": 0,
        "rebalanceThreshold": 0,
        "recordFileDurations": false,
        "shardAffinityRules": [],
        "shardStrategy": "hash",
      }
    `)
  })

  test('balanceShardsByTime implies time strategy', async () => {
    const { ctx } = await resolve({ balanceShardsByTime: true })
    expect(ctx!.config.sequence.shardStrategy).toBe('time')
    expect(ctx!.config.sequence.balanceShardsByTime).toBe(true)
  })

  test('balanceShardsByTime is disabled for other strategies', async () => {
    const { ctx } = await resolve({ balanceShardsByTime: true, shardStrategy: 'round-robin' })
    expect(ctx!.config.sequence.shardStrategy).toBe('round-robin')
    expect(ctx!.config.sequence.balanceShardsByTime).toBe(false)
  })

  test('options are serialized to the worker config', async () => {
    const { ctx } = await resolve({
      shardStrategy: 'affinity',
      recordFileDurations: true,
      durationBasedSorting: true,
      durationHistoryTTL: 1000,
      durationHistoryPath: 'cache/history.json',
      durationHistoryMaxRuns: 3,
      durationSmoothing: 'p95',
      shardAffinityRules: [{ pattern: 'slow/**', shardIndex: 1 }],
      rebalanceThreshold: 0.5,
      isolateSlowThreshold: 2000,
      durationFallbackStrategy: 'equal-split',
    })
    const { seed: _seed, ...sequence } = ctx!.projects[0].serializedConfig.sequence
    expect(sequence).toMatchInlineSnapshot(`
      {
        "balanceShardsByTime": false,
        "concurrent": undefined,
        "durationBasedSorting": true,
        "durationFallbackStrategy": "equal-split",
        "durationHistoryMaxRuns": 3,
        "durationHistoryPath": "cache/history.json",
        "durationHistoryTTL": 1000,
        "durationSmoothing": "p95",
        "hooks": "stack",
        "isolateSlowThreshold": 2000,
        "rebalanceThreshold": 0.5,
        "recordFileDurations": true,
        "setupFiles": undefined,
        "shardAffinityRules": [
          {
            "pattern": "slow/**",
            "shardIndex": 1,
          },
        ],
        "shardStrategy": "affinity",
        "shuffle": undefined,
      }
    `)
  })
})

const testFiles = Object.fromEntries(
  ['a', 'b', 'c', 'd', 'e', 'f'].map(name => [
    `test/${name}.test.js`,
    `import { test } from 'vitest'\ntest('${name}', () => {})\n`,
  ]),
)

const history = JSON.stringify({
  'test/a.test.js': { duration: 1000, recordedAt: 1 },
  'test/b.test.js': { duration: 900, recordedAt: 1 },
  'test/c.test.js': { duration: 300, recordedAt: 1 },
  'test/d.test.js': { duration: 200, recordedAt: 1 },
  'test/e.test.js': { duration: 100, recordedAt: 1 },
})

describe('sharding', () => {
  test.each([
    ['1/3', ['test/a.test.js', 'test/f.test.js']],
    ['2/3', ['test/b.test.js', 'test/e.test.js']],
    ['3/3', ['test/c.test.js', 'test/d.test.js']],
  ])('round-robin shard %s', async (shard, expected) => {
    const { results, stderr } = await runInlineTests({
      ...testFiles,
      'duration-history.json': history,
    }, {
      shard,
      sequence: { shardStrategy: 'round-robin' },
    })
    expect(stderr).toBe('')
    expect(results.map(m => m.relativeModuleId).sort()).toEqual(expected)
  })

  test('warns when shards are not balanced', async () => {
    const { stderr } = await runInlineTests({
      ...testFiles,
      'duration-history.json': history,
    }, {
      shard: '1/3',
      sequence: { shardStrategy: 'round-robin', rebalanceThreshold: 0.75 },
    })
    expect(stderr).toMatchInlineSnapshot(`
      "Shard load imbalance detected: ratio=0.50 is below threshold=0.75 (min=500ms, max=1000ms, loads=[1000, 1000, 500])
      "
    `)
  })
})

describe('recordFileDurations', () => {
  test('writes durations after the run and preserves other entries', async () => {
    const { fs, stderr } = await runInlineTests({
      'test/a.test.js': `import { test } from 'vitest'\ntest('a', () => {})\n`,
      'test/b.test.js': `import { test } from 'vitest'\ntest('b', () => {})\n`,
      'history/durations.json': JSON.stringify({ 'test/removed.test.js': 5000 }),
    }, {
      sequence: {
        recordFileDurations: true,
        durationHistoryPath: 'history/durations.json',
      },
    })
    expect(stderr).toBe('')
    const written = JSON.parse(fs.readFile('history/durations.json'))
    expect(Object.keys(written).sort()).toEqual(['test/a.test.js', 'test/b.test.js', 'test/removed.test.js'])
    expect(written['test/removed.test.js']).toBe(5000)
    for (const key of ['test/a.test.js', 'test/b.test.js']) {
      expect(written[key]).toEqual({ duration: expect.any(Number), recordedAt: expect.any(Number) })
      expect(Number.isInteger(written[key].duration)).toBe(true)
    }
  })

  test('keeps multiple observations with durationHistoryMaxRuns', async () => {
    const { fs } = await runInlineTests({
      'test/a.test.js': `import { test } from 'vitest'\ntest('a', () => {})\n`,
      'duration-history.json': JSON.stringify({
        'test/a.test.js': { observations: [{ duration: 1, recordedAt: 1 }, { duration: 2, recordedAt: 2 }] },
      }),
    }, {
      sequence: {
        recordFileDurations: true,
        durationHistoryMaxRuns: 2,
      },
    })
    const written = JSON.parse(fs.readFile('duration-history.json'))
    expect(written['test/a.test.js'].observations).toEqual([
      { duration: 2, recordedAt: 2 },
      { duration: expect.any(Number), recordedAt: expect.any(Number) },
    ])
  })
})
