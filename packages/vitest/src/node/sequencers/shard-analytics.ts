import type { Logger } from '../logger'

export interface ShardItem<T = unknown> {
  item: T
  path: string
  duration: number
}

export interface ShardBalance {
  loads: number[]
  minLoad: number
  maxLoad: number
  ratio: number
}

export function computeShardLoads(shards: ShardItem[][]): number[] {
  return shards.map(shard => shard.reduce((total, item) => total + item.duration, 0))
}

export function getShardBalance(shards: ShardItem[][]): ShardBalance {
  const loads = computeShardLoads(shards)
  const minLoad = loads.length ? Math.min(...loads) : 0
  const maxLoad = loads.length ? Math.max(...loads) : 0
  const ratio = maxLoad > 0 ? minLoad / maxLoad : 1
  return { loads, minLoad, maxLoad, ratio }
}

export function warnIfUnbalanced(
  shards: ShardItem[][],
  threshold: number,
  logger: Pick<Logger, 'warn'> | undefined,
): ShardBalance | undefined {
  if (!threshold || threshold <= 0) {
    return undefined
  }
  const balance = getShardBalance(shards)
  if (balance.ratio < threshold) {
    logger?.warn(
      `Shards are unbalanced: ratio=${balance.ratio.toFixed(2)} is below threshold=${threshold.toFixed(2)} `
      + `(min load ${balance.minLoad}ms, max load ${balance.maxLoad}ms). `
      + 'Consider recording file durations or adjusting the shard strategy.',
    )
  }
  return balance
}
