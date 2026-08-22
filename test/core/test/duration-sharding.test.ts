import { mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { join } from 'pathe'
import { describe, expect, test } from 'vitest'
import { readDurationHistory, writeFileDurations } from '../../../packages/vitest/src/node/sequencers/duration-history'
import { smoothDuration } from '../../../packages/vitest/src/node/sequencers/duration-smoothing'
import { matchAffinityShard } from '../../../packages/vitest/src/node/sequencers/shard-affinity'

function withTmpDir(run: (tmpDir: string) => void | Promise<void>) {
  const tmpDir = join(import.meta.dirname, 'fixtures', `duration-history-${Math.random().toString(36).slice(2)}`)
  mkdirSync(tmpDir, { recursive: true })
  return Promise.resolve(run(tmpDir)).finally(() => {
    rmSync(tmpDir, { recursive: true, force: true })
  })
}

describe('duration-smoothing', () => {
  test('latest uses highest recordedAt', () => {
    expect(smoothDuration([
      { duration: 100, recordedAt: 1 },
      { duration: 500, recordedAt: 3 },
      { duration: 200, recordedAt: 2 },
    ], 'latest')).toBe(500)
  })

  test('average rounds mean duration', () => {
    expect(smoothDuration([
      { duration: 100, recordedAt: 1 },
      { duration: 200, recordedAt: 2 },
      { duration: 301, recordedAt: 3 },
    ], 'average')).toBe(200)
  })

  test('p95 uses 95th percentile index', () => {
    expect(smoothDuration([
      { duration: 10, recordedAt: 1 },
      { duration: 20, recordedAt: 2 },
      { duration: 30, recordedAt: 3 },
      { duration: 40, recordedAt: 4 },
      { duration: 100, recordedAt: 5 },
    ], 'p95')).toBe(100)
  })

  test('median uses middle value', () => {
    expect(smoothDuration([
      { duration: 10, recordedAt: 1 },
      { duration: 30, recordedAt: 2 },
      { duration: 20, recordedAt: 3 },
    ], 'median')).toBe(20)
  })
})

describe('duration-history', () => {
  test('returns null for missing file', async () => {
    await withTmpDir((tmpDir) => {
      expect(readDurationHistory(tmpDir, 'missing.json', 0, 'latest')).toBeNull()
    })
  })

  test('migrates legacy numeric entries', async () => {
    await withTmpDir((tmpDir) => {
      writeFileSync(join(tmpDir, 'history.json'), JSON.stringify({ 'test/a.ts': 5000 }))
      expect(readDurationHistory(tmpDir, 'history.json', 0, 'latest')).toEqual(new Map([['test/a.ts', 5000]]))
    })
  })

  test('returns null for corrupt json', async () => {
    await withTmpDir((tmpDir) => {
      writeFileSync(join(tmpDir, 'history.json'), '{invalid')
      expect(readDurationHistory(tmpDir, 'history.json', 0, 'latest')).toBeNull()
    })
  })

  test('writes single observation when maxRuns is 1', async () => {
    await withTmpDir(async (tmpDir) => {
      await writeFileDurations(tmpDir, 'history.json', 1, 0, new Map([['test/a.ts', 1234.6]]))
      const raw = JSON.parse(readFileSync(join(tmpDir, 'history.json'), 'utf8'))
      expect(raw['test/a.ts']).toMatchObject({ duration: 1235 })
      expect(raw['test/a.ts'].recordedAt).toEqual(expect.any(Number))
    })
  })

  test('preserves other files when recording', async () => {
    await withTmpDir(async (tmpDir) => {
      writeFileSync(join(tmpDir, 'history.json'), JSON.stringify({ 'test/existing.ts': { duration: 100, recordedAt: 1 } }))
      await writeFileDurations(tmpDir, 'history.json', 1, 0, new Map([['test/new.ts', 200]]))
      const raw = JSON.parse(readFileSync(join(tmpDir, 'history.json'), 'utf8'))
      expect(raw['test/existing.ts']).toEqual({ duration: 100, recordedAt: 1 })
      expect(raw['test/new.ts'].duration).toBe(200)
    })
  })
})

describe('shard-affinity', () => {
  test('first matching rule wins and clamps shard index', () => {
    const rules = [
      { pattern: 'test/**/*.slow.ts', shardIndex: 5 },
      { pattern: 'test/**/*.ts', shardIndex: 1 },
    ]
    expect(matchAffinityShard('test/a.slow.ts', rules, 3)).toBe(2)
    expect(matchAffinityShard('test/a.ts', rules, 3)).toBe(1)
    expect(matchAffinityShard('other/a.ts', rules, 3)).toBeUndefined()
  })
})
