import { BuiltLogic, Logic, Selector } from '../types'
import { getContext, getStoreState } from '../kea/context'

export type SelectorHealthEntry = {
  dependencies: string[]
  dependents: string[]
  evaluations: number
  dirtyCause: string | null
}

export type SelectorHealth = {
  selectors: Record<string, SelectorHealthEntry>
  topologicalOrder: string[]
}

export type AtomicNode = {
  name: string
  identity: string
  inputFns: Selector[]
  inputNames: (string | null)[]
  compute: (...args: any[]) => any
  memoizeOptions?: any
  lastValue: any
  hasValue: boolean
  lastLeaves: Record<string, any>
  lastSelectorInputs: Record<string, any>
  dependencies: string[]
  dependents: Set<string>
  evaluations: number
  dirty: boolean
  dirtyCause: string | null
  computing: boolean
  isDerived: boolean
}

export type AtomicCache = {
  nodes: Record<string, AtomicNode>
  fnToName: Map<Function, string>
  lastReducerState: any
  initialized: boolean
}

export function isAtomicSelectorsEnabled(): boolean {
  try {
    return !!(getContext() as any)?.options?.atomicSelectors
  } catch {
    return false
  }
}

export function selectorIdentity(logic: Logic, name: string): string {
  return `${logic.pathString}::${name}`
}

export function getAtomicCache(logic: Logic): AtomicCache {
  if (!logic.cache.atomicSelectors) {
    logic.cache.atomicSelectors = {
      nodes: {},
      fnToName: new Map(),
      lastReducerState: undefined,
      initialized: false,
    } as AtomicCache
  }
  return logic.cache.atomicSelectors as AtomicCache
}

export function registerSelectorFn(logic: Logic, name: string, fn: Function): void {
  if (!isAtomicSelectorsEnabled()) {
    return
  }
  const cache = getAtomicCache(logic)
  cache.fnToName.set(fn, name)
}

export function ensureAtomicNode(
  logic: Logic,
  name: string,
  options: Partial<Pick<AtomicNode, 'inputFns' | 'inputNames' | 'compute' | 'memoizeOptions' | 'isDerived'>> = {},
): AtomicNode {
  const cache = getAtomicCache(logic)
  if (!cache.nodes[name]) {
    cache.nodes[name] = {
      name,
      identity: selectorIdentity(logic, name),
      inputFns: options.inputFns ?? [],
      inputNames: options.inputNames ?? [],
      compute: options.compute ?? ((v) => v),
      memoizeOptions: options.memoizeOptions,
      lastValue: undefined,
      hasValue: false,
      lastLeaves: {},
      lastSelectorInputs: {},
      dependencies: [],
      dependents: new Set(),
      evaluations: 0,
      dirty: true,
      dirtyCause: null,
      computing: false,
      isDerived: options.isDerived ?? false,
    }
  } else {
    const node = cache.nodes[name]
    if (options.inputFns) {
      node.inputFns = options.inputFns
    }
    if (options.inputNames) {
      node.inputNames = options.inputNames
    }
    if (options.compute) {
      node.compute = options.compute
    }
    if (options.memoizeOptions !== undefined) {
      node.memoizeOptions = options.memoizeOptions
    }
    if (options.isDerived !== undefined) {
      node.isDerived = options.isDerived
    }
  }
  return cache.nodes[name]
}

function isPlainObject(value: any): boolean {
  if (value === null || typeof value !== 'object') {
    return false
  }
  const proto = Object.getPrototypeOf(value)
  return proto === Object.prototype || proto === null
}

function cloneLeafValue(value: any): any {
  if (value instanceof Map) {
    return new Map(value)
  }
  if (value instanceof Set) {
    return new Set(value)
  }
  return value
}

function leavesEqual(a: any, b: any): boolean {
  if (a instanceof Map && b instanceof Map) {
    if (a.size !== b.size) {
      return false
    }
    for (const [k, v] of a) {
      if (!b.has(k) || !Object.is(b.get(k), v)) {
        return false
      }
    }
    return true
  }
  if (a instanceof Set && b instanceof Set) {
    if (a.size !== b.size) {
      return false
    }
    for (const v of a) {
      if (!b.has(v)) {
        return false
      }
    }
    return true
  }
  return Object.is(a, b)
}

