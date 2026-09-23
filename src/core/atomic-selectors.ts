import type { BuiltLogic, Logic, Selector, SelectorHealth } from '../types'
import { getStoreState } from '../kea/context'

/**
 * Leaf-level selector graph.
 *
 * Metadata is stored on the logic and keyed by `${pathString}::${localName}` so it
 * survives the wrapper functions Kea installs while building selectors.
 */

const PROP_TAG = Symbol.for('kea.atomicPropName')
const ATOMIC_ID = Symbol.for('kea.atomicSelectorId')
const ATOMIC_IDENTITY = Symbol.for('kea.atomicSelectorIdentity')

const proxyRaw = new WeakMap<object, any>()

const registry = new Map<string, SelectorRuntime>()
const evalStack: SelectorRuntime[] = []

interface RuntimeDep {
  id: string
  value: any
  hidden?: boolean
  /** Public id to report when only a hidden structural dep (length/size) changed. */
  causeId?: string
  read: (state: any, props: any) => any
}

interface SelectorRuntime {
  name: string
  identity: string
  staticDeps: string[]
  inputFns: Array<(state?: any, props?: any) => any>
  inputNames: (string | null)[]
  func: (...args: any[]) => any
  equalityCheck: (a: any, b: any) => boolean
  resultEqualityCheck?: (a: any, b: any) => boolean
  hasResult: boolean
  result: any
  deps: RuntimeDep[]
  dependencies: string[]
  evaluations: number
  dirtyCause: string | null
}

interface Engine {
  order: string[]
  runtimes: Record<string, SelectorRuntime>
  staticDeps: Record<string, string[]>
}

interface RecordOptions {
  hidden?: boolean
  causeId?: string
}

interface ProxyCtx {
  getCurrent: () => any
  /** Path relative to the input value. Empty at the input root. */
  relPath: string
  readFromRoot: (root: any) => any
  fullId: (relPath: string) => string
  onRecord: (relPath: string, value: any, read: (root: any) => any, options?: RecordOptions) => void
  flags: { suppressLength: boolean }
  seen: Set<string>
}

function atomicIdentity(logic: { pathString: string }, name: string): string {
  return `${logic.pathString}::${name}`
}

function ensureEngine(logic: Logic): Engine {
  if (!logic.cache) {
    logic.cache = {}
  }
  if (!logic.cache.atomicEngine) {
    logic.cache.atomicEngine = { order: [], runtimes: {}, staticDeps: {} }
  }
  return logic.cache.atomicEngine as Engine
}

function remember(logic: Logic, runtime: SelectorRuntime): void {
  const id = atomicIdentity(logic, runtime.name)
  if (runtime.identity && runtime.identity !== id) {
    registry.delete(runtime.identity)
  }
  runtime.identity = id
  registry.set(id, runtime)
}

function stamp(fn: Function, logic: Logic, name: string): void {
  Object.defineProperty(fn, ATOMIC_ID, { configurable: true, value: name })
  Object.defineProperty(fn, ATOMIC_IDENTITY, {
    configurable: true,
    get() {
      return atomicIdentity(logic, name)
    },
  })
}

function addSelectorAndValue(logic: Logic, key: string, selector: Selector): void {
  logic.selectors[key] = selector
  if (!Object.prototype.hasOwnProperty.call(logic.values, key)) {
    Object.defineProperty(logic.values, key, {
      get() {
        return logic.selectors[key](getStoreState(), logic.props)
      },
      enumerable: true,
    })
  }
}

function isTrackable(value: any): value is object {
  return value !== null && typeof value === 'object'
}

/** Plain objects, arrays, maps, and sets. Class instances are compared by identity. */
function shouldProxy(value: any): value is object {
  if (!isTrackable(value)) {
    return false
  }
  if (Array.isArray(value) || value instanceof Map || value instanceof Set) {
    return true
  }
  const prototype = Object.getPrototypeOf(value)
  return prototype === Object.prototype || prototype === null
}

function sameValue(a: any, b: any): boolean {
  return a === b
}

function unwrap(value: any): any {
  if (!isTrackable(value)) {
    return value
  }
  const raw = proxyRaw.get(value)
  return raw === undefined ? value : raw
}

