import type { Middleware } from 'redux'
import type { DefaultMemoizeOptions } from 'reselect'
import { BuiltLogic, Logic, Selector, SelectorHealth, SelectorHealthReport } from '../types'
import { getContext, getPluginContext } from '../kea/context'

/**
  Atomic Signal Selector Engine, enabled with `resetContext({ atomicSelectors: true })`.

  Every selector built with `selectors()` becomes a node in a per-logic graph. While a node's compute function
  runs, its state inputs are wrapped in tracking proxies that record the exact leaf paths that were read
  (`user.name`, `list.0`, `data.map:a`, `data.set:a`). A node re-evaluates only when one of those leaves, or the
  output of one of its local selector dependencies, changes.
*/

type EqualityCheck = (a: any, b: any) => boolean
type TrackKind = 'root' | 'prop' | 'map' | 'set' | 'size'

interface AtomicInput {
  name: string
  fn: Selector
  isSelector: boolean
}

interface ProxyRecord {
  session: TrackingSession
  original: any
  proxy: any
  nodes: TrackNode[]
}

const proxyRecords = new WeakMap<object, ProxyRecord>()
const collectionMutators = new Set<PropertyKey>(['set', 'add', 'delete', 'clear'])
const collectionIterators = new Set<PropertyKey>(['forEach', 'keys', 'values', 'entries', Symbol.iterator])

function hasOwn(object: any, key: PropertyKey): boolean {
  return Object.prototype.hasOwnProperty.call(object, key)
}

function isPlainObject(value: any): boolean {
  const proto = Object.getPrototypeOf(value)
  return proto === Object.prototype || proto === null
}

function isTrackable(value: any): boolean {
  if (typeof value !== 'object' || value === null || proxyRecords.has(value)) {
    return false
  }
  return Array.isArray(value) || value instanceof Map || value instanceof Set || isPlainObject(value)
}

function unwrap<T>(value: T): T {
  const record = typeof value === 'object' && value !== null ? proxyRecords.get(value as any) : undefined
  return record ? record.original : value
}

function sameValueZero(a: any, b: any): boolean {
  return a === b || (a !== a && b !== b)
}

function toInteger(value: any): number {
  const number = Math.trunc(Number(value))
  return Number.isNaN(number) ? 0 : number
}

function ownKeysOf(value: any): PropertyKey[] {
  return typeof value === 'object' && value !== null ? Reflect.ownKeys(value) : []
}

function sameKeys(a: PropertyKey[], b: PropertyKey[]): boolean {
  return a.length === b.length && a.every((key, index) => key === b[index])
}

/** One path read by a compute function, e.g. `user.name` */
class TrackNode {
  value: any = undefined
  reached = false
  hasCheck = false
  hasResult = false
  keys: PropertyKey[] | null = null
  whole = false
  children: TrackNode[] = []
  private index: Partial<Record<TrackKind, Map<any, TrackNode>>> = {}

  constructor(readonly path: string, readonly kind: TrackKind, readonly key: any) {}

  child(kind: TrackKind, key: any): TrackNode {
    const byKey = (this.index[kind] ??= new Map())
    let node = byKey.get(key)
    if (!node) {
      const segment = kind === 'map' ? `map:${String(key)}` : kind === 'set' ? `set:${String(key)}` : String(key)
      node = new TrackNode(`${this.path}.${segment}`, kind, key)
      byKey.set(key, node)
      this.children.push(node)
    }
    return node
  }
}

function childValue(node: TrackNode, parent: any): any {
  if (node.kind === 'map') {
    return parent instanceof Map ? parent.get(node.key) : undefined
  }
  if (node.kind === 'size') {
    return parent instanceof Map || parent instanceof Set ? parent.size : undefined
  }
  return parent === null || parent === undefined ? undefined : parent[node.key]
}

function childHas(node: TrackNode, parent: any): boolean {
  if (node.kind === 'map') {
    return parent instanceof Map && parent.has(node.key)
  }
  if (node.kind === 'set') {
    return parent instanceof Set && parent.has(node.key)
  }
  return typeof parent === 'object' && parent !== null && node.key in parent
}

