import type { Middleware } from 'redux'
import { Context, Logic, Selector, SelectorHealth } from '../types'
import { getContext } from '../kea/context'

/**
  Atomic Signal Selector Engine, enabled with `resetContext({ atomicSelectors: true })`.

  Selectors built with `selectors({})` record which leaves of their inputs the compute function actually
  reads (`user.name`, `data.map:a`, `list.0`, ...). On the next call, only those leaves are compared, so
  a change to `user.age` does not re-run a selector that only read `user.name`.
*/

type SelectorKind = 'reducer' | 'selector' | 'prop'

interface SelectorIdentity {
  id: string
  pathString: string
  name: string
  kind: SelectorKind
}

// Kept outside of `context.plugins` so that enabling the engine does not change the plugin registry or event order
interface AtomicSelectorsContext {
  identities: WeakMap<Function, SelectorIdentity>
  logicNodes: WeakMap<Logic, Record<string, AtomicSelectorNode>>
  pass: number
}

type EqualityCheck = (a: any, b: any) => boolean

interface InputSource {
  fn: Selector
  name: string
  // set if the input is another selector built with selectors({}), compared by value instead of by leaves
  derived: SelectorIdentity | null
  localDerived: boolean
}

const UNSET = {}
const atomicContexts = new WeakMap<Context, AtomicSelectorsContext>()

export function isAtomicSelectorsEnabled(): boolean {
  return !!getContext()?.options?.atomicSelectors
}

function getAtomicContext(): AtomicSelectorsContext {
  const context = getContext()
  let atomicContext = atomicContexts.get(context)
  if (!atomicContext) {
    atomicContext = { identities: new WeakMap(), logicNodes: new WeakMap(), pass: 0 }
    atomicContexts.set(context, atomicContext)
  }
  return atomicContext
}

export function selectorId(pathString: string, name: string): string {
  return `${pathString}::${name}`
}

/** Remember which logic and local name a selector function belongs to, so that it survives re-wrapping. */
export function tagSelector(fn: Selector, logic: Logic, name: string, kind: SelectorKind): void {
  getAtomicContext().identities.set(fn, { id: selectorId(logic.pathString, name), pathString: logic.pathString, name, kind })
}

/** Find the node of a selector on the currently mounted logic with that identity */
function findMountedNode(identity: SelectorIdentity): AtomicSelectorNode | undefined {
  const logic = getContext().mount.mounted[identity.pathString]
  return logic ? getAtomicContext().logicNodes.get(logic)?.[identity.name] : undefined
}

function getLogicNodes(logic: Logic): Record<string, AtomicSelectorNode> {
  const { logicNodes } = getAtomicContext()
  let nodes = logicNodes.get(logic)
  if (!nodes) {
    nodes = {}
    logicNodes.set(logic, nodes)
  }
  return nodes
}

// ---------------------------------------------------------------------------------------------------------------
// Access tracking
// ---------------------------------------------------------------------------------------------------------------

type Step = { t: 'prop'; k: PropertyKey } | { t: 'map'; k: any } | { t: 'set'; k: any }

class TrackNode {
  path: string
  step: Step | null
  children: TrackNode[] = []
  propChildren: Map<PropertyKey, TrackNode> | undefined
  mapChildren: Map<any, TrackNode> | undefined
  setChildren: Map<any, TrackNode> | undefined
  readValue = false
  readHas = false
  whole = false
  value: any = undefined
  has = false

  constructor(path: string, step: Step | null) {
    this.path = path
    this.step = step
  }

  child(t: Step['t'], k: any): TrackNode {
    const store =
      t === 'prop'
        ? (this.propChildren ??= new Map())
        : t === 'map'
        ? (this.mapChildren ??= new Map())
        : (this.setChildren ??= new Map())
    let child = store.get(k)
    if (!child) {
      const segment = t === 'prop' ? String(k) : `${t}:${String(k)}`
      child = new TrackNode(`${this.path}.${segment}`, { t, k } as Step)
      store.set(k, child)
      this.children.push(child)
    }
    return child
  }

  readChild(t: Step['t'], k: any, value: any): TrackNode {
    const child = this.child(t, k)
    if (!child.readValue) {
      child.readValue = true
      child.value = value
    }
    return child
  }

  hasChild(t: Step['t'], k: any, has: boolean): TrackNode {
    const child = this.child(t, k)
    if (!child.readHas) {
      child.readHas = true
      child.has = has
    }
    return child
  }
}

function isObjectLike(value: any): value is Record<PropertyKey, any> {
  return value !== null && (typeof value === 'object' || typeof value === 'function')
}

