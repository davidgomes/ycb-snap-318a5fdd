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
  lastInputValues: any[]
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

const selectorIdentityKey = Symbol.for('kea.atomicSelectorIdentity')

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
  try {
    Object.defineProperty(fn, selectorIdentityKey, {
      configurable: true,
      value: selectorIdentity(logic, name),
    })
  } catch {
    // Some user supplied selectors may be non-extensible. The local map still
    // handles the normal build-time wrapper in that case.
  }
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
      lastInputValues: [],
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

function appendPath(path: string, part: string): string {
  return path ? `${path}.${part}` : part
}

export function normalizeDependencies(dependencies: Iterable<string>): string[] {
  const unique = Array.from(new Set(dependencies)).filter(Boolean)
  return unique.filter(
    (dependency) => !unique.some((other) => other !== dependency && other.startsWith(`${dependency}.`)),
  )
}

function collectionPath(path: string, kind: 'map' | 'set', value: any): string {
  return appendPath(path, `${kind}:${String(value)}`)
}

function isArrayIndex(prop: PropertyKey): boolean {
  return typeof prop === 'string' && /^(0|[1-9]\d*)$/.test(prop)
}

function trackedMapIterator(
  iterator: IterableIterator<[any, any]>,
  path: string,
  collect: (dep: string) => void,
  kind: 'entries' | 'keys' | 'values',
): IterableIterator<any> {
  return {
    next() {
      const result = iterator.next()
      if (result.done) {
        collect(appendPath(path, 'size'))
        return result
      }
      const [key, value] = result.value
      collect(collectionPath(path, 'map', key))
      if (kind === 'keys') {
        return { done: false, value: key }
      }
      if (kind === 'values') {
        return {
          done: false,
          value: createTrackingProxy(value, collectionPath(path, 'map', key), collect),
        }
      }
      return {
        done: false,
        value: [key, createTrackingProxy(value, collectionPath(path, 'map', key), collect)],
      }
    },
    [Symbol.iterator]() {
      return this
    },
  } as IterableIterator<any>
}

function trackedSetIterator(
  iterator: IterableIterator<any>,
  path: string,
  collect: (dep: string) => void,
  kind: 'entries' | 'values',
): IterableIterator<any> {
  return {
    next() {
      const result = iterator.next()
      if (result.done) {
        collect(appendPath(path, 'size'))
        return result
      }
      const value = result.value
      collect(collectionPath(path, 'set', value))
      const proxiedValue = createTrackingProxy(value, collectionPath(path, 'set', value), collect)
      return {
        done: false,
        value: kind === 'entries' ? [proxiedValue, proxiedValue] : proxiedValue,
      }
    },
    [Symbol.iterator]() {
      return this
    },
  } as IterableIterator<any>
}

function trackedArrayIterator(
  target: any[],
  path: string,
  collect: (dep: string) => void,
): IterableIterator<any> {
  let index = 0
  return {
    next() {
      if (index >= target.length) {
        collect(appendPath(path, 'length'))
        return { done: true, value: undefined }
      }
      const currentIndex = index++
      const itemPath = appendPath(path, String(currentIndex))
      collect(itemPath)
      return { done: false, value: createTrackingProxy(target[currentIndex], itemPath, collect) }
    },
    [Symbol.iterator]() {
      return this
    },
  } as IterableIterator<any>
}

function trackedArrayEntries(
  target: any[],
  path: string,
  collect: (dep: string) => void,
): IterableIterator<[number, any]> {
  let index = 0
  return {
    next() {
      if (index >= target.length) {
        collect(appendPath(path, 'length'))
        return { done: true, value: undefined }
      }
      const currentIndex = index++
      const itemPath = appendPath(path, String(currentIndex))
      collect(itemPath)
      return {
        done: false,
        value: [currentIndex, createTrackingProxy(target[currentIndex], itemPath, collect)],
      }
    },
    [Symbol.iterator]() {
      return this
    },
  } as IterableIterator<[number, any]>
}

