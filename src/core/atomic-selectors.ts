import { BuiltLogic, KeaPlugin, Logic, Selector, SelectorHealth } from '../types'
import { getContext, getStoreState } from '../kea/context'

/**
 * Fine-grained selector graph. Nodes are keyed by `${logic.pathString}::${name}` so health
 * stays attached when Kea wraps selector functions during build.
 */

const RAW = Symbol('kea.atomic.raw')
const proxyRaw = new WeakMap<object, any>()

const CACHE_KEY = '__atomicSelectors'

type NodeKind = 'reducer' | 'derived' | 'linked'

interface Dep {
  id: string
  /** Shown in selectorHealth().dependencies */
  report: boolean
  value: any
  cause: string
  kind: 'leaf' | 'selector' | 'external'
  read: (state: any, props: any) => any
}

interface AtomicInput {
  type: 'selector' | 'external'
  name?: string
  invoke: (state: any, props: any) => any
}

interface AtomicNode {
  id: string
  name: string
  kind: NodeKind
  staticDeps: string[]
  dependencies: string[]
  dependentNames: string[]
  deps: Dep[]
  inputs: AtomicInput[]
  compute: ((...args: any[]) => any) | null
  resultEqualityCheck: ((previous: any, next: any) => boolean) | null
  evaluations: number
  dirtyCause: string | null
  committedDirtyCause: string | null
  optimisticDirty: boolean
  dirty: boolean
  hasValue: boolean
  value: any
  cachedState: any
  computing: boolean
}

interface AtomicState {
  nodes: Record<string, AtomicNode>
  definitionOrder: string[]
  knownFns: Map<Function, string>
  byId: Map<string, AtomicNode>
}

interface TrackSpec {
  id: string
  read: (state: any) => any
}

interface ProxyMeta {
  id: string
  raw: any
  read: (state: any) => any
  accessed: boolean
  sawKeys: boolean
}

interface PendingPrimitive {
  id: string
  value: any
  read: (state: any) => any
  cause: string
}

const trackerStack: Tracker[] = []
const subscribedStores = new WeakSet<object>()
let refreshing = false

function topTracker(): Tracker | null {
  return trackerStack.length ? trackerStack[trackerStack.length - 1] : null
}

export function isAtomicEnabled(): boolean {
  const context = getContext()
  return !!context && context.options.atomicSelectors === true
}

function stableId(logic: Logic, name: string): string {
  return `${logic.pathString}::${name}`
}

function refreshMountedSelectors(): void {
  if (refreshing) {
    return
  }
  const context = getContext()
  if (!context?.mount?.mounted) {
    return
  }
  refreshing = true
  try {
    for (const logic of Object.values(context.mount.mounted)) {
      refreshLogic(logic)
    }
  } finally {
    refreshing = false
  }
}

function refreshLogic(logic: Logic): void {
  const atomic = logic.cache?.[CACHE_KEY] as AtomicState | undefined
  if (!atomic) {
    return
  }
  let state: any
  try {
    state = getStoreState()
  } catch (error) {
    return
  }
  const props = logic.props
  for (const name of topologicalOrder(atomic)) {
    const node = atomic.nodes[name]
    if (node?.kind === 'derived' && node.hasValue && !node.computing) {
      try {
        evaluate(logic, node, state, props)
      } catch (error) {
        // Keep the dispatch alive; the next direct read surfaces the selector error.
        node.dirty = true
        node.cachedState = undefined
      }
    }
  }
}

function ensureStoreSubscription(): void {
  const context = getContext()
  const store = context?.__store
  if (!store || subscribedStores.has(store)) {
    return
  }
  subscribedStores.add(store)
  store.subscribe(() => {
    const current = getContext()
    if (!current || current.__store !== store) {
      return
    }
    refreshMountedSelectors()
  })
}

function ensureAtomic(logic: Logic): AtomicState {
  if (!logic.cache[CACHE_KEY]) {
    logic.cache[CACHE_KEY] = {
      nodes: {},
      definitionOrder: [],
      knownFns: new Map<Function, string>(),
      byId: new Map<string, AtomicNode>(),
    } as AtomicState
  }
  const atomic = logic.cache[CACHE_KEY] as AtomicState
  if (!logic.selectorHealth) {
    logic.selectorHealth = () => buildHealth(logic)
  }
  ensureStoreSubscription()
  return atomic
}