function keyToString(key: unknown): string {
  if (typeof key === 'string') {
    return key
  }
  if (typeof key === 'number' || typeof key === 'boolean' || key == null) {
    return String(key)
  }
  return String(key)
}

function childPath(parent: string, part: string): string {
  return parent ? `${parent}.${part}` : part
}

function isIndexProp(prop: PropertyKey): prop is string {
  return typeof prop === 'string' && /^\d+$/.test(prop)
}

function rootOf(id: string): string {
  if (!id || id.charCodeAt(0) === 0) {
    return ''
  }
  const dot = id.indexOf('.')
  return dot === -1 ? id : id.slice(0, dot)
}

function deepUnwrap(value: any, seen = new Set<any>()): any {
  if (!isTrackable(value)) {
    return value
  }
  const raw = proxyRaw.get(value)
  if (raw !== undefined) {
    return deepUnwrap(raw, seen)
  }
  if (seen.has(value)) {
    return value
  }
  seen.add(value)

  if (value instanceof Map) {
    let changed = false
    const next = new Map()
    value.forEach((entry, mapKey) => {
      const unwrappedKey = deepUnwrap(mapKey, seen)
      const unwrappedValue = deepUnwrap(entry, seen)
      if (unwrappedKey !== mapKey || unwrappedValue !== entry) {
        changed = true
      }
      next.set(unwrappedKey, unwrappedValue)
    })
    return changed ? next : value
  }

  if (value instanceof Set) {
    let changed = false
    const next = new Set()
    value.forEach((entry) => {
      const unwrapped = deepUnwrap(entry, seen)
      if (unwrapped !== entry) {
        changed = true
      }
      next.add(unwrapped)
    })
    return changed ? next : value
  }

  if (Array.isArray(value)) {
    let changed = false
    const next = value.map((entry) => {
      const unwrapped = deepUnwrap(entry, seen)
      if (unwrapped !== entry) {
        changed = true
      }
      return unwrapped
    })
    return changed ? next : value
  }

  const prototype = Object.getPrototypeOf(value)
  if (prototype !== Object.prototype && prototype !== null) {
    return value
  }

  let changed = false
  const record = value as Record<string, any>
  const next: Record<string, any> = {}
  for (const key of Object.keys(record)) {
    const unwrapped = deepUnwrap(record[key], seen)
    if (unwrapped !== record[key]) {
      changed = true
    }
    next[key] = unwrapped
  }
  return changed ? next : value
}

function createTrackingProxy(ctx: ProxyCtx): any {
  const current = ctx.getCurrent()
  if (!shouldProxy(current)) {
    return current
  }
  const proxy = new Proxy(current, {
    get(_target, prop, receiver) {
      return readProxyProp(ctx, prop, receiver)
    },
    has(_target, prop) {
      const live = ctx.getCurrent()
      if (!isTrackable(live) || typeof prop === 'symbol') {
        return live != null && prop in (live as object)
      }
      recordProperty(ctx, String(prop))
      return prop in (live as object)
    },
    ownKeys(_target) {
      const live = ctx.getCurrent()
      if (!isTrackable(live)) {
        return []
      }
      return Reflect.ownKeys(live)
    },
    getOwnPropertyDescriptor(_target, prop) {
      const live = ctx.getCurrent()
      if (!isTrackable(live)) {
        return undefined
      }
      const descriptor = Reflect.getOwnPropertyDescriptor(live, prop)
      if (descriptor && !descriptor.configurable) {
        descriptor.configurable = true
      }
      return descriptor
    },
  })
  proxyRaw.set(proxy, current)
  return proxy
}

function recordProperty(ctx: ProxyCtx, prop: string): void {
  const live = ctx.getCurrent()
  const rel = childPath(ctx.relPath, prop)
  const value = live == null ? undefined : (live as any)[prop]
  const read = (root: any) => {
    const parent = ctx.readFromRoot(root)
    return parent == null ? undefined : parent[prop]
  }
  ctx.seen.add(rel)
  ctx.onRecord(rel, unwrap(value), read)
}

