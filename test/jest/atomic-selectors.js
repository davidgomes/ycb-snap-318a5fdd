import React from 'react'
import { render, screen, act } from '@testing-library/react'
import { kea, resetContext, actions, reducers, selectors, afterMount, useValues, getContext } from '../../src'

describe('atomic selectors', () => {
  beforeEach(() => {
    resetContext({ atomicSelectors: true })
  })

  const userLogic = () =>
    kea([
      actions({ setName: (name) => ({ name }), setAge: (age) => ({ age }), setBoth: (name, age) => ({ name, age }) }),
      reducers({
        user: [
          { name: 'a', age: 1 },
          {
            setName: (state, { name }) => ({ ...state, name }),
            setAge: (state, { age }) => ({ ...state, age }),
            setBoth: (state, { name, age }) => ({ ...state, name, age }),
          },
        ],
      }),
      selectors({
        userName: [(s) => [s.user], (user) => user.name],
        greeting: [(s) => [s.userName], (userName) => `hi ${userName}`],
        summary: [(s) => [s.user], (user) => `${user.name} ${user.age}`],
      }),
    ])

  test('disabled by default', () => {
    resetContext()
    const logic = userLogic()
    logic.mount()
    expect(getContext().options.atomicSelectors).toBe(false)
    expect(logic.selectorHealth).toBeUndefined()
    expect(logic.values.greeting).toBe('hi a')
  })

  test('tracks leaf dependencies', () => {
    const logic = userLogic()
    logic.mount()
    expect(logic.values.greeting).toBe('hi a')
    expect(logic.values.summary).toBe('a 1')

    const health = logic.selectorHealth()
    expect(health.selectors.userName.dependencies).toEqual(['user.name'])
    expect(health.selectors.summary.dependencies).toEqual(['user.name', 'user.age'])
    expect(health.selectors.greeting.dependencies).toEqual(['userName'])
    expect(health.selectors.userName.dependents).toEqual(['greeting'])
    expect(health.topologicalOrder.indexOf('userName')).toBeLessThan(health.topologicalOrder.indexOf('greeting'))
    expect(health.selectors.userName.evaluations).toBe(1)

    logic.actions.setAge(2)
    expect(logic.values.greeting).toBe('hi a')
    expect(logic.values.summary).toBe('a 2')
    expect(logic.selectorHealth().selectors.userName.evaluations).toBe(1)
    expect(logic.selectorHealth().selectors.greeting.evaluations).toBe(1)
    expect(logic.selectorHealth().selectors.summary.dirtyCause).toBe('user.age')

    logic.actions.setName('b')
    expect(logic.values.greeting).toBe('hi b')
    const after = logic.selectorHealth().selectors
    expect(after.userName.evaluations).toBe(2)
    expect(after.userName.dirtyCause).toBe('user.name')
    expect(after.greeting.evaluations).toBe(2)
    expect(after.greeting.dirtyCause).toBe('selector:userName')
  })

  test('multiple changes in one action evaluate once', () => {
    const logic = userLogic()
    logic.mount()
    expect(logic.values.summary).toBe('a 1')
    logic.actions.setBoth('b', 2)
    expect(logic.values.summary).toBe('b 2')
    expect(logic.values.summary).toBe('b 2')
    expect(logic.selectorHealth().selectors.summary.evaluations).toBe(2)
  })

  test('unchanged intermediate outputs stop propagation', () => {
    const logic = kea([
      actions({ setAge: (age) => ({ age }) }),
      reducers({ user: [{ age: 20 }, { setAge: (s, { age }) => ({ ...s, age }) }] }),
      selectors({
        isAdult: [(s) => [s.user], (user) => user.age >= 18],
        label: [(s) => [s.isAdult], (isAdult) => (isAdult ? 'adult' : 'minor')],
        shout: [(s) => [s.label], (label) => label.toUpperCase()],
      }),
    ])
    logic.mount()
    expect(logic.values.shout).toBe('ADULT')
    expect(logic.selectorHealth().topologicalOrder).toEqual(['isAdult', 'label', 'shout'])

    logic.actions.setAge(30)
    // refreshed right after the action, without reading any values
    let health = logic.selectorHealth().selectors
    expect(health.isAdult.evaluations).toBe(2)
    expect(health.isAdult.dirtyCause).toBe('user.age')
    expect(health.label.evaluations).toBe(1)
    expect(health.shout.evaluations).toBe(1)

    logic.actions.setAge(10)
    health = logic.selectorHealth().selectors
    expect(health.label.evaluations).toBe(2)
    expect(health.shout.evaluations).toBe(2)
    expect(health.shout.dirtyCause).toBe('selector:label')
    expect(logic.values.shout).toBe('MINOR')
    expect(logic.selectorHealth().selectors.shout.evaluations).toBe(2)
  })

  test('collections', () => {
    const logic = kea([
      actions({ setMap: (map) => ({ map }), setSet: (set) => ({ set }), setList: (list) => ({ list }) }),
      reducers({
        map: [
          new Map([
            ['a', 1],
            ['b', 2],
          ]),
          { setMap: (_, { map }) => map },
        ],
        set: [new Set(['a']), { setSet: (_, { set }) => set }],
        list: [[1, 2, 3], { setList: (_, { list }) => list }],
      }),
      selectors({
        mapA: [(s) => [s.map], (map) => map.get('a')],
        hasA: [(s) => [s.set], (set) => set.has('a')],
        hasTwo: [(s) => [s.list], (list) => list.includes(2)],
        firstTwo: [(s) => [s.list], (list) => list[0] + list[1]],
      }),
    ])
    logic.mount()
    expect(logic.values.mapA).toBe(1)
    expect(logic.values.hasA).toBe(true)
    expect(logic.values.hasTwo).toBe(true)
    expect(logic.values.firstTwo).toBe(3)

    const health = logic.selectorHealth().selectors
    expect(health.mapA.dependencies).toEqual(['map.map:a'])
    expect(health.hasA.dependencies).toEqual(['set.set:a'])
    expect(health.hasTwo.dependencies).toEqual(['list.0', 'list.1'])
    expect(health.firstTwo.dependencies).toEqual(['list.0', 'list.1'])

    logic.actions.setMap(
      new Map([
        ['a', 1],
        ['b', 3],
      ]),
    )
    logic.actions.setSet(new Set(['a', 'b']))
    logic.actions.setList([1, 2, 4])
    expect(logic.values.mapA).toBe(1)
    expect(logic.values.hasA).toBe(true)
    expect(logic.values.hasTwo).toBe(true)
    expect(logic.values.firstTwo).toBe(3)
    const unchanged = logic.selectorHealth().selectors
    expect([unchanged.mapA, unchanged.hasA, unchanged.hasTwo, unchanged.firstTwo].map((s) => s.evaluations)).toEqual([
      1, 1, 1, 1,
    ])

    logic.actions.setMap(new Map([['a', 5]]))
    logic.actions.setList([2])
    expect(logic.values.mapA).toBe(5)
    expect(logic.values.hasTwo).toBe(true)
    expect(logic.selectorHealth().selectors.mapA.dirtyCause).toBe('map.map:a')
    expect(logic.selectorHealth().selectors.hasTwo.evaluations).toBe(2)
  })

  test('returned state is not proxied', () => {
    const logic = kea([
      actions({ setAge: (age) => ({ age }) }),
      reducers({ user: [{ name: 'a', age: 1, tags: [{ id: 1 }] }, { setAge: (s, { age }) => ({ ...s, age }) }] }),
      selectors({
        whole: [(s) => [s.user], (user) => user],
        tags: [(s) => [s.user], (user) => user.tags.filter((t) => t.id)],
      }),
    ])
    logic.mount()
    const state = logic.values.user
    expect(logic.values.whole).toBe(state)
    expect(logic.values.tags[0]).toBe(state.tags[0])
    logic.actions.setAge(2)
    expect(logic.values.whole).toBe(logic.values.user)
    expect(logic.values.whole.age).toBe(2)
  })

  test('works with frozen state', () => {
    const logic = kea([
      reducers({ user: [Object.freeze({ profile: Object.freeze({ name: 'a' }) }), {}] }),
      selectors({ name: [(s) => [s.user], (user) => user.profile.name] }),
    ])
    logic.mount()
    expect(logic.values.name).toBe('a')
    expect(logic.selectorHealth().selectors.name.dependencies).toEqual(['user.profile.name'])
  })

  test('circular dependencies throw while building', () => {
    const logic = kea([
      reducers({ a: [1, {}] }),
      selectors({
        b: [(s) => [s.c], (c) => c],
        c: [(s) => [s.b], (b) => b],
      }),
    ])
    expect(() => logic.build()).toThrow('[KEA] Circular dependency detected')
  })

  test('afterMount order is unchanged', () => {
    const calls = []
    const logic = kea([
      reducers({ a: [1, {}] }),
      selectors({ b: [(s) => [s.a], (a) => a + 1] }),
      afterMount(({ values }) => calls.push(values.b)),
    ])
    logic.mount()
    expect(calls).toEqual([2])
  })

  test('components only re-render when accessed values change', async () => {
    const logic = userLogic()
    let renders = 0
    function Component() {
      const { greeting } = useValues(logic)
      renders++
      return <div data-testid="greeting">{greeting}</div>
    }
    render(<Component />)
    expect(screen.getByTestId('greeting')).toHaveTextContent('hi a')
    const initialRenders = renders

    act(() => logic.actions.setAge(5))
    expect(renders).toBe(initialRenders)

    act(() => logic.actions.setName('z'))
    expect(screen.getByTestId('greeting')).toHaveTextContent('hi z')
    expect(renders).toBe(initialRenders + 1)
  })
})