function ensureNode(atomic: AtomicState, logic: Logic, name: string, kind: NodeKind): AtomicNode {
  const existing = atomic.nodes[name]
  if (existing) {
    if (existing.kind === 'linked' && kind !== 'linked') {
      existing.kind = kind
    }
    return existing
  }
  const node: AtomicNode = {
    id: stableId(logic, name),
    name,
    kind,
    staticDeps: [],
    dependencies: [],
    dependentNames: [],
    deps: [],
    inputs: [],
    compute: null,
    resultEqualityCheck: null,
    evaluations: 0,
    dirtyCause: null,
    committedDirtyCause: null,
    optimisticDirty: false,
    dirty: false,
    hasValue: false,
    value: undefined,
    cachedState: undefined,
    computing: false,
  }
  atomic.nodes[name] = node
  atomic.byId.set(node.id, node)
  atomic.definitionOrder.push(name)
  return node
}

export function registerKnownFunction(logic: Logic, name: string, fn: Function): void {
  if (!isAtomicEnabled()) {
    return
  }
  const atomic = ensureAtomic(logic)
  atomic.knownFns.set(fn, name)
  // Touch the stable id so a later wrapper still resolves to this selector's node.
  ensureNode(atomic, logic, name, atomic.nodes[name]?.kind ?? 'derived')
  atomic.byId.set(stableId(logic, name), atomic.nodes[name])
}

function readReducerValue(logic: Logic, key: string, state?: any): any {
  if (!logic.selector) {
    return undefined
  }
  const slice = logic.selector(state ?? getStoreState())
  if (slice == null) {
    return undefined
  }
  return slice[key]
}

function rememberReducerValue(node: AtomicNode, value: any): void {
  if (!node.hasValue || !Object.is(node.value, value)) {
    if (node.hasValue) {
      node.dirtyCause = node.name
      node.committedDirtyCause = node.name
    }
    node.evaluations += 1
    node.value = value
    node.hasValue = true
  }
}

export function createReducerSelector(logic: Logic, key: string): Selector {
  const atomic = ensureAtomic(logic)
  const node = ensureNode(atomic, logic, key, 'reducer')
  const read = (state?: any) => readReducerValue(logic, key, state)
  const fn: Selector = (state = getStoreState()) => {
    const value = read(state)
    rememberReducerValue(node, value)
    const tracker = topTracker()
    if (!tracker) {
      return value
    }
    if (value !== null && typeof value === 'object') {
      return tracker.track(value, { id: key, read })
    }
    tracker.notePrimitive({ id: key, value, read, cause: key })
    return value
  }
  atomic.knownFns.set(fn, key)
  atomic.byId.set(node.id, node)
  return fn
}

function describeInput(logic: Logic, fn: Selector): AtomicInput {
  const atomic = ensureAtomic(logic)
  const known = atomic.knownFns.get(fn)
  if (known && logic.selectors[known]) {
    return {
      type: 'selector',
      name: known,
      invoke: (state, props) => logic.selectors[known](state, props),
    }
  }
  for (const name of Object.keys(logic.selectors)) {
    if (logic.selectors[name] === fn) {
      return {
        type: 'selector',
        name,
        invoke: (state, props) => logic.selectors[name](state, props),
      }
    }
  }
  return { type: 'external', invoke: fn }
}

export function trackSelectorInputs(
  logic: Logic,
  key: string,
  input: (selectors: Record<string, Selector>, props: any) => Selector[],
  propSelectors: any,
): Selector[] {
  const atomic = ensureAtomic(logic)
  const node = ensureNode(atomic, logic, key, 'derived')
  const staticDeps: string[] = []
  const seen = new Set<string>()
  const proxy = new Proxy(logic.selectors, {
    get(target, prop, receiver) {
      if (typeof prop === 'string' && Object.prototype.hasOwnProperty.call(target, prop) && !seen.has(prop)) {
        seen.add(prop)
        staticDeps.push(prop)
      }
      return Reflect.get(target, prop, receiver)
    },
  })
  const fns = input(proxy, propSelectors) || []
  node.inputs = fns.map((fn) => describeInput(logic, fn))
  for (const item of node.inputs) {
    if (item.type === 'selector' && item.name && !seen.has(item.name)) {
      seen.add(item.name)
      staticDeps.push(item.name)
    }
  }
  node.staticDeps = staticDeps
  return fns
}

