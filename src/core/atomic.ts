import { BuiltLogic, KeaPlugin, Logic, Selector, SelectorHealth } from '../types'
import { getContext, getPluginContext } from '../kea/context'
import { shallowCompare } from '../utils'

/**
  Atomic Signal Selector Engine, enabled via resetContext({ atomicSelectors: true })

  Derived selectors record the exact leaf paths they read from reducer values (via tracking proxies), and only
  re-evaluate when one of those leaves (or an upstream derived selector's output) changes.
*/

type StepType = 'prop' | 'in' | 'mapget' | 'maphas' | 'sethas' | 'keys' | 'size' | 'self'
type Step = { t: StepType; k?: any }

interface DepRecord {
  key: string
  label: string
  steps: Step[]
  value: any
  isRoot: boolean
  hasChildren: boolean
  forced: boolean
  /** implicit structural reads (array length, object keys, collection size) */
  structural: boolean
  parent: DepRecord | null
}

type InputKind = 'tracked' | 'selector' | 'opaque'

interface InputMeta {
  kind: InputKind
  label: string
  fn: Selector
}

interface AtomicNode {
  id: string
  name: string
  logic: Logic
  inputs: InputMeta[]
  func: (...args: any[]) => any
  equalityCheck: (a: any, b: any) => boolean
  resultEqualityCheck?: (a: any, b: any) => boolean
  hasValue: boolean
  value: any
  lastState: any
  lastProps: any
  inputValues: any[]
  deps: (DepRecord[] | null)[]
  exposed: (string[] | null)[]
  evaluations: number
  dirtyCause: string | null
}

interface LogicEntry {
  nodes: Map<string, AtomicNode>
  order: string[]
}

interface AtomicPluginContext {
  entries: WeakMap<Logic, LogicEntry>
}

const PLUGIN_NAME = 'atomicSelectors'

const trackedSelectors = new WeakSet<Function>()
const selectorLabels = new WeakMap<Function, string>()
const proxyMeta = new WeakMap<object, { raw: any; record: DepRecord; tracker: Tracker }>()

export function isAtomicSelectorsEnabled(): boolean {
  return !!getContext()?.options?.atomicSelectors
}

/** Mark a selector as returning a raw reducer value, whose reads are tracked at the leaf level */
export function markTrackedSelector(selector: Selector): void {
  trackedSelectors.add(selector)
}

export function labelSelector<T extends Function>(selector: T, label: string): T {
  selectorLabels.set(selector, label)
  return selector
}

function getAtomicContext(): AtomicPluginContext {
  const context = getPluginContext<Partial<AtomicPluginContext>>(PLUGIN_NAME)
  if (!context.entries) {
    context.entries = new WeakMap()
  }
  return context as AtomicPluginContext
}

function getEntry(logic: Logic, create = false): LogicEntry | undefined {
  const { entries } = getAtomicContext()
  let entry = entries.get(logic)
  if (!entry && create) {
    entry = { nodes: new Map(), order: [] }
    entries.set(logic, entry)
  }
  return entry
}

// ------------------------------------------------------------------------------------------------
// Tracking proxies

function isWrappable(value: any): boolean {
  if (value === null || typeof value !== 'object' || proxyMeta.has(value)) {
    return false
  }
  if (Array.isArray(value) || value instanceof Map || value instanceof Set) {
    return true
  }
  const proto = Object.getPrototypeOf(value)
  return proto === Object.prototype || proto === null
}

function unwrap(value: any): any {
  const meta = value !== null && (typeof value === 'object' || typeof value === 'function') && proxyMeta.get(value)
  return meta ? meta.raw : value
}

function readSteps(root: any, steps: Step[]): any {
  let v = root
  for (const step of steps) {
    switch (step.t) {
      case 'prop':
        v = v === null || typeof v === 'undefined' ? undefined : v[step.k]
        break
      case 'in':
        v = v !== null && typeof v === 'object' ? step.k in v : false
        break
      case 'mapget':
        v = v instanceof Map ? v.get(step.k) : undefined
        break
      case 'maphas':
        v = v instanceof Map ? v.has(step.k) : false
        break
      case 'sethas':
        v = v instanceof Set ? v.has(step.k) : false
        break
      case 'keys':
        v = v !== null && typeof v === 'object' ? Reflect.ownKeys(v) : undefined
        break
      case 'size':
        v = v instanceof Map || v instanceof Set ? v.size : undefined
        break
      case 'self':
        break
    }
  }
  return v
}