/** Returns the path of the first tracked leaf that differs in `value`, or null. Queues value updates otherwise. */
function diffNode(node: TrackNode, value: any, equals: EqualityCheck, updates: [TrackNode, any][]): string | null {
  if (Object.is(node.value, value)) {
    return null
  }
  if (node.whole || (node.children.length === 0 && node.keys === null)) {
    if (!equals(node.value, value)) {
      return node.path
    }
  } else {
    if (node.keys !== null && !sameKeys(node.keys, ownKeysOf(value))) {
      return node.path
    }
    for (const child of node.children) {
      if (child.hasCheck && childHas(child, value) !== child.hasResult) {
        return child.path
      }
      if (child.reached) {
        const cause = diffNode(child, childValue(child, value), equals, updates)
        if (cause !== null) {
          return cause
        }
      }
    }
  }
  updates.push([node, value])
  return null
}

function collectLeaves(node: TrackNode, leaves: Set<string>): void {
  if (node.whole || node.children.length === 0) {
    leaves.add(node.path)
    return
  }
  for (const child of node.children) {
    collectLeaves(child, leaves)
  }
}

/** Frozen targets would make the proxy invariants reject returning nested proxies, so we proxy a loose copy. */
function unfrozenCopy(object: any): any {
  const copy = Array.isArray(object) ? [] : Object.create(Object.getPrototypeOf(object))
  for (const key of Reflect.ownKeys(object)) {
    if (Array.isArray(object) && key === 'length') {
      continue
    }
    const descriptor = Reflect.getOwnPropertyDescriptor(object, key)!
    descriptor.configurable = true
    if ('value' in descriptor) {
      descriptor.writable = true
    }
    Object.defineProperty(copy, key, descriptor)
  }
  if (Array.isArray(object)) {
    copy.length = object.length
  }
  return copy
}

function bindToOriginal(target: any, prop: PropertyKey): any {
  const value = Reflect.get(target, prop, target)
  if (typeof value !== 'function') {
    return value
  }
  return collectionMutators.has(prop) ? (...args: any[]) => value.apply(target, args.map(unwrap)) : value.bind(target)
}

function searchArray(
  record: ProxyRecord,
  method: 'includes' | 'indexOf' | 'lastIndexOf',
  search: any,
  fromIndex?: any,
): boolean | number {
  const { session } = record
  const list: any[] = record.original
  const target = unwrap(search)
  const length = list.length

  if (method === 'lastIndexOf') {
    const start = fromIndex === undefined ? length - 1 : toInteger(fromIndex)
    session.read(record, 'prop', 'length', length)
    for (let i = start < 0 ? length + start : Math.min(start, length - 1); i >= 0; i--) {
      session.readWhole(record, String(i), list[i])
      if (i in list && list[i] === target) {
        return i
      }
    }
    return -1
  }

  let start = fromIndex === undefined ? 0 : toInteger(fromIndex)
  const dependsOnLength = start < 0
  if (start < 0) {
    start = Math.max(length + start, 0)
  }
  for (let i = start; i < length; i++) {
    const value = list[i]
    session.readWhole(record, String(i), value)
    if (method === 'includes' ? sameValueZero(value, target) : i in list && value === target) {
      // an `undefined` element still reads as `undefined` after the array shrinks past it
      if (dependsOnLength || value === undefined) {
        session.read(record, 'prop', 'length', length)
      }
      return method === 'includes' ? true : i
    }
  }
  session.read(record, 'prop', 'length', length)
  return method === 'includes' ? false : -1
}