function readProxyProp(ctx: ProxyCtx, prop: PropertyKey, receiver: any): any {
  const live = ctx.getCurrent()
  if (!isTrackable(live)) {
    return undefined
  }

  if (prop === Symbol.iterator) {
    if (Array.isArray(live)) {
      return arrayIterator(ctx)
    }
    if (live instanceof Map) {
      return mapIterator(ctx)
    }
    if (live instanceof Set) {
      return setIterator(ctx)
    }
  }

  if (typeof prop === 'symbol') {
    const value = Reflect.get(live, prop, live)
    return typeof value === 'function' ? value.bind(live) : value
  }

  const key = String(prop)

  if (prop === 'then' && typeof (live as any).then !== 'function') {
    return undefined
  }
  if (prop === 'constructor') {
    return (live as any).constructor
  }

  if (live instanceof Map) {
    return readMapProp(ctx, live, key)
  }
  if (live instanceof Set) {
    return readSetProp(ctx, live, key)
  }

  if (Array.isArray(live) && key === 'length' && ctx.flags.suppressLength) {
    return live.length
  }

  if (Array.isArray(live) && isIndexProp(key)) {
    recordProperty(ctx, key)
    const value = live[Number(key)]
    return wrapChild(ctx, key, value, (root) => {
      const parent = ctx.readFromRoot(root)
      return parent == null ? undefined : parent[key]
    })
  }

  const value = Reflect.get(live, prop, live)
  if (Array.isArray(live) && typeof value === 'function') {
    return wrapArrayMethod(value, receiver, ctx)
  }
  if (typeof value === 'function') {
    const fn = value
    return (...args: any[]) => {
      const owner = ctx.getCurrent()
      if (!ctx.relPath) {
        ctx.onRecord('', owner, (root) => ctx.readFromRoot(root))
      }
      return fn.apply(owner, args)
    }
  }

  recordProperty(ctx, key)
  return wrapChild(ctx, key, value, (root) => {
    const parent = ctx.readFromRoot(root)
    return parent == null ? undefined : parent[key]
  })
}

function wrapChild(ctx: ProxyCtx, prop: string, value: any, readFromRoot: (root: any) => any): any {
  const raw = unwrap(value)
  if (!shouldProxy(raw)) {
    return raw
  }
  const rel = childPath(ctx.relPath, prop)
  return createTrackingProxy({
    ...ctx,
    relPath: rel,
    getCurrent: () => {
      const parent = ctx.getCurrent()
      return parent == null ? undefined : unwrap(parent[prop])
    },
    readFromRoot,
    // Nested collections track their own scans. Sharing `seen` makes a child
    // `includes` look like it already walked the parent.
    flags: { suppressLength: false },
    seen: new Set<string>(),
  })
}

function readMapProp(ctx: ProxyCtx, _map: Map<any, any>, prop: string): any {
  if (prop === 'get') {
    return (key: any) => {
      const map = ctx.getCurrent()
      const value = map instanceof Map ? map.get(key) : undefined
      const part = `map:${keyToString(key)}`
      const rel = childPath(ctx.relPath, part)
      const read = (root: any) => {
        const parent = ctx.readFromRoot(root)
        return parent instanceof Map ? parent.get(key) : undefined
      }
      ctx.seen.add(rel)
      ctx.onRecord(rel, unwrap(value), read)
      if (!shouldProxy(unwrap(value))) {
        return value
      }
      return createTrackingProxy({
        ...ctx,
        relPath: rel,
        getCurrent: () => {
          const parent = ctx.getCurrent()
          return parent instanceof Map ? parent.get(key) : undefined
        },
        readFromRoot: read,
        flags: { suppressLength: false },
        seen: new Set<string>(),
      })
    }
  }
  if (prop === 'has') {
    return (key: any) => {
      const map = ctx.getCurrent()
      const value = map instanceof Map ? map.has(key) : false
      const part = `map:${keyToString(key)}`
      const rel = childPath(ctx.relPath, part)
      const read = (root: any) => {
        const parent = ctx.readFromRoot(root)
        return parent instanceof Map ? parent.has(key) : false
      }
      ctx.seen.add(rel)
      ctx.onRecord(rel, value, read)
      return value
    }
  }
  if (prop === 'size') {
    const map = ctx.getCurrent()
    const size = map instanceof Map ? map.size : undefined
    const rel = childPath(ctx.relPath, 'size')
    ctx.seen.add(rel)
    ctx.onRecord(rel, size, (root) => {
      const parent = ctx.readFromRoot(root)
      return parent instanceof Map ? parent.size : undefined
    })
    return size
  }
  const map = ctx.getCurrent()
  const value = map instanceof Map ? Reflect.get(map, prop, map) : undefined
  return typeof value === 'function' ? value.bind(map) : value
}