export function createDerivedSelector(
  logic: Logic,
  key: string,
  compute: (...args: any[]) => any,
  memoizeOptions?: { resultEqualityCheck?: (previous: any, next: any) => boolean },
): Selector {
  const atomic = ensureAtomic(logic)
  const node = ensureNode(atomic, logic, key, 'derived')
  node.compute = compute
  node.resultEqualityCheck = memoizeOptions?.resultEqualityCheck ?? null
  const fn: Selector = (state = getStoreState(), props = logic.props) => evaluate(logic, node, state, props)
  atomic.knownFns.set(fn, key)
  atomic.byId.set(node.id, node)
  return fn
}

function isTrackable(value: any): boolean {
  if (value === null || typeof value !== 'object') {
    return false
  }
  if (value instanceof Date || value instanceof RegExp) {
    return false
  }
  return true
}

function isProxy(value: any): boolean {
  return !!value && (typeof value === 'object' || typeof value === 'function') && proxyRaw.has(value)
}

class Tracker {
  leaves: Dep[] = []
  seen = new Set<string>()
  metas: ProxyMeta[] = []
  pending: PendingPrimitive[] = []

  addLeaf(dep: Dep): void {
    if (this.seen.has(dep.id)) {
      return
    }
    this.seen.add(dep.id)
    this.leaves.push(dep)
  }

  notePrimitive(pending: PendingPrimitive): void {
    if (this.pending.some((item) => item.id === pending.id)) {
      return
    }
    this.pending.push(pending)
  }

  track(value: any, spec: TrackSpec): any {
    if (!isTrackable(value)) {
      this.addLeaf({
        id: spec.id,
        report: true,
        value,
        cause: spec.id,
        kind: 'leaf',
        read: (state) => spec.read(state),
      })
      return value
    }
    if (isProxy(value)) {
      return value
    }
    if (value instanceof Map) {
      return this.proxyMap(value, spec)
    }
    if (value instanceof Set) {
      return this.proxySet(value, spec)
    }
    if (Array.isArray(value)) {
      return this.proxyArray(value, spec)
    }
    return this.proxyObject(value, spec)
  }

  finalize(): Dep[] {
    for (const meta of this.metas) {
      if (!meta.accessed) {
        this.addLeaf({
          id: meta.id,
          report: true,
          value: meta.raw,
          cause: meta.id,
          kind: 'leaf',
          read: (state) => meta.read(state),
        })
      }
      if (meta.sawKeys) {
        const id = `${meta.id}.__keys`
        this.addLeaf({
          id,
          report: false,
          value: Object.keys(meta.raw).join('\0'),
          cause: meta.id,
          kind: 'leaf',
          read: (state) => {
            const base = meta.read(state)
            if (base == null || typeof base !== 'object') {
              return ''
            }
            return Object.keys(base).join('\0')
          },
        })
      }
    }
    for (const pending of this.pending) {
      if (this.leaves.some((leaf) => covers(pending.id, leaf.id))) {
        continue
      }
      this.addLeaf({
        id: pending.id,
        report: true,
        value: pending.value,
        cause: pending.cause,
        kind: 'leaf',
        read: (state) => pending.read(state),
      })
    }
    return this.leaves
  }

  private metaFor(spec: TrackSpec, raw: any): ProxyMeta {
    const meta: ProxyMeta = { id: spec.id, raw, read: spec.read, accessed: false, sawKeys: false }
    this.metas.push(meta)
    return meta
  }

  private childSpec(spec: TrackSpec, id: string, read: (state: any) => any): TrackSpec {
    return { id, read }
  }

