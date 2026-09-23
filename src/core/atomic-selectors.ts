import { BuiltLogic, KeaPlugin, Logic, Selector, SelectorHealth } from '../types'
import { getContext, getStoreState } from '../kea/context'

const CIRCULAR = '[KEA] Circular dependency detected'
const TRACKED = Symbol('kea.atomic.tracked')

interface SelectorMeta {
  name: string
  id: string
  isReducer: boolean
  buildDeps: string[]
  dependencies: string[]
  dependents: string[]
  evaluations: number
  dirtyCause: string | null
  dirty: boolean
  cache: { value: any } | null
  snaps: Map<string, any>
  reducerRaw: { seen: boolean; value: any }
  resultEqualityCheck?: (a: any, b: any) => boolean
  run?: (state: any, props: any) => any
}

interface EngineState {
  byId: Map<string, SelectorMeta>
  byLogic: Map<string, Map<string, SelectorMeta>>
  subscribed?: boolean
}

interface Tracker {
  deps: string[]
  prefixes: string[]
}

let recorder: { deps: string[] } | null = null
const trackerStack: Tracker[] = []
const evalStack: string[] = []
const trackedRaws = new WeakMap<object, any>()

function engineState(): EngineState {
  const contexts = getContext().plugins.contexts
  if (!contexts.atomicSelectors) {
    contexts.atomicSelectors = {
      byId: new Map(),
      byLogic: new Map(),
    }
  }
  return contexts.atomicSelectors as EngineState
}

export function atomicEnabled(): boolean {
  return !!getContext()?.options?.atomicSelectors
}

export function selectorId(logic: Logic, name: string): string {
  return `${logic.pathString}::${name}`
}

function metasFor(logic: Logic): Map<string, SelectorMeta> {
  const state = engineState()
  let map = state.byLogic.get(logic.pathString)
  if (!map) {
    map = new Map()
    state.byLogic.set(logic.pathString, map)
  }
  return map
}

function selectorTarget(logic: Logic): Record<string, Selector> {
  return (logic.cache.__keaAtomicSelectorTarget as Record<string, Selector>) || logic.selectors
}

function selectorNames(logic: Logic): string[] {
  return Object.keys(selectorTarget(logic))
}

export function ensureMeta(logic: Logic, name: string, isReducer: boolean): SelectorMeta {
  const id = selectorId(logic, name)
  const state = engineState()
  let meta = state.byId.get(id)
  if (!meta) {
    meta = {
      name,
      id,
      isReducer,
      buildDeps: [],
      dependencies: [],
      dependents: [],
      evaluations: 0,
      dirtyCause: null,
      dirty: false,
      cache: null,
      snaps: new Map(),
      reducerRaw: { seen: false, value: undefined },
    }
    state.byId.set(id, meta)
    metasFor(logic).set(name, meta)
  } else if (isReducer) {
    meta.isReducer = true
  }
  return meta
}

export function notePropRead(prop: string): void {
  const tracker = trackerStack[trackerStack.length - 1]
  if (tracker) {
    tracker.deps.push(`props.${prop}`)
  }
}

export function installSelectorsProxy(logic: Logic): void {
  const existing = logic.selectors as any
  if (existing && existing.__keaAtomicProxy) {
    return
  }
  const target = existing ?? {}
  logic.cache.__keaAtomicSelectorTarget = target
  const proxy = new Proxy(target, {
    get(obj, prop, receiver) {
      if (typeof prop === 'string' && recorder && Object.prototype.hasOwnProperty.call(obj, prop)) {
        recorder.deps.push(prop)
      }
      return Reflect.get(obj, prop, receiver)
    },
  })
  ;(proxy as any).__keaAtomicProxy = true
  logic.selectors = proxy
}

export function recordBuildDependencies(logic: Logic, name: string, collect: () => void): void {
  const meta = ensureMeta(logic, name, false)
  const previous = recorder
  const deps: string[] = []
  recorder = { deps }
  try {
    collect()
  } finally {
    recorder = previous
  }
  const names = selectorNames(logic)
  meta.buildDeps = unique(deps.filter((dep) => dep !== name && names.includes(dep)))
  assertAcyclic(logic)
}