function readSetProp(ctx: ProxyCtx, _set: Set<any>, prop: string): any {
  if (prop === 'has') {
    return (value: any) => {
      const set = ctx.getCurrent()
      const has = set instanceof Set ? set.has(value) : false
      const part = `set:${keyToString(value)}`
      const rel = childPath(ctx.relPath, part)
      const read = (root: any) => {
        const parent = ctx.readFromRoot(root)
        return parent instanceof Set ? parent.has(value) : false
      }
      ctx.seen.add(rel)
      ctx.onRecord(rel, has, read)
      return has
    }
  }
  if (prop === 'size') {
    const set = ctx.getCurrent()
    const size = set instanceof Set ? set.size : undefined
    const rel = childPath(ctx.relPath, 'size')
    ctx.seen.add(rel)
    ctx.onRecord(rel, size, (root) => {
      const parent = ctx.readFromRoot(root)
      return parent instanceof Set ? parent.size : undefined
    })
    return size
  }
  const set = ctx.getCurrent()
  const value = set instanceof Set ? Reflect.get(set, prop, set) : undefined
  return typeof value === 'function' ? value.bind(set) : value
}

function wrapArrayMethod(method: Function, receiver: any, ctx: ProxyCtx): (...args: any[]) => any {
  return (...args: any[]) => {
    const previous = ctx.flags.suppressLength
    ctx.flags.suppressLength = true
    try {
      return method.apply(receiver, args)
    } finally {
      ctx.flags.suppressLength = previous
      const live = ctx.getCurrent()
      const length = live?.length ?? 0
      if (length > 0) {
        let full = true
        for (let index = 0; index < length; index++) {
          if (!ctx.seen.has(childPath(ctx.relPath, String(index)))) {
            full = false
            break
          }
        }
        if (full) {
          ctx.onRecord(
            `\0len:${ctx.relPath}`,
            length,
            (root) => {
              const array = ctx.readFromRoot(root)
              return array?.length
            },
            { hidden: true, causeId: ctx.fullId(ctx.relPath) },
          )
        }
      }
    }
  }
}

function arrayIterator(ctx: ProxyCtx): () => Iterator<any> {
  return function* () {
    const array = ctx.getCurrent()
    const length = array?.length ?? 0
    for (let index = 0; index < length; index++) {
      recordProperty(ctx, String(index))
      const value = array[index]
      if (isTrackable(unwrap(value))) {
        yield wrapChild(ctx, String(index), value, (root) => {
          const parent = ctx.readFromRoot(root)
          return parent == null ? undefined : parent[index]
        })
      } else {
        yield value
      }
    }
    ctx.onRecord(`\0len:${ctx.relPath}`, length, (root) => ctx.readFromRoot(root)?.length, {
      hidden: true,
      causeId: ctx.fullId(ctx.relPath),
    })
  }
}

function mapIterator(ctx: ProxyCtx): () => Iterator<any> {
  return function* () {
    const map = ctx.getCurrent()
    if (!(map instanceof Map)) {
      return
    }
    for (const [key, value] of map) {
      const part = `map:${keyToString(key)}`
      const rel = childPath(ctx.relPath, part)
      const read = (root: any) => {
        const parent = ctx.readFromRoot(root)
        return parent instanceof Map ? parent.get(key) : undefined
      }
      ctx.seen.add(rel)
      ctx.onRecord(rel, unwrap(value), read)
      yield [
        key,
        shouldProxy(unwrap(value))
          ? createTrackingProxy({
              ...ctx,
              relPath: rel,
              getCurrent: () => {
                const parent = ctx.getCurrent()
                return parent instanceof Map ? parent.get(key) : undefined
              },
              readFromRoot: read,
              flags: { suppressLength: false },
              seen: new Set<string>(),
            })
          : value,
      ]
    }
    ctx.onRecord(childPath(ctx.relPath, 'size'), map.size, (root) => {
      const parent = ctx.readFromRoot(root)
      return parent instanceof Map ? parent.size : undefined
    })
  }
}

