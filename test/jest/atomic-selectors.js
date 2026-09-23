import {
  kea,
  resetContext,
  getContext,
  actions,
  reducers,
  selectors,
  props,
  path,
  afterMount,
  activatePlugin,
  useValues,
  corePlugin,
} from '../../src'

import React from 'react'
import { render, screen, act } from '@testing-library/react'

function userLogicInput() {
  return [
    actions({
      setName: (name) => ({ name }),
      setAge: (age) => ({ age }),
      setBoth: (name, age) => ({ name, age }),
      setOther: (other) => ({ other }),
    }),
    reducers({
      user: [
        { name: 'bob', age: 30 },
        {
          setName: (state, { name }) => ({ ...state, name }),
          setAge: (state, { age }) => ({ ...state, age }),
          setBoth: (state, { name, age }) => ({ ...state, name, age }),
        },
      ],
      other: [0, { setOther: (_, { other }) => other }],
    }),
  ]
}

describe('atomic selectors', () => {
  beforeEach(() => {
    resetContext({ atomicSelectors: true })
  })

  test('is disabled by default', () => {
    resetContext()
    expect(getContext().options.atomicSelectors).toEqual(false)
    const logic = kea([...userLogicInput(), selectors({ userName: [(s) => [s.user], (user) => user.name] })])
    logic.mount()
    expect(logic.selectorHealth).toBeUndefined()
    expect(logic.build().selectorHealth).toBeUndefined()
    expect(logic.values.userName).toEqual('bob')
  })

  test('tracks leaf dependencies and skips unrelated changes', () => {
    const logic = kea([...userLogicInput(), selectors({ userName: [(s) => [s.user], (user) => user.name] })])
    logic.mount()

    expect(typeof logic.selectorHealth).toEqual('function')
    expect(logic.values.userName).toEqual('bob')
    expect(logic.selectorHealth().selectors.userName).toEqual({
      dependencies: ['user.name'],
      dependents: [],
      evaluations: 1,
      dirtyCause: null,
    })

    logic.actions.setAge(31)
    expect(logic.values.userName).toEqual('bob')
    expect(logic.selectorHealth().selectors.userName.evaluations).toEqual(1)
    expect(logic.selectorHealth().selectors.userName.dirtyCause).toEqual(null)

    logic.actions.setName('alice')
    expect(logic.selectorHealth().selectors.userName.dirtyCause).toEqual('user.name')
    expect(logic.values.userName).toEqual('alice')
    expect(logic.selectorHealth().selectors.userName.evaluations).toEqual(2)
  })

  test('nested paths only list the leaves', () => {
    const logic = kea([
      actions({ setCity: (city) => ({ city }), setStreet: (street) => ({ street }) }),
      reducers({
        user: [
          { address: { city: 'Paris', street: 'Rue' } },
          {
            setCity: (state, { city }) => ({ ...state, address: { ...state.address, city } }),
            setStreet: (state, { street }) => ({ ...state, address: { ...state.address, street } }),
          },
        ],
      }),
      selectors({ city: [(s) => [s.user], (user) => user.address.city] }),
    ])
    logic.mount()
    expect(logic.values.city).toEqual('Paris')
    expect(logic.selectorHealth().selectors.city.dependencies).toEqual(['user.address.city'])
    logic.actions.setStreet('Avenue')
    expect(logic.values.city).toEqual('Paris')
    expect(logic.selectorHealth().selectors.city.evaluations).toEqual(1)
    logic.actions.setCity('Tallinn')
    expect(logic.values.city).toEqual('Tallinn')
    expect(logic.selectorHealth().selectors.city.evaluations).toEqual(2)
  })

  test('returned objects are raw and tracked as a whole', () => {
    const logic = kea([
      ...userLogicInput(),
      selectors({
        userCopy: [(s) => [s.user], (user) => user],
        wrapped: [(s) => [s.user], (user) => ({ inner: user })],
      }),
    ])
    logic.mount()
    const state = getContext().store.getState()
    expect(logic.values.userCopy).toBe(logic.values.user)
    expect(logic.values.wrapped.inner).toBe(logic.values.user)
    expect(logic.selectorHealth().selectors.userCopy.dependencies).toEqual(['user'])
    logic.actions.setAge(40)
    expect(logic.values.userCopy).toEqual({ name: 'bob', age: 40 })
    expect(logic.values.wrapped.inner).toEqual({ name: 'bob', age: 40 })
    expect(getContext().store.getState()).not.toBe(state)
  })

  test('works with frozen state', () => {
    const logic = kea([
      actions({ setAge: (age) => ({ age }) }),
      reducers({
        user: [
          Object.freeze({ name: 'bob', age: 30, tags: Object.freeze(['a']) }),
          { setAge: (state, { age }) => Object.freeze({ ...state, age }) },
        ],
      }),
      selectors({ firstTag: [(s) => [s.user], (user) => user.tags[0]] }),
    ])
    logic.mount()
    expect(logic.values.firstTag).toEqual('a')
    expect(logic.selectorHealth().selectors.firstTag.dependencies).toEqual(['user.tags.0'])
    logic.actions.setAge(3)
    expect(logic.values.firstTag).toEqual('a')
    expect(logic.selectorHealth().selectors.firstTag.evaluations).toEqual(1)
  })

  test('map keys, set membership and array elements', () => {
    const logic = kea([
      actions({
        setMap: (key, value) => ({ key, value }),
        addToSet: (value) => ({ value }),
        setList: (list) => ({ list }),
      }),
      reducers({
        map: [
          new Map([
            ['a', 1],
            ['b', 2],
          ]),
          { setMap: (state, { key, value }) => new Map(state).set(key, value) },
        ],
        set: [new Set(['a']), { addToSet: (state, { value }) => new Set(state).add(value) }],
        list: [['x', 'y', 'z'], { setList: (_, { list }) => list }],
      }),
      selectors({
        mapA: [(s) => [s.map], (map) => map.get('a')],
        setHasA: [(s) => [s.set], (set) => set.has('a')],
        hasY: [(s) => [s.list], (list) => list.includes('y')],
      }),
    ])
    logic.mount()

    expect(logic.values.mapA).toEqual(1)
    expect(logic.values.setHasA).toEqual(true)
    expect(logic.values.hasY).toEqual(true)

    const health = () => logic.selectorHealth().selectors
    expect(health().mapA.dependencies).toEqual(['map.map:a'])
    expect(health().setHasA.dependencies).toEqual(['set.set:a'])
    expect(health().hasY.dependencies).toEqual(['list.0', 'list.1'])

    logic.actions.setMap('b', 3)
    expect(logic.values.mapA).toEqual(1)
    expect(health().mapA.evaluations).toEqual(1)
    logic.actions.setMap('a', 5)
    expect(logic.values.mapA).toEqual(5)
    expect(health().mapA.evaluations).toEqual(2)
    expect(health().mapA.dirtyCause).toEqual('map.map:a')

    logic.actions.addToSet('b')
    expect(logic.values.setHasA).toEqual(true)
    expect(health().setHasA.evaluations).toEqual(1)

    logic.actions.setList(['x', 'y', 'q'])
    expect(logic.values.hasY).toEqual(true)
    expect(health().hasY.evaluations).toEqual(1)
    logic.actions.setList(['x', 'w', 'q'])
    expect(logic.values.hasY).toEqual(false)
    expect(health().hasY.evaluations).toEqual(2)
    expect(health().hasY.dirtyCause).toEqual('list.1')
    // not found: depends on every element and the length
    logic.actions.setList(['x', 'w', 'q', 'y'])
    expect(logic.values.hasY).toEqual(true)
  })

  test('includes compares against raw objects', () => {
    const item = { id: 1 }
    const logic = kea([
      reducers({ list: [[item, { id: 2 }], {}], selected: [item, {}] }),
      selectors({ isSelectedListed: [(s) => [s.list, s.selected], (list, selected) => list.includes(selected)] }),
    ])
    logic.mount()
    expect(logic.values.isSelectedListed).toEqual(true)
  })

  test('multi-level chains only propagate to affected selectors', () => {
    const logic = kea([
      ...userLogicInput(),
      selectors({
        userName: [(s) => [s.user], (user) => user.name],
        userAge: [(s) => [s.user], (user) => user.age],
        greeting: [(s) => [s.userName], (userName) => `hi ${userName}`],
        shout: [(s) => [s.greeting], (greeting) => greeting.toUpperCase()],
      }),
    ])
    logic.mount()
    expect(logic.values.shout).toEqual('HI BOB')
    expect(logic.values.userAge).toEqual(30)

    logic.actions.setAge(50)
    expect(logic.values.shout).toEqual('HI BOB')
    expect(logic.values.userAge).toEqual(50)
    let health = logic.selectorHealth()
    expect(health.selectors.userName.evaluations).toEqual(1)
    expect(health.selectors.greeting.evaluations).toEqual(1)
    expect(health.selectors.shout.evaluations).toEqual(1)
    expect(health.selectors.userAge.evaluations).toEqual(2)

    logic.actions.setName('alice')
    health = logic.selectorHealth()
    // flagged right after the dispatch, before anything is read
    expect(health.selectors.userName.dirtyCause).toEqual('user.name')
    expect(health.selectors.greeting.dirtyCause).toEqual('selector:userName')
    expect(health.selectors.shout.dirtyCause).toEqual('selector:greeting')

    expect(logic.values.shout).toEqual('HI ALICE')
    health = logic.selectorHealth()
    expect(health.selectors.greeting.dependencies).toEqual(['userName'])
    expect(health.selectors.userName.dependents).toEqual(['greeting'])
    expect(health.selectors.greeting.dependents).toEqual(['shout'])
    expect(health.selectors.shout.evaluations).toEqual(2)
    expect(health.topologicalOrder).toEqual(['userName', 'userAge', 'greeting', 'shout'])
  })

  test('topological order follows dependencies, not definition order', () => {
    const logic = kea([
      reducers({ a: [1, {}] }),
      selectors(({ selectors }) => ({
        last: [() => [selectors.middle], (m) => m + 1],
        middle: [() => [selectors.first], (f) => f + 1],
        first: [() => [selectors.a], (a) => a + 1],
      })),
    ])
    logic.mount()
    expect(logic.values.last).toEqual(4)
    expect(logic.selectorHealth().topologicalOrder).toEqual(['first', 'middle', 'last'])
  })

  test('multiple changes in one action evaluate once', () => {
    const logic = kea([
      ...userLogicInput(),
      selectors({ summary: [(s) => [s.user], (user) => `${user.name} ${user.age}`] }),
    ])
    logic.mount()
    expect(logic.values.summary).toEqual('bob 30')
    logic.actions.setBoth('alice', 20)
    expect(logic.values.summary).toEqual('alice 20')
    expect(logic.values.summary).toEqual('alice 20')
    expect(logic.selectorHealth().selectors.summary.evaluations).toEqual(2)
  })

  test('props and custom memoization still work', () => {
    const logic = kea([
      props({ id: 1 }),
      actions({ add: (value) => ({ value }) }),
      reducers({ values: [[], { add: (state, { value }) => [...state, value] }] }),
      selectors({
        withId: [(s, p) => [s.values, p.id], (values, id) => `${id}:${values.length}`],
        stable: [(s) => [s.values], (values) => values.map((v) => v), { resultEqualityCheck: () => true }],
      }),
    ])
    logic.mount()
    const stable = logic.values.stable
    expect(logic.values.withId).toEqual('1:0')
    logic.actions.add('a')
    expect(logic.values.withId).toEqual('1:1')
    expect(logic.values.stable).toBe(stable)
  })

  test('circular dependencies throw while building', () => {
    const logic = kea([
      reducers({ a: [1, {}] }),
      selectors(({ selectors }) => ({
        x: [() => [selectors.y], (y) => y],
        y: [() => [selectors.x], (x) => x],
      })),
    ])
    expect(() => logic.mount()).toThrow('[KEA] Circular dependency detected')

    const selfLogic = kea([selectors({ me: [(s) => [s.me], (me) => me] })])
    expect(() => selfLogic.build()).toThrow('[KEA] Circular dependency detected')
  })

  test('identity survives the build-time wrapping of selectors', () => {
    const logic = kea([
      ...userLogicInput(),
      selectors({ userName: [(s) => [s.user], (user) => user.name] }),
      selectors({ upperName: [(s) => [s.userName], (name) => name.toUpperCase()] }),
    ])
    logic.mount()
    expect(logic.values.upperName).toEqual('BOB')
    const health = logic.selectorHealth()
    expect(health.selectors.upperName.dependencies).toEqual(['userName'])
    expect(health.selectors.userName.dependents).toEqual(['upperName'])
  })

  test('plugin events and mount order are unchanged', () => {
    const events = []
    activatePlugin({
      name: 'test',
      events: {
        beforeMount: (logic) => events.push(`plugin beforeMount ${logic.pathString}`),
        afterMount: (logic) => events.push(`plugin afterMount ${logic.pathString}`),
      },
    })
    expect(getContext().plugins.activated.map((p) => p.name)).toEqual([corePlugin.name, 'test'])
    const logic = kea([
      path(['a']),
      ...userLogicInput(),
      selectors({ userName: [(s) => [s.user], (user) => user.name] }),
      afterMount(({ values }) => events.push(`logic afterMount ${values.userName}`)),
    ])
    logic.mount()
    expect(events).toEqual(['plugin beforeMount a', 'plugin afterMount a', 'logic afterMount bob'])
  })

  test('components only re-render when what they read changes', async () => {
    const logic = kea([
      ...userLogicInput(),
      selectors({ userInfo: [(s) => [s.user], (user) => ({ name: user.name })] }),
    ])
    let renders = 0
    function Component() {
      const { userInfo } = useValues(logic)
      renders += 1
      return <div data-testid="name">{userInfo.name}</div>
    }
    render(<Component />)
    expect(screen.getByTestId('name')).toHaveTextContent('bob')
    const initialRenders = renders

    act(() => logic.actions.setOther(5))
    act(() => logic.actions.setAge(99))
    expect(renders).toEqual(initialRenders)

    act(() => logic.actions.setName('alice'))
    expect(screen.getByTestId('name')).toHaveTextContent('alice')
    expect(renders).toEqual(initialRenders + 1)
  })
})