function assertAcyclic(logic: Logic): void {
  const metas = metasFor(logic)
  const color = new Map<string, number>()
  const visit = (name: string) => {
    const state = color.get(name) ?? 0
    if (state === 1) {
      throw new Error(CIRCULAR)
    }
    if (state === 2) {
      return
    }
    color.set(name, 1)
    for (const dep of metas.get(name)?.buildDeps ?? []) {
      if (metas.has(dep) || selectorNames(logic).includes(dep)) {
        visit(dep)
      }
    }
    color.set(name, 2)
  }
  for (const name of selectorNames(logic)) {
    visit(name)
  }
}

function unique(values: string[]): string[] {
  const out: string[] = []
  for (const value of values) {
    if (!out.includes(value)) {
      out.push(value)
    }
  }
  return out
}

function isTracked(value: any): boolean {
  return !!value && typeof value === 'object' && trackedRaws.has(value)
}

function markTracked(tracked: object, raw: any): void {
  trackedRaws.set(tracked, raw)
}

export function trackValue(value: any, path: string, tracker: Tracker): any {
  if (value === null || typeof value !== 'object') {
    tracker.deps.push(path)
    return value
  }
  if (isTracked(value)) {
    tracker.prefixes.push(path)
    return value
  }
  tracker.prefixes.push(path)
  if (value instanceof Map) {
    return trackMap(value, path, tracker)
  }
  if (value instanceof Set) {
    return trackSet(value, path, tracker)
  }
  return trackObject(value, path, tracker)
}

function addDep(tracker: Tracker, dep: string): void {
  tracker.deps.push(dep)
}

function trackObject(value: any, path: string, tracker: Tracker): any {
  const proxy = new Proxy(value, {
    get(target, prop, receiver) {
      if (prop === TRACKED) {
        return true
      }
      if (typeof prop === 'symbol') {
        return Reflect.get(target, prop, receiver)
      }
      if (Array.isArray(target) && (prop === 'includes' || prop === 'indexOf')) {
        return (search: any, fromIndex = 0) => {
          const start = fromIndex < 0 ? Math.max(target.length + fromIndex, 0) : fromIndex
          for (let i = start; i < target.length; i++) {
            addDep(tracker, `${path}.${i}`)
            if (target[i] === search) {
              return prop === 'includes' ? true : i
            }
          }
          return prop === 'includes' ? false : -1
        }
      }
      if (
        Array.isArray(target) &&
        (prop === 'forEach' ||
          prop === 'map' ||
          prop === 'filter' ||
          prop === 'some' ||
          prop === 'every' ||
          prop === 'find' ||
          prop === 'findIndex')
      ) {
        const method = (target as any)[prop]
        return (...args: any[]) => {
          for (let i = 0; i < target.length; i++) {
            addDep(tracker, `${path}.${i}`)
          }
          return method.apply(target, args)
        }
      }
      const next = Reflect.get(target, prop, receiver)
      if (typeof next === 'function') {
        return next.bind(target)
      }
      const child = `${path}.${String(prop)}`
      return trackValue(next, child, tracker)
    },
    has(target, prop) {
      if (typeof prop === 'string') {
        addDep(tracker, `${path}.${prop}`)
      }
      return Reflect.has(target, prop)
    },
    ownKeys(target) {
      const keys = Reflect.ownKeys(target)
      for (const key of keys) {
        if (typeof key === 'string') {
          addDep(tracker, `${path}.${key}`)
        }
      }
      return keys
    },
  })
  markTracked(proxy, value)
  return proxy
}

function keyString(key: any): string {
  return String(key)
}

function trackMap(map: Map<any, any>, path: string, tracker: Tracker): any {
  const proxy = new Proxy(map, {
    get(target, prop, receiver) {
      if (prop === 'get') {
        return (key: any) => trackValue(target.get(key), `${path}.map:${keyString(key)}`, tracker)
      }
      if (prop === 'has') {
        return (key: any) => {
          addDep(tracker, `${path}.map:${keyString(key)}`)
          return target.has(key)
        }
      }
      if (prop === 'size') {
        addDep(tracker, `${path}.size`)
        return target.size
      }
      const next = Reflect.get(target, prop, receiver)
      if (typeof next === 'function') {
        return next.bind(target)
      }
      return next
    },
  })
  markTracked(proxy, map)
  return proxy
}

