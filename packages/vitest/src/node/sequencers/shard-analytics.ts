import type { Logger } from '../logger'

export function warnRebalanceThreshold(
  logger: Logger,
  loads: number[],
  threshold: number,
): void {
  if (threshold <= 0 || loads.length === 0) {
    return
  }

  const maxLoad = Math.max(...loads)
  const minLoad = Math.min(...loads)

  if (maxLoad === 0) {
    return
  }

  const ratio = minLoad / maxLoad
  if (ratio < threshold) {
    logger.warn(`Shard load imbalance detected: ratio=${ratio.toFixed(2)} threshold=${threshold.toFixed(2)}`)
  }
}
