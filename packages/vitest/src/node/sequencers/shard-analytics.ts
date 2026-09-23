export interface ShardBalance {
  loads: number[]
  minLoad: number
  maxLoad: number
  ratio: number
}

export function getShardLoads<T>(groups: T[][], getDuration: (item: T) => number): number[] {
  return groups.map(group => group.reduce((load, item) => load + getDuration(item), 0))
}

export function getShardBalance(loads: number[]): ShardBalance | undefined {
  if (!loads.length) {
    return undefined
  }
  const minLoad = Math.min(...loads)
  const maxLoad = Math.max(...loads)
  if (maxLoad <= 0) {
    return undefined
  }
  return { loads, minLoad, maxLoad, ratio: minLoad / maxLoad }
}

export function formatShardImbalanceWarning(balance: ShardBalance, threshold: number): string {
  return `Shard load imbalance detected: ratio=${balance.ratio.toFixed(2)} is below threshold=${threshold.toFixed(2)} `
    + `(min=${Math.round(balance.minLoad)}ms, max=${Math.round(balance.maxLoad)}ms, loads=[${balance.loads.map(Math.round).join(', ')}])`
}