function isPlainObject(value: any): boolean {
  if (typeof value !== 'object' || value === null) {
    return false
  }
  const proto = Object.getPrototypeOf(value)
  return proto === Object.prototype || proto === null
}

function readStep(parent: any, step: Step): any {
  if (step.t === 'prop') {
    return isObjectLike(parent) ? parent[step.k as any] : undefined
  } else if (step.t === 'map') {
    return parent instanceof Map ? parent.get(step.k) : undefined
  } else {
    return parent instanceof Set ? parent.has(step.k) : undefined
  }
}

function hasStep(parent: any, step: Step): boolean {
  if (step.t === 'prop') {
    return isObjectLike(parent) ? step.k in parent : false
  } else if (step.t === 'map') {
    return parent instanceof Map ? parent.has(step.k) : false
  } else {
    return parent instanceof Set ? parent.has(step.k) : false
  }
}

/** Returns the first tracked leaf whose value differs in `current`, or null if nothing that was read changed. */
function findChange(node: TrackNode, current: any, eq: EqualityCheck): TrackNode | null {
  if (node.readValue && current === node.value) {
    return null // immutable state: an identical object means nothing below it changed either
  }
  if (node.whole || node.children.length === 0) {
    return node.readValue && !eq(current, node.value) ? node : null
  }
  for (const child of node.children) {
    if (child.readHas && hasStep(current, child.step!) !== child.has) {
      return child
    }
    if (child.readValue) {
      const changed = findChange(child, readStep(current, child.step!), eq)
      if (changed) {
        return changed
      }
    }
  }
  return null
}

function collectLeaves(node: TrackNode, out: string[]): void {
  if (node.whole || node.children.length === 0) {
    out.push(node.path)
    return
  }
  for (const child of node.children) {
    collectLeaves(child, out)
  }
}

function unfrozenCopy(raw: any): any {
  if (Array.isArray(raw)) {
    return raw.slice()
  }
  const descriptors = Object.getOwnPropertyDescriptors(raw)
  for (const key of Reflect.ownKeys(descriptors)) {
    const descriptor = descriptors[key as any]
    descriptor.configurable = true
    if ('value' in descriptor) {
      descriptor.writable = true
    }
  }
  return Object.create(Object.getPrototypeOf(raw), descriptors)
}

const sameValueZero = (a: any, b: any) => a === b || (a !== a && b !== b)
const toIntegerOrInfinity = (value: any) => {
  const n = Number(value)
  return n !== n ? 0 : Math.trunc(n)
}

/** Hands out proxies for one run of a compute function and records what they read into TrackNode trees. */
class TrackingSession {
  open = true
  roots: (TrackNode | null)[] = []
  proxies = new WeakMap<object, any>()
  proxyInfo = new WeakMap<object, { raw: any; node: TrackNode }>()
  raws = new WeakSet<object>()

  root(name: string, value: any): any {
    const node = new TrackNode(name, null)
    node.readValue = true
    node.value = value
    this.roots.push(node)
    return this.wrap(value, node)
  }

  skip(value: any): any {
    this.roots.push(null)
    if (isObjectLike(value)) {
      this.raws.add(value)
    }
    return value
  }

  unwrapArg(value: any): any {
    const info = isObjectLike(value) ? this.proxyInfo.get(value) : undefined
    if (info) {
      info.node.whole = true
      return info.raw
    }
    return value
  }

  wrap(value: any, node: TrackNode): any {
    if (!this.open || typeof value !== 'object' || value === null) {
      return value
    }
    const isMap = value instanceof Map
    const isSet = value instanceof Set
    if (!isMap && !isSet && !Array.isArray(value) && !isPlainObject(value)) {
      return value // Dates, class instances, etc: compared by identity
    }
    const cached = this.proxies.get(value)
    if (cached) {
      return cached
    }
    const proxy = isMap ? this.mapProxy(value, node) : isSet ? this.setProxy(value, node) : this.objectProxy(value, node)
    this.proxies.set(value, proxy)
    this.proxyInfo.set(proxy, { raw: value, node })
    this.raws.add(value)
    return proxy
  }

