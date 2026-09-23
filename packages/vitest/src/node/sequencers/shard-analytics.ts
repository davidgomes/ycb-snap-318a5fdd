export function rebalanceWarning(loads: readonly number[], threshold: number): string | undefined {
  if (!(threshold > 0) || loads.length === 0) {
    return undefined
  }
  let minLoad = loads[0]!
  let maxLoad = loads[0]!
  for (const load of loads) {
    if (load < minLoad) {
      minLoad = load
    }
    if (load > maxLoad) {
      maxLoad = load
    }
  }
  if (!(maxLoad > 0)) {
    return undefined
  }
  const ratio = minLoad / maxLoad
  if (ratio < threshold) {
    return `Shard load is imbalanced: ratio=${ratio.toFixed(2)} threshold=${threshold.toFixed(2)}`
  }
  return undefined
}
