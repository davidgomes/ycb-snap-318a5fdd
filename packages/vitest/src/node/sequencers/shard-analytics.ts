export interface ShardItem {
  key: string
  duration: number
}

export function lptAssign(items: ShardItem[], count: number, loads: number[], assignment: Map<string, number>): void {
  const sorted = [...items].sort((a, b) => b.duration - a.duration || (a.key < b.key ? -1 : a.key > b.key ? 1 : 0))
  for (const item of sorted) {
    let target = 0
    for (let i = 1; i < count; i++) {
      if (loads[i] < loads[target]) {
        target = i
      }
    }
    loads[target] += item.duration
    assignment.set(item.key, target)
  }
}

export function roundRobinAssign(items: ShardItem[], count: number, loads: number[], assignment: Map<string, number>): void {
  const sorted = [...items].sort((a, b) => b.duration - a.duration || (a.key < b.key ? -1 : a.key > b.key ? 1 : 0))
  let pointer = 0
  let direction = 1
  for (const item of sorted) {
    loads[pointer] += item.duration
    assignment.set(item.key, pointer)
    pointer += direction
    if (pointer >= count) {
      pointer = count - 1
      direction = -1
    }
    else if (pointer < 0) {
      pointer = 0
      direction = 1
    }
  }
}

export function computeLoadRatio(loads: number[]): number {
  if (!loads.length) {
    return 1
  }
  const max = Math.max(...loads)
  const min = Math.min(...loads)
  return max === 0 ? 1 : min / max
}
