import { BuiltLogic, Logic, SelectorHealth } from '../types'
import { getPluginContext } from '../kea/context'

type Segment = { kind: 'prop' | 'map' | 'set'; key: any }

interface Dependency {
  id: string
  arg: number
  segments: Segment[]
  value: any
  hidden: boolean
  whole: boolean
  selector?: string
}

interface SelectorMeta {
  name: string
  staticDeps: string[]
  dependencies: string[] | null
  evaluations: number
  dirtyCause: string | null
}

interface AtomicRegistry {
  selectors: Map<string, SelectorMeta>
  logics: Map<string, string[]>
}

const MISSING = Symbol('missing')
const RAW = Symbol('raw')

function getRegistry(): AtomicRegistry {
  const ctx = getPluginContext<Partial<AtomicRegistry>>('atomicSelectors')
  if (!ctx.selectors) {
    ctx.selectors = new Map()
    ctx.logics = new Map()
  }
  return ctx as AtomicRegistry
}

const metaId = (logic: Logic, name: string) => `${logic.pathString}:${name}`

function getLogicMetas(logic: Logic): SelectorMeta[] {
  const { selectors, logics } = getRegistry()
  return (logics.get(logic.pathString) ?? []).map((name) => selectors.get(metaId(logic, name)) as SelectorMeta)
}

function resolve(value: any, segments: Segment[]): any {
  let current = value
  for (const { kind, key } of segments) {
    if (kind === 'map') {
      current = current instanceof Map ? (current.has(key) ? current.get(key) : MISSING) : MISSING
    } else if (kind === 'set') {
      current = current instanceof Set ? current.has(key) : MISSING
    } else {
      current = current !== null && typeof current === 'object' ? current[key] : MISSING
    }
    if (current === MISSING) {
      return MISSING
    }
  }
  return current
}

function segmentsToId(root: string, segments: Segment[]): string {
  return segments.reduce(
    (id, { kind, key }) => `${id}.${kind === 'prop' ? String(key) : `${kind}:${String(key)}`}`,
    root,
  )
}

const unwrap = (value: any): any => (value !== null && typeof value === 'object' && value[RAW]) || value

function unwrapResult(value: any): any {
  const raw = unwrap(value)
  if (Array.isArray(raw) && raw.some((v) => unwrap(v) !== v)) {
    return raw.map(unwrap)
  }
  return raw
}

class Tracker {
  deps = new Map<string, Dependency>()
  active = true

  record(arg: number, root: string, segments: Segment[], value: any, opts: Partial<Dependency> = {}): void {
    if (!this.active) {
      return
    }
    const id = segmentsToId(root, segments)
    const existing = this.deps.get(id)
    if (existing) {
      existing.whole = existing.whole || !!opts.whole
      existing.hidden = existing.hidden && !!opts.hidden
      return
    }
    this.deps.set(id, { id, arg, segments, value, hidden: false, whole: false, ...opts })
  }

