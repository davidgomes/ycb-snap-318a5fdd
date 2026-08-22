import { kea, resetContext, actions, reducers, selectors, afterMount } from '../../src'
import React from 'react'
import { render, fireEvent, act } from '@testing-library/react'
import { useValues, useActions } from '../../src'

describe('atomic selectors', () => {
  test('selectorHealth is undefined when atomicSelectors is disabled', () => {
    resetContext({ createStore: true, atomicSelectors: false })
    const logic = kea([
      reducers({ user: [{ name: 'Ada', age: 1 }, {}] }),
      selectors({
        userName: [(s) => [s.user], (user) => user.name],
      }),
    ])
    logic.mount()
    expect(logic.selectorHealth).toBeUndefined()
  })

  test('tracks leaf dependencies and ignores unrelated sibling fields', () => {
    resetContext({ createStore: true, atomicSelectors: true })
    const logic = kea([
      actions({
        setName: (name) => ({ name }),
        setAge: (age) => ({ age }),
      }),
      reducers({
        user: [
          { name: 'Ada', age: 36 },
          {
            setName: (state, { name }) => ({ ...state, name }),
            setAge: (state, { age }) => ({ ...state, age }),
          },
        ],
      }),
      selectors({
        userName: [(s) => [s.user], (user) => user.name],
      }),
    ])
    logic.mount()

    expect(logic.values.userName).toBe('Ada')
    expect(logic.selectorHealth().selectors.userName.evaluations).toBe(1)
    expect(logic.selectorHealth().selectors.userName.dependencies).toEqual(['user.name'])

    logic.actions.setAge(37)
    expect(logic.values.userName).toBe('Ada')
    expect(logic.selectorHealth().selectors.userName.evaluations).toBe(1)
    expect(logic.selectorHealth().selectors.userName.dirtyCause).toBe(null)

    logic.actions.setName('Grace')
    expect(logic.values.userName).toBe('Grace')
    expect(logic.selectorHealth().selectors.userName.evaluations).toBe(2)
    expect(logic.selectorHealth().selectors.userName.dirtyCause).toBe('user.name')
  })

  test('tracks Map, Set and Array collection access', () => {
    resetContext({ createStore: true, atomicSelectors: true })
    const logic = kea([
      actions({
        setMap: (data) => ({ data }),
        setSet: (data) => ({ data }),
        setList: (list) => ({ list }),
      }),
      reducers({
        data: [
          new Map([['a', 1], ['b', 2]]),
          {
            setMap: (_, { data }) => data,
          },
        ],
        items: [
          new Set(['a', 'b']),
          {
            setSet: (_, { data }) => data,
          },
        ],
        list: [
          ['a', 'b'],
          {
            setList: (_, { list }) => list,
          },
        ],
      }),
      selectors({
        mapA: [(s) => [s.data], (data) => data.get('a')],
        hasA: [(s) => [s.items], (items) => items.has('a')],
        includesA: [(s) => [s.list], (list) => list.includes('a')],
        firstTwo: [(s) => [s.list], (list) => list[0] + list[1]],
      }),
    ])
    logic.mount()

    expect(logic.values.mapA).toBe(1)
    expect(logic.values.hasA).toBe(true)
    expect(logic.values.includesA).toBe(true)
    expect(logic.values.firstTwo).toBe('ab')

    const health = logic.selectorHealth()
    expect(health.selectors.mapA.dependencies).toEqual(['data.map:a'])
    expect(health.selectors.hasA.dependencies).toEqual(['items.set:a'])
    expect(health.selectors.firstTwo.dependencies).toEqual(['list.0', 'list.1'])
    expect(health.selectors.includesA.dependencies).toEqual(['list.0'])

    const mapEvals = health.selectors.mapA.evaluations
    logic.actions.setMap(new Map([['a', 1], ['b', 99]]))
    expect(logic.values.mapA).toBe(1)
    expect(logic.selectorHealth().selectors.mapA.evaluations).toBe(mapEvals)
  })

  test('propagates through selector chains and batches one action', () => {
    resetContext({ createStore: true, atomicSelectors: true })
    const logic = kea([
      actions({
        setUser: (user) => ({ user }),
        setBoth: (name, extra) => ({ name, extra }),
      }),
      reducers({
        user: [
          { name: 'Ada', extra: 'x' },
          {
            setUser: (_, { user }) => user,
            setBoth: (state, { name, extra }) => ({ ...state, name, extra }),
          },
        ],
      }),
      selectors({
        greeting: [(s) => [s.userName], (userName) => `Hello ${userName}`],
        userName: [(s) => [s.user], (user) => user.name],
        unused: [(s) => [s.user], (user) => user.extra],
      }),
    ])
    logic.mount()

    expect(logic.values.greeting).toBe('Hello Ada')
    expect(logic.values.unused).toBe('x')
    expect(logic.selectorHealth().selectors.greeting.dependencies).toEqual(['userName'])
    expect(logic.selectorHealth().selectors.userName.dependents).toEqual(['greeting'])
    expect(logic.selectorHealth().topologicalOrder).toEqual(
      expect.arrayContaining(['userName', 'greeting', 'unused']),
    )
    expect(
      logic.selectorHealth().topologicalOrder.indexOf('userName') <
        logic.selectorHealth().topologicalOrder.indexOf('greeting'),
    ).toBe(true)

    const greetingEvals = logic.selectorHealth().selectors.greeting.evaluations
    logic.actions.setUser({ name: 'Ada', extra: 'y' })
    expect(logic.values.greeting).toBe('Hello Ada')
    expect(logic.selectorHealth().selectors.greeting.evaluations).toBe(greetingEvals)
    expect(logic.values.unused).toBe('y')

    logic.actions.setBoth('Grace', 'z')
    expect(logic.values.greeting).toBe('Hello Grace')
    expect(logic.selectorHealth().selectors.greeting.evaluations).toBe(greetingEvals + 1)
    expect(logic.selectorHealth().selectors.greeting.dirtyCause).toBe('selector:userName')
    expect(logic.selectorHealth().selectors.userName.dirtyCause).toBe('user.name')
  })

  test('throws a circular dependency error while building', () => {
    resetContext({ createStore: true, atomicSelectors: true })
    const logic = kea([
      reducers({ n: [1, {}] }),
      selectors({
        a: [(s) => [s.b], (b) => b],
        b: [(s) => [s.a], (a) => a],
      }),
    ])
    expect(() => logic.mount()).toThrow('[KEA] Circular dependency detected')
  })

  test('does not disrupt afterMount plugin event ordering', () => {
    resetContext({ createStore: true, atomicSelectors: true })
    const order = []
    const plugin = {
      name: 'order-test',
      events: {
        afterMount() {
          order.push('plugin.afterMount')
        },
      },
    }
    const { activatePlugin } = require('../../src')
    activatePlugin(plugin)

    const logic = kea([
      reducers({ n: [1, {}] }),
      afterMount(() => {
        order.push('logic.afterMount')
      }),
    ])
    logic.mount()
    expect(order).toEqual(['plugin.afterMount', 'logic.afterMount'])
  })

  test('react components re-render only for accessed leaves', () => {
    resetContext({ createStore: true, atomicSelectors: true })
    const logic = kea([
      actions({
        setName: (name) => ({ name }),
        setAge: (age) => ({ age }),
      }),
      reducers({
        user: [
          { name: 'Ada', age: 36 },
          {
            setName: (state, { name }) => ({ ...state, name }),
            setAge: (state, { age }) => ({ ...state, age }),
          },
        ],
      }),
    ])

    let renders = 0
    function Comp() {
      const { user } = useValues(logic)
      const { setName, setAge } = useActions(logic)
      renders += 1
      return (
        <div>
          <div data-testid="name">{user.name}</div>
          <button data-testid="age" onClick={() => setAge(40)} />
          <button data-testid="name-btn" onClick={() => setName('Grace')} />
        </div>
      )
    }

    const { getByTestId } = render(<Comp />)
    const start = renders
    act(() => {
      fireEvent.click(getByTestId('age'))
    })
    expect(renders).toBe(start)
    act(() => {
      fireEvent.click(getByTestId('name-btn'))
    })
    expect(getByTestId('name').textContent).toBe('Grace')
    expect(renders).toBe(start + 1)
  })

  test('tracks nested collection leaves and advanced array methods', () => {
    resetContext({ createStore: true, atomicSelectors: true })
    const logic = kea([
      actions({
        setList: (list) => ({ list }),
        setMap: (data) => ({ data }),
      }),
      reducers({
        list: [
          [
            { id: 1, name: 'Ada' },
            { id: 2, name: 'Grace' },
          ],
          { setList: (_, { list }) => list },
        ],
        data: [
          new Map([
            ['a', { name: 'Ada' }],
            ['b', { name: 'Grace' }],
          ]),
          { setMap: (_, { data }) => data },
        ],
      }),
      selectors({
        firstMatch: [(s) => [s.list], (list) => list.find((item) => item.id === 1)?.name],
        mapName: [(s) => [s.data], (data) => data.get('a')?.name],
      }),
    ])
    logic.mount()

    expect(logic.values.firstMatch).toBe('Ada')
    expect(logic.values.mapName).toBe('Ada')
    expect(logic.selectorHealth().selectors.firstMatch.dependencies).toEqual(['list.0.id', 'list.0.name'])
    expect(logic.selectorHealth().selectors.mapName.dependencies).toEqual(['data.map:a.name'])

    const firstMatchEvaluations = logic.selectorHealth().selectors.firstMatch.evaluations
    const mapNameEvaluations = logic.selectorHealth().selectors.mapName.evaluations
    logic.actions.setList([
      { id: 1, name: 'Ada' },
      { id: 2, name: 'Updated' },
    ])
    logic.actions.setMap(
      new Map([
        ['a', { name: 'Ada' }],
        ['b', { name: 'Updated' }],
      ]),
    )
    expect(logic.values.firstMatch).toBe('Ada')
    expect(logic.values.mapName).toBe('Ada')
    expect(logic.selectorHealth().selectors.firstMatch.evaluations).toBe(firstMatchEvaluations)
    expect(logic.selectorHealth().selectors.mapName.evaluations).toBe(mapNameEvaluations)
  })

  test('invalidates selectors when props used as inputs change', () => {
    resetContext({ createStore: true, atomicSelectors: true })
    const logic = kea([
      reducers({ names: [['Ada', 'Grace'], {}] }),
      selectors({
        selectedName: [(s, p) => [s.names, p.index], (names, index) => names[index]],
      }),
    ])
    const builtLogic = logic({ index: 0 })
    builtLogic.mount()
    expect(builtLogic.values.selectedName).toBe('Ada')

    const nextLogic = logic({ index: 1 })
    expect(nextLogic).toBe(builtLogic)
    expect(builtLogic.values.selectedName).toBe('Grace')
  })

  test('applies atomic subscriptions to wrapped components', () => {
    resetContext({ createStore: true, atomicSelectors: true })
    const logic = kea([
      actions({
        setName: (name) => ({ name }),
        setAge: (age) => ({ age }),
      }),
      reducers({
        user: [
          { name: 'Ada', age: 36 },
          {
            setName: (state, { name }) => ({ ...state, name }),
            setAge: (state, { age }) => ({ ...state, age }),
          },
        ],
      }),
    ])

    let renders = 0
    const Component = ({ user }) => {
      renders += 1
      return <div data-testid="wrapped-name">{user.name}</div>
    }
    const Wrapped = logic(Component)
    const { getByTestId } = render(<Wrapped />)
    const start = renders

    act(() => logic.actions.setAge(40))
    expect(renders).toBe(start)
    act(() => logic.actions.setName('Grace'))
    expect(getByTestId('wrapped-name').textContent).toBe('Grace')
    expect(renders).toBe(start + 1)
  })
})