function depChanged(dep: DepRecord, root: any): boolean {
  const newValue = readSteps(root, dep.steps)
  const lastStep = dep.steps[dep.steps.length - 1]
  if (lastStep?.t === 'keys') {
    const a = dep.value as any[] | undefined
    const b = newValue as any[] | undefined
    if (!a || !b) {
      return a !== b
    }
    return a.length !== b.length || a.some((k, i) => k !== b[i])
  }
  return !Object.is(dep.value, newValue)
}

class Tracker {
  active = true
  records = new Map<string, DepRecord>()
  proxies = new Map<string, any>()
  root: DepRecord
  private keyIds = new Map<any, number>()

  constructor(label: string, raw: any) {
    this.root = {
      key: '',
      label,
      steps: [],
      value: raw,
      isRoot: true,
      hasChildren: false,
      forced: false,
      structural: false,
      parent: null,
    }
    this.records.set('', this.root)
  }

  private keyId(k: any): string {
    if (typeof k === 'string') {
      return 's' + k
    }
    if (typeof k === 'number' || typeof k === 'boolean' || typeof k === 'undefined' || typeof k === 'bigint') {
      return typeof k + String(k)
    }
    if (k === null) {
      return 'null'
    }
    let id = this.keyIds.get(k)
    if (typeof id === 'undefined') {
      id = this.keyIds.size
      this.keyIds.set(k, id)
    }
    return 'o' + id
  }

  record(parent: DepRecord, step: Step, label: string, value: any, structural = false): DepRecord {
    const key = `${parent.key}\u0001${step.t}:${'k' in step ? this.keyId(step.k) : ''}`
    let record = this.records.get(key)
    if (!record) {
      record = {
        key,
        label,
        steps: [...parent.steps, step],
        value,
        isRoot: false,
        hasChildren: false,
        forced: false,
        structural,
        parent,
      }
      this.records.set(key, record)
    }
    parent.hasChildren = true
    return record
  }

  access(parent: DepRecord, step: Step, label: string, value: any, structural = false): any {
    if (!this.active) {
      return value
    }
    const record = this.record(parent, step, label, value, structural)
    return isWrappable(value) ? this.proxyFor(record) : value
  }

  proxyFor(record: DepRecord): any {
    let proxy = this.proxies.get(record.key)
    if (!proxy) {
      proxy = createTrackingProxy(record.value, record, this)
      this.proxies.set(record.key, proxy)
    }
    return proxy
  }

  /** The dependencies to validate against: leaves, forced reads, structural reads */
  effective(): DepRecord[] {
    const result: DepRecord[] = []
    for (const record of this.records.values()) {
      if (record.forced || (!record.isRoot && !record.hasChildren)) {
        result.push(record)
      }
    }
    return result
  }

  /** The human readable dependency paths */
  exposed(effective: DepRecord[]): string[] {
    const labels: string[] = []
    const add = (label: string) => !labels.includes(label) && labels.push(label)
    const hasNonStructuralSibling = (record: DepRecord) =>
      effective.some((r) => r !== record && r.parent === record.parent && !r.structural)

    for (const record of effective) {
      const lastStep = record.steps[record.steps.length - 1]
      if (record.structural) {
        // only surface implicit reads (e.g. `list.length`) if nothing more specific was read next to them
        if (!hasNonStructuralSibling(record)) {
          add(record.label)
        }
      } else if (lastStep?.t === 'in') {
        // `in` checks that are followed by a read of the same key are covered by that read
        const readKey = record.key.replace(/\u0001in:([^\u0001]*)$/, '\u0001prop:$1')
        if (!this.records.has(readKey)) {
          add(record.label)
        }
      } else {
        add(record.label)
      }
    }
    return labels
  }
}