  objectProxy(raw: any, node: TrackNode): any {
    const session = this
    const target = Object.isFrozen(raw) ? unfrozenCopy(raw) : raw
    const isArray = Array.isArray(raw)
    return new Proxy(target, {
      get(t, key) {
        if (!session.open || typeof key === 'symbol') {
          return Reflect.get(raw, key)
        }
        if (isArray && (key === 'includes' || key === 'indexOf' || key === 'lastIndexOf')) {
          return session.arraySearch(raw, node, key)
        }
        const value = Reflect.get(raw, key)
        if (!Object.prototype.hasOwnProperty.call(raw, key) && key in raw) {
          return value // inherited, e.g. array.map, which then reads through this proxy
        }
        const child = node.readChild('prop', key, value)
        const descriptor = Reflect.getOwnPropertyDescriptor(t, key)
        if (descriptor && !descriptor.configurable && descriptor.writable === false) {
          return value // proxy invariant: must return the exact value
        }
        return session.wrap(value, child)
      },
      has(t, key) {
        const result = Reflect.has(raw, key)
        if (session.open && typeof key !== 'symbol') {
          node.hasChild('prop', key, result)
        }
        return result
      },
      ownKeys(t) {
        if (session.open) {
          node.whole = true
        }
        return Reflect.ownKeys(t)
      },
      getOwnPropertyDescriptor(t, key) {
        if (session.open && typeof key !== 'symbol') {
          node.readChild('prop', key, Reflect.get(raw, key))
          node.hasChild('prop', key, Object.prototype.hasOwnProperty.call(raw, key))
        }
        return Reflect.getOwnPropertyDescriptor(t, key)
      },
    })
  }

  arraySearch(raw: any[], node: TrackNode, method: 'includes' | 'indexOf' | 'lastIndexOf'): (...args: any[]) => any {
    const session = this
    return function (searchElement: any, fromIndex?: any) {
      const search = session.unwrapArg(searchElement)
      const length = raw.length
      const readLength = () => node.readChild('prop', 'length', length)
      const readIndex = (i: number) => node.readChild('prop', String(i), raw[i]).value
      const matches = (value: any, i: number) =>
        method === 'includes' ? sameValueZero(value, search) : value === search && (value !== undefined || i in raw)

      if (method === 'lastIndexOf') {
        readLength()
        let start = arguments.length > 1 ? toIntegerOrInfinity(fromIndex) : length - 1
        start = start < 0 ? length + start : Math.min(start, length - 1)
        for (let i = start; i >= 0; i--) {
          if (matches(readIndex(i), i)) {
            return i
          }
        }
        return -1
      }

      let start = arguments.length > 1 ? toIntegerOrInfinity(fromIndex) : 0
      if (start < 0) {
        start = Math.max(length + start, 0)
      }
      if (arguments.length > 1 || search === undefined) {
        readLength()
      }
      for (let i = start; i < length; i++) {
        if (matches(readIndex(i), i)) {
          return method === 'includes' ? true : i
        }
      }
      readLength()
      return method === 'includes' ? false : -1
    }
  }

  mapProxy(raw: Map<any, any>, node: TrackNode): any {
    const session = this
    return new Proxy(raw, {
      get(_, key) {
        if (session.open) {
          if (key === 'get') {
            return (k: any) => {
              const mapKey = session.unwrapArg(k)
              const value = raw.get(mapKey)
              return session.wrap(value, node.readChild('map', mapKey, value))
            }
          } else if (key === 'has') {
            return (k: any) => {
              const mapKey = session.unwrapArg(k)
              const result = raw.has(mapKey)
              node.hasChild('map', mapKey, result)
              return result
            }
          } else if (key === 'size') {
            return node.readChild('prop', 'size', raw.size).value
          }
          node.whole = true // iteration or anything else looks at the whole collection
        }
        const value = Reflect.get(raw, key, raw)
        return typeof value === 'function' ? value.bind(raw) : value
      },
    })
  }

  setProxy(raw: Set<any>, node: TrackNode): any {
    const session = this
    return new Proxy(raw, {
      get(_, key) {
        if (session.open) {
          if (key === 'has') {
            return (v: any) => {
              const member = session.unwrapArg(v)
              return node.readChild('set', member, raw.has(member)).value
            }
          } else if (key === 'size') {
            return node.readChild('prop', 'size', raw.size).value
          }
          node.whole = true
        }
        const value = Reflect.get(raw, key, raw)
        return typeof value === 'function' ? value.bind(raw) : value
      },
    })
  }