function setIterator(ctx: ProxyCtx): () => Iterator<any> {
  return function* () {
    const set = ctx.getCurrent()
    if (!(set instanceof Set)) {
      return
    }
    for (const value of set) {
      const part = `set:${keyToString(value)}`
      const rel = childPath(ctx.relPath, part)
      const read = (root: any) => {
        const parent = ctx.readFromRoot(root)
        return parent instanceof Set ? parent.has(value) : false
      }
      ctx.seen.add(rel)
      ctx.onRecord(rel, true, read)
      yield value
    }
    ctx.onRecord(childPath(ctx.relPath, 'size'), set.size, (root) => {
      const parent = ctx.readFromRoot(root)
      return parent instanceof Set ? parent.size : undefined
    })
  }
}

function finalizeDeps(records: RuntimeDep[]): { all: RuntimeDep[]; publicIds: string[] } {
  const visible = records.filter((record) => !record.hidden)
  const visibleIds = visible.map((record) => record.id)
  const leaves = visible.filter(
    (record) => !visibleIds.some((other) => other !== record.id && other.startsWith(`${record.id}.`)),
  )
  const leafIds = new Set(leaves.map((record) => record.id))
  const all = records.filter((record) => record.hidden || leafIds.has(record.id))
  return { all, publicIds: leaves.map((record) => record.id) }
}

function formatDirtyCause(id: string, logic: Logic, engine: Engine): string {
  const root = rootOf(id)
  if (!root) {
    return id
  }
  // Local reducer leaves stay as paths (`user.name`, `data.map:a`). Derived
  // selectors are reported as `selector:<name>`. Connected values are paths too.
  if (logic.reducers && Object.prototype.hasOwnProperty.call(logic.reducers, root)) {
    return id
  }
  if (engine.runtimes[root]) {
    return `selector:${root}`
  }
  return id
}

function firstChanged(deps: RuntimeDep[], state: any, props: any, eq: (a: any, b: any) => boolean): RuntimeDep | null {
  let hidden: RuntimeDep | null = null
  for (const dep of deps) {
    const next = dep.read(state, props)
    if (!eq(next, dep.value)) {
      if (dep.hidden) {
        if (!hidden) {
          hidden = dep
        }
        continue
      }
      return dep
    }
  }
  return hidden
}

function depIdsForLinks(runtime: SelectorRuntime): string[] {
  if (runtime.evaluations > 0) {
    return runtime.dependencies
  }
  return runtime.staticDeps
}

function dependentsOf(engine: Engine, name: string): string[] {
  const dependents: string[] = []
  for (const other of engine.order) {
    if (other === name) {
      continue
    }
    const runtime = engine.runtimes[other]
    if (!runtime) {
      continue
    }
    const ids = depIdsForLinks(runtime)
    if (ids.some((id) => rootOf(id) === name)) {
      dependents.push(other)
    }
  }
  return dependents
}

function topologicalOrder(engine: Engine): string[] {
  const indegree = new Map<string, number>()
  const children = new Map<string, string[]>()
  for (const name of engine.order) {
    indegree.set(name, 0)
    children.set(name, [])
  }
  for (const name of engine.order) {
    const deps = [...new Set((engine.staticDeps[name] || []).filter((dep) => indegree.has(dep) && dep !== name))]
    indegree.set(name, deps.length)
    for (const dep of deps) {
      children.get(dep)!.push(name)
    }
  }
  const queue = engine.order.filter((name) => indegree.get(name) === 0)
  const ordered: string[] = []
  while (queue.length > 0) {
    const name = queue.shift() as string
    ordered.push(name)
    for (const child of children.get(name) || []) {
      const next = (indegree.get(child) || 0) - 1
      indegree.set(child, next)
      if (next === 0) {
        queue.push(child)
      }
    }
  }
  return ordered
}