function trackSet(set: Set<any>, path: string, tracker: Tracker): any {
  const proxy = new Proxy(set, {
    get(target, prop, receiver) {
      if (prop === 'has') {
        return (value: any) => {
          addDep(tracker, `${path}.set:${keyString(value)}`)
          return target.has(value)
        }
      }
      if (prop === 'size') {
        addDep(tracker, `${path}.size`)
        return target.size
      }
      const next = Reflect.get(target, prop, receiver)
      if (typeof next === 'function') {
        return next.bind(target)
      }
      return next
    },
  })
  markTracked(proxy, set)
  return proxy
}

function unwrap(value: any): any {
  if (isTracked(value)) {
    return trackedRaws.get(value)
  }
  return value
}

function finalizeDeps(tracker: Tracker): string[] {
  const deps = unique(tracker.deps)
  for (const prefix of unique(tracker.prefixes)) {
    const used = deps.some((dep) => dep === prefix || dep.startsWith(`${prefix}.`))
    if (!used) {
      deps.push(prefix)
    }
  }
  return deps
}

export function readLeaf(logic: Logic, dep: string): any {
  if (dep.startsWith('props.')) {
    return logic.props?.[dep.slice('props.'.length)]
  }
  const root = logic.selector ? logic.selector(getStoreState()) : undefined
  let cur: any = root
  for (const part of dep.split('.')) {
    if (cur == null) {
      return undefined
    }
    if (part.startsWith('map:')) {
      const key = part.slice(4)
      if (!(cur instanceof Map)) {
        return undefined
      }
      if (cur.has(key)) {
        return cur.get(key)
      }
      const asNum = Number(key)
      if (key !== '' && !Number.isNaN(asNum) && cur.has(asNum)) {
        return cur.get(asNum)
      }
      return undefined
    }
    if (part.startsWith('set:')) {
      const key = part.slice(4)
      if (!(cur instanceof Set)) {
        return undefined
      }
      if (cur.has(key) || (!Number.isNaN(Number(key)) && key !== '' && cur.has(Number(key)))) {
        return true
      }
      return false
    }
    cur = cur instanceof Map ? cur.get(part) : cur[part]
  }
  return cur
}

function isStateLeaf(logic: Logic, dep: string): boolean {
  if (dep.startsWith('props.') || dep.includes('.') || dep.includes('map:') || dep.includes('set:')) {
    return true
  }
  return !!metasFor(logic).get(dep)?.isReducer
}

function diffCause(logic: Logic, meta: SelectorMeta): string | null {
  let cause: string | null = null
  for (const dep of meta.dependencies) {
    if (isStateLeaf(logic, dep)) {
      let next: any
      try {
        next = readLeaf(logic, dep)
      } catch {
        continue
      }
      if (meta.snaps.has(dep) && !Object.is(meta.snaps.get(dep), next)) {
        cause = dep
      }
    } else {
      const other = metasFor(logic).get(dep)
      if (other?.cache && meta.snaps.has(dep) && !Object.is(meta.snaps.get(dep), other.cache.value)) {
        cause = `selector:${dep}`
      }
    }
  }
  return cause
}

function writeSnaps(logic: Logic, meta: SelectorMeta): void {
  for (const dep of meta.dependencies) {
    if (isStateLeaf(logic, dep)) {
      try {
        meta.snaps.set(dep, readLeaf(logic, dep))
      } catch {
        // Logic reducer is not attached yet.
      }
    } else {
      const other = metasFor(logic).get(dep)
      if (other?.cache) {
        meta.snaps.set(dep, other.cache.value)
      }
    }
  }
}

