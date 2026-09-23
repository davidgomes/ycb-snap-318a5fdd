import { getContext, getStoreState, setAtomicReducerHook, withAtomicReadState } from '../kea/context'
import { KeaPlugin, Logic, Selector, SelectorHealth } from '../types'

/**
 * Fine-grained selector graph. Metadata is keyed by the built logic object and the
 * selector's local name so it survives Kea's build-time function wrapping.
 * `stableId` (`pathString::name`) is the same identity in string form.
 */

const ATOMIC_META = Symbol.for('kea.atomic.meta')
const RAW = Symbol.for('kea.atomic.raw')
const proxies = new WeakSet<object>()

const CIRCULAR = '[KEA] Circular dependency detected'

type Watch = {
  id: string
  public: boolean
  /** Another selector's output, not a state leaf. */
  selector: boolean
  /** Compared against the latest props / inline input, not prev/next redux state. */
  external: boolean
  propName?: string
  fn?: Selector
  lastExternal?: any
  read: (root: any) => any
}

type InputSpec =
  | { type: 'reducer'; localName: string; owner: Logic; reducerKey: string }
  | { type: 'computed'; localName: string; node: AtomicNode }
  | { type: 'prop'; localName: string; propName: string }
  | { type: 'inline'; localName: string; fn: Selector; index: number }

type AtomicNode = {
  stableId: string
  logic: Logic
  name: string
  kind: 'reducer' | 'computed'
  evaluations: number
  dirtyCause: string | null
  dirty: boolean
  hasValue: boolean
  lastValue: any
  dependencies: string[]
  watches: Watch[]
  inputSpecs: InputSpec[]
  staticSelectorDeps: string[]
  func?: (...args: any[]) => any
  resultEqual?: (previous: any, next: any) => boolean
  evaluating: boolean
  suppressDirty: boolean
  seenSlice: boolean
  lastSlice: any
}

type AtomicMeta = {
  kind: 'reducer' | 'computed' | 'prop'
  name: string
  logic?: Logic
  node?: AtomicNode
}

type Tracker = {
  publicWatches: Watch[]
  extraWatches: Watch[]
}

let activeTracker: Tracker | null = null
let notifying = false

const nodesByLogic = new WeakMap<Logic, Map<string, AtomicNode>>()
const nodesByContext = new WeakMap<object, Set<AtomicNode>>()

export function isAtomicSelectorsEnabled(): boolean {
  const context = getContext()
  return !!context && context.options.atomicSelectors === true
}

export const atomicSelectorsPlugin: KeaPlugin = {
  name: 'atomicSelectors',
  events: {
    afterPlugin() {
      setAtomicReducerHook(notifyAtomicStateChange)
    },
  },
  defaults: () => ({
    selectorHealth(this: Logic): SelectorHealth {
      const host = this as Logic & {
        _isKeaBuild?: boolean
        _isKea?: boolean
        findMounted?: () => Logic | null
        build?: () => Logic
      }
      const target =
        host && host._isKeaBuild ? host : host && host._isKea ? host.findMounted?.() || host.build?.() : host
      return reportSelectorHealth((target || host) as Logic)
    },
  }),
}

function logicNodes(logic: Logic): Map<string, AtomicNode> {
  let map = nodesByLogic.get(logic)
  if (!map) {
    map = new Map()
    nodesByLogic.set(logic, map)
  }
  return map
}

function registerNode(node: AtomicNode): void {
  logicNodes(node.logic).set(node.name, node)
  const context = getContext()
  if (!context) {
    return
  }
  let set = nodesByContext.get(context)
  if (!set) {
    set = new Set()
    nodesByContext.set(context, set)
  }
  set.add(node)
}

function contextNodeList(): AtomicNode[] {
  const context = getContext()
  const set = context ? nodesByContext.get(context) : undefined
  return set ? [...set] : []
}

function tag(fn: Selector, meta: AtomicMeta): Selector {
  Object.defineProperty(fn, ATOMIC_META, { value: meta, enumerable: false })
  return fn
}

