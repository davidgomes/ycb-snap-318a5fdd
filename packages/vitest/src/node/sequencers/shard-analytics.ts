import type { TestSpecification } from '../test-specification'
import type { Vitest } from '../core'
import { relative } from 'pathe'
import { getDuration, readDurationHistory, resolveDurationHistory } from './duration-history'
import { matchShardAffinity } from './shard-affinity'

export async function shardByDuration(ctx: Vitest, files: TestSpecification[]): Promise<TestSpecification[]> {
  const config = ctx.config
  const sequence = config.sequence
  const shard = config.shard!
  const paths = files.map(spec => ({ spec, path: relative(config.root, spec.moduleId).replaceAll('\\', '/') }))
  const history = await readDurationHistory(resolveDurationHistory(config.root, sequence.durationHistoryPath), sequence.durationHistoryTTL)
  const entries = paths.map(entry => ({ ...entry, duration: getDuration(history, entry.path, sequence.durationSmoothing) }))
  if (sequence.isolateSlowThreshold > 0) {
    const slow = entries.filter(e => e.duration > sequence.isolateSlowThreshold).sort((a, b) => b.duration - a.duration)
    const remaining = entries.filter(e => e.duration <= sequence.isolateSlowThreshold)
    if (slow.length) {
      const isolated = Array.from({ length: shard.count }, () => [] as typeof entries)
      slow.forEach((entry, index) => isolated[Math.min(index, shard.count - 1)].push(entry))
      if (slow.length < shard.count) {
        remaining.forEach((entry, index) => isolated[index % shard.count].push(entry))
      } else {
        remaining.forEach(entry => isolated[shard.count - 1].push(entry))
      }
      return isolated[shard.index - 1].map(e => e.spec)
    }
  }
  if (!history && sequence.durationFallbackStrategy === 'equal-split') {
    return entries.sort((a, b) => a.path.localeCompare(b.path)).filter((_, i) => i % shard.count + 1 === shard.index).map(e => e.spec)
  }
  const buckets = Array.from({ length: shard.count }, () => [] as typeof entries)
  const loads = Array.from({ length: shard.count }, () => 0)
  const sorted = entries.sort((a, b) => b.duration - a.duration || a.path.localeCompare(b.path))
  if (sequence.shardStrategy === 'round-robin') {
    let index = 0; let direction = 1
    for (const entry of sorted) {
      buckets[index].push(entry)
      index += direction
      if (index >= shard.count || index < 0) { index = Math.max(0, Math.min(shard.count - 1, index)); direction *= -1 }
    }
  }
  else {
    for (const entry of sorted) {
      const affinity = sequence.shardStrategy === 'affinity' ? matchShardAffinity(entry.path, sequence.shardAffinityRules) : undefined
      const index = affinity === undefined ? loads.indexOf(Math.min(...loads)) : Math.min(affinity, shard.count - 1)
      buckets[index].push(entry); loads[index] += entry.duration
    }
  }
  const ratio = Math.min(...loads) / Math.max(...loads, 1)
  if (sequence.rebalanceThreshold > 0 && ratio < sequence.rebalanceThreshold) {
    ctx.logger.warn(`Shard load imbalance: ratio=${ratio.toFixed(2)} threshold=${sequence.rebalanceThreshold.toFixed(2)}`)
  }
  return buckets[shard.index - 1].map(e => e.spec)
}
