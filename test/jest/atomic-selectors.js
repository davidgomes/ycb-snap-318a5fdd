import { kea, resetContext, getContext, activatePlugin } from '../../src'
import React from 'react'
import { render, screen, act } from '@testing-library/react'
import { useValues, useActions } from '../../src'

describe('atomic selectors', () => {
  beforeEach(() => {
    resetContext({ createStore: true })
  })

  test('selectorHealth is absent unless atomicSelectors is enabled', () => {
    const logic = kea({
      reducers: () => ({
        count: [0, {}],
      }),
    })
    logic.mount()
    expect(getContext().options.atomicSelectors).toBe(false)
    expect(logic.selectorHealth).toBeUndefined()
  })

  test('tracks leaf dependencies and skips unrelated updates', () => {
    resetContext({ createStore: true, atomicSelectors: true })
    const logic = kea({
      actions: {
        setName: (name) => ({ name }),
        setAge: (age) => ({ age }),
        setBoth: (name, age) => ({ name, age }),
      },
      reducers: ({ actions }) => ({
        user: [
          { name: 'Ada', age: 1 },
          {
            [actions.setName]: (state, { name }) => ({ ...state, name }),
            [actions.setAge]: (state, { age }) => ({ ...state, age }),
            [actions.setBoth]: (_, { name, age }) => ({ name, age }),
          },
        ],
      }),
      selectors: ({ selectors }) => ({
        userName: [(s) => [s.user], (user) => user.name],
        greeting: [(s) => [s.userName], (userName) => `Hi ${userName}`],
        description: [(s) => [s.user], (user) => `${user.name}:${user.age}`],
      }),
    })
    logic.mount()

    expect(logic.values.userName).toBe('Ada')
    expect(logic.values.greeting).toBe('Hi Ada')
    expect(logic.values.description).toBe('Ada:1')

    let health = logic.selectorHealth()
    expect(health.selectors.userName.dependencies).toEqual(['user.name'])
    expect(health.selectors.userName.evaluations).toBe(1)
    expect(health.selectors.userName.dirtyCause).toBeNull()
    expect(health.selectors.greeting.dependencies).toEqual(['userName'])
    expect(health.selectors.greeting.evaluations).toBe(1)
    expect(health.selectors.userName.dependents).toEqual(['greeting'])
    expect(health.selectors.description.dependencies).toEqual(['user.name', 'user.age'])
    expect(health.topologicalOrder.indexOf('user')).toBeLessThan(health.topologicalOrder.indexOf('userName'))
    expect(health.topologicalOrder.indexOf('userName')).toBeLessThan(health.topologicalOrder.indexOf('greeting'))

    logic.actions.setAge(2)
    expect(logic.values.userName).toBe('Ada')
    expect(logic.values.greeting).toBe('Hi Ada')
    health = logic.selectorHealth()
    expect(health.selectors.userName.evaluations).toBe(1)
    expect(health.selectors.greeting.evaluations).toBe(1)
    expect(health.selectors.description.evaluations).toBe(2)
    expect(health.selectors.description.dirtyCause).toBe('user.age')
    expect(logic.values.description).toBe('Ada:2')

    logic.actions.setName('Bea')
    expect(logic.values.userName).toBe('Bea')
    expect(logic.values.greeting).toBe('Hi Bea')
    health = logic.selectorHealth()
    expect(health.selectors.userName.evaluations).toBe(2)
    expect(health.selectors.userName.dirtyCause).toBe('user.name')
    expect(health.selectors.greeting.evaluations).toBe(2)
    expect(health.selectors.greeting.dirtyCause).toBe('selector:userName')

    const before = logic.selectorHealth().selectors.description.evaluations
    logic.actions.setBoth('Cleo', 3)
    expect(logic.values.description).toBe('Cleo:3')
    expect(logic.selectorHealth().selectors.description.evaluations).toBe(before + 1)
  })

  test('tracks map, set, and array access', () => {
    resetContext({ createStore: true, atomicSelectors: true })
    const logic = kea({
      actions: {
        setData: (data) => ({ data }),
        setTags: (tags) => ({ tags }),
        setList: (list) => ({ list }),
      },
      reducers: ({ actions }) => ({
        data: [
          new Map([
            ['a', 1],
            ['b', 2],
          ]),
          { [actions.setData]: (_, { data }) => data },
        ],
        tags: [new Set(['a', 'b']), { [actions.setTags]: (_, { tags }) => tags }],
        list: [['x', 'y', 'z'], { [actions.setList]: (_, { list }) => list }],
      }),
      selectors: ({ selectors }) => ({
        valueA: [(s) => [s.data], (data) => data.get('a')],
        hasA: [(s) => [s.tags], (tags) => tags.has('a')],
        first: [(s) => [s.list], (list) => list[0]],
        hasX: [(s) => [s.list], (list) => list.includes('x')],
        hasZ: [(s) => [s.list], (list) => list.includes('missing')],
      }),
    })
    logic.mount()

    expect(logic.values.valueA).toBe(1)
    expect(logic.values.hasA).toBe(true)
    expect(logic.values.first).toBe('x')
    expect(logic.values.hasX).toBe(true)
    expect(logic.values.hasZ).toBe(false)

    const health = logic.selectorHealth()
    expect(health.selectors.valueA.dependencies).toEqual(['data.map:a'])
    expect(health.selectors.hasA.dependencies).toEqual(['tags.set:a'])
    expect(health.selectors.first.dependencies).toEqual(['list.0'])
    expect(health.selectors.hasX.dependencies).toEqual(['list.0'])
    expect(health.selectors.hasZ.dependencies).toEqual(['list.0', 'list.1', 'list.2'])

    logic.actions.setData(
      new Map([
        ['a', 1],
        ['b', 9],
      ]),
    )
    expect(logic.values.valueA).toBe(1)
    expect(logic.selectorHealth().selectors.valueA.evaluations).toBe(1)

    logic.actions.setData(
      new Map([
        ['a', 4],
        ['b', 9],
      ]),
    )
    expect(logic.values.valueA).toBe(4)
    expect(logic.selectorHealth().selectors.valueA.evaluations).toBe(2)
    expect(logic.selectorHealth().selectors.valueA.dirtyCause).toBe('data.map:a')

    logic.actions.setList(['x', 'changed', 'z'])
    expect(logic.values.hasX).toBe(true)
    expect(logic.selectorHealth().selectors.hasX.evaluations).toBe(1)
    expect(logic.values.first).toBe('x')
    expect(logic.selectorHealth().selectors.first.evaluations).toBe(1)
    // includes('missing') had read index 1, so that selector runs again
    expect(logic.selectorHealth().selectors.hasZ.evaluations).toBe(2)

    logic.actions.setList(['nope', 'y', 'z'])
    expect(logic.values.hasX).toBe(false)
    expect(logic.selectorHealth().selectors.hasX.evaluations).toBe(2)
    expect(logic.values.hasZ).toBe(false)
    expect(logic.selectorHealth().selectors.hasZ.evaluations).toBe(3)
  })

  test('throws when selectors form a cycle during build', () => {
    resetContext({ createStore: true, atomicSelectors: true })
    const logic = kea({
      selectors: () => ({
        a: [(s) => [s.b], (b) => b],
        b: [(s) => [s.c], (c) => c],
        c: [(s) => [s.a], (a) => a],
      }),
    })
    expect(() => logic.mount()).toThrow('[KEA] Circular dependency detected')
  })

  test('does not disturb mount event ordering', () => {
    resetContext({ createStore: true, atomicSelectors: true })
    const actions = []
    activatePlugin({
      name: 'order-probe',
      events: {
        beforeMount() {
          actions.push('plugin.beforeMount')
        },
        afterMount() {
          actions.push('plugin.afterMount')
        },
      },
    })
    const connectedLogic = kea({
      reducers: () => ({
        value: [true],
      }),
      events: () => ({
        beforeMount() {
          actions.push('connectedLogic.beforeMount')
        },
        afterMount() {
          actions.push('connectedLogic.afterMount')
        },
      }),
    })
    const logic = kea({
      connect: {
        values: [connectedLogic, ['value']],
      },
      events: () => ({
        beforeMount() {
          actions.push('logic.beforeMount')
        },
        afterMount() {
          actions.push('logic.afterMount')
        },
      }),
    })
    logic.mount()
    expect(actions).toEqual([
      'plugin.beforeMount',
      'connectedLogic.beforeMount',
      'plugin.afterMount',
      'connectedLogic.afterMount',
      'plugin.beforeMount',
      'logic.beforeMount',
      'plugin.afterMount',
      'logic.afterMount',
    ])
    expect(logic.values.value).toBe(true)
  })

  test('tracks nested leaves and dynamic indexes', () => {
    resetContext({ createStore: true, atomicSelectors: true })
    const logic = kea({
      actions: {
        setCity: (city) => ({ city }),
        setId: (id) => ({ id }),
        setTitle: (title) => ({ title }),
      },
      reducers: ({ actions }) => ({
        user: [
          { name: 'Ada', address: { city: 'London' } },
          {
            [actions.setCity]: (state, { city }) => ({ ...state, address: { ...state.address, city } }),
          },
        ],
        bookId: [1, { [actions.setId]: (_, { id }) => id }],
        books: [{ 1: 'one', 2: 'two' }, { [actions.setTitle]: (state, { title }) => ({ ...state, 2: title }) }],
      }),
      selectors: ({ selectors }) => ({
        city: [(s) => [s.user], (user) => user.address.city],
        cityFromInput: [() => [(state) => selectors.user(state).address.city], (city) => city],
        book: [(s) => [s.books, s.bookId], (books, bookId) => books[bookId]],
      }),
    })
    logic.mount()
    expect(logic.values.city).toBe('London')
    expect(logic.values.cityFromInput).toBe('London')
    expect(logic.values.book).toBe('one')
    expect(logic.selectorHealth().selectors.city.dependencies).toEqual(['user.address.city'])
    expect(logic.selectorHealth().selectors.cityFromInput.dependencies).toEqual(['user.address.city'])
    expect(logic.selectorHealth().selectors.book.dependencies).toEqual(['books.1', 'bookId'])

    logic.actions.setTitle('TWO')
    expect(logic.values.book).toBe('one')
    expect(logic.selectorHealth().selectors.book.evaluations).toBe(1)

    logic.actions.setId(2)
    expect(logic.values.book).toBe('TWO')
    expect(logic.selectorHealth().selectors.book.evaluations).toBe(2)
    expect(logic.selectorHealth().selectors.book.dirtyCause).toBe('bookId')
    expect(logic.selectorHealth().selectors.city.evaluations).toBe(1)
  })

  test('react subscribers rerender only for accessed values', () => {
    resetContext({ createStore: true, atomicSelectors: true })
    const logic = kea({
      actions: {
        setName: (name) => ({ name }),
        setAge: (age) => ({ age }),
      },
      reducers: ({ actions }) => ({
        user: [
          { name: 'Ada', age: 1 },
          {
            [actions.setName]: (state, { name }) => ({ ...state, name }),
            [actions.setAge]: (state, { age }) => ({ ...state, age }),
          },
        ],
      }),
      selectors: ({ selectors }) => ({
        userName: [(s) => [s.user], (user) => ({ name: user.name })],
      }),
    })

    let renders = 0
    function Sample() {
      const { userName } = useValues(logic)
      const { setName } = useActions(logic)
      renders += 1
      return (
        <div>
          <span data-testid="name">{userName.name}</span>
          <button data-testid="rename" onClick={() => setName('Bea')} />
        </div>
      )
    }

    render(<Sample />)
    expect(screen.getByTestId('name')).toHaveTextContent('Ada')
    expect(renders).toBe(1)

    act(() => {
      logic.actions.setAge(4)
    })
    expect(renders).toBe(1)
    expect(screen.getByTestId('name')).toHaveTextContent('Ada')

    act(() => {
      logic.actions.setName('Bea')
    })
    expect(renders).toBe(2)
    expect(screen.getByTestId('name')).toHaveTextContent('Bea')
  })
})