  proxyObject(value: object, spec: TrackSpec): any {
    const meta = this.metaFor(spec, value)
    const tracker = this
    const proxy = new Proxy(value, {
      get(target, prop, receiver) {
        if (prop === RAW) {
          return target
        }
        if (typeof prop === 'symbol') {
          return Reflect.get(target, prop, receiver)
        }
        if (prop === 'constructor' || prop === 'prototype' || prop === 'toJSON' || prop === 'then') {
          return Reflect.get(target, prop, receiver)
        }
        const current = Reflect.get(target, prop, receiver)
        if (typeof current === 'function' && !Object.prototype.hasOwnProperty.call(target, prop)) {
          return current.bind(target)
        }
        meta.accessed = true
        const childId = `${spec.id}.${String(prop)}`
        const read = (state: any) => {
          const base = spec.read(state)
          return base == null ? undefined : base[prop as any]
        }
        return tracker.track(current, tracker.childSpec(spec, childId, read))
      },
      ownKeys(target) {
        meta.sawKeys = true
        return Reflect.ownKeys(target)
      },
    })
    proxyRaw.set(proxy, value)
    return proxy
  }

  proxyArray(value: any[], spec: TrackSpec): any {
    const meta = this.metaFor(spec, value)
    const tracker = this
    let methodDepth = 0
    const recordIndex = (index: number, rawValue: any, report = true) => {
      const id = `${spec.id}.${index}`
      tracker.addLeaf({
        id,
        report,
        value: rawValue,
        cause: id,
        kind: 'leaf',
        read: (state) => {
          const base = spec.read(state)
          return base == null ? undefined : base[index]
        },
      })
    }
    const recordLength = (report: boolean) => {
      const id = `${spec.id}.length`
      tracker.addLeaf({
        id,
        report,
        value: value.length,
        cause: id,
        kind: 'leaf',
        read: (state) => {
          const base = spec.read(state)
          return base == null ? undefined : base.length
        },
      })
    }
    const proxy: any = new Proxy(value, {
      get(target, prop, receiver) {
        if (prop === RAW) {
          return target
        }
        if (prop === 'includes') {
          meta.accessed = true
          return (search: any, fromIndex?: number) =>
            scanArray(target, search, fromIndex, false, recordIndex, recordLength)
        }
        if (prop === 'indexOf') {
          meta.accessed = true
          return (search: any, fromIndex?: number) =>
            scanArray(target, search, fromIndex, false, recordIndex, recordLength, true)
        }
        if (prop === 'lastIndexOf') {
          meta.accessed = true
          return (search: any, fromIndex?: number) =>
            scanArray(target, search, fromIndex, true, recordIndex, recordLength, true)
        }
        if (prop === 'length') {
          if (methodDepth === 0) {
            meta.accessed = true
            recordLength(true)
          }
          return target.length
        }
        if (typeof prop === 'string' && isArrayIndex(prop)) {
          meta.accessed = true
          const index = Number(prop)
          if (methodDepth > 0) {
            recordIndex(index, target[index])
            return target[index]
          }
          const read = (state: any) => {
            const base = spec.read(state)
            return base == null ? undefined : base[index]
          }
          return tracker.track(target[index], { id: `${spec.id}.${index}`, read })
        }
        if (typeof prop === 'symbol') {
          const current = Reflect.get(target, prop, receiver)
          return typeof current === 'function' ? current.bind(proxy) : current
        }
        const current = Reflect.get(target, prop, receiver)
        if (typeof current === 'function') {
          return (...args: any[]) => {
            methodDepth += 1
            const before = tracker.leaves.length
            try {
              return current.apply(proxy, args)
            } finally {
              methodDepth -= 1
              const visitedEvery =
                target.length === 0 ||
                tracker.leaves.slice(before).filter((leaf) => leaf.report && leaf.id.startsWith(`${spec.id}.`))
                  .length >= target.length
              if (visitedEvery) {
                recordLength(false)
              }
            }
          }
        }
        return current
      },
    })
    proxyRaw.set(proxy, value)
    return proxy
  }

