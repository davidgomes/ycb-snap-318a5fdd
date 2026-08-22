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
} from '../core/atomicSelectors'

/** True if we dispatched an action in a component's body *while* rendering. For example when mounting a logic.
 * Old subscriptions shouldn't update until after rendering. */
export let pauseCounter = 0
export const isPaused = () => pauseCounter !== 0

const getStoreState = () => getContext().store.getState()

export function useSelector(selector: Selector): any {
  return useSyncExternalStore(getContext().store.subscribe, () => selector(getStoreState()))
}

function fingerprintValue(value: any): string {
  if (value instanceof Map) {
    return `Map(${[...value.entries()].map(([k, v]) => `${String(k)}:${String(v)}`).join(',')})`
  }
  if (value instanceof Set) {
    return `Set(${[...value].map(String).join(',')})`
  }
  if (typeof value === 'object' && value !== null) {
    try {
      return JSON.stringify(value)
    } catch {
      return String(value)
    }
  }
  return String(value)
}

export function useValues<L extends Logic = Logic>(logic: BuiltLogic<L> | LogicWrapper<L>): L['values'] {
  const builtLogic = useMountedLogic(logic)
  const accessedRef = useRef({ keys: new Set<string>(), leaves: new Set<string>() })
  const versionRef = useRef(0)
  const lastFingerprintRef = useRef<string | null>(null)

  const computeAtomicFingerprint = () => {
    let reducerState: any
    try {
      reducerState = builtLogic.selector ? builtLogic.selector(getStoreState()) : undefined
    } catch {
      reducerState = undefined
    }
    const parts: string[] = []
    for (const key of accessedRef.current.keys) {
      const cache = getAtomicCache(builtLogic)
      if (cache.nodes[key]?.isDerived) {
        parts.push(`${key}:${fingerprintValue(builtLogic.selectors[key]())}`)
      }
    }
    for (const leaf of accessedRef.current.leaves) {
      parts.push(`${leaf}:${fingerprintValue(getValueAtPath(reducerState, leaf))}`)
    }
    return parts.join('|')
  }

  const getAtomicSnapshot = () => {
    const fingerprint = computeAtomicFingerprint()
    if (lastFingerprintRef.current === null || fingerprint === lastFingerprintRef.current) {
      return versionRef.current
    }
    lastFingerprintRef.current = fingerprint
    versionRef.current += 1
    return versionRef.current
  }

  useLayoutEffect(() => {
    if (isAtomicSelectorsEnabled()) {
      lastFingerprintRef.current = computeAtomicFingerprint()
    }
  })

  useSyncExternalStore(
    isAtomicSelectorsEnabled() ? getContext().store.subscribe : () => () => {},
    isAtomicSelectorsEnabled() ? getAtomicSnapshot : () => 0,
  )

  return useMemo(() => {
    const response = {}

    for (const key of Object.keys(builtLogic.selectors)) {
      Object.defineProperty(response, key, {
        get: () => {
          if (isAtomicSelectorsEnabled()) {
            accessedRef.current.keys.add(key)
            const value = builtLogic.selectors[key](getStoreState(), builtLogic.props)
            return createTrackingProxy(value, key, (dep) => {
              accessedRef.current.leaves.add(dep)
            })
          }
          return useSelector(builtLogic.selectors[key])
        },
      })
    }

    return response
  }, [builtLogic.pathString])
}

export function useAllValues<L extends Logic = Logic>(logic: BuiltLogic<L> | LogicWrapper<L>): L['values'] {
  const builtLogic = useMountedLogic(logic)

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