function linkDependents(logic: Logic, meta: SelectorMeta): void {
  const metas = metasFor(logic)
  for (const other of metas.values()) {
    other.dependents = other.dependents.filter((name) => name !== meta.name)
  }
  const owners = new Set<string>()
  for (const dep of [...meta.buildDeps, ...meta.dependencies]) {
    if (metas.has(dep) && !isStateLeaf(logic, dep)) {
      owners.add(dep)
    }
    const head = dep.startsWith('props.') ? '' : dep.split('.')[0]
    if (head && metas.has(head)) {
      owners.add(head)
    }
  }
  for (const owner of owners) {
    const target = metas.get(owner)
    if (target && owner !== meta.name && !target.dependents.includes(meta.name)) {
      target.dependents.push(meta.name)
    }
  }
}

function propagate(logic: Logic, meta: SelectorMeta, previous: any, next: any): void {
  if (meta.isReducer || Object.is(previous, next)) {
    return
  }
  for (const other of metasFor(logic).values()) {
    if (other.name === meta.name) {
      continue
    }
    if (other.dependencies.includes(meta.name) || other.buildDeps.includes(meta.name)) {
      other.dirty = true
      other.dirtyCause = `selector:${meta.name}`
    }
  }
}

export function tagSelector(selector: Selector, name: string): Selector {
  ;(selector as any).__keaAtomicName = name
  return selector
}

export function bindDerivedRunner(
  logic: Logic,
  name: string,
  inputs: Selector[],
  func: (...args: any[]) => any,
  resultEqualityCheck?: (a: any, b: any) => boolean,
): Selector {
  const meta = ensureMeta(logic, name, false)
  meta.resultEqualityCheck = resultEqualityCheck
  const run = (state?: any, props?: any) =>
    runDerived(logic, name, state ?? getStoreState(), props ?? logic.props, inputs, func)
  meta.run = run
  return tagSelector(run, name)
}

export function runDerived(
  logic: Logic,
  name: string,
  state: any,
  props: any,
  inputs: Selector[],
  func: (...args: any[]) => any,
): any {
  const meta = ensureMeta(logic, name, false)
  if (evalStack.includes(meta.id)) {
    throw new Error(CIRCULAR)
  }
  evalStack.push(meta.id)
  try {
    for (const dep of meta.buildDeps) {
      const other = metasFor(logic).get(dep)
      if (other && !other.isReducer && other.run && !evalStack.includes(other.id)) {
        other.run(state, props)
      }
    }

    const change = meta.cache ? diffCause(logic, meta) : null
    if (meta.cache && !meta.dirty && !change) {
      return meta.cache.value
    }
    if (change) {
      meta.dirtyCause = change
    }

    const tracker: Tracker = { deps: [], prefixes: [] }
    trackerStack.push(tracker)
    let result: any
    try {
      const args = inputs.map((input) => input(state, props))
      meta.evaluations += 1
      result = unwrap(func(...args))
    } finally {
      trackerStack.pop()
    }

    const dependencies = finalizeDeps(tracker)
    for (const input of inputs) {
      const tagged = (input as any).__keaAtomicName as string | undefined
      if (!tagged) {
        continue
      }
      const inputMeta = metasFor(logic).get(tagged)
      if (!inputMeta || inputMeta.isReducer) {
        continue
      }
      const covered = dependencies.some((dep) => dep === tagged || dep.startsWith(`${tagged}.`))
      if (!covered) {
        dependencies.push(tagged)
      }
    }
    meta.dependencies = unique(dependencies)
    linkDependents(logic, meta)

    if (meta.cache && meta.resultEqualityCheck && meta.resultEqualityCheck(meta.cache.value, result)) {
      result = meta.cache.value
    }
    const previous = meta.cache?.value
    meta.cache = { value: result }
    meta.dirty = false
    writeSnaps(logic, meta)
    propagate(logic, meta, previous, result)
    return result
  } finally {
    evalStack.pop()
  }
}

export function bindReducerRunner(logic: Logic, name: string): Selector {
  const meta = ensureMeta(logic, name, true)
  const run = (state?: any, props?: any) => {
    const resolvedState = state ?? getStoreState()
    const value = logic.selector!(resolvedState)[name]
    if (!meta.reducerRaw.seen || !Object.is(meta.reducerRaw.value, value)) {
      meta.evaluations += 1
      meta.reducerRaw.seen = true
      meta.reducerRaw.value = value
      meta.cache = { value }
    }
    const tracker = trackerStack[trackerStack.length - 1]
    if (!tracker) {
      return value
    }
    return trackValue(value, name, tracker)
  }
  meta.run = run
  return tagSelector(run, name)
}