  /** Replace any proxies that escaped into the result with their raw objects, and depend on them as a whole. */
  unwrap(value: any, seen: Map<any, any> = new Map()): any {
    if (typeof value !== 'object' || value === null) {
      return value
    }
    const info = this.proxyInfo.get(value)
    if (info) {
      info.node.whole = true
      return info.raw
    }
    if (this.raws.has(value)) {
      return value
    }
    if (seen.has(value)) {
      return seen.get(value)
    }
    seen.set(value, value)

    if (Array.isArray(value) || isPlainObject(value)) {
      let result = value
      for (const key of Object.keys(value)) {
        const descriptor = Object.getOwnPropertyDescriptor(value, key)
        if (!descriptor || !('value' in descriptor)) {
          continue
        }
        const unwrapped = this.unwrap(descriptor.value, seen)
        if (unwrapped !== descriptor.value) {
          if (result === value && Object.isFrozen(value)) {
            result = Array.isArray(value) ? value.slice() : { ...value }
          }
          result[key] = unwrapped
        }
      }
      seen.set(value, result)
      return result
    }
    if (value instanceof Map || value instanceof Set) {
      let changed = false
      const entries = Array.from(value.entries()).map(([k, v]) => {
        const [uk, uv] = [this.unwrap(k, seen), this.unwrap(v, seen)]
        changed = changed || uk !== k || uv !== v
        return [uk, uv]
      })
      const result = !changed
        ? value
        : value instanceof Map
        ? new Map(entries as [any, any][])
        : new Set(entries.map(([k]) => k))
      seen.set(value, result)
      return result
    }
    return value
  }
}

// ---------------------------------------------------------------------------------------------------------------
// Selector nodes
// ---------------------------------------------------------------------------------------------------------------

export class AtomicSelectorNode {
  id: string
  logic: Logic
  name: string
  inputs: InputSource[]
  func: (...args: any[]) => any
  equalityCheck: EqualityCheck
  resultEqualityCheck: EqualityCheck | undefined

  evaluations = 0
  dirtyCause: string | null = null
  dirty = false
  hasValue = false
  value: any = undefined
  lastState: any = UNSET
  changedState: any = UNSET
  inputValues: any[] = []
  roots: (TrackNode | null)[] = []
  evaluating = false
  markPass = -1
  markResult = false

  constructor(logic: Logic, name: string, inputs: InputSource[], func: (...args: any[]) => any, memoizeOptions: any) {
    this.id = selectorId(logic.pathString, name)
    this.logic = logic
    this.name = name
    this.inputs = inputs
    this.func = func
    const options = typeof memoizeOptions === 'function' ? { equalityCheck: memoizeOptions } : memoizeOptions ?? {}
    this.equalityCheck = options.equalityCheck ?? Object.is
    this.resultEqualityCheck = options.resultEqualityCheck
  }

  get localDependencies(): string[] {
    return this.inputs.filter((input) => input.localDerived).map((input) => input.name)
  }

  get dependencies(): string[] {
    const out: string[] = []
    this.inputs.forEach((input, index) => {
      const root = this.roots[index]
      if (!input.derived && root) {
        collectLeaves(root, out)
      } else {
        out.push(input.name)
      }
    })
    return Array.from(new Set(out))
  }

  circularError(): Error {
    return new Error(
      `[KEA] Circular dependency detected: selector "${this.name}" on logic "${this.logic.pathString}" depends on itself.`,
    )
  }

  findCause(values: any[]): string | null {
    for (let i = 0; i < this.inputs.length; i++) {
      const input = this.inputs[i]
      if (input.derived) {
        if (!this.equalityCheck(values[i], this.inputValues[i])) {
          return `selector:${input.name}`
        }
      } else {
        const root = this.roots[i]
        const changed = root ? findChange(root, values[i], this.equalityCheck) : null
        if (changed) {
          return changed.path
        }
      }
    }
    return null
  }

  evaluate(state: any, props: any): any {
    if (this.evaluating) {
      throw this.circularError()
    }
    this.evaluating = true
    try {
      const values = this.inputs.map((input) => input.fn(state, props))
      let cause: string | null = null
      if (this.hasValue) {
        cause = this.findCause(values)
        if (cause === null) {
          this.lastState = state
          this.dirty = false
          return this.value
        }
      }
      return this.compute(values, cause, state)
    } finally {
      this.evaluating = false
    }
  }

  compute(values: any[], cause: string | null, state: any): any {
    const session = new TrackingSession()
    const args = values.map((value, i) =>
      this.inputs[i].derived ? session.skip(value) : session.root(this.inputs[i].name, value),
    )
    let result: any
    try {
      result = this.func(...args)
    } finally {
      session.open = false
    }
    result = session.unwrap(result)

    this.evaluations += 1
    if (cause !== null) {
      this.dirtyCause = cause
    }
    this.roots = session.roots
    this.inputValues = values

    if (!(this.hasValue && this.resultEqualityCheck && this.resultEqualityCheck(this.value, result))) {
      this.value = result
      this.changedState = state
    }
    this.hasValue = true
    this.lastState = state
    this.dirty = false
    return this.value
  }