function createTrackedMap(value: Map<any, any>, path: string, collect: (dep: string) => void): any {
  let proxy: any
  proxy = new Proxy(value, {
    get(target, prop) {
      if (prop === 'size') {
        collect(appendPath(path, 'size'))
        return target.size
      }
      if (prop === 'get' || prop === 'has') {
        return (key: any) => {
          const keyPath = collectionPath(path, 'map', key)
          collect(keyPath)
          const result = (target as any)[prop](key)
          return prop === 'get' ? createTrackingProxy(result, keyPath, collect) : result
        }
      }
      if (prop === 'forEach') {
        return (callback: (value: any, key: any, map: Map<any, any>) => void, thisArg?: any) =>
          target.forEach((entryValue, key) => {
            const keyPath = collectionPath(path, 'map', key)
            collect(keyPath)
            callback.call(thisArg, createTrackingProxy(entryValue, keyPath, collect), key, proxy)
          })
      }
      if (prop === 'entries' || prop === Symbol.iterator) {
        return () => trackedMapIterator(target.entries(), path, collect, 'entries')
      }
      if (prop === 'keys') {
        return () => trackedMapIterator(target.entries(), path, collect, 'keys')
      }
      if (prop === 'values') {
        return () => trackedMapIterator(target.entries(), path, collect, 'values')
      }
      const result = Reflect.get(target, prop, target)
      return typeof result === 'function' ? result.bind(target) : result
    },
  })
  return proxy
}

function createTrackedSet(value: Set<any>, path: string, collect: (dep: string) => void): any {
  let proxy: any
  proxy = new Proxy(value, {
    get(target, prop) {
      if (prop === 'size') {
        collect(appendPath(path, 'size'))
        return target.size
      }
      if (prop === 'has') {
        return (item: any) => {
          collect(collectionPath(path, 'set', item))
          return target.has(item)
        }
      }
      if (prop === 'forEach') {
        return (callback: (value: any, key: any, set: Set<any>) => void, thisArg?: any) =>
          target.forEach((item) => {
            const itemPath = collectionPath(path, 'set', item)
            collect(itemPath)
            const proxiedItem = createTrackingProxy(item, itemPath, collect)
            callback.call(thisArg, proxiedItem, proxiedItem, proxy)
          })
      }
      if (prop === 'entries' || prop === 'values' || prop === 'keys' || prop === Symbol.iterator) {
        return () =>
          trackedSetIterator(
            target.values(),
            path,
            collect,
            prop === 'entries' ? 'entries' : 'values',
          )
      }
      const result = Reflect.get(target, prop, target)
      return typeof result === 'function' ? result.bind(target) : result
    },
  })
  return proxy
}

const trackedArrayMethods = new Set([
  'at',
  'concat',
  'copyWithin',
  'entries',
  'every',
  'fill',
  'filter',
  'find',
  'findIndex',
  'findLast',
  'findLastIndex',
  'flat',
  'flatMap',
  'forEach',
  'includes',
  'indexOf',
  'join',
  'keys',
  'lastIndexOf',
  'map',
  'pop',
  'push',
  'reduce',
  'reduceRight',
  'reverse',
  'shift',
  'slice',
  'some',
  'sort',
  'splice',
  'toReversed',
  'toSorted',
  'toSpliced',
  'unshift',
  'values',
  'with',
])

const arrayValueCallbackMethods = new Set([
  'every',
  'filter',
  'find',
  'findIndex',
  'findLast',
  'findLastIndex',
  'forEach',
  'flatMap',
  'map',
  'some',
])