export function syncAtomicSelectors(): void {
  const state = engineState()
  const mounted = getContext().mount.mounted
  for (const [pathString, metas] of state.byLogic) {
    const logic = mounted[pathString]
    if (!logic) {
      continue
    }
    for (const meta of metas.values()) {
      if (!meta.cache || meta.dependencies.length === 0 || meta.isReducer) {
        continue
      }
      const cause = diffCause(logic, meta)
      if (cause) {
        meta.dirty = true
        meta.dirtyCause = cause
      }
    }
  }
}

function topologicalOrder(logic: Logic): string[] {
  const metas = metasFor(logic)
  const names = selectorNames(logic)
  const indegree = new Map<string, number>()
  const outgoing = new Map<string, string[]>()
  for (const name of names) {
    indegree.set(name, 0)
    outgoing.set(name, [])
  }
  const link = (from: string, to: string) => {
    if (!indegree.has(from) || !indegree.has(to) || from === to) {
      return
    }
    const list = outgoing.get(from)!
    if (!list.includes(to)) {
      list.push(to)
      indegree.set(to, (indegree.get(to) ?? 0) + 1)
    }
  }
  for (const name of names) {
    const meta = metas.get(name)
    if (!meta) {
      continue
    }
    const sources = new Set<string>(meta.buildDeps)
    for (const dep of meta.dependencies) {
      if (metas.has(dep) && !isStateLeaf(logic, dep)) {
        sources.add(dep)
      }
      const head = dep.startsWith('props.') ? '' : dep.split('.')[0]
      if (head && metas.has(head)) {
        sources.add(head)
      }
    }
    for (const source of sources) {
      link(source, name)
    }
  }
  const queue = names.filter((name) => (indegree.get(name) ?? 0) === 0)
  const ordered: string[] = []
  while (queue.length) {
    const name = queue.shift()!
    ordered.push(name)
    for (const next of outgoing.get(name) ?? []) {
      const degree = (indegree.get(next) ?? 1) - 1
      indegree.set(next, degree)
      if (degree === 0) {
        queue.push(next)
      }
    }
  }
  for (const name of names) {
    if (!ordered.includes(name)) {
      ordered.push(name)
    }
  }
  return ordered
}

export function selectorHealth(logic: Logic): SelectorHealth {
  const metas = metasFor(logic)
  for (const name of selectorNames(logic)) {
    const meta = ensureMeta(logic, name, !!logic.reducers?.[name])
    if (logic.reducers?.[name]) {
      meta.isReducer = true
    }
  }
  for (const name of selectorNames(logic)) {
    linkDependents(logic, metas.get(name)!)
  }
  const selectors: SelectorHealth['selectors'] = {}
  for (const name of selectorNames(logic)) {
    const meta = metas.get(name)!
    selectors[name] = {
      dependencies: meta.dependencies.slice(),
      dependents: meta.dependents.slice(),
      evaluations: meta.evaluations,
      dirtyCause: meta.dirtyCause,
    }
  }
  return {
    selectors,
    topologicalOrder: topologicalOrder(logic),
  }
}

export const atomicSelectorsPlugin: KeaPlugin = {
  name: 'atomicSelectors',
  defaults: () => ({
    selectorHealth: undefined,
  }),
  events: {
    beforeBuild(logic: BuiltLogic): void {
      installSelectorsProxy(logic)
    },
    afterBuild(logic: BuiltLogic): void {
      logic.selectorHealth = () => selectorHealth(logic)
      for (const name of selectorNames(logic)) {
        ensureMeta(logic, name, !!logic.reducers?.[name])
      }
      assertAcyclic(logic)
    },
    afterReduxStore(_options, store): void {
      const state = engineState()
      if (state.subscribed) {
        return
      }
      state.subscribed = true
      store.subscribe(() => {
        syncAtomicSelectors()
      })
    },
  },
}