function scanArray(
  record: ProxyRecord,
  proxy: any,
  method: 'find' | 'findIndex' | 'some' | 'every',
  callback: (value: any, index: number, array: any) => unknown,
  thisArg?: any,
): any {
  const { session } = record
  const list: any[] = record.original
  const length = list.length
  const skipHoles = method === 'some' || method === 'every'
  for (let i = 0; i < length; i++) {
    if (skipHoles && !(i in list)) {
      session.has(record, 'prop', String(i), false)
      continue
    }
    const element = session.read(record, 'prop', String(i), list[i])
    const result = callback.call(thisArg, element, i, proxy)
    if (method === 'find' && result) {
      return element
    } else if (method === 'findIndex' && result) {
      return i
    } else if (method === 'some' && result) {
      return true
    } else if (method === 'every' && !result) {
      return false
    }
  }
  session.read(record, 'prop', 'length', length)
  return method === 'find' ? undefined : method === 'findIndex' ? -1 : method === 'every'
}

const arrayMethods: Record<string, (record: ProxyRecord, proxy: any) => (...args: any[]) => any> = {
  includes: (record) => (search, fromIndex) => searchArray(record, 'includes', search, fromIndex),
  indexOf: (record) => (search, fromIndex) => searchArray(record, 'indexOf', search, fromIndex),
  lastIndexOf:
    (record) =>
    (search, ...fromIndex) =>
      searchArray(record, 'lastIndexOf', search, fromIndex.length > 0 ? toInteger(fromIndex[0]) : undefined),
  find: (record, proxy) => (callback, thisArg) => scanArray(record, proxy, 'find', callback, thisArg),
  findIndex: (record, proxy) => (callback, thisArg) => scanArray(record, proxy, 'findIndex', callback, thisArg),
  some: (record, proxy) => (callback, thisArg) => scanArray(record, proxy, 'some', callback, thisArg),
  every: (record, proxy) => (callback, thisArg) => scanArray(record, proxy, 'every', callback, thisArg),
}

function objectHandler(record: ProxyRecord): ProxyHandler<any> {
  const { session, original } = record
  return {
    get(target, prop, receiver) {
      if (typeof prop === 'symbol' || !session.active) {
        return Reflect.get(original, prop)
      }
      if (Array.isArray(original) && hasOwn(arrayMethods, prop)) {
        return arrayMethods[prop](record, receiver)
      }
      if (!hasOwn(original, prop) && prop in original) {
        // inherited members (array methods, toString, ...) are not state
        return Reflect.get(original, prop)
      }
      const value = Reflect.get(original, prop)
      const tracked = session.read(record, 'prop', prop, value)
      const descriptor = Reflect.getOwnPropertyDescriptor(target, prop)
      return descriptor && !descriptor.configurable && descriptor.writable === false ? value : tracked
    },
    has(target, prop) {
      const result = Reflect.has(original, prop)
      if (typeof prop !== 'symbol' && session.active && (!result || hasOwn(original, prop))) {
        session.has(record, 'prop', prop, result)
      }
      return result
    },
    ownKeys() {
      const keys = Reflect.ownKeys(original)
      session.keys(record, keys)
      return keys
    },
    set(target, prop, value) {
      return Reflect.set(original, prop, unwrap(value))
    },
    deleteProperty(target, prop) {
      return Reflect.deleteProperty(original, prop)
    },
    defineProperty(target, prop, descriptor) {
      return Reflect.defineProperty(original, prop, descriptor)
    },
  }
}

function collectionHandler(record: ProxyRecord): ProxyHandler<any> {
  const { session, original } = record
  const kind: TrackKind = original instanceof Map ? 'map' : 'set'
  return {
    get(target, prop) {
      if (session.active) {
        if (prop === 'size') {
          return session.read(record, 'size', 'size', original.size)
        }
        if (prop === 'get' && kind === 'map') {
          return (key: any) => {
            const originalKey = unwrap(key)
            return session.read(record, 'map', originalKey, original.get(originalKey))
          }
        }
        if (prop === 'has') {
          return (key: any) => {
            const originalKey = unwrap(key)
            const result = original.has(originalKey)
            session.has(record, kind, originalKey, result)
            return result
          }
        }
        if (collectionIterators.has(prop)) {
          session.markWhole(record)
        }
      }
      return bindToOriginal(original, prop)
    },
  }
}