function createTrackedArray(value: any[], path: string, collect: (dep: string) => void): any {
  let proxy: any
  let methodProxy: any

  const handler: ProxyHandler<any[]> = {
    get(target, prop, receiver) {
      if (prop === 'length') {
        if (receiver !== methodProxy) {
          collect(appendPath(path, 'length'))
        }
        return target.length
      }
      if (isArrayIndex(prop)) {
        const itemPath = appendPath(path, String(prop))
        collect(itemPath)
        return createTrackingProxy(target[prop as any], itemPath, collect)
      }
      if (prop === Symbol.iterator) {
        return () => trackedArrayIterator(target, path, collect)
      }
      if (prop === 'entries') {
        return () => trackedArrayEntries(target, path, collect)
      }
      if (prop === 'values') {
        return () => trackedArrayIterator(target, path, collect)
      }
      if (typeof prop === 'string' && trackedArrayMethods.has(prop)) {
        const method = (target as any)[prop]
        return (...args: any[]) => {
          const methodArgs = args.slice()
          let matchedIndex: number | undefined
          if (arrayValueCallbackMethods.has(prop) && typeof methodArgs[0] === 'function') {
            const callback = methodArgs[0]
            const callbackThisArg = methodArgs[1]
            methodArgs[0] = (item: any, index: number, array: any[]) => {
              const result = callback.call(
                callbackThisArg,
                createTrackingProxy(item, appendPath(path, String(index)), collect),
                index,
                proxy,
              )
              if ((prop === 'find' || prop === 'findLast') && result) {
                matchedIndex = index
              }
              return result
            }
          } else if ((prop === 'reduce' || prop === 'reduceRight') && typeof methodArgs[0] === 'function') {
            const callback = methodArgs[0]
            methodArgs[0] = (accumulator: any, item: any, index: number, array: any[]) =>
              callback(accumulator, createTrackingProxy(item, appendPath(path, String(index)), collect), index, proxy)
          }
          const result = method.apply(methodProxy, methodArgs)
          if ((prop === 'find' || prop === 'findLast') && matchedIndex !== undefined) {
            return createTrackingProxy(result, appendPath(path, String(matchedIndex)), collect)
          }
          return result
        }
      }
      const result = Reflect.get(target, prop, receiver)
      return typeof result === 'function' ? result.bind(target) : result
    },
  }

  proxy = new Proxy(value, handler)
  methodProxy = new Proxy(value, {
    get(target, prop) {
      if (prop === 'length') {
        return target.length
      }
      if (isArrayIndex(prop)) {
        const itemPath = appendPath(path, String(prop))
        collect(itemPath)
        return target[prop as any]
      }
      return Reflect.get(target, prop, target)
    },
  })
  return proxy
}