function assertAcyclic(engine: Engine): void {
  const color = new Map<string, number>()
  const visit = (name: string): void => {
    const state = color.get(name)
    if (state === 1) {
      throw new Error('[KEA] Circular dependency detected')
    }
    if (state === 2) {
      return
    }
    color.set(name, 1)
    for (const dep of engine.staticDeps[name] || []) {
      if (!engine.order.includes(dep)) {
        continue
      }
      visit(dep)
    }
    color.set(name, 2)
  }
  for (const name of engine.order) {
    if (color.get(name) !== 2) {
      visit(name)
    }
  }
}

function healthReport(logic: Logic): SelectorHealth {
  const engine = ensureEngine(logic)
  const selectors: SelectorHealth['selectors'] = {}
  for (const name of engine.order) {
    const runtime = engine.runtimes[name]
    if (!runtime) {
      continue
    }
    remember(logic, runtime)
    selectors[name] = {
      dependencies: [...runtime.dependencies],
      dependents: dependentsOf(engine, name),
      evaluations: runtime.evaluations,
      dirtyCause: runtime.dirtyCause,
    }
  }
  return {
    selectors,
    topologicalOrder: topologicalOrder(engine),
  }
}

export function ensureSelectorHealth(logic: BuiltLogic): void {
  const engine = ensureEngine(logic)
  for (const name of engine.order) {
    const runtime = engine.runtimes[name]
    if (runtime) {
      remember(logic, runtime)
    }
  }
  Object.defineProperty(logic, 'selectorHealth', {
    configurable: true,
    enumerable: false,
    writable: true,
    value: () => healthReport(logic),
  })
}

function createPropSelectors(logic: Logic): any {
  if (typeof Proxy === 'undefined') {
    return Object.fromEntries(Object.keys(logic.props || {}).map((key) => [key, () => logic.props[key]]))
  }
  return new Proxy(logic.props || {}, {
    get(target, prop) {
      if (!(prop in target)) {
        throw new Error(
          `[KEA] Prop "${String(prop)}" not found for logic "${
            logic.pathString
          }". Attempted to use in a selector. Please specify a default via props({ ${String(prop)}: '' }) to resolve.`,
        )
      }
      const read = () => (target as any)[prop]
      Object.defineProperty(read, PROP_TAG, { value: String(prop) })
      return read
    },
  })
}

interface SelectorPlan {
  key: string
  inputFns: Selector[]
  inputNames: (string | null)[]
  staticDeps: string[]
  func: (...args: any[]) => any
  memoizeOptions?: { equalityCheck?: (a: any, b: any) => boolean; resultEqualityCheck?: (a: any, b: any) => boolean }
}

