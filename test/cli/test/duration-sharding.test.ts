import { expect, test } from 'vitest'
import { runInlineTests } from '../../test-utils'

test('records file durations after the run', async () => {
  const { fs, stderr } = await runInlineTests({
    'test/fast.test.ts': `test('fast', () => {})`,
    'test/slow.test.ts': `test('slow', async () => { await new Promise(r => setTimeout(r, 50)) })`,
    'history/durations.json': JSON.stringify({ 'test/removed.test.ts': 5000 }),
  }, {
    globals: true,
    sequence: {
      recordFileDurations: true,
      durationHistoryPath: 'history/durations.json',
    },
  })
  expect(stderr).toBe('')

  const history = JSON.parse(fs.readFile('history/durations.json'))
  expect(Object.keys(history).sort()).toEqual(['test/fast.test.ts', 'test/removed.test.ts', 'test/slow.test.ts'])
  expect(history['test/removed.test.ts']).toBe(5000)
  expect(Number.isInteger(history['test/slow.test.ts'].duration)).toBe(true)
  expect(history['test/slow.test.ts'].duration).toBeGreaterThanOrEqual(40)
  expect(typeof history['test/slow.test.ts'].recordedAt).toBe('number')
})

test('shards files by recorded durations', async () => {
  const structure = {
    'a.test.ts': `test('a', () => {})`,
    'b.test.ts': `test('b', () => {})`,
    'c.test.ts': `test('c', () => {})`,
    'd.test.ts': `test('d', () => {})`,
    'duration-history.json': JSON.stringify({
      'a.test.ts': { duration: 100, recordedAt: 1 },
      'b.test.ts': { duration: 90, recordedAt: 1 },
      'c.test.ts': { duration: 20, recordedAt: 1 },
      'd.test.ts': { duration: 10, recordedAt: 1 },
    }),
  }

  const shards: string[][] = []
  for (const index of [1, 2]) {
    const { results, stderr } = await runInlineTests(structure, {
      globals: true,
      shard: `${index}/2`,
      sequence: { shardStrategy: 'time' },
    })
    expect(stderr).toBe('')
    shards.push(results.map(module => module.relativeModuleId).sort())
  }

  expect(shards).toEqual([
    ['a.test.ts', 'd.test.ts'],
    ['b.test.ts', 'c.test.ts'],
  ])
})