export function createTrackingProxy(value: any, path: string, collect: (dep: string) => void): any {
  if (value === null || value === undefined || typeof value === 'function') {
    return value
  }

  if (value instanceof Map) {
    return createTrackedMap(value, path, collect)
  }

  if (value instanceof Set) {
    return createTrackedSet(value, path, collect)
  }

  if (Array.isArray(value)) {
    return createTrackedArray(value, path, collect)
  }

  if (isPlainObject(value)) {
    return new Proxy(value, {
      get(target, prop) {
        if (typeof prop === 'symbol' || prop === 'then' || prop === 'toJSON' || prop === '$$typeof') {
          return (target as any)[prop]
        }
        const nextPath = appendPath(path, String(prop))
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
      if (current instanceof Map) {
        current = Array.from(current.entries()).find(([mapKey]) => String(mapKey) === key)?.[1]
      } else {
        current = current[part]
      }
    } else if (part.startsWith('set:')) {
      const key = part.slice(4)
      current = current instanceof Set ? Array.from(current).some((item) => String(item) === key) : undefined
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
      const childPath = collectionPath(path, 'map', key)
      if (!prevMap.has(key) || !nextMap.has(key) || !Object.is(prevMap.get(key), nextMap.get(key))) {
        out.push(childPath)
        collectChangedLeaves(prevMap.get(key), nextMap.get(key), childPath, out)
      }
    }
    if (prevMap.size !== nextMap.size) {
      out.push(appendPath(path, 'size'))
    }
    if (path) {
      out.push(path)
    }
    return
  }

  if (prev instanceof Set || next instanceof Set) {
    const prevSet = prev instanceof Set ? prev : new Set()
    const nextSet = next instanceof Set ? next : new Set()
    const values = new Set<any>([...prevSet, ...nextSet])
    for (const value of values) {
      if (prevSet.has(value) !== nextSet.has(value)) {
        out.push(collectionPath(path, 'set', value))
      }
    }
    if (prevSet.size !== nextSet.size) {
      out.push(appendPath(path, 'size'))
    }
    if (path) {
      out.push(path)
    }
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
      out.push(appendPath(path, 'length'))
    }
    for (let i = 0; i < len; i++) {
      collectChangedLeaves(prevArr[i], nextArr[i], appendPath(path, String(i)), out)
    }
    if (path) {
      out.push(path)
    }
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
    for (const dep of getDerivedDependencies(cache, node)) {
      if (cache.nodes[dep]) {
        cache.nodes[dep].dependents.add(node.name)
      }
    }
  }
}

function getDerivedDependencies(cache: AtomicCache, node: AtomicNode): string[] {
  return Array.from(
    new Set(
      node.dependencies
        .concat(node.inputNames.filter((name): name is string => !!name))
        .filter((name) => cache.nodes[name]?.isDerived),
    ),
  )
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
    for (const dep of getDerivedDependencies(cache, node)) {
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

export function invalidateAtomicSelectors(logic: Logic, cause: string): void {
  if (!isAtomicSelectorsEnabled()) {
    return
  }
  const cache = getAtomicCache(logic)
  const seen = new Set<string>()
  for (const node of Object.values(cache.nodes)) {
    if (node.isDerived) {
      markDirty(cache, node.name, cause, seen)
    }
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

function leavesUnchanged(cache: AtomicCache, node: AtomicNode, reducerState: any): boolean {
  for (const dep of node.dependencies) {
    if (dep === '' || dep.startsWith('selector:') || cache.nodes[dep]?.isDerived) {
      continue
    }
    if (!node.lastLeaves || !(dep in node.lastLeaves)) {
      return false
    }
    if (leavesEqual(node.lastLeaves[dep], getValueAtPath(reducerState, dep))) {
      continue
    }
    return false
  }
  return true
}

function selectorInputsUnchanged(cache: AtomicCache, node: AtomicNode, rawInputs: any[]): boolean {
  return node.inputNames.every((inputName, index) => {
    if (inputName && cache.nodes[inputName]?.isDerived) {
      return Object.is(node.lastInputValues[index], rawInputs[index])
    }
    if (!inputName) {
      return Object.is(node.lastInputValues[index], rawInputs[index])
    }
    return true
  })
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

  if (node.hasValue && !node.dirty && leavesUnchanged(cache, node, reducerState)) {
    return node.lastValue
  }

  if (node.computing) {
    throw new Error('[KEA] Circular dependency detected')
  }

  node.computing = true
  try {
    const rawInputs = node.inputFns.map((fn) => fn(storeState, logicProps))
    const selectorInputsChanged = !node.hasValue || !selectorInputsUnchanged(cache, node, rawInputs)
    const leafInputsChanged = !node.hasValue || !leavesUnchanged(cache, node, reducerState)

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
        node.dependencies = normalizeDependencies(collected)
        node.lastLeaves = snapshotLeaves(reducerState, node.dependencies)
        node.lastInputValues = rawInputs
        rebuildDependents(cache)
        node.dirty = false
        node.hasValue = true
        return node.lastValue
      }
    }
    node.lastValue = nextValue
    node.hasValue = true
    node.dependencies = normalizeDependencies(collected)
    node.lastLeaves = snapshotLeaves(reducerState, node.dependencies)
    node.lastInputValues = rawInputs
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
        dependencies: normalizeDependencies(node.dependencies.concat(getDerivedDependencies(cache, node))),
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
  for (const [name, selector] of Object.entries(logic.selectors)) {
    registerSelectorFn(logic, name, selector)
  }
  for (const node of Object.values(cache.nodes)) {
    node.inputNames = resolveInputNames(logic, node.inputFns)
  }
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
  return inputFns.map((fn) => {
    const localName = cache.fnToName.get(fn)
    if (localName) {
      return localName
    }
    const identity = (fn as any)[selectorIdentityKey]
    const prefix = `${logic.pathString}::`
    return typeof identity === 'string' && identity.startsWith(prefix) ? identity.slice(prefix.length) : null
  })
}