  wrap(value: any, arg: number, root: string, segments: Segment[]): any {
    if (value === null || typeof value !== 'object') {
      return value
    }
    // eslint-disable-next-line @typescript-eslint/no-this-alias
    const tracker = this
    const child = (seg: Segment, childValue: any, hidden = false) => {
      const childSegments = [...segments, seg]
      tracker.record(arg, root, childSegments, childValue, { hidden })
      return tracker.wrap(childValue, arg, root, childSegments)
    }
    const whole = () => tracker.record(arg, root, segments, value, { whole: true })

    if (value instanceof Map || value instanceof Set) {
      const isMap = value instanceof Map
      return new Proxy(Object.create(null), {
        get(_, prop) {
          if (prop === RAW) {
            return value
          }
          if (prop === 'size') {
            tracker.record(arg, root, [...segments, { kind: 'prop', key: 'size' }], value.size, { hidden: true })
            return value.size
          }
          if (prop === 'has') {
            return (k: any) => {
              const has = value.has(k)
              tracker.record(arg, root, [...segments, { kind: isMap ? 'map' : 'set', key: k }], isMap ? (has ? (value as Map<any, any>).get(k) : MISSING) : has)
              return has
            }
          }
          if (isMap && prop === 'get') {
            return (k: any) => {
              const map = value as Map<any, any>
              const v = map.has(k) ? map.get(k) : MISSING
              tracker.record(arg, root, [...segments, { kind: 'map', key: k }], v)
              return v === MISSING ? undefined : tracker.wrap(v, arg, root, [...segments, { kind: 'map', key: k }])
            }
          }
          whole()
          const result = (value as any)[prop]
          return typeof result === 'function' ? result.bind(value) : result
        },
      })
    }

    const isArray = Array.isArray(value)
    const proxy: any = new Proxy(isArray ? [] : {}, {
      get(_, prop) {
        if (prop === RAW) {
          return value
        }
        if (typeof prop === 'symbol') {
          if (prop === Symbol.iterator) {
            whole()
          }
          const result = Reflect.get(value, prop)
          return typeof result === 'function' ? result.bind(value) : result
        }
        if (isArray && prop === 'length') {
          tracker.record(arg, root, [...segments, { kind: 'prop', key: 'length' }], value.length, { hidden: true })
          return value.length
        }
        if (isArray && (prop === 'includes' || prop === 'indexOf' || prop === 'lastIndexOf')) {
          return (search: any, ...rest: any[]) => {
            tracker.record(arg, root, [...segments, { kind: 'prop', key: 'length' }], value.length, { hidden: true })
            const target = unwrap(search)
            const indices = [...Array(value.length).keys()]
            if (prop === 'lastIndexOf') {
              indices.reverse()
            }
            const from = typeof rest[0] === 'number' ? rest[0] : undefined
            for (const i of indices) {
              if (from !== undefined && (prop === 'lastIndexOf' ? i > from : i < from)) {
                continue
              }
              tracker.record(arg, root, [...segments, { kind: 'prop', key: String(i) }], value[i])
              const v = value[i]
              if (prop === 'includes' ? v === target || (v !== v && target !== target) : v === target) {
                return prop === 'includes' ? true : i
              }
            }
            return prop === 'includes' ? false : -1
          }
        }
        if (Object.prototype.hasOwnProperty.call(value, prop)) {
          return child({ kind: 'prop', key: prop }, value[prop])
        }
        const result = Reflect.get(value, prop)
        if (typeof result === 'function') {
          return (...fnArgs: any[]) => result.apply(isArray ? proxy : value, fnArgs)
        }
        return result
      },
      has(_, prop) {
        if (typeof prop !== 'symbol') {
          tracker.record(arg, root, [...segments, { kind: 'prop', key: prop }], prop in value ? value[prop] : MISSING)
        }
        return prop in value
      },
      ownKeys() {
        whole()
        const keys = Reflect.ownKeys(value)
        return isArray && !keys.includes('length') ? [...keys, 'length'] : keys
      },
      getOwnPropertyDescriptor(target, prop) {
        if (isArray && prop === 'length') {
          return Reflect.getOwnPropertyDescriptor(target, prop)
        }
        const descriptor = Reflect.getOwnPropertyDescriptor(value, prop)
        return descriptor ? { ...descriptor, configurable: true } : undefined
      },
    })
    return proxy
  }
}

/** Dependencies used for invalidation: hidden ones plus every leaf (intermediate nodes are pruned). */
function finalizeDependencies(deps: Map<string, Dependency>): { compare: Dependency[]; exposed: string[] } {
  const all = [...deps.values()]
  const isPruned = (dep: Dependency) =>
    !dep.whole && !dep.selector && all.some((other) => other !== dep && other.arg === dep.arg && other.id.startsWith(`${dep.id}.`))
  const kept = all.filter((dep) => !isPruned(dep))
  return {
    compare: kept,
    exposed: kept.filter((dep) => !dep.hidden).map((dep) => dep.id),
  }
}