export function createTrackingProxy(value: any, path: string, collect: (dep: string) => void): any {
  if (value === null || value === undefined) {
    return value
  }

  if (value instanceof Map) {
    return new Proxy(value, {
      get(target, prop, receiver) {
        if (prop === 'get' || prop === 'has') {
          return (key: any) => {
            collect(`${path}.map:${key}`)
            const result = (target as any)[prop](key)
            return prop === 'get' ? createTrackingProxy(result, `${path}.map:${key}`, collect) : result
          }
        }
        const result = Reflect.get(target, prop, receiver)
        return typeof result === 'function' ? result.bind(target) : result
      },
    })
  }

  if (value instanceof Set) {
    return new Proxy(value, {
      get(target, prop, receiver) {
        if (prop === 'has') {
          return (key: any) => {
            collect(`${path}.set:${key}`)
            return target.has(key)
          }
        }
        const result = Reflect.get(target, prop, receiver)
        return typeof result === 'function' ? result.bind(target) : result
      },
    })
  }

  if (Array.isArray(value)) {
    return new Proxy(value, {
      get(target, prop, receiver) {
        if (prop === 'includes') {
          return (search: any) => {
            for (let i = 0; i < target.length; i++) {
              collect(`${path}.${i}`)
              if (Object.is(target[i], search)) {
                return true
              }
            }
            collect(`${path}.length`)
            return false
          }
        }
        if (prop === 'length' || (typeof prop === 'string' && /^\d+$/.test(prop))) {
          collect(`${path}.${String(prop)}`)
          const result = (target as any)[prop]
          return createTrackingProxy(result, `${path}.${String(prop)}`, collect)
        }
        const result = Reflect.get(target, prop, receiver)
        return typeof result === 'function' ? result.bind(target) : result
      },
    })
  }

  if (isPlainObject(value)) {
    return new Proxy(value, {
      get(target, prop) {
        if (typeof prop === 'symbol' || prop === 'then' || prop === 'toJSON' || prop === '$$typeof') {
          return (target as any)[prop]
        }
        const nextPath = path ? `${path}.${String(prop)}` : String(prop)
        collect(nextPath)
        return createTrackingProxy((target as any)[prop], nextPath, collect)
      },
    })
  }

  return value
}

export function getValueAtPath(root: any, path: string): any {
  if (!path) {
    return root
  }
  const parts = path.split('.')
  let current = root
  for (const part of parts) {
    if (current === null || current === undefined) {
      return undefined
    }
    if (part.startsWith('map:')) {
      const key = part.slice(4)
      current = current instanceof Map ? current.get(key) : current[key]
    } else if (part.startsWith('set:')) {
      const key = part.slice(4)
      current = current instanceof Set ? current.has(key) : undefined
    } else if (part.startsWith('includes:')) {
      const key = part.slice(9)
      current = Array.isArray(current) ? current.includes(key) : undefined
    } else {
      current = current[part]
    }
  }
  return current
}

function collectChangedLeaves(prev: any, next: any, path: string, out: string[]): void {
  if (leavesEqual(prev, next)) {
    return
  }

  if (prev instanceof Map || next instanceof Map) {
    const prevMap = prev instanceof Map ? prev : new Map()
    const nextMap = next instanceof Map ? next : new Map()
    const keys = new Set<any>([...prevMap.keys(), ...nextMap.keys()])
    for (const key of keys) {
      const childPath = `${path}.map:${key}`
      if (!prevMap.has(key) || !nextMap.has(key) || !Object.is(prevMap.get(key), nextMap.get(key))) {
        out.push(childPath)
        collectChangedLeaves(prevMap.get(key), nextMap.get(key), childPath, out)
      }
    }
    out.push(path)
    return
  }

  if (prev instanceof Set || next instanceof Set) {
    const prevSet = prev instanceof Set ? prev : new Set()
    const nextSet = next instanceof Set ? next : new Set()
    const values = new Set<any>([...prevSet, ...nextSet])
    for (const value of values) {
      if (prevSet.has(value) !== nextSet.has(value)) {
        out.push(`${path}.set:${value}`)
      }
    }
    out.push(path)
    return
  }

  const prevIsObj = prev !== null && typeof prev === 'object'
  const nextIsObj = next !== null && typeof next === 'object'

  if (!prevIsObj || !nextIsObj) {
    if (path) {
      out.push(path)
    }
    return
  }

  if (Array.isArray(prev) || Array.isArray(next)) {
    const prevArr = Array.isArray(prev) ? prev : []
    const nextArr = Array.isArray(next) ? next : []
    const len = Math.max(prevArr.length, nextArr.length)
    if (prevArr.length !== nextArr.length) {
      out.push(`${path}.length`)
    }
    for (let i = 0; i < len; i++) {
      collectChangedLeaves(prevArr[i], nextArr[i], `${path}.${i}`, out)
    }
    out.push(path)
    return
  }

  const keys = new Set([...Object.keys(prev), ...Object.keys(next)])
  for (const key of keys) {
    collectChangedLeaves(prev[key], next[key], path ? `${path}.${key}` : key, out)
  }
  if (path) {
    out.push(path)
  }
}