  /** After a dispatch: flag this selector as dirty if anything it read changed, without recomputing it. */
  mark(state: any, pass: number): boolean {
    if (this.markPass === pass) {
      return this.markResult
    }
    this.markPass = pass
    this.markResult = false
    if (!this.hasValue || this.evaluating) {
      return false
    }
    if (this.lastState === state) {
      return (this.markResult = this.changedState === state)
    }
    if (this.dirty) {
      return (this.markResult = true)
    }

    let cause: string | null = null
    for (let i = 0; i < this.inputs.length && cause === null; i++) {
      const input = this.inputs[i]
      if (input.derived) {
        if (findMountedNode(input.derived)?.mark(state, pass)) {
          cause = `selector:${input.name}`
        }
      } else {
        const root = this.roots[i]
        if (!root) {
          continue
        }
        let value
        try {
          value = input.fn(state, this.logic.props)
        } catch (e) {
          continue
        }
        cause = findChange(root, value, this.equalityCheck)?.path ?? null
      }
    }
    if (cause !== null) {
      this.dirty = true
      this.dirtyCause = cause
      this.markResult = true
    }
    return this.markResult
  }
}

function resolveInput(logic: Logic, fn: Selector, index: number): InputSource {
  const identity = getAtomicContext().identities.get(fn)
  const localName =
    identity?.pathString === logic.pathString
      ? identity.name
      : Object.keys(logic.selectors).find((key) => logic.selectors[key] === fn)
  const name = localName ?? (identity ? `${identity.pathString}.${identity.name}` : `input:${index}`)
  const derived = identity?.kind === 'selector' ? identity : null
  return { fn, name, derived, localDerived: !!derived && derived.pathString === logic.pathString }
}

export function createAtomicSelector(
  logic: Logic,
  name: string,
  args: Selector[],
  func: (...args: any[]) => any,
  memoizeOptions: any,
): Selector {
  const node = new AtomicSelectorNode(
    logic,
    name,
    args.map((fn, index) => resolveInput(logic, fn, index)),
    func,
    memoizeOptions,
  )
  getLogicNodes(logic)[name] = node
  return (state, props) => node.evaluate(state, props)
}

/** Walk the logic's selector graph depth-first, throwing on cycles. Returns the names in evaluation order. */
function sortSelectors(logic: Logic): string[] {
  const nodes = getLogicNodes(logic)
  const status: Record<string, 'visiting' | 'done'> = {}
  const stack: string[] = []
  const order: string[] = []

  const visit = (name: string) => {
    if (status[name] === 'done') {
      return
    }
    if (status[name] === 'visiting') {
      const cycle = [...stack.slice(stack.indexOf(name)), name].join(' -> ')
      throw new Error(`[KEA] Circular dependency detected in logic "${logic.pathString}": ${cycle}`)
    }
    status[name] = 'visiting'
    stack.push(name)
    for (const dependency of nodes[name].localDependencies) {
      if (nodes[dependency]) {
        visit(dependency)
      }
    }
    stack.pop()
    status[name] = 'done'
    order.push(name)
  }

  Object.keys(nodes).forEach(visit)
  return order
}

export function assertNoCircularSelectors(logic: Logic): void {
  sortSelectors(logic)
}

export function getSelectorHealth(logic: Logic): SelectorHealth {
  const nodes = getLogicNodes(logic)
  const selectors: SelectorHealth['selectors'] = {}
  for (const [name, node] of Object.entries(nodes)) {
    selectors[name] = {
      dependencies: node.dependencies,
      dependents: [],
      evaluations: node.evaluations,
      dirtyCause: node.dirtyCause,
    }
  }
  for (const [name, node] of Object.entries(nodes)) {
    for (const dependency of node.localDependencies) {
      if (selectors[dependency] && !selectors[dependency].dependents.includes(name)) {
        selectors[dependency].dependents.push(name)
      }
    }
  }
  return { selectors, topologicalOrder: sortSelectors(logic) }
}

/** Flags selectors of mounted logic as dirty right after a dispatch, so `dirtyCause` is known before a read. */
export const atomicSelectorsMiddleware: Middleware = (store) => (next) => (action) => {
  const previousState = store.getState()
  const response = next(action)
  const state = store.getState()
  if (state !== previousState && isAtomicSelectorsEnabled()) {
    const atomicContext = getAtomicContext()
    const pass = ++atomicContext.pass
    for (const logic of Object.values(getContext().mount.mounted)) {
      const nodes = atomicContext.logicNodes.get(logic)
      if (nodes) {
        for (const node of Object.values(nodes)) {
          node.mark(state, pass)
        }
      }
    }
  }
  return response
}