export function createAtomicSelector(
  logic: BuiltLogic,
  name: string,
  args: ((state: any, props: any) => any)[],
  func: (...args: any[]) => any,
  memoizeOptions?: any,
): (state: any, props: any) => any {
  const registry = getRegistry()

  const argInfo = args.map((arg, index) => {
    const key = Object.keys(logic.selectors).find((k) => logic.selectors[k] === arg)
    if (key === undefined) {
      return { kind: 'opaque' as const, key: `arg${index}` }
    }
    return { kind: key in logic.reducers ? ('reducer' as const) : ('selector' as const), key }
  })

  const meta: SelectorMeta = {
    name,
    staticDeps: argInfo.filter((a) => a.kind === 'selector').map((a) => a.key),
    dependencies: null,
    evaluations: 0,
    dirtyCause: null,
  }
  registry.selectors.set(metaId(logic, name), meta)
  const names = registry.logics.get(logic.pathString) ?? []
  if (!names.includes(name)) {
    names.push(name)
  }
  registry.logics.set(logic.pathString, names)

  const equalityCheck: (a: any, b: any) => boolean =
    typeof memoizeOptions === 'function'
      ? memoizeOptions
      : memoizeOptions?.resultEqualityCheck ?? memoizeOptions?.equalityCheck ?? ((a: any, b: any) => a === b)
  const hasOpaque = argInfo.some((a) => a.kind === 'opaque')

  let cache: { state: any; result: any; deps: Dependency[] } | null = null

  return (state: any, props: any) => {
    if (cache && !hasOpaque && cache.state === state) {
      return cache.result
    }
    const values = args.map((arg) => arg(state, props))

    if (cache) {
      const changed = cache.deps.find((dep) => resolve(values[dep.arg], dep.segments) !== dep.value)
      if (!changed) {
        cache.state = state
        return cache.result
      }
      meta.dirtyCause = changed.selector ? `selector:${changed.selector}` : changed.id
    }

    const tracker = new Tracker()
    const proxiedValues = values.map((value, index) => {
      const info = argInfo[index]
      if (info.kind === 'reducer') {
        tracker.record(index, info.key, [], value)
        return tracker.wrap(value, index, info.key, [])
      }
      tracker.record(index, info.key, [], value, {
        hidden: info.kind === 'opaque',
        selector: info.kind === 'selector' ? info.key : undefined,
      })
      return value
    })

    let result: any
    try {
      meta.evaluations += 1
      result = unwrapResult(func(...proxiedValues))
    } finally {
      tracker.active = false
    }

    const { compare, exposed } = finalizeDependencies(tracker.deps)
    meta.dependencies = exposed
    if (cache && equalityCheck(cache.result, result)) {
      result = cache.result
    }
    cache = { state, result, deps: compare }
    return result
  }
}

export function detectCircularSelectors(logic: Logic): void {
  const metas = new Map(getLogicMetas(logic).map((meta) => [meta.name, meta]))
  const visiting = new Set<string>()
  const done = new Set<string>()
  const stack: string[] = []

  const visit = (name: string) => {
    if (done.has(name)) {
      return
    }
    if (visiting.has(name)) {
      const cycle = [...stack.slice(stack.indexOf(name)), name].join(' -> ')
      throw new Error(`[KEA] Circular dependency detected in logic "${logic.pathString}": ${cycle}`)
    }
    visiting.add(name)
    stack.push(name)
    for (const dep of metas.get(name)?.staticDeps ?? []) {
      if (metas.has(dep)) {
        visit(dep)
      }
    }
    stack.pop()
    visiting.delete(name)
    done.add(name)
  }

  for (const name of metas.keys()) {
    visit(name)
  }
}

export function getSelectorHealth(logic: Logic): SelectorHealth {
  const metas = getLogicMetas(logic)
  const names = new Set(metas.map((meta) => meta.name))
  const selectors: SelectorHealth['selectors'] = {}

  for (const meta of metas) {
    selectors[meta.name] = {
      dependencies: [...(meta.dependencies ?? meta.staticDeps)],
      dependents: metas.filter((other) => other.staticDeps.includes(meta.name)).map((other) => other.name),
      evaluations: meta.evaluations,
      dirtyCause: meta.dirtyCause,
    }
  }

  const topologicalOrder: string[] = []
  const placed = new Set<string>()
  while (topologicalOrder.length < metas.length) {
    const next = metas.find(
      (meta) => !placed.has(meta.name) && meta.staticDeps.every((dep) => !names.has(dep) || placed.has(dep)),
    )
    if (!next) {
      break
    }
    placed.add(next.name)
    topologicalOrder.push(next.name)
  }

  return { selectors, topologicalOrder }
}