function createTrackingProxy(raw: any, record: DepRecord, tracker: Tracker): any {
  const label = record.label
  let proxy: any

  if (raw instanceof Map || raw instanceof Set) {
    const isMap = raw instanceof Map
    proxy = new Proxy(isMap ? new Map() : new Set(), {
      get(_, prop) {
        if (prop === 'get' && isMap) {
          return (k: any) => {
            const key = unwrap(k)
            return tracker.access(record, { t: 'mapget', k: key }, `${label}.map:${String(key)}`, raw.get(key))
          }
        }
        if (prop === 'has') {
          return (k: any) => {
            const key = unwrap(k)
            const has = raw.has(key)
            tracker.access(
              record,
              { t: isMap ? 'maphas' : 'sethas', k: key },
              `${label}.${isMap ? 'map' : 'set'}:${String(key)}`,
              has,
            )
            return has
          }
        }
        if (prop === 'size') {
          tracker.access(record, { t: 'size' }, `${label}.size`, raw.size, true)
          return raw.size
        }
        const value = Reflect.get(raw, prop, raw)
        if (typeof value === 'function') {
          // iterating over a collection depends on all of its contents
          tracker.access(record, { t: 'self' }, label, raw)
          return value.bind(raw)
        }
        return value
      },
      getPrototypeOf: () => Object.getPrototypeOf(raw),
    })
  } else {
    const isArray = Array.isArray(raw)
    const searchMethods: Record<string, (...args: any[]) => any> = {}
    if (isArray) {
      const search = (fromEnd: boolean, sameValueZero: boolean) =>
        function (searchElement: any, fromIndex?: number) {
          const target = unwrap(searchElement)
          const length = raw.length
          tracker.access(record, { t: 'prop', k: 'length' }, `${label}.length`, length, true)
          let start = typeof fromIndex === 'number' ? fromIndex : fromEnd ? length - 1 : 0
          if (start < 0) {
            start = Math.max(fromEnd ? -1 : 0, length + start)
          }
          for (let i = start; fromEnd ? i >= 0 : i < length; fromEnd ? i-- : i++) {
            const element = raw[i]
            tracker.access(record, { t: 'prop', k: String(i) }, `${label}.${i}`, element)
            if (element === target || (sameValueZero && element !== element && target !== target)) {
              return sameValueZero ? true : i
            }
          }
          return sameValueZero ? false : -1
        }
      searchMethods.includes = search(false, true)
      searchMethods.indexOf = search(false, false)
      searchMethods.lastIndexOf = search(true, false)
    }

    proxy = new Proxy(isArray ? ([] as any) : {}, {
      get(_, prop) {
        if (typeof prop === 'symbol') {
          return Reflect.get(raw, prop)
        }
        if (isArray && searchMethods[prop] && (raw as any)[prop] === (Array.prototype as any)[prop]) {
          return searchMethods[prop]
        }
        if (!Object.prototype.hasOwnProperty.call(raw, prop) && prop in raw) {
          return raw[prop]
        }
        const structural = isArray && prop === 'length'
        return tracker.access(record, { t: 'prop', k: prop }, `${label}.${prop}`, raw[prop], structural)
      },
      has(_, prop) {
        const has = prop in raw
        if (typeof prop !== 'symbol') {
          tracker.access(record, { t: 'in', k: prop }, `${label}.${prop}`, has)
        }
        return has
      },
      ownKeys() {
        const keys = Reflect.ownKeys(raw)
        tracker.access(record, { t: 'keys' }, label, keys, true)
        return keys
      },
      getOwnPropertyDescriptor(target, prop) {
        const descriptor = Reflect.getOwnPropertyDescriptor(raw, prop)
        if (!descriptor || (isArray && prop === 'length')) {
          return descriptor ?? Reflect.getOwnPropertyDescriptor(target, prop)
        }
        return { ...descriptor, configurable: true }
      },
      getPrototypeOf: () => Object.getPrototypeOf(raw),
      set: (_, prop, value) => Reflect.set(raw, prop, unwrap(value)),
      defineProperty: (_, prop, descriptor) => Reflect.defineProperty(raw, prop, descriptor),
      deleteProperty: (_, prop) => Reflect.deleteProperty(raw, prop),
    })
  }

  proxyMeta.set(proxy, { raw, record, tracker })
  return proxy
}