/** Records the reads made by one run of a selector's compute function */
class TrackingSession {
  active = true
  roots: (TrackNode | undefined)[] = []
  private records = new Map<object, ProxyRecord>()

  root(index: number, name: string, value: any): any {
    const node = new TrackNode(name, 'root', undefined)
    node.reached = true
    node.value = value
    this.roots[index] = node
    return this.wrap(value, [node])
  }

  wrap(value: any, nodes: TrackNode[]): any {
    if (!isTrackable(value)) {
      return value
    }
    let record = this.records.get(value)
    if (!record) {
      record = { session: this, original: value, proxy: undefined, nodes: [] }
      record.proxy =
        value instanceof Map || value instanceof Set
          ? new Proxy(value, collectionHandler(record))
          : new Proxy(Object.isFrozen(value) ? unfrozenCopy(value) : value, objectHandler(record))
      this.records.set(value, record)
      proxyRecords.set(record.proxy, record)
    }
    for (const node of nodes) {
      if (!record.nodes.includes(node)) {
        record.nodes.push(node)
      }
    }
    return record.proxy
  }

  read(record: ProxyRecord, kind: TrackKind, key: any, value: any): any {
    if (!this.active) {
      return value
    }
    const children = record.nodes.map((node) => {
      const child = node.child(kind, key)
      child.reached = true
      child.value = value
      return child
    })
    return this.wrap(value, children)
  }

  readWhole(record: ProxyRecord, key: string, value: any): void {
    if (this.active) {
      for (const node of record.nodes) {
        const child = node.child('prop', key)
        child.reached = true
        child.value = value
        child.whole = true
      }
    }
  }

  has(record: ProxyRecord, kind: TrackKind, key: any, result: boolean): void {
    if (this.active) {
      for (const node of record.nodes) {
        const child = node.child(kind, key)
        child.hasCheck = true
        child.hasResult = result
      }
    }
  }

  keys(record: ProxyRecord, keys: PropertyKey[]): void {
    if (this.active) {
      for (const node of record.nodes) {
        node.keys = [...keys]
      }
    }
  }

  markWhole(record: ProxyRecord): void {
    for (const node of record.nodes) {
      node.whole = true
    }
  }

  /** Swap tracking proxies in a compute function's result back to the originals, tracking them by identity. */
  untrack(value: any, seen = new Map<object, any>()): any {
    if (typeof value !== 'object' || value === null) {
      return value
    }
    const record = proxyRecords.get(value)
    if (record) {
      if (record.session === this) {
        this.markWhole(record)
      }
      return record.original
    }
    if (seen.has(value)) {
      return seen.get(value)
    }
    seen.set(value, value)

    let result = value
    if (Array.isArray(value) || isPlainObject(value)) {
      for (const key of Object.keys(value)) {
        const item = value[key]
        const untracked = this.untrack(item, seen)
        if (untracked !== item) {
          if (result === value && Object.isFrozen(value)) {
            result = Array.isArray(value)
              ? [...value]
              : Object.assign(Object.create(Object.getPrototypeOf(value)), value)
          }
          result[key] = untracked
        }
      }
      if (result !== value) {
        Object.freeze(result)
      }
    } else if (value instanceof Map) {
      const entries = Array.from(value, ([k, v]) => [k, v, this.untrack(k, seen), this.untrack(v, seen)])
      if (entries.some(([k, v, uk, uv]) => k !== uk || v !== uv)) {
        value.clear()
        entries.forEach(([, , uk, uv]) => value.set(uk, uv))
      }
    } else if (value instanceof Set) {
      const items = Array.from(value, (item) => [item, this.untrack(item, seen)])
      if (items.some(([item, untracked]) => item !== untracked)) {
        value.clear()
        items.forEach(([, untracked]) => value.add(untracked))
      }
    }
    seen.set(value, result)
    return result
  }

  close(): void {
    this.active = false
    this.records.clear()
  }
}

