import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'pathe'
import type { DurationObservation } from './duration-smoothing'
import { smoothDurations } from './duration-smoothing'

type Entry = number | { duration: number, recordedAt: number } | { observations: DurationObservation[] }
export type DurationHistory = Record<string, DurationObservation[]>

export async function readDurationHistory(path: string, ttl: number): Promise<DurationHistory | null> {
  let value: unknown
  try { value = JSON.parse(await readFile(path, 'utf8')) } catch { return null }
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  const cutoff = Date.now() - ttl
  const result: DurationHistory = {}
  for (const [file, entry] of Object.entries(value)) {
    const observations = typeof entry === 'number'
      ? [{ duration: entry, recordedAt: 0 }]
      : entry && typeof entry === 'object' && 'observations' in entry
        ? entry.observations
        : entry && typeof entry === 'object' && 'duration' in entry
          ? [entry]
          : []
    if (!Array.isArray(observations)) continue
    result[file] = observations.filter(o =>
      o && typeof o.duration === 'number' && typeof o.recordedAt === 'number'
      && (ttl === 0 || o.recordedAt === 0 || o.recordedAt >= cutoff),
    )
  }
  return result
}

export async function recordFileDurations(
  path: string,
  durations: Record<string, number>,
  maxRuns: number,
): Promise<void> {
  const old = await readDurationHistory(path, 0) ?? {}
  const now = Date.now()
  const output: Record<string, Entry> = {}
  for (const [file, observations] of Object.entries(old)) {
    output[file] = maxRuns === 1
      ? { duration: observations.at(-1)?.duration ?? 0, recordedAt: observations.at(-1)?.recordedAt ?? 0 }
      : { observations: observations.slice(-maxRuns) }
  }
  for (const [file, duration] of Object.entries(durations)) {
    const observations = [...(old[file] ?? []), { duration: Math.round(duration), recordedAt: now }].slice(-maxRuns)
    output[file] = maxRuns === 1 ? observations[0] : { observations }
  }
  await mkdir(dirname(path), { recursive: true })
  await writeFile(path, JSON.stringify(output, null, 2))
}

export function getDuration(history: DurationHistory | null, file: string, smoothing: 'latest' | 'average' | 'p95' | 'median'): number {
  return smoothDurations(history?.[file] ?? [], smoothing)
}

export function resolveDurationHistory(root: string, path: string): string {
  return resolve(root, path)
}
