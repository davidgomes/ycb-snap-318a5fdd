import type { Logger } from '../logger'
import c from 'tinyrainbow'

export function getShardLoads(shards: { duration: number }[][]): number[] {
  return shards.map(shard => shard.reduce((total, { duration }) => total + duration, 0))
}

export function warnUnbalancedShards(
  logger: Pick<Logger, 'warn'>,
  loads: number[],
  threshold: number,
): void {
  if (threshold <= 0 || !loads.length) {
    return
  }
  const maxLoad = Math.max(...loads)
  if (maxLoad <= 0) {
    return
  }
  const minLoad = Math.min(...loads)
  const ratio = minLoad / maxLoad
  if (ratio < threshold) {
    logger.warn(c.yellow(
      `Shards are unbalanced: the least loaded shard (${minLoad}ms) and the most loaded shard (${maxLoad}ms) `
      + `have ratio=${ratio.toFixed(2)}, which is below threshold=${threshold.toFixed(2)}.`,
    ))
  }
}