function circularDependencyError(logic: Logic, cycle: string[]): Error {
  return new Error(`[KEA] Circular dependency detected in logic "${logic.pathString}": ${cycle.join(' -> ')}`)
}

const readStack: AtomicSelectorNode[] = []

class AtomicSelectorNode {
  evaluations = 0
  dirtyCause: string | null = null
  private hasValue = false
  private value: any = undefined
  private inputValues: any[] = []
  private trees: (TrackNode | undefined)[] = []
  private readonly equals: EqualityCheck
  private readonly resultEqualityCheck?: EqualityCheck

  constructor(
    readonly graph: AtomicSelectorGraph,
    readonly name: string,
    readonly inputs: AtomicInput[],
    private readonly func: (...args: any[]) => any,
    memoizeOptions?: DefaultMemoizeOptions,
  ) {
    this.equals = memoizeOptions?.equalityCheck ?? Object.is
    this.resultEqualityCheck = memoizeOptions?.resultEqualityCheck
  }

  get isActive(): boolean {
    return this.hasValue
  }

  read(state: any, props: any): any {
    const cycleStart = readStack.indexOf(this)
    if (cycleStart !== -1) {
      const cycle = readStack.slice(cycleStart).map((node) => node.name)
      throw circularDependencyError(this.graph.logic, [...cycle, this.name])
    }
    readStack.push(this)
    try {
      const inputValues = this.inputs.map((input) => input.fn(state, props))
      if (this.hasValue) {
        const cause = this.findChange(inputValues)
        if (cause === null) {
          return this.value
        }
        this.dirtyCause = cause
      }
      return this.evaluate(inputValues)
    } finally {
      readStack.pop()
    }
  }

  dependencies(): string[] {
    const dependencies = new Set<string>()
    this.inputs.forEach((input, index) => {
      const tree = this.trees[index]
      if (input.isSelector) {
        dependencies.add(input.name)
      } else if (tree) {
        collectLeaves(tree, dependencies)
      }
    })
    // not `[...dependencies]`: the loose Babel build compiles spreads as if they were always arrays
    return Array.from(dependencies)
  }

  private findChange(inputValues: any[]): string | null {
    const updates: [TrackNode, any][] = []
    for (let i = 0; i < this.inputs.length; i++) {
      const input = this.inputs[i]
      const previous = this.inputValues[i]
      const next = inputValues[i]
      if (input.isSelector) {
        if (!this.equals(previous, next)) {
          return `selector:${input.name}`
        }
      } else if (!Object.is(previous, next)) {
        const cause = diffNode(this.trees[i]!, next, this.equals, updates)
        if (cause !== null) {
          return cause
        }
      }
    }
    for (const [node, value] of updates) {
      node.value = value
    }
    this.inputValues = inputValues
    return null
  }

  private evaluate(inputValues: any[]): any {
    const session = new TrackingSession()
    const args = inputValues.map((value, index) => {
      const input = this.inputs[index]
      return input.isSelector ? value : session.root(index, input.name, value)
    })
    this.evaluations += 1
    let result
    try {
      result = session.untrack(this.func(...args))
    } finally {
      session.close()
    }
    if (this.hasValue && this.resultEqualityCheck?.(this.value, result)) {
      result = this.value
    }
    this.value = result
    this.hasValue = true
    this.inputValues = inputValues
    this.trees = session.roots
    return result
  }
}

/** All atomic selectors of one logic, keyed by their local name */
export class AtomicSelectorGraph {
  private nodes = new Map<string, AtomicSelectorNode | undefined>()
  private order: string[] = []

  constructor(readonly logic: BuiltLogic) {}

  declare(name: string): void {
    this.nodes.set(name, undefined)
  }