  proxyMap(value: Map<any, any>, spec: TrackSpec): any {
    const meta = this.metaFor(spec, value)
    const tracker = this
    const readKey = (key: any, op: 'get' | 'has') => {
      const id = `${spec.id}.map:${String(key)}`
      const read = (state: any) => {
        const base = spec.read(state) as Map<any, any>
        if (base == null) {
          return undefined
        }
        return op === 'has' ? base.has(key) : base.get(key)
      }
      return { id, read }
    }
    const proxy = new Proxy(value, {
      get(target, prop, receiver) {
        if (prop === RAW) {
          return target
        }
        if (prop === 'get') {
          meta.accessed = true
          return (key: any) => {
            const { id, read } = readKey(key, 'get')
            return tracker.track(target.get(key), { id, read })
          }
        }
        if (prop === 'has') {
          meta.accessed = true
          return (key: any) => {
            const { id, read } = readKey(key, 'has')
            const result = target.has(key)
            tracker.addLeaf({ id, report: true, value: result, cause: id, kind: 'leaf', read })
            return result
          }
        }
        if (prop === 'size') {
          meta.accessed = true
          const id = `${spec.id}.size`
          tracker.addLeaf({
            id,
            report: true,
            value: target.size,
            cause: id,
            kind: 'leaf',
            read: (state) => {
              const base = spec.read(state) as Map<any, any>
              return base == null ? undefined : base.size
            },
          })
          return target.size
        }
        const current = Reflect.get(target, prop, receiver)
        return typeof current === 'function' ? current.bind(target) : current
      },
    })
    proxyRaw.set(proxy, value)
    return proxy
  }

  proxySet(value: Set<any>, spec: TrackSpec): any {
    const meta = this.metaFor(spec, value)
    const tracker = this
    const proxy = new Proxy(value, {
      get(target, prop, receiver) {
        if (prop === RAW) {
          return target
        }
        if (prop === 'has') {
          meta.accessed = true
          return (member: any) => {
            const id = `${spec.id}.set:${String(member)}`
            const result = target.has(member)
            tracker.addLeaf({
              id,
              report: true,
              value: result,
              cause: id,
              kind: 'leaf',
              read: (state) => {
                const base = spec.read(state) as Set<any>
                return base == null ? undefined : base.has(member)
              },
            })
            return result
          }
        }
        if (prop === 'size') {
          meta.accessed = true
          const id = `${spec.id}.size`
          tracker.addLeaf({
            id,
            report: true,
            value: target.size,
            cause: id,
            kind: 'leaf',
            read: (state) => {
              const base = spec.read(state) as Set<any>
              return base == null ? undefined : base.size
            },
          })
          return target.size
        }
        const current = Reflect.get(target, prop, receiver)
        return typeof current === 'function' ? current.bind(target) : current
      },
    })
    proxyRaw.set(proxy, value)
    return proxy
  }
}

function isArrayIndex(prop: string): boolean {
  return /^(0|[1-9]\d*)$/.test(prop)
}

function sameValueZero(a: any, b: any): boolean {
  return a === b || (typeof a === 'number' && typeof b === 'number' && isNaN(a) && isNaN(b))
}

function scanArray(
  target: any[],
  search: any,
  fromIndex: number | undefined,
  reverse: boolean,
  recordIndex: (index: number, rawValue: any) => void,
  recordLength: (report: boolean) => void,
  returnIndex = false,
): any {
  const len = target.length
  if (len === 0) {
    recordLength(false)
    return returnIndex ? -1 : false
  }
  let start = fromIndex == null || Number.isNaN(fromIndex) ? (reverse ? len - 1 : 0) : Math.trunc(fromIndex)
  if (reverse) {
    if (start < 0) {
      start = len + start
    }
    if (start >= len) {
      start = len - 1
    }
    for (let i = start; i >= 0; i--) {
      recordIndex(i, target[i])
      if (sameValueZero(target[i], search)) {
        return returnIndex ? i : true
      }
    }
    recordLength(false)
    return returnIndex ? -1 : false
  }
  if (start < 0) {
    start = Math.max(len + start, 0)
  }
  if (start >= len) {
    recordLength(false)
    return returnIndex ? -1 : false
  }
  for (let i = start; i < len; i++) {
    recordIndex(i, target[i])
    if (sameValueZero(target[i], search)) {
      return returnIndex ? i : true
    }
  }
  recordLength(false)
  return returnIndex ? -1 : false
}

function covers(parent: string, id: string): boolean {
  return id === parent || id.startsWith(`${parent}.`)
}

function isAncestor(parent: string, child: string): boolean {
  return child.startsWith(`${parent}.`)
}

