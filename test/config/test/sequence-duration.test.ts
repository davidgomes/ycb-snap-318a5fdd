import { expect, test } from 'vitest'
import { resolveConfig } from 'vitest/node'

test('resolves duration-aware sequence defaults', async () => {
  const { vitestConfig } = await resolveConfig({ sequence: { balanceShardsByTime: true } })
  const { sequencer: _, seed: __, ...sequence } = vitestConfig.sequence
  expect(sequence).toMatchInlineSnapshot(`
    {
      "balanceShardsByTime": true,
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
      "shardStrategy": "time",
    }
  `)
})

test('balanceShardsByTime is disabled for other strategies', async () => {
  const { vitestConfig } = await resolveConfig({ sequence: { balanceShardsByTime: true, shardStrategy: 'hash' } })
  expect(vitestConfig.sequence.balanceShardsByTime).toBe(false)
})

test.each([
  { shardStrategy: 'size' },
  { recordFileDurations: 'yes' },
  { durationHistoryTTL: Infinity },
  { durationHistoryTTL: -1 },
  { durationHistoryPath: ' history.json' },
  { durationHistoryPath: '' },
  { durationHistoryMaxRuns: 1.5 },
  { durationHistoryMaxRuns: 0 },
  { durationSmoothing: 'max' },
  { shardAffinityRules: [{ pattern: 'a', shardIndex: -1 }] },
  { rebalanceThreshold: 1.1 },
  { isolateSlowThreshold: -1 },
  { durationFallbackStrategy: 'random' },
])('throws on invalid %o', async (sequence) => {
  await expect(resolveConfig({ sequence: sequence as any })).rejects.toThrow('"sequence.')
})