/** Replace any tracking proxies that escaped into the result with their raw values */
function unwrapResult(value: any, seen: WeakSet<object>): any {
  if (value === null || typeof value !== 'object') {
    return value
  }
  const meta = proxyMeta.get(value)
  if (meta) {
    // the whole value escapes, so the result depends on all of it
    meta.record.forced = true
    return meta.raw
  }
  if (seen.has(value) || Object.isFrozen(value)) {
    return value
  }
  seen.add(value)
  try {
    if (Array.isArray(value)) {
      for (let i = 0; i < value.length; i++) {
        const newValue = unwrapResult(value[i], seen)
        if (newValue !== value[i]) {
          value[i] = newValue
        }
      }
    } else if (value instanceof Map) {
      for (const [k, v] of Array.from(value.entries())) {
        const newKey = unwrapResult(k, seen)
        const newValue = unwrapResult(v, seen)
        if (newKey !== k) {
          value.delete(k)
        }
        if (newKey !== k || newValue !== v) {
          value.set(newKey, newValue)
        }
      }
    } else if (value instanceof Set) {
      for (const v of Array.from(value.values())) {
        const newValue = unwrapResult(v, seen)
        if (newValue !== v) {
          value.delete(v)
          value.add(newValue)
        }
      }
    } else {
      const proto = Object.getPrototypeOf(value)
      if (proto === Object.prototype || proto === null) {
        for (const k of Object.keys(value)) {
          const newValue = unwrapResult(value[k], seen)
          if (newValue !== value[k]) {
            value[k] = newValue
          }
        }
      }
    }
  } catch (e) {
    // non-writable output, leave as is
  }
  return value
}

// ------------------------------------------------------------------------------------------------
// Selector nodes

function parseMemoizeOptions(memoizeOptions: any): Pick<AtomicNode, 'equalityCheck' | 'resultEqualityCheck'> {
  if (typeof memoizeOptions === 'function') {
    return { equalityCheck: memoizeOptions }
  }
  return {
    equalityCheck: memoizeOptions?.equalityCheck ?? Object.is,
    resultEqualityCheck: memoizeOptions?.resultEqualityCheck,
  }
}

function describeInput(logic: Logic, fn: Selector, index: number): InputMeta {
  let localName: string | undefined
  for (const [key, selector] of Object.entries(logic.selectors)) {
    if (selector === fn) {
      localName = key
      break
    }
  }
  if (trackedSelectors.has(fn)) {
    return { kind: 'tracked', label: localName ?? selectorLabels.get(fn) ?? `input:${index}`, fn }
  }
  if (localName) {
    return { kind: 'selector', label: localName, fn }
  }
  return { kind: 'opaque', label: selectorLabels.get(fn) ?? `input:${index}`, fn }
}

export function createAtomicSelector(
  logic: BuiltLogic,
  name: string,
  inputFns: Selector[],
  func: (...args: any[]) => any,
  memoizeOptions?: any,
): Selector {
  const entry = getEntry(logic, true)!
  const node: AtomicNode = {
    id: `${logic.pathString}.${name}`,
    name,
    logic,
    inputs: inputFns.map((fn, index) => describeInput(logic, fn, index)),
    func,
    ...parseMemoizeOptions(memoizeOptions),
    hasValue: false,
    value: undefined,
    lastState: undefined,
    lastProps: undefined,
    inputValues: [],
    deps: [],
    exposed: [],
    evaluations: 0,
    dirtyCause: null,
  }
  entry.nodes.set(name, node)
  return (state: any, props: any) => getNodeValue(node, state, props)
}

function sameProps(a: any, b: any): boolean {
  return a === b || (!!a && !!b && typeof a === 'object' && typeof b === 'object' && shallowCompare(a, b))
}

function getNodeValue(node: AtomicNode, state: any, props: any): any {
  if (node.hasValue && state === node.lastState && sameProps(props, node.lastProps)) {
    return node.value
  }

  const inputValues = node.inputs.map((input) => input.fn(state, props))

  let cause: string | null = null
  if (node.hasValue) {
    for (let i = 0; i < node.inputs.length && !cause; i++) {
      const input = node.inputs[i]
      const newValue = inputValues[i]
      const oldValue = node.inputValues[i]
      if (node.equalityCheck(newValue, oldValue)) {
        continue
      }
      if (input.kind === 'tracked') {
        const changed = (node.deps[i] ?? []).find((dep) => depChanged(dep, newValue))
        if (changed) {
          cause = changed.label
        }
      } else {
        cause = input.kind === 'selector' ? `selector:${input.label}` : input.label
      }
    }

    if (!cause) {
      node.inputValues = inputValues
      node.lastState = state
      node.lastProps = props ? { ...props } : props
      return node.value
    }
  }

  const trackers: (Tracker | null)[] = node.inputs.map((input, i) =>
    input.kind === 'tracked' ? new Tracker(input.label, inputValues[i]) : null,
  )
  const args = inputValues.map((value, i) => {
    const tracker = trackers[i]
    if (!tracker) {
      return value
    }
    if (isWrappable(value)) {
      return tracker.proxyFor(tracker.root)
    }
    tracker.root.forced = true
    return value
  })

  node.evaluations += 1
  node.dirtyCause = cause
  let result: any
  try {
    result = unwrapResult(node.func(...args), new WeakSet())
  } finally {
    for (const tracker of trackers) {
      tracker && (tracker.active = false)
    }
  }

  node.deps = trackers.map((tracker) => (tracker ? tracker.effective() : null))
  node.exposed = trackers.map((tracker, i) => (tracker ? tracker.exposed(node.deps[i]!) : null))

  if (!(node.hasValue && node.resultEqualityCheck && node.resultEqualityCheck(node.value, result))) {
    node.value = result
  }
  node.hasValue = true
  node.inputValues = inputValues
  node.lastState = state
  node.lastProps = props ? { ...props } : props
  return node.value
}