function rebuildDependents(cache: AtomicCache): void {
  for (const node of Object.values(cache.nodes)) {
    node.dependents.clear()
  }
  for (const node of Object.values(cache.nodes)) {
    for (const dep of node.dependencies) {
      if (cache.nodes[dep]) {
        cache.nodes[dep].dependents.add(node.name)
      }
    }
  }
}

function topologicalOrder(cache: AtomicCache): string[] {
  const names = Object.keys(cache.nodes)
  const incoming: Record<string, number> = {}
  const edges: Record<string, string[]> = {}
  for (const name of names) {
    incoming[name] = 0
    edges[name] = []
  }
  for (const node of Object.values(cache.nodes)) {
    for (const dep of node.dependencies) {
      if (cache.nodes[dep]) {
        edges[dep].push(node.name)
        incoming[node.name]++
      }
    }
  }
  const queue = names.filter((n) => incoming[n] === 0)
  const order: string[] = []
  while (queue.length) {
    const next = queue.shift()!
    order.push(next)
    for (const dest of edges[next]) {
      incoming[dest]--
      if (incoming[dest] === 0) {
        queue.push(dest)
      }
    }
  }
  for (const name of names) {
    if (!order.includes(name)) {
      order.push(name)
    }
  }
  return order
}

export function detectCircularDependencies(logic: Logic): void {
  const cache = getAtomicCache(logic)
  const visiting = new Set<string>()
  const visited = new Set<string>()

  const visit = (name: string) => {
    if (visited.has(name)) {
      return
    }
    if (visiting.has(name)) {
      throw new Error('[KEA] Circular dependency detected')
    }
    visiting.add(name)
    const node = cache.nodes[name]
    if (node) {
      for (const dep of node.inputNames) {
        if (dep && cache.nodes[dep]) {
          visit(dep)
        }
      }
      for (const dep of node.dependencies) {
        if (cache.nodes[dep]) {
          visit(dep)
        }
      }
    }
    visiting.delete(name)
    visited.add(name)
  }

  for (const name of Object.keys(cache.nodes)) {
    visit(name)
  }
}

function markDirty(cache: AtomicCache, name: string, cause: string, seen: Set<string> = new Set()): void {
  if (seen.has(name)) {
    return
  }
  seen.add(name)
  const node = cache.nodes[name]
  if (!node) {
    return
  }
  node.dirty = true
  node.dirtyCause = cause
  for (const dependent of node.dependents) {
    markDirty(cache, dependent, `selector:${name}`, seen)
  }
}

export function processLogicStateChange(logic: BuiltLogic): string[] {
  if (!isAtomicSelectorsEnabled() || !logic.selector) {
    return []
  }
  const cache = getAtomicCache(logic)
  let nextState: any
  try {
    nextState = logic.selector(getStoreState())
  } catch {
    return []
  }
  const changed: string[] = []
  collectChangedLeaves(cache.lastReducerState, nextState, '', changed)
  cache.lastReducerState = nextState

  const unique = Array.from(new Set(changed.filter(Boolean)))
  const seen = new Set<string>()
  for (const node of Object.values(cache.nodes)) {
    if (!node.isDerived) {
      continue
    }
    const hit = node.dependencies.find((dep) => unique.includes(dep))
    if (hit) {
      markDirty(cache, node.name, hit, seen)
    }
  }
  return unique
}

export function flushAtomicSelectors(): void {
  if (!isAtomicSelectorsEnabled()) {
    return
  }
  const { mount } = getContext()
  for (const logic of Object.values(mount.mounted)) {
    processLogicStateChange(logic)
  }
}

function snapshotLeaves(reducerState: any, deps: string[]): Record<string, any> {
  const result: Record<string, any> = {}
  for (const dep of deps) {
    result[dep] = cloneLeafValue(getValueAtPath(reducerState, dep))
  }
  return result
}

function leavesUnchanged(node: AtomicNode, reducerState: any): boolean {
  for (const dep of node.dependencies) {
    if (node.lastSelectorInputs && dep in node.lastSelectorInputs) {
      continue
    }
    if (!leavesEqual(node.lastLeaves[dep], getValueAtPath(reducerState, dep))) {
      return false
    }
  }
  return true
}