export function installAtomicSelectors(logic: BuiltLogic, selectorInputs: Record<string, any>): void {
  const engine = ensureEngine(logic)
  const built: Record<string, Selector> = {}

  for (const key of Object.keys(selectorInputs)) {
    if (typeof logic.selectors[key] !== 'undefined') {
      throw new Error(`[KEA] Logic "${logic.pathString}" selector "${key}" already exists`)
    }
    const placeholder = ((...args: any[]) => built[key](...args)) as Selector
    stamp(placeholder, logic, key)
    addSelectorAndValue(logic, key, placeholder)
  }

  const fnToName = new Map<Function, string>()
  for (const [name, fn] of Object.entries(logic.selectors)) {
    if (!fnToName.has(fn)) {
      fnToName.set(fn, name)
    }
  }

  const atomicNames = new Set<string>([...engine.order, ...Object.keys(selectorInputs)])
  const propSelectors = createPropSelectors(logic)
  const plans: SelectorPlan[] = []

  for (const key of Object.keys(selectorInputs)) {
    const arr = selectorInputs[key]
    if (!arr) {
      throw new Error(`[KEA] Logic "${logic.pathString}" selector "${key}" is undefined`)
    }
    const [input, func, memoizeOptions] = arr as [
      (selectors: Record<string, Selector>, propSelectors: any) => Selector[],
      (...args: any[]) => any,
      SelectorPlan['memoizeOptions'],
    ]
    const inputFns = input(logic.selectors, propSelectors) as Selector[]
    if (inputFns.filter((entry) => typeof entry !== 'function').length > 0) {
      const argTypes = inputFns.map((entry) => typeof entry).join(', ')
      throw new Error(`[KEA] Logic "${logic.pathString}", selector "${key}" has incorrect input: [${argTypes}].`)
    }
    const inputNames = inputFns.map((fn, index) => {
      const mapped = fnToName.get(fn)
      if (mapped) {
        return mapped
      }
      const propName = (fn as any)[PROP_TAG]
      if (typeof propName === 'string') {
        return `props:${propName}`
      }
      return `inline:${index}`
    })
    const staticDeps = [
      ...new Set(inputNames.filter((name) => !!name && atomicNames.has(name) && !name.startsWith('inline:'))),
    ] as string[]
    engine.staticDeps[key] = staticDeps
    if (!engine.order.includes(key)) {
      engine.order.push(key)
    }
    plans.push({ key, inputFns, inputNames, staticDeps, func, memoizeOptions })
  }

  assertAcyclic(engine)

  for (const plan of plans) {
    const runtime: SelectorRuntime = {
      name: plan.key,
      identity: atomicIdentity(logic, plan.key),
      staticDeps: plan.staticDeps,
      inputFns: plan.inputFns,
      inputNames: plan.inputNames,
      func: plan.func,
      equalityCheck: plan.memoizeOptions?.equalityCheck || sameValue,
      resultEqualityCheck: plan.memoizeOptions?.resultEqualityCheck,
      hasResult: false,
      result: undefined,
      deps: [],
      dependencies: [],
      evaluations: 0,
      dirtyCause: null,
    }
    engine.runtimes[plan.key] = runtime
    remember(logic, runtime)

    const atomicSelector = ((state?: any, props?: any) => {
      const resolvedState = state === undefined ? getStoreState() : state
      const resolvedProps = props === undefined ? logic.props : props
      return runSelector(logic, runtime, resolvedState, resolvedProps)
    }) as Selector
    stamp(atomicSelector, logic, plan.key)
    built[plan.key] = atomicSelector

    const wrapper = ((state = getStoreState(), props = logic.props) => built[plan.key](state, props)) as Selector
    stamp(wrapper, logic, plan.key)
    addSelectorAndValue(logic, plan.key, wrapper)
  }
}

function runSelector(logic: Logic, runtime: SelectorRuntime, state: any, props: any): any {
  if (evalStack.includes(runtime)) {
    throw new Error('[KEA] Circular dependency detected')
  }
  const currentIdentity = atomicIdentity(logic, runtime.name)
  if (currentIdentity !== runtime.identity) {
    remember(logic, runtime)
  }

  const engine = ensureEngine(logic)
  evalStack.push(runtime)
  try {
    if (runtime.hasResult) {
      const changed = firstChanged(runtime.deps, state, props, runtime.equalityCheck)
      if (!changed) {
        return runtime.result
      }
      runtime.dirtyCause = formatDirtyCause(changed.causeId || changed.id, logic, engine)
    }
    return recomputeSelector(logic, runtime, state, props)
  } finally {
    evalStack.pop()
  }
}