function metaOf(fn: unknown): AtomicMeta | undefined {
  if (typeof fn !== 'function') {
    return undefined
  }
  return (fn as Selector & { [ATOMIC_META]?: AtomicMeta })[ATOMIC_META]
}

function stableId(logic: Logic, name: string): string {
  return `${logic.pathString}::${name}`
}

function mountSelector(logic: Logic, key: string, selector: Selector): void {
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

function readReducerValue(owner: Logic, key: string, root: any): any {
  if (!owner.selector) {
    return undefined
  }
  try {
    const slice = owner.selector(root)
    if (slice == null) {
      return undefined
    }
    return slice[key]
  } catch {
    return undefined
  }
}

function markDirty(node: AtomicNode, cause: string): void {
  if (node.suppressDirty || node.kind !== 'computed') {
    return
  }
  if (!node.dirty) {
    node.dirty = true
    node.dirtyCause = cause
  }
}

function resultsEqual(node: AtomicNode, previous: any, next: any): boolean {
  if (node.resultEqual) {
    return node.resultEqual(previous, next)
  }
  return Object.is(previous, next)
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

function unwrap(value: any): any {
  if (value && typeof value === 'object' && proxies.has(value)) {
    return (value as any)[RAW]
  }
  return value
}

function note(tracker: Tracker, watch: Watch): void {
  const list = watch.public ? tracker.publicWatches : tracker.extraWatches
  if (!watch.public) {
    if (!list.some((item) => item.id === watch.id)) {
      list.push(watch)
    }
    return
  }
  tracker.publicWatches = tracker.publicWatches.filter((item) => !watch.id.startsWith(`${item.id}.`))
  if (tracker.publicWatches.some((item) => item.id === watch.id || item.id.startsWith(`${watch.id}.`))) {
    return
  }
  tracker.publicWatches.push(watch)
}

function leafWatch(id: string, read: (root: any) => any, isPublic = true): Watch {
  return { id, public: isPublic, selector: false, external: false, read }
}

function wrapAny(value: any, path: string, readParent: (root: any) => any, tracker: Tracker): any {
  if (Array.isArray(value)) {
    return wrapArray(value, path, readParent, tracker)
  }
  if (value instanceof Map) {
    return wrapMap(value, path, readParent, tracker)
  }
  if (value instanceof Set) {
    return wrapSet(value, path, readParent, tracker)
  }
  return wrapObject(value, path, readParent, tracker)
}

function childRead(readParent: (root: any) => any, prop: string | number): (root: any) => any {
  return (root) => {
    const parent = readParent(root)
    if (parent == null) {
      return undefined
    }
    return parent[prop as any]
  }
}

function exposeValue(value: any, path: string, read: (root: any) => any, tracker: Tracker): any {
  if (!isTrackable(value)) {
    note(tracker, leafWatch(path, read, true))
    return value
  }
  return wrapAny(value, path, read, tracker)
}

function wrapObject(obj: any, path: string, readParent: (root: any) => any, tracker: Tracker): any {
  const proxy = new Proxy(obj, {
    get(target, prop, receiver) {
      if (prop === RAW) {
        return target
      }
      if (typeof prop !== 'string') {
        return Reflect.get(target, prop, receiver)
      }
      const value = target[prop]
      if (typeof value === 'function') {
        return value.bind(target)
      }
      const id = `${path}.${prop}`
      const read = childRead(readParent, prop)
      return exposeValue(value, id, read, tracker)
    },
    has(target, prop) {
      if (prop === RAW) {
        return true
      }
      return Reflect.has(target, prop)
    },
    ownKeys(target) {
      const keys = Reflect.ownKeys(target)
      for (const key of keys) {
        if (typeof key === 'string') {
          const value = target[key]
          const id = `${path}.${key}`
          const read = childRead(readParent, key)
          if (!isTrackable(value)) {
            note(tracker, leafWatch(id, read, true))
          }
        }
      }
      return keys
    },
  })
  proxies.add(proxy)
  return proxy
}

function noteIndex(target: any[], path: string, readParent: (root: any) => any, tracker: Tracker, index: number): any {
  if (index < 0 || index >= target.length) {
    note(tracker, leafWatch(`${path}.${index}`, childRead(readParent, index), true))
    return undefined
  }
  const value = target[index]
  const id = `${path}.${index}`
  return exposeValue(value, id, childRead(readParent, index), tracker)
}

function noteLength(path: string, readParent: (root: any) => any, tracker: Tracker): void {
  note(
    tracker,
    leafWatch(
      `${path}.length`,
      (root) => {
        const parent = readParent(root)
        return parent == null ? undefined : parent.length
      },
      false,
    ),
  )
}

function wrapArray(arr: any[], path: string, readParent: (root: any) => any, tracker: Tracker): any {
  const proxy = new Proxy(arr, {
    get(target, prop, receiver) {
      if (prop === RAW) {
        return target
      }
      if (prop === 'includes') {
        return (search: any, fromIndex = 0) => {
          const start = fromIndex >= 0 ? fromIndex : Math.max(0, target.length + fromIndex)
          let found = false
          for (let i = start; i < target.length; i++) {
            noteIndex(target, path, readParent, tracker, i)
            if (target[i] === search || (Number.isNaN(target[i]) && Number.isNaN(search))) {
              found = true
              break
            }
          }
          if (!found) {
            noteLength(path, readParent, tracker)
          }
          return found
        }
      }
      if (prop === 'indexOf' || prop === 'lastIndexOf' || prop === 'findIndex') {
        return (...args: any[]) => {
          const result = (target as any)[prop](...args)
          if (typeof result === 'number' && result >= 0) {
            noteIndex(target, path, readParent, tracker, result)
          } else {
            for (let i = 0; i < target.length; i++) {
              noteIndex(target, path, readParent, tracker, i)
            }
            noteLength(path, readParent, tracker)
          }
          return result
        }
      }
      if (
        prop === 'map' ||
        prop === 'filter' ||
        prop === 'forEach' ||
        prop === 'reduce' ||
        prop === 'reduceRight' ||
        prop === 'some' ||
        prop === 'every' ||
        prop === 'find' ||
        prop === 'flatMap' ||
        prop === 'join' ||
        prop === 'flat' ||
        prop === 'slice' ||
        prop === 'concat'
      ) {
        return (...args: any[]) => {
          for (let i = 0; i < target.length; i++) {
            noteIndex(target, path, readParent, tracker, i)
          }
          noteLength(path, readParent, tracker)
          const rebound = (target as any)[prop]
          if (
            prop === 'map' ||
            prop === 'filter' ||
            prop === 'forEach' ||
            prop === 'some' ||
            prop === 'every' ||
            prop === 'find' ||
            prop === 'flatMap'
          ) {
            const cb = args[0]
            const thisArg = args[1]
            if (typeof cb === 'function') {
              const wrappedCb = (item: any, index: number, array: any[]) => {
                const exposed = exposeValue(item, `${path}.${index}`, childRead(readParent, index), tracker)
                return cb.call(thisArg, exposed, index, array)
              }
              return rebound.call(target, wrappedCb, thisArg)
            }
          }
          return rebound.apply(target, args)
        }
      }
      if (prop === Symbol.iterator) {
        return function* iterator() {
          for (let i = 0; i < target.length; i++) {
            yield noteIndex(target, path, readParent, tracker, i)
          }
        }
      }
      if (prop === 'length') {
        note(
          tracker,
          leafWatch(
            `${path}.length`,
            (root) => {
              const parent = readParent(root)
              return parent == null ? undefined : parent.length
            },
            true,
          ),
        )
        return target.length
      }
      if (prop === 'at') {
        return (index: number) => {
          const resolved = index < 0 ? target.length + index : index
          return noteIndex(target, path, readParent, tracker, resolved)
        }
      }
      if (typeof prop === 'string' && prop !== 'constructor') {
        if (prop in Array.prototype && typeof (target as any)[prop] === 'function') {
          return (target as any)[prop].bind(target)
        }
        const index = Number(prop)
        if (prop !== '' && !Number.isNaN(index)) {
          return noteIndex(target, path, readParent, tracker, index)
        }
      }
      return Reflect.get(target, prop, receiver)
    },
  })
  proxies.add(proxy)
  return proxy
}

function wrapMap(map: Map<any, any>, path: string, readParent: (root: any) => any, tracker: Tracker): any {
  const proxy = new Proxy(map, {
    get(target, prop, receiver) {
      if (prop === RAW) {
        return target
      }
      if (prop === 'get' || prop === 'has') {
        return (key: any) => {
          const id = `${path}.map:${String(key)}`
          const read = (root: any) => {
            const parent = readParent(root)
            if (!(parent instanceof Map)) {
              return undefined
            }
            return prop === 'has' ? parent.has(key) : parent.get(key)
          }
          if (prop === 'has') {
            note(tracker, leafWatch(id, read, true))
            return target.has(key)
          }
          return exposeValue(target.get(key), id, read, tracker)
        }
      }
      if (prop === 'size') {
        note(
          tracker,
          leafWatch(
            `${path}.size`,
            (root) => {
              const parent = readParent(root)
              return parent instanceof Map ? parent.size : undefined
            },
            true,
          ),
        )
        return target.size
      }
      if (
        prop === 'forEach' ||
        prop === 'keys' ||
        prop === 'values' ||
        prop === 'entries' ||
        prop === Symbol.iterator
      ) {
        return (...args: any[]) => {
          target.forEach((_value, key) => {
            const id = `${path}.map:${String(key)}`
            note(
              tracker,
              leafWatch(
                id,
                (root) => {
                  const parent = readParent(root)
                  return parent instanceof Map ? parent.get(key) : undefined
                },
                true,
              ),
            )
          })
          const value = Reflect.get(target, prop, receiver)
          return typeof value === 'function' ? value.apply(target, args) : value
        }
      }
      const value = Reflect.get(target, prop, receiver)
      return typeof value === 'function' ? value.bind(target) : value
    },
  })
  proxies.add(proxy)
  return proxy
}

function wrapSet(set: Set<any>, path: string, readParent: (root: any) => any, tracker: Tracker): any {
  const proxy = new Proxy(set, {
    get(target, prop, receiver) {
      if (prop === RAW) {
        return target
      }
      if (prop === 'has') {
        return (value: any) => {
          const id = `${path}.set:${String(value)}`
          note(
            tracker,
            leafWatch(
              id,
              (root) => {
                const parent = readParent(root)
                return parent instanceof Set ? parent.has(value) : undefined
              },
              true,
            ),
          )
          return target.has(value)
        }
      }
      if (prop === 'size') {
        note(
          tracker,
          leafWatch(
            `${path}.size`,
            (root) => {
              const parent = readParent(root)
              return parent instanceof Set ? parent.size : undefined
            },
            true,
          ),
        )
        return target.size
      }
      if (
        prop === 'forEach' ||
        prop === 'keys' ||
        prop === 'values' ||
        prop === 'entries' ||
        prop === Symbol.iterator
      ) {
        return (...args: any[]) => {
          target.forEach((value) => {
            const id = `${path}.set:${String(value)}`
            note(
              tracker,
              leafWatch(
                id,
                (root) => {
                  const parent = readParent(root)
                  return parent instanceof Set ? parent.has(value) : undefined
                },
                true,
              ),
            )
          })
          const fn = Reflect.get(target, prop, receiver)
          return typeof fn === 'function' ? fn.apply(target, args) : fn
        }
      }
      const value = Reflect.get(target, prop, receiver)
      return typeof value === 'function' ? value.bind(target) : value
    },
  })
  proxies.add(proxy)
  return proxy
}

function resultEqualityFrom(memoizeOptions: any): ((previous: any, next: any) => boolean) | undefined {
  if (typeof memoizeOptions === 'function') {
    return memoizeOptions
  }
  if (memoizeOptions && typeof memoizeOptions.resultEqualityCheck === 'function') {
    return memoizeOptions.resultEqualityCheck
  }
  return undefined
}

function findLocalName(logic: Logic, fn: Selector, fallback: string): string {
  for (const [key, value] of Object.entries(logic.selectors)) {
    if (value === fn) {
      return key
    }
  }
  return fallback
}

function coverInput(tracker: Tracker, spec: InputSpec, readParent: (root: any) => any): void {
  const covered = tracker.publicWatches.some(
    (watch) => watch.id === spec.localName || watch.id.startsWith(`${spec.localName}.`),
  )
  if (!covered) {
    note(tracker, leafWatch(spec.localName, readParent, true))
  }
}

function runCompute(node: AtomicNode, state: any, props: any): boolean {
  if (node.evaluating) {
    throw new Error(CIRCULAR)
  }
  node.evaluating = true
  node.suppressDirty = true
  node.dirty = false
  const previous = node.lastValue
  const hadValue = node.hasValue
  try {
    const tracker: Tracker = { publicWatches: [], extraWatches: [] }
    const args = node.inputSpecs.map((spec) => {
      if (spec.type === 'computed') {
        note(tracker, {
          id: spec.localName,
          public: true,
          selector: true,
          external: false,
          read: () => undefined,
        })
        const prev = activeTracker
        activeTracker = null
        try {
          return node.logic.selectors[spec.localName](state, props)
        } finally {
          activeTracker = prev
        }
      }
      if (spec.type === 'prop') {
        const read = () => (node.logic.props ? node.logic.props[spec.propName] : undefined)
        const watch = leafWatch(spec.localName, read, true)
        watch.external = true
        watch.propName = spec.propName
        watch.lastExternal = props ? props[spec.propName] : read()
        note(tracker, watch)
        return props ? props[spec.propName] : read()
      }
      if (spec.type === 'inline') {
        const read = (root: any) => spec.fn(root, props ?? node.logic.props)
        const watch: Watch = {
          id: spec.localName,
          public: false,
          selector: false,
          external: true,
          fn: spec.fn,
          read,
        }
        watch.lastExternal = spec.fn(state, props)
        note(tracker, watch)
        return watch.lastExternal
      }
      const readParent = (root: any) => readReducerValue(spec.owner, spec.reducerKey, root)
      const raw = readParent(state)
      return exposeValue(raw, spec.localName, readParent, tracker)
    })

    // Whole-value dependency when an object input is never opened.
    node.inputSpecs.forEach((spec, index) => {
      if (spec.type === 'reducer') {
        const readParent = (root: any) => readReducerValue(spec.owner, spec.reducerKey, root)
        coverInput(tracker, spec, readParent)
        return
      }
      if (spec.type === 'inline') {
        return
      }
      // Touch `index` so unused object inputs still count when exposeValue short-circuited.
      void args[index]
    })

    const prevTracker = activeTracker
    activeTracker = tracker
    let result: any
    try {
      result = node.func!(...args)
    } finally {
      activeTracker = prevTracker
    }
    result = unwrap(result)
    node.evaluations += 1
    node.dependencies = tracker.publicWatches.map((watch) => watch.id)
    node.watches = tracker.publicWatches.concat(tracker.extraWatches)
    for (const watch of node.watches) {
      if (watch.external && watch.lastExternal === undefined) {
        try {
          watch.lastExternal = watch.propName ? props?.[watch.propName] : watch.fn?.(state, props)
        } catch {
          watch.lastExternal = undefined
        }
      }
    }
    if (!hadValue || !resultsEqual(node, previous, result)) {
      node.lastValue = result
      node.hasValue = true
      return true
    }
    node.hasValue = true
    return false
  } finally {
    node.evaluating = false
    node.suppressDirty = false
    node.dirty = false
  }
}

function externalCause(node: AtomicNode, state: any, props: any): string | null {
  for (const watch of node.watches) {
    if (!watch.external) {
      continue
    }
    let current: any
    try {
      current = watch.propName ? props?.[watch.propName] : watch.fn ? watch.fn(state, props) : watch.read(state)
    } catch {
      continue
    }
    if (!Object.is(current, watch.lastExternal)) {
      return watch.id
    }
  }
  return null
}

function computedFnFor(node: AtomicNode): Selector {
  const fn = ((state: any = getStoreState(), props: any = node.logic.props) => {
    if (activeTracker && node.kind === 'computed') {
      note(activeTracker, {
        id: node.name,
        public: true,
        selector: true,
        external: false,
        read: () => undefined,
      })
    }
    const suspended = activeTracker
    activeTracker = null
    try {
      if (!node.func) {
        return undefined
      }
      const cause = node.hasValue ? externalCause(node, state, props) : null
      if (cause) {
        markDirty(node, cause)
      }
      if (!node.hasValue || node.dirty) {
        runCompute(node, state, props)
      }
      return node.lastValue
    } finally {
      activeTracker = suspended
    }
  }) as Selector
  return tag(fn, { kind: 'computed', name: node.name, logic: node.logic, node })
}

function assertNoCycle(nodes: AtomicNode[]): void {
  const color = new Map<AtomicNode, number>()
  const visit = (node: AtomicNode): void => {
    const state = color.get(node) ?? 0
    if (state === 1) {
      throw new Error(CIRCULAR)
    }
    if (state === 2) {
      return
    }
    color.set(node, 1)
    for (const spec of node.inputSpecs) {
      if (spec.type === 'computed') {
        visit(spec.node)
      }
    }
    color.set(node, 2)
  }
  for (const node of nodes) {
    if (node.kind === 'computed') {
      visit(node)
    }
  }
}

function propSelectorsFor(logic: Logic): any {
  if (typeof Proxy === 'undefined') {
    return Object.fromEntries(
      Object.keys(logic.props).map((key) => {
        const fn = (() => logic.props[key]) as Selector
        return [key, tag(fn, { kind: 'prop', name: key })]
      }),
    )
  }
  return new Proxy(logic.props, {
    get(target, prop) {
      if (typeof prop === 'symbol') {
        return (target as any)[prop]
      }
      if (!(prop in target)) {
        throw new Error(
          `[KEA] Prop "${String(prop)}" not found for logic "${
            logic.pathString
          }". Attempted to use in a selector. Please specify a default via props({ ${String(prop)}: '' }) to resolve.`,
        )
      }
      const fn = (() => target[prop as any]) as Selector
      return tag(fn, { kind: 'prop', name: String(prop) })
    },
  })
}

function bindInputs(logic: Logic, node: AtomicNode, inputFns: Selector[]): void {
  node.inputSpecs = inputFns.map((fn, index) => {
    const meta = metaOf(fn)
    const localName = findLocalName(logic, fn, meta?.name ?? `inline:${index}`)
    if (meta?.kind === 'reducer' && meta.logic && meta.node) {
      return { type: 'reducer', localName, owner: meta.logic, reducerKey: meta.name }
    }
    if (meta?.kind === 'computed' && meta.node) {
      node.staticSelectorDeps.push(localName)
      return { type: 'computed', localName, node: meta.node }
    }
    if (meta?.kind === 'prop') {
      return { type: 'prop', localName: `props.${meta.name}`, propName: meta.name }
    }
    return { type: 'inline', localName: `inline:${index}`, fn, index }
  })
}

export function installAtomicSelectors(logic: Logic, selectorInputs: Record<string, any>): void {
  const entries = Object.entries(selectorInputs)
  for (const [key] of entries) {
    if (typeof logic.selectors[key] !== 'undefined') {
      throw new Error(`[KEA] Logic "${logic.pathString}" selector "${key}" already exists`)
    }
  }

  const created: AtomicNode[] = []
  for (const [key] of entries) {
    const node: AtomicNode = {
      stableId: stableId(logic, key),
      logic,
      name: key,
      kind: 'computed',
      evaluations: 0,
      dirtyCause: null,
      dirty: false,
      hasValue: false,
      lastValue: undefined,
      dependencies: [],
      watches: [],
      inputSpecs: [],
      staticSelectorDeps: [],
      evaluating: false,
      suppressDirty: false,
      seenSlice: false,
      lastSlice: undefined,
    }
    registerNode(node)
    created.push(node)
    mountSelector(logic, key, computedFnFor(node))
  }

  const propSelectors = propSelectorsFor(logic)
  for (const [key, arr] of entries) {
    if (!arr) {
      throw new Error(`[KEA] Logic "${logic.pathString}" selector "${key}" is undefined`)
    }
    const [input, func, memoizeOptions] = arr as any
    const node = logicNodes(logic).get(key)!
    const args = input(logic.selectors, propSelectors) as Selector[]
    if (args.filter((candidate) => typeof candidate !== 'function').length > 0) {
      const argTypes = args.map((candidate) => typeof candidate).join(', ')
      throw new Error(`[KEA] Logic "${logic.pathString}", selector "${key}" has incorrect input: [${argTypes}].`)
    }
    node.func = func
    node.resultEqual = resultEqualityFrom(memoizeOptions)
    bindInputs(logic, node, args)
  }

  try {
    assertNoCycle([...logicNodes(logic).values()])
  } catch (error) {
    const context = getContext()
    const set = context ? nodesByContext.get(context) : undefined
    for (const node of created) {
      logicNodes(logic).delete(node.name)
      set?.delete(node)
    }
    throw error
  }
}

export function createAtomicReducerSelector(logic: Logic, key: string): Selector {
  const node: AtomicNode = {
    stableId: stableId(logic, key),
    logic,
    name: key,
    kind: 'reducer',
    evaluations: 0,
    dirtyCause: null,
    dirty: false,
    hasValue: false,
    lastValue: undefined,
    dependencies: [],
    watches: [],
    inputSpecs: [],
    staticSelectorDeps: [],
    evaluating: false,
    suppressDirty: false,
    seenSlice: false,
    lastSlice: undefined,
  }
  registerNode(node)

  const fn = ((state: any = getStoreState()) => {
    let slice: any
    try {
      slice = logic.selector ? logic.selector(state) : undefined
    } catch {
      slice = undefined
    }
    if (!node.seenSlice || slice !== node.lastSlice) {
      node.seenSlice = true
      node.lastSlice = slice
      node.evaluations += 1
      node.lastValue = slice == null ? undefined : slice[key]
      node.hasValue = true
    }
    const value = slice == null ? undefined : slice[key]
    if (activeTracker) {
      return exposeValue(value, key, (root) => readReducerValue(logic, key, root), activeTracker)
    }
    return value
  }) as Selector

  return tag(fn, { kind: 'reducer', name: key, logic, node })
}

function dependentsOf(logic: Logic, name: string): string[] {
  const dependents: string[] = []
  for (const node of logicNodes(logic).values()) {
    if (node.kind !== 'computed') {
      continue
    }
    const deps = node.hasValue ? node.dependencies : node.staticSelectorDeps
    if (deps.includes(name)) {
      dependents.push(node.name)
    }
  }
  return dependents
}

function topologicalOrder(logic: Logic): string[] {
  const nodes = [...logicNodes(logic).values()]
  const byName = new Map(nodes.map((node) => [node.name, node]))
  const indegree = new Map<string, number>()
  const outgoing = new Map<string, string[]>()
  for (const node of nodes) {
    indegree.set(node.name, 0)
    outgoing.set(node.name, [])
  }
  for (const node of nodes) {
    if (node.kind !== 'computed') {
      continue
    }
    const deps = node.dependencies.length > 0 || node.hasValue ? node.dependencies : node.staticSelectorDeps
    const seen = new Set<string>()
    for (const dep of deps) {
      if (!byName.has(dep) || seen.has(dep)) {
        continue
      }
      seen.add(dep)
      indegree.set(node.name, (indegree.get(node.name) || 0) + 1)
      outgoing.get(dep)!.push(node.name)
    }
  }
  const queue = nodes.filter((node) => (indegree.get(node.name) || 0) === 0).map((node) => node.name)
  const order: string[] = []
  while (queue.length > 0) {
    const name = queue.shift()!
    order.push(name)
    for (const next of outgoing.get(name) || []) {
      const remaining = (indegree.get(next) || 0) - 1
      indegree.set(next, remaining)
      if (remaining === 0) {
        queue.push(next)
      }
    }
  }
  for (const node of nodes) {
    if (!order.includes(node.name)) {
      order.push(node.name)
    }
  }
  return order
}

export function reportSelectorHealth(logic: Logic): SelectorHealth {
  const selectors: SelectorHealth['selectors'] = {}
  for (const node of logicNodes(logic).values()) {
    const dependencies = node.hasValue ? node.dependencies : [...node.staticSelectorDeps]
    selectors[node.name] = {
      dependencies: [...dependencies],
      dependents: dependentsOf(logic, node.name),
      evaluations: node.evaluations,
      dirtyCause: node.dirtyCause,
    }
  }
  return { selectors, topologicalOrder: topologicalOrder(logic) }
}

function markSelectorDependents(node: AtomicNode): void {
  for (const other of contextNodeList()) {
    if (other.kind !== 'computed' || other === node) {
      continue
    }
    for (const spec of other.inputSpecs) {
      if (spec.type === 'computed' && spec.node === node) {
        markDirty(other, `selector:${spec.localName}`)
      }
    }
  }
}

function flushDirty(state: any): void {
  const nodes = contextNodeList().filter((node) => node.kind === 'computed')
  const indegree = new Map<AtomicNode, number>()
  const outgoing = new Map<AtomicNode, AtomicNode[]>()
  for (const node of nodes) {
    indegree.set(node, 0)
    outgoing.set(node, [])
  }
  for (const node of nodes) {
    const seen = new Set<AtomicNode>()
    for (const spec of node.inputSpecs) {
      if (spec.type === 'computed' && indegree.has(spec.node) && !seen.has(spec.node)) {
        seen.add(spec.node)
        indegree.set(node, (indegree.get(node) || 0) + 1)
        outgoing.get(spec.node)!.push(node)
      }
    }
  }
  const queue = nodes.filter((node) => (indegree.get(node) || 0) === 0)
  const order: AtomicNode[] = []
  while (queue.length > 0) {
    const node = queue.shift()!
    order.push(node)
    for (const next of outgoing.get(node) || []) {
      const remaining = (indegree.get(next) || 0) - 1
      indegree.set(next, remaining)
      if (remaining === 0) {
        queue.push(next)
      }
    }
  }
  for (const node of nodes) {
    if (!order.includes(node)) {
      order.push(node)
    }
  }

  for (const node of order) {
    if (!node.dirty || !node.hasValue || !node.func) {
      continue
    }
    const before = node.lastValue
    const changed = runCompute(node, state, node.logic.props)
    if (changed && !resultsEqual(node, before, node.lastValue)) {
      markSelectorDependents(node)
    }
  }
}

function implicitCause(id: string): string {
  if (id.endsWith('.length')) {
    return id.slice(0, -'.length'.length)
  }
  return id
}

export function notifyAtomicStateChange(previous: any, next: any): void {
  if (!isAtomicSelectorsEnabled() || notifying || previous === next) {
    return
  }
  notifying = true
  const suspended = activeTracker
  activeTracker = null
  try {
    withAtomicReadState(next, () => {
      for (const node of contextNodeList()) {
        if (node.kind !== 'computed' || !node.hasValue) {
          continue
        }
        for (const watch of node.watches) {
          if (watch.selector || watch.external) {
            continue
          }
          let before: any
          let after: any
          try {
            before = watch.read(previous)
            after = watch.read(next)
          } catch {
            continue
          }
          if (!Object.is(before, after)) {
            markDirty(node, watch.public ? watch.id : implicitCause(watch.id))
            break
          }
        }
        if (!node.dirty) {
          const cause = externalCause(node, next, node.logic.props)
          if (cause) {
            markDirty(node, cause)
          }
        }
      }
      flushDirty(next)
    })
  } finally {
    activeTracker = suspended
    notifying = false
  }
}