  define(
    name: string,
    args: Selector[],
    inputNames: Map<unknown, string>,
    func: (...args: any[]) => any,
    memoizeOptions?: DefaultMemoizeOptions,
  ): void {
    const { selectors } = this.logic
    const inputs = args.map((fn, index) => {
      const inputName =
        inputNames.get(fn) ?? Object.keys(selectors).find((key) => selectors[key] === fn) ?? `arg${index}`
      return { name: inputName, fn, isSelector: this.nodes.has(inputName) && selectors[inputName] === fn }
    })
    this.nodes.set(name, new AtomicSelectorNode(this, name, inputs, func, memoizeOptions))
  }

  read(name: string, state: any, props: any): any {
    return this.nodes.get(name)!.read(state, props)
  }

  /** Orders the selectors so that dependencies come first. Throws on circular dependencies. */
  sort(): void {
    const order: string[] = []
    const visiting: string[] = []
    const done = new Set<string>()
    const visit = (name: string) => {
      if (done.has(name)) {
        return
      }
      if (visiting.includes(name)) {
        throw circularDependencyError(this.logic, [...visiting.slice(visiting.indexOf(name)), name])
      }
      visiting.push(name)
      for (const input of this.nodes.get(name)?.inputs ?? []) {
        if (input.isSelector) {
          visit(input.name)
        }
      }
      visiting.pop()
      done.add(name)
      order.push(name)
    }
    for (const name of this.nodes.keys()) {
      visit(name)
    }
    this.order = order
  }

  /** Re-validate every selector that has been evaluated, dependencies first */
  refresh(state: any): void {
    for (const name of this.order) {
      const node = this.nodes.get(name)
      if (node?.isActive) {
        try {
          node.read(state, this.logic.props)
        } catch (error) {
          // the node stays invalidated, so the error surfaces to whoever reads it next
        }
      }
    }
  }

  health(): SelectorHealthReport {
    const selectors: Record<string, SelectorHealth> = {}
    for (const [name, node] of this.nodes) {
      if (node) {
        selectors[name] = {
          dependencies: node.dependencies(),
          dependents: [],
          evaluations: node.evaluations,
          dirtyCause: node.dirtyCause,
        }
      }
    }
    for (const [name, node] of this.nodes) {
      for (const input of node?.inputs ?? []) {
        const dependents = selectors[input.name]?.dependents
        if (input.isSelector && dependents && !dependents.includes(name)) {
          dependents.push(name)
        }
      }
    }
    return { selectors, topologicalOrder: [...this.order] }
  }
}

function getGraphs(): WeakMap<Logic, AtomicSelectorGraph> {
  const pluginContext = getPluginContext<{ graphs?: WeakMap<Logic, AtomicSelectorGraph> }>('atomicSelectors')
  return (pluginContext.graphs ??= new WeakMap())
}

export function getAtomicSelectorGraph(logic: BuiltLogic): AtomicSelectorGraph {
  const graphs = getGraphs()
  let graph = graphs.get(logic)
  if (!graph) {
    graph = new AtomicSelectorGraph(logic)
    graphs.set(logic, graph)
  }
  return graph
}

export function getSelectorHealth(logic: Logic): SelectorHealthReport {
  return getGraphs().get(logic)?.health() ?? { selectors: {}, topologicalOrder: [] }
}

/** Remembers under which key each selector function was taken from `target`, e.g. `s.user` or `p.id` */
export function trackSelectorNames<T extends object>(target: T, names: Map<unknown, string>, prefix = ''): T {
  return new Proxy(target, {
    get(target, prop) {
      const value = Reflect.get(target, prop)
      if (typeof prop === 'string' && typeof value === 'function' && !names.has(value)) {
        names.set(value, `${prefix}${prop}`)
      }
      return value
    },
  })
}

/** Pushes every state change through the evaluated atomic selectors of all mounted logic, once per action */
export const atomicSelectorsMiddleware: Middleware = (store) => (next) => (action) => {
  const previousState = store.getState()
  const response = next(action)
  const state = store.getState()
  if (state !== previousState) {
    const graphs = getGraphs()
    for (const logic of Object.values(getContext().mount.mounted)) {
      graphs.get(logic)?.refresh(state)
    }
  }
  return response
}
