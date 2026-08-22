import { useMemo, useEffect, useLayoutEffect, useRef, useContext, createContext } from 'react'
import { useSyncExternalStore } from 'use-sync-external-store/shim'
import { LogicWrapper, BuiltLogic, Logic, Selector } from '../types'
import { getContext } from '../kea/context'
import { isLogicWrapper } from '../utils'
import {
  createTrackingProxy,
  getAtomicCache,
  getValueAtPath,
  isAtomicSelectorsEnabled,
  normalizeDependencies,
} from '../core/atomicSelectors'

/** True if we dispatched an action in a component's body *while* rendering. For example when mounting a logic.
 * Old subscriptions shouldn't update until after rendering. */
export let pauseCounter = 0
export const isPaused = () => pauseCounter !== 0

const getStoreState = () => getContext().store.getState()

export function useSelector(selector: Selector): any {
  return useSyncExternalStore(getContext().store.subscribe, () => selector(getStoreState()))
}

type AtomicAccess = {
  keys: Set<string>
  leaves: Set<string>
}

type AtomicSelection = {
  key: string
  value: any
}[]

function selectionEqual(previous: AtomicSelection | undefined, next: AtomicSelection): boolean {
  return (
    !!previous &&
    previous.length === next.length &&
    previous.every((entry, index) => entry.key === next[index].key && Object.is(entry.value, next[index].value))
  )
}

function getAtomicSelection(builtLogic: BuiltLogic, accessed: AtomicAccess): AtomicSelection {
  let reducerState: any
  try {
    reducerState = builtLogic.selector ? builtLogic.selector(getStoreState()) : undefined
  } catch {
    reducerState = undefined
  }

  const cache = getAtomicCache(builtLogic)
  const leaves = normalizeDependencies(accessed.leaves)
  const selection: AtomicSelection = []

  for (const key of accessed.keys) {
    const hasNestedStateLeaf = leaves.some((leaf) => leaf === key || leaf.startsWith(`${key}.`))
    if (cache.nodes[key]?.isDerived || !hasNestedStateLeaf) {
      selection.push({ key: `selector:${key}`, value: builtLogic.selectors[key](getStoreState(), builtLogic.props) })
    }
  }

  for (const leaf of leaves) {
    if (!Array.from(accessed.keys).some((key) => cache.nodes[key]?.isDerived && (leaf === key || leaf.startsWith(`${key}.`)))) {
      selection.push({ key: `leaf:${leaf}`, value: getValueAtPath(reducerState, leaf) })
    }
  }
  return selection
}

function useAtomicValues<L extends Logic = Logic>(builtLogic: BuiltLogic<L>): L['values'] {
  const accessedRef = useRef<AtomicAccess>({ keys: new Set<string>(), leaves: new Set<string>() })
  const versionRef = useRef(0)
  const selectionRef = useRef<AtomicSelection>()

  const getSnapshot = () => {
    const selection = getAtomicSelection(builtLogic, accessedRef.current)
    if (!selectionEqual(selectionRef.current, selection)) {
      selectionRef.current = selection
      versionRef.current += 1
    }
    return versionRef.current
  }

  useSyncExternalStore(getContext().store.subscribe, getSnapshot, getSnapshot)

  useLayoutEffect(() => {
    selectionRef.current = getAtomicSelection(builtLogic, accessedRef.current)
  })

  return useMemo(() => {
    const response: Record<string, any> = {}
    for (const key of Object.keys(builtLogic.selectors)) {
      Object.defineProperty(response, key, {
        enumerable: true,
        get: () => {
          accessedRef.current.keys.add(key)
          const value = builtLogic.selectors[key](getStoreState(), builtLogic.props)
          return createTrackingProxy(value, key, (dep) => accessedRef.current.leaves.add(dep))
        },
      })
    }
    return response as L['values']
  }, [builtLogic.pathString])
}

export function useValues<L extends Logic = Logic>(logic: BuiltLogic<L> | LogicWrapper<L>): L['values'] {
  const builtLogic = useMountedLogic(logic)
  if (isAtomicSelectorsEnabled()) {
    return useAtomicValues(builtLogic)
  }

  return useMemo(() => {
    const response = {}
    for (const key of Object.keys(builtLogic.selectors)) {
      Object.defineProperty(response, key, {
        get: () => useSelector(builtLogic.selectors[key]),
      })
    }
    return response
  }, [builtLogic.pathString])
}

export function useAllValues<L extends Logic = Logic>(logic: BuiltLogic<L> | LogicWrapper<L>): L['values'] {
  const builtLogic = useMountedLogic(logic)
  if (isAtomicSelectorsEnabled()) {
    return useAtomicValues(builtLogic)
  }

  const response: Record<string, any> = {}
  for (const key of Object.keys(builtLogic.selectors)) {
    response[key] = useSelector(builtLogic.selectors[key])
  }

  return response
}

export function useActions<L extends Logic = Logic>(logic: BuiltLogic<L> | LogicWrapper<L>): L['actions'] {
  const builtLogic = useMountedLogic(logic)
  return builtLogic['actions']
}

export function useAsyncActions<L extends Logic = Logic>(logic: BuiltLogic<L> | LogicWrapper<L>): L['asyncActions'] {
  const builtLogic = useMountedLogic(logic)
  return builtLogic['asyncActions']
}

const blankContext = createContext(undefined as BuiltLogic | undefined)

export function useMountedLogic<L extends Logic = Logic>(logic: BuiltLogic<L> | LogicWrapper<L>): BuiltLogic<L> {
  const builtLogicContext = isLogicWrapper(logic) ? getContext().react.contexts.get(logic) : null
  const defaultBuiltLogic = useContext(builtLogicContext || blankContext)
  const builtLogic = isLogicWrapper(logic) ? defaultBuiltLogic || logic.build() : logic

  const unmount = useRef(undefined as undefined | (() => void))

  if (!unmount.current) {
    batchChanges(() => {
      unmount.current = builtLogic.mount()
    })
  }

  const pathString = useRef(builtLogic.pathString)

  if (pathString.current !== builtLogic.pathString) {
    batchChanges(() => {
      unmount.current?.()
      unmount.current = builtLogic.mount()
      pathString.current = builtLogic.pathString
    })
  }

  useEffect(function useMountedLogicEffect() {
    // React Fast Refresh calls `useMountedLogicEffectCleanup` followed directly by `useMountedLogicEffect`.
    // Thus if we're here and there's still no `unmount.current`, it's because we just refreshed.
    // Normally we still mount the logic sync in the component, just to have the data there when selectors fire.
    if (!unmount.current) {
      batchChanges(() => {
        unmount.current = builtLogic.mount()
        pathString.current = builtLogic.pathString
      })
    }

    return function useMountedLogicEffectCleanup() {
      batchChanges(() => {
        unmount.current && unmount.current()
        unmount.current = undefined
      })
    }
  }, [])

  return builtLogic as BuiltLogic<L>
}

let timeout: any
/** Delay Redux subscriptions from firing and asking React to re-render.
 * Will set a Timeout to flush if store changed during callback. */
export function batchChanges(callback: () => void) {
  const previousState = getStoreState()
  pauseCounter += 1
  try {
    callback()
  } catch (e) {
  } finally {
    pauseCounter -= 1
  }
  const newState = getStoreState()
  if (previousState !== newState) {
    timeout && clearTimeout(timeout)
    timeout = setTimeout(() => getContext().store.dispatch({ type: '@KEA/FLUSH' }), 0)
  }
}