function recomputeSelector(logic: Logic, runtime: SelectorRuntime, state: any, props: any): any {
  const rawArgs = runtime.inputFns.map((fn) => fn(state, props))
  const records: RuntimeDep[] = []
  const proxiedArgs = rawArgs.map((raw, index) => {
    const name = runtime.inputNames[index]
    const readRoot = (nextState: any, nextProps: any) => runtime.inputFns[index](nextState, nextProps)
    if (!name) {
      return raw
    }
    const onRecord = (relPath: string, value: any, read: (root: any) => any, options?: RecordOptions) => {
      const hidden = !!options?.hidden
      const id = hidden ? `\0${name}:${relPath}` : relPath ? `${name}.${relPath}` : name
      if (records.some((record) => record.id === id)) {
        return
      }
      records.push({
        id,
        value: unwrap(value),
        hidden,
        causeId: options?.causeId,
        read: (nextState, nextProps) => read(readRoot(nextState, nextProps)),
      })
    }
    if (!shouldProxy(raw)) {
      records.push({
        id: name,
        value: raw,
        read: readRoot,
      })
      return raw
    }
    // Parent dep. Dropped if any leaf under this input is read.
    records.push({
      id: name,
      value: raw,
      read: readRoot,
    })
    return createTrackingProxy({
      getCurrent: () => raw,
      relPath: '',
      readFromRoot: (root) => root,
      fullId: (relPath: string) => (relPath ? `${name}.${relPath}` : name),
      onRecord,
      flags: { suppressLength: false },
      seen: new Set<string>(),
    })
  })

  runtime.evaluations += 1
  const computed = deepUnwrap(runtime.func(...proxiedArgs))
  const finalized = finalizeDeps(records)
  const nextResult =
    runtime.hasResult && runtime.resultEqualityCheck && runtime.resultEqualityCheck(runtime.result, computed)
      ? runtime.result
      : computed
  runtime.result = nextResult
  runtime.hasResult = true
  runtime.deps = finalized.all
  runtime.dependencies = finalized.publicIds
  return nextResult
}

interface RenderDep {
  id: string
  value: any
  hidden?: boolean
  read: (raw: any) => any
}

interface RenderBox {
  raw: any
  deps: RenderDep[]
  flags: { suppressLength: boolean }
  seen: Set<string>
}

interface RenderTrack {
  has: boolean
  state: any
  snapshot: any
  proxy: any
  box: RenderBox | null
}

function renderAccessChanged(deps: RenderDep[], raw: any): boolean {
  const visible = deps.filter((dep) => !dep.hidden)
  const visibleIds = visible.map((dep) => dep.id)
  const leaves = deps.filter(
    (dep) => dep.hidden || !visibleIds.some((other) => other !== dep.id && other.startsWith(`${dep.id}.`)),
  )
  for (const dep of leaves) {
    if (dep.read(raw) !== dep.value) {
      return true
    }
  }
  return false
}

/**
 * Snapshot for `useSelector` when atomic selectors are enabled.
 * Objects keep a stable proxy identity until an accessed leaf changes, so a
 * component that read `user.name` does not re-render when `user.age` changes.
 */
export function readTrackedSnapshot(track: { current: RenderTrack | null }, selector: Selector, state: any): any {
  if (!track.current) {
    track.current = { has: false, state: undefined, snapshot: undefined, proxy: undefined, box: null }
  }
  const tracked = track.current
  if (tracked.has && tracked.state === state) {
    return tracked.snapshot
  }

  const raw = selector(state)
  if (!shouldProxy(raw)) {
    tracked.has = true
    tracked.state = state
    tracked.snapshot = raw
    tracked.proxy = undefined
    tracked.box = null
    return raw
  }

  if (tracked.box && tracked.proxy && tracked.box.raw === raw) {
    tracked.has = true
    tracked.state = state
    tracked.snapshot = tracked.proxy
    return tracked.proxy
  }

  if (tracked.box && tracked.proxy && tracked.box.deps.length > 0 && !renderAccessChanged(tracked.box.deps, raw)) {
    tracked.box.raw = raw
    proxyRaw.set(tracked.proxy, raw)
    tracked.has = true
    tracked.state = state
    tracked.snapshot = tracked.proxy
    return tracked.proxy
  }

  const box: RenderBox = { raw, deps: [], flags: { suppressLength: false }, seen: new Set<string>() }
  const proxy = createTrackingProxy({
    getCurrent: () => box.raw,
    relPath: '',
    readFromRoot: (root) => root,
    fullId: (relPath: string) => relPath,
    flags: box.flags,
    seen: box.seen,
    onRecord: (relPath, value, read, options) => {
      const hidden = !!options?.hidden
      const id = hidden ? `\0${relPath}` : relPath || '$'
      if (box.deps.some((dep) => dep.id === id)) {
        return
      }
      box.deps.push({
        id,
        value: unwrap(value),
        hidden,
        read,
      })
    },
  })
  tracked.box = box
  tracked.proxy = proxy
  tracked.snapshot = proxy
  tracked.has = true
  tracked.state = state
  return proxy
}