// ------------------------------------------------------------------------------------------------
// Graph

function localEdges(entry: LogicEntry, node: AtomicNode): string[] {
  return node.inputs
    .filter((input) => input.kind === 'selector' && entry.nodes.has(input.label))
    .map((input) => input.label)
}

/** Sort the logic's selectors topologically, throwing if there is a loop */
export function sortAtomicSelectors(logic: Logic): void {
  const entry = getEntry(logic)
  if (!entry) {
    return
  }
  const order: string[] = []
  const state: Record<string, 'visiting' | 'done'> = {}

  const visit = (name: string, trail: string[]) => {
    if (state[name] === 'done') {
      return
    }
    if (state[name] === 'visiting') {
      const loop = [...trail.slice(trail.indexOf(name)), name].join(' -> ')
      throw new Error(`[KEA] Circular dependency detected in logic "${logic.pathString}": ${loop}`)
    }
    state[name] = 'visiting'
    for (const dependency of localEdges(entry, entry.nodes.get(name)!)) {
      visit(dependency, [...trail, name])
    }
    state[name] = 'done'
    order.push(name)
  }

  for (const name of entry.nodes.keys()) {
    visit(name, [])
  }
  entry.order = order
}

export function getSelectorHealth(logic: Logic): SelectorHealth {
  const entry = getEntry(logic)
  const health: SelectorHealth = { selectors: {}, topologicalOrder: entry ? [...entry.order] : [] }
  if (!entry) {
    return health
  }

  for (const node of entry.nodes.values()) {
    const dependencies: string[] = []
    node.inputs.forEach((input, i) => {
      const labels = node.hasValue && input.kind === 'tracked' ? node.exposed[i] ?? [] : [input.label]
      for (const label of labels) {
        !dependencies.includes(label) && dependencies.push(label)
      }
    })
    health.selectors[node.name] = {
      dependencies,
      dependents: [],
      evaluations: node.evaluations,
      dirtyCause: node.dirtyCause,
    }
  }
  for (const node of entry.nodes.values()) {
    for (const dependency of localEdges(entry, node)) {
      const dependents = health.selectors[dependency].dependents
      !dependents.includes(node.name) && dependents.push(node.name)
    }
  }
  return health
}

/** After each state change, bring the selectors that were up to date with the store up to date again */
function refreshMountedSelectors(previousState: any, state: any): void {
  const { entries } = getAtomicContext()
  for (const logic of Object.values(getContext().mount.mounted)) {
    const entry = entries.get(logic)
    if (!entry) {
      continue
    }
    for (const name of entry.order) {
      const node = entry.nodes.get(name)
      if (node && node.hasValue && node.lastState === previousState) {
        try {
          getNodeValue(node, state, logic.props)
        } catch (e) {
          // errors surface when the selector is read
        }
      }
    }
  }
}

export const atomicSelectorsPlugin: KeaPlugin = {
  name: PLUGIN_NAME,
  defaults: () => ({ selectorHealth: undefined }),
  events: {
    afterPlugin(): void {
      getAtomicContext()
    },
    beforeReduxStore(options): void {
      options.middleware.push((store) => (next) => (action) => {
        const previousState = store.getState()
        const response = next(action)
        const state = store.getState()
        if (state !== previousState) {
          refreshMountedSelectors(previousState, state)
        }
        return response
      })
    },
    beforeBuild(logic): void {
      logic.selectorHealth = () => getSelectorHealth(logic)
    },
  },
}
