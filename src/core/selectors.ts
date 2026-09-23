import { Logic, LogicBuilder, LogicPropSelectors, Selector, SelectorDefinition, SelectorDefinitions } from '../types'
import { createSelector, createSelectorCreator, defaultMemoize, ParametricSelector } from 'reselect'
import { getContext, getStoreState } from '../kea/context'
import { getAtomicSelectorGraph, trackSelectorNames } from './atomic'

/**
  Logic builder:
      props({} as { id: number })
      selectors({
        duckAndChicken: [
          (s, p) => [s.duckId, s.chickenId, p.id],
          (duckId, chickenId, id) => duckId + chickenId + id,
          (a: any, b: any) => bool, // custom isEquals, defaults to `a === b`
        ],
      })

  Adds:

      logic.selector = state => state.scenes.farm // memoized via reselect
      logic.selectors = {
        duckAndChicken: state => logic.selector(state).duckAndChicken // memoized via reselect
      }
*/
export function selectors<L extends Logic = Logic>(
  input: SelectorDefinitions<L> | ((logic: L) => SelectorDefinitions<L>),
): LogicBuilder<L> {
  return (logic) => {
    const selectorInputs = typeof input === 'function' ? input(logic) : input
    const atomicGraph = getContext().options.atomicSelectors ? getAtomicSelectorGraph(logic) : null

    // small cache so the order would not count
    const builtSelectors: Record<string, Selector> = {}
    for (const key of Object.keys(selectorInputs)) {
      if (typeof logic.selectors[key] !== 'undefined') {
        throw new Error(`[KEA] Logic "${logic.pathString}" selector "${key}" already exists`)
      }
      if (atomicGraph) {
        atomicGraph.declare(key)
        addSelectorAndValue(logic, key, (state = getStoreState(), props = logic.props) =>
          atomicGraph.read(key, state, props),
        )
      } else {
        addSelectorAndValue(logic, key, (...args) => builtSelectors[key](...args))
      }
    }

    const propSelectors =
      typeof Proxy !== 'undefined'
        ? new Proxy(logic.props, {
            get(target, prop) {
              if (!(prop in target)) {
                throw new Error(
                  `[KEA] Prop "${String(prop)}" not found for logic "${
                    logic.pathString
                  }". Attempted to use in a selector. Please specify a default via props({ ${String(
                    prop,
                  )}: '' }) to resolve.`,
                )
              }
              return () => target[prop]
            },
          })
        : (Object.fromEntries(
            Object.keys(logic.props).map((key) => [key, () => logic.props[key]]),
          ) as LogicPropSelectors<L>)

    for (const entry of Object.entries(selectorInputs)) {
      const [key, arr]: [string, SelectorDefinition<L['selectors'], LogicPropSelectors<L>, any> | undefined] = entry
      if (!arr) {
        throw new Error(`[KEA] Logic "${logic.pathString}" selector "${key}" is undefined`)
      }
      const [input, func, memoizeOptions] = arr
      const inputNames = atomicGraph && new Map<unknown, string>()
      const args: ParametricSelector<any, any, any>[] = inputNames
        ? input(
            trackSelectorNames(logic.selectors, inputNames),
            trackSelectorNames(propSelectors, inputNames, 'props.'),
          )
        : input(logic.selectors, propSelectors)

      if (args.filter((a) => typeof a !== 'function').length > 0) {
        const argTypes = args.map((a) => typeof a).join(', ')
        const msg = `[KEA] Logic "${logic.pathString}", selector "${key}" has incorrect input: [${argTypes}].`
        throw new Error(msg)
      }
      if (atomicGraph && inputNames) {
        atomicGraph.define(key, args, inputNames, func, memoizeOptions)
        continue
      }
      builtSelectors[key] = createSelector(args, func, { memoizeOptions })

      addSelectorAndValue(logic, key, (state = getStoreState(), props = logic.props) =>
        builtSelectors[key](state, props),
      )

      if (!logic.values.hasOwnProperty(key)) {
        Object.defineProperty(logic.values, key, {
          get: function () {
            return logic.selectors[key](getStoreState(), logic.props)
          },
          enumerable: true,
        })
      }
    }

    atomicGraph?.sort()
  }
}

export function addSelectorAndValue<L extends Logic = Logic>(logic: L, key: string, selector: Selector): void {
  logic.selectors[key] = selector
  if (!logic.values.hasOwnProperty(key)) {
    Object.defineProperty(logic.values, key, {
      get: function () {
        return logic.selectors[key](getStoreState(), logic.props)
      },
      enumerable: true,
    })
  }
}