export function evaluateAtomicSelector(logic: Logic, name: string, state?: any, props?: any): any {
  const cache = getAtomicCache(logic)
  const node = cache.nodes[name]
  if (!node) {
    return undefined
  }

  const storeState = state ?? getStoreState()
  const logicProps = props ?? logic.props
  let reducerState: any
  try {
    reducerState = logic.selector ? logic.selector(storeState) : undefined
  } catch {
    reducerState = undefined
  }

  if (node.hasValue && !node.dirty && leavesUnchanged(node, reducerState)) {
    return node.lastValue
  }

  if (node.computing) {
    throw new Error('[KEA] Circular dependency detected')
  }

  node.computing = true
  try {
    const rawInputs = node.inputFns.map((fn) => fn(storeState, logicProps))
    const selectorInputsChanged = node.inputNames.some((inputName, index) => {
      if (!inputName || !cache.nodes[inputName]?.isDerived) {
        return false
      }
      return !node.hasValue || !Object.is(node.lastSelectorInputs[inputName], rawInputs[index])
    })
    const leafInputsChanged = !node.hasValue || !leavesUnchanged(node, reducerState)

    if (node.hasValue && !selectorInputsChanged && !leafInputsChanged) {
      node.dirty = false
      return node.lastValue
    }

    const collected = new Set<string>()
    const inputNestedCounts: number[] = []
    const proxiedInputs = rawInputs.map((value, index) => {
      const inputName = node.inputNames[index]
      if (inputName && cache.nodes[inputName]?.isDerived) {
        collected.add(inputName)
        inputNestedCounts[index] = -1
        return value
      }
      const rootPath = inputName || ''
      inputNestedCounts[index] = 0
      return createTrackingProxy(value, rootPath, (dep) => {
        if (dep) {
          inputNestedCounts[index] += 1
          collected.add(dep)
        }
      })
    })

    node.evaluations += 1
    const nextValue = node.compute(...proxiedInputs)
    node.inputNames.forEach((inputName, index) => {
      if (inputName && inputNestedCounts[index] === 0 && !cache.nodes[inputName]?.isDerived) {
        collected.add(inputName)
      }
    })
    if (node.memoizeOptions?.resultEqualityCheck && node.hasValue) {
      if (node.memoizeOptions.resultEqualityCheck(node.lastValue, nextValue)) {
        node.dependencies = Array.from(collected)
        node.lastLeaves = snapshotLeaves(reducerState, node.dependencies)
        node.lastSelectorInputs = Object.fromEntries(
          node.inputNames
            .map((n, i) => [n, rawInputs[i]] as const)
            .filter((entry): entry is [string, any] => !!entry[0]),
        )
        rebuildDependents(cache)
        node.dirty = false
        node.hasValue = true
        return node.lastValue
      }
    }
    node.lastValue = nextValue
    node.hasValue = true
    node.dependencies = Array.from(collected)
    node.lastLeaves = snapshotLeaves(reducerState, node.dependencies)
    node.lastSelectorInputs = Object.fromEntries(
      node.inputNames
        .map((n, i) => [n, rawInputs[i]] as const)
        .filter((entry): entry is [string, any] => !!entry[0]),
    )
    rebuildDependents(cache)
    node.dirty = false
    return node.lastValue
  } finally {
    node.computing = false
  }
}

export function attachSelectorHealth(logic: BuiltLogic): void {
  if (!isAtomicSelectorsEnabled()) {
    delete (logic as any).selectorHealth
    return
  }

  logic.selectorHealth = () => {
    const cache = getAtomicCache(logic)
    const selectors: Record<string, SelectorHealthEntry> = {}
    for (const [name, node] of Object.entries(cache.nodes)) {
      if (!node.isDerived) {
        continue
      }
      selectors[name] = {
        dependencies: [...node.dependencies],
        dependents: Array.from(node.dependents),
        evaluations: node.evaluations,
        dirtyCause: node.dirtyCause,
      }
    }
    return {
      selectors,
      topologicalOrder: topologicalOrder(cache).filter((name) => cache.nodes[name]?.isDerived),
    }
  }
}

export function finalizeAtomicLogic(logic: BuiltLogic): void {
  if (!isAtomicSelectorsEnabled()) {
    return
  }
  const cache = getAtomicCache(logic)
  detectCircularDependencies(logic)
  rebuildDependents(cache)
  attachSelectorHealth(logic)
  cache.initialized = true
  try {
    cache.lastReducerState = logic.selector ? logic.selector(getStoreState()) : undefined
  } catch {
    cache.lastReducerState = undefined
  }
}

export function resolveInputNames(logic: Logic, inputFns: Selector[]): (string | null)[] {
  const cache = getAtomicCache(logic)
  return inputFns.map((fn) => cache.fnToName.get(fn) ?? null)
}