function unwrapDeep(value: any, seen = new Set<any>()): any {
  if (!value || (typeof value !== 'object' && typeof value !== 'function')) {
    return value
  }
  if (isProxy(value)) {
    return unwrapDeep(proxyRaw.get(value), seen)
  }
  if (seen.has(value)) {
    return value
  }
  if (value instanceof Map) {
    seen.add(value)
    let changed = false
    const next = new Map()
    value.forEach((entry, key) => {
      const nextKey = unwrapDeep(key, seen)
      const nextValue = unwrapDeep(entry, seen)
      if (nextKey !== key || nextValue !== entry) {
        changed = true
      }
      next.set(nextKey, nextValue)
    })
    return changed ? next : value
  }
  if (value instanceof Set) {
    seen.add(value)
    let changed = false
    const next = new Set()
    value.forEach((entry) => {
      const unwrapped = unwrapDeep(entry, seen)
      if (unwrapped !== entry) {
        changed = true
      }
      next.add(unwrapped)
    })
    return changed ? next : value
  }
  if (Array.isArray(value)) {
    seen.add(value)
    let changed = false
    const next = value.map((entry) => {
      const unwrapped = unwrapDeep(entry, seen)
      if (unwrapped !== entry) {
        changed = true
      }
      return unwrapped
    })
    return changed ? next : value
  }
  const proto = Object.getPrototypeOf(value)
  if (proto !== Object.prototype && proto !== null) {
    return value
  }
  seen.add(value)
  let changed = false
  const next: Record<string, any> = {}
  for (const key of Object.keys(value)) {
    const unwrapped = unwrapDeep(value[key], seen)
    if (unwrapped !== value[key]) {
      changed = true
    }
    next[key] = unwrapped
  }
  return changed ? next : value
}

function rootName(id: string): string {
  const dot = id.indexOf('.')
  return dot === -1 ? id : id.slice(0, dot)
}

function upstreamNames(atomic: AtomicState, node: AtomicNode): string[] {
  const names: string[] = []
  const seen = new Set<string>()
  const add = (name: string | null | undefined) => {
    if (!name || seen.has(name) || !atomic.nodes[name] || name === node.name) {
      return
    }
    seen.add(name)
    names.push(name)
  }
  for (const dep of node.staticDeps) {
    add(dep)
  }
  for (const id of node.dependencies) {
    add(id)
    add(rootName(id))
  }
  return names
}

function topologicalOrder(atomic: AtomicState): string[] {
  const names = atomic.definitionOrder.filter((name) => atomic.nodes[name])
  const indegree = new Map<string, number>()
  const children = new Map<string, string[]>()
  for (const name of names) {
    indegree.set(name, 0)
    children.set(name, [])
  }
  for (const name of names) {
    for (const upstream of upstreamNames(atomic, atomic.nodes[name])) {
      children.get(upstream)!.push(name)
      indegree.set(name, (indegree.get(name) || 0) + 1)
    }
  }
  const queue = names.filter((name) => indegree.get(name) === 0)
  const result: string[] = []
  while (queue.length) {
    const next = queue.shift() as string
    result.push(next)
    const ready: string[] = []
    for (const child of children.get(next) || []) {
      const remaining = (indegree.get(child) || 0) - 1
      indegree.set(child, remaining)
      if (remaining === 0) {
        ready.push(child)
      }
    }
    if (ready.length) {
      queue.push(...ready)
      queue.sort((a, b) => names.indexOf(a) - names.indexOf(b))
    }
  }
  if (result.length < names.length) {
    for (const name of names) {
      if (!result.includes(name)) {
        result.push(name)
      }
    }
  }
  return result
}

function recomputeDependents(atomic: AtomicState): void {
  for (const name of atomic.definitionOrder) {
    const node = atomic.nodes[name]
    if (node) {
      node.dependentNames = []
    }
  }
  for (const name of atomic.definitionOrder) {
    const node = atomic.nodes[name]
    if (!node) {
      continue
    }
    for (const upstream of upstreamNames(atomic, node)) {
      const parent = atomic.nodes[upstream]
      if (parent && !parent.dependentNames.includes(node.name)) {
        parent.dependentNames.push(node.name)
      }
    }
  }
}

function selectorCause(logic: Logic, name: string): string {
  const atomic = logic.cache[CACHE_KEY] as AtomicState | undefined
  const node = atomic?.nodes[name]
  if (node?.kind === 'reducer') {
    return name
  }
  return `selector:${name}`
}

function sameResult(node: AtomicNode, previous: any, next: any): boolean {
  if (node.resultEqualityCheck) {
    return node.resultEqualityCheck(previous, next) || Object.is(previous, next)
  }
  return Object.is(previous, next)
}

function readDep(logic: Logic, dep: Dep, state: any, props: any): any {
  if (dep.kind === 'selector') {
    const atomic = logic.cache[CACHE_KEY] as AtomicState
    const depNode = atomic.nodes[dep.id]
    if (depNode?.kind === 'derived') {
      return evaluate(logic, depNode, state, props)
    }
    if (depNode?.kind === 'reducer') {
      return readReducerValue(logic, dep.id, state)
    }
  }
  return dep.read(state, props)
}

function externalsChanged(logic: Logic, node: AtomicNode, state: any, props: any): boolean {
  for (const dep of node.deps) {
    if (dep.kind !== 'external') {
      continue
    }
    if (!Object.is(dep.read(state, props), dep.value)) {
      return true
    }
  }
  return false
}

function evaluate(logic: Logic, node: AtomicNode, state: any, props: any): any {
  if (node.computing) {
    throw new Error('[KEA] Circular dependency detected')
  }
  if (node.hasValue && node.cachedState === state && !node.dirty && !externalsChanged(logic, node, state, props)) {
    return node.value
  }

  node.computing = true
  try {
    let cause: string | null = null
    if (node.hasValue) {
      for (const dep of node.deps) {
        const current = readDep(logic, dep, state, props)
        if (!Object.is(current, dep.value)) {
          cause = dep.cause
          break
        }
      }
    }

    if (node.hasValue && !cause) {
      node.cachedState = state
      node.dirty = false
      if (node.optimisticDirty) {
        node.dirtyCause = node.committedDirtyCause
        node.optimisticDirty = false
      }
      return node.value
    }

    const tracker = new Tracker()
    trackerStack.push(tracker)
    let result: any
    const inputValues: any[] = []
    try {
      for (const input of node.inputs) {
        inputValues.push(input.invoke(state, props))
      }
      result = node.compute ? node.compute(...inputValues) : undefined
      result = unwrapDeep(result)
    } finally {
      trackerStack.pop()
    }

    node.evaluations += 1
    const leaves = tracker.finalize()
    const deps: Dep[] = [...leaves]
    node.inputs.forEach((input, index) => {
      const value = unwrapDeep(inputValues[index])
      if (input.type === 'selector' && input.name) {
        const covered = leaves.some((leaf) => covers(input.name as string, leaf.id))
        if (!covered && !isProxy(inputValues[index])) {
          deps.push({
            id: input.name,
            report: true,
            value,
            cause: selectorCause(logic, input.name),
            kind: 'selector',
            read: (nextState, nextProps) => {
              const atomic = logic.cache[CACHE_KEY] as AtomicState
              const depNode = atomic.nodes[input.name as string]
              if (depNode?.kind === 'derived') {
                return evaluate(logic, depNode, nextState, nextProps)
              }
              if (depNode?.kind === 'reducer') {
                return readReducerValue(logic, input.name as string, nextState)
              }
              return logic.selectors[input.name as string](nextState, nextProps)
            },
          })
        }
      } else {
        deps.push({
          id: `external:${index}`,
          report: false,
          value,
          cause: 'props',
          kind: 'external',
          read: (nextState, nextProps) => input.invoke(nextState, nextProps),
        })
      }
    })

    const publicIds = deps.filter((dep) => dep.report).map((dep) => dep.id)
    const keptIds = publicIds.filter((id) => !publicIds.some((other) => other !== id && isAncestor(id, other)))
    const kept = new Set(keptIds)
    node.deps = deps.filter((dep) => !dep.report || kept.has(dep.id))
    node.dependencies = node.deps.filter((dep) => dep.report).map((dep) => dep.id)

    if (!node.hasValue || !sameResult(node, node.value, result)) {
      node.value = result
    }
    if (cause) {
      node.dirtyCause = cause
      node.committedDirtyCause = cause
    }
    node.optimisticDirty = false
    node.hasValue = true
    node.cachedState = state
    node.dirty = false
    const atomic = logic.cache[CACHE_KEY] as AtomicState
    recomputeDependents(atomic)
    return node.value
  } finally {
    node.computing = false
  }
}

function peek(logic: Logic): void {
  const atomic = logic.cache[CACHE_KEY] as AtomicState | undefined
  if (!atomic) {
    return
  }
  let state: any
  try {
    state = getStoreState()
  } catch (error) {
    return
  }
  const props = logic.props
  const order = topologicalOrder(atomic)
  const invalidated = new Set<string>()
  for (const name of order) {
    const node = atomic.nodes[name]
    if (!node || node.kind !== 'derived' || !node.hasValue) {
      continue
    }
    if (node.cachedState === state && !node.dirty) {
      continue
    }
    let cause: string | null = null
    for (const dep of node.deps) {
      if (dep.kind === 'selector' && atomic.nodes[dep.id]?.kind === 'derived') {
        if (invalidated.has(dep.id) || atomic.nodes[dep.id].dirty) {
          cause = `selector:${dep.id}`
          break
        }
        continue
      }
      let current: any
      try {
        current = dep.kind === 'selector' ? readDep(logic, dep, state, props) : dep.read(state, props)
      } catch (error) {
        continue
      }
      if (!Object.is(current, dep.value)) {
        cause = dep.cause
        break
      }
    }
    if (cause) {
      node.dirty = true
      node.dirtyCause = cause
      node.optimisticDirty = true
      invalidated.add(name)
    }
  }
}

function ensureLinkedNodes(logic: Logic): void {
  const atomic = ensureAtomic(logic)
  for (const name of Object.keys(logic.selectors)) {
    if (!atomic.nodes[name]) {
      ensureNode(atomic, logic, name, 'linked')
    }
  }
}

function buildHealth(logic: Logic): SelectorHealth {
  const atomic = ensureAtomic(logic)
  ensureLinkedNodes(logic)
  peek(logic)
  recomputeDependents(atomic)
  const selectors: SelectorHealth['selectors'] = {}
  for (const name of atomic.definitionOrder) {
    const node = atomic.nodes[name]
    if (!node) {
      continue
    }
    selectors[name] = {
      dependencies: [...node.dependencies],
      dependents: [...node.dependentNames],
      evaluations: node.evaluations,
      dirtyCause: node.dirtyCause,
    }
  }
  return {
    selectors,
    topologicalOrder: topologicalOrder(atomic),
  }
}

export function assertNoCircularDependencies(logic: Logic): void {
  if (!isAtomicEnabled()) {
    return
  }
  const atomic = logic.cache[CACHE_KEY] as AtomicState | undefined
  if (!atomic) {
    return
  }
  const color = new Map<string, number>()
  const visit = (name: string) => {
    const node = atomic.nodes[name]
    if (!node || node.kind !== 'derived') {
      return
    }
    color.set(name, 1)
    for (const dep of node.staticDeps) {
      const depNode = atomic.nodes[dep]
      if (!depNode || depNode.kind !== 'derived') {
        continue
      }
      const mark = color.get(dep) ?? 0
      if (mark === 1) {
        throw new Error('[KEA] Circular dependency detected')
      }
      if (mark === 0) {
        visit(dep)
      }
    }
    color.set(name, 2)
  }
  for (const name of atomic.definitionOrder) {
    if ((color.get(name) ?? 0) === 0) {
      visit(name)
    }
  }
}

export const atomicSelectorsPlugin: KeaPlugin = {
  name: 'atomicSelectors',
  // Registering the field makes Kea proxy `logic.selectorHealth` onto the wrapper.
  // The value stays undefined until a logic is built with the engine enabled.
  defaults: () => ({
    selectorHealth: undefined,
  }),
  events: {
    afterReduxStore() {
      ensureStoreSubscription()
    },
    afterBuild(logic: BuiltLogic) {
      ensureAtomic(logic)
      ensureLinkedNodes(logic)
      assertNoCircularDependencies(logic)
    },
  },
}
