import { kea, resetContext, useValues } from '../../src'
import React from 'react'
import { render, act } from '@testing-library/react'

describe('atomic selectors', () => {
  beforeEach(() => {
    resetContext({ createStore: true, atomicSelectors: true })
  })

  test('selectorHealth is absent unless enabled', () => {
    resetContext({ createStore: true })
    const logic = kea({
      reducers: () => ({
        user: [{ name: 'Ada', age: 1 }, {}],
      }),
    })
    logic.mount()
    expect(logic.selectorHealth).toBeUndefined()
  })

  test('tracks leaf paths and skips unrelated changes', () => {
    let nameRuns = 0
    let ageRuns = 0
    let labelRuns = 0

    const logic = kea({
      actions: () => ({
        setUser: (user) => ({ user }),
      }),
      reducers: ({ actions }) => ({
        user: [
          { name: 'Ada', age: 1 },
          {
            [actions.setUser]: (_, { user }) => user,
          },
        ],
      }),
      selectors: ({ selectors }) => ({
        userName: [
          () => [selectors.user],
          (user) => {
            nameRuns += 1
            return user.name
          },
        ],
        userAge: [
          () => [selectors.user],
          (user) => {
            ageRuns += 1
            return user.age
          },
        ],
        label: [
          () => [selectors.userName],
          (userName) => {
            labelRuns += 1
            return `name:${userName}`
          },
        ],
      }),
    })

    logic.mount()
    expect(logic.values.userName).toEqual('Ada')
    expect(logic.values.userAge).toEqual(1)
    expect(logic.values.label).toEqual('name:Ada')
    expect(nameRuns).toEqual(1)
    expect(ageRuns).toEqual(1)
    expect(labelRuns).toEqual(1)

    const health = logic.selectorHealth()
    expect(health.selectors.userName.dependencies).toEqual(['user.name'])
    expect(health.selectors.userAge.dependencies).toEqual(['user.age'])
    expect(health.selectors.label.dependencies).toEqual(['userName'])
    expect(health.selectors.user.dependents).toEqual(expect.arrayContaining(['userName', 'userAge']))
    expect(health.selectors.userName.dependents).toEqual(['label'])
    expect(health.selectors.userName.evaluations).toEqual(1)
    expect(health.selectors.userName.dirtyCause).toBeNull()
    expect(health.topologicalOrder.indexOf('user')).toBeLessThan(health.topologicalOrder.indexOf('userName'))
    expect(health.topologicalOrder.indexOf('userName')).toBeLessThan(health.topologicalOrder.indexOf('label'))

    logic.actions.setUser({ name: 'Ada', age: 2 })
    expect(logic.values.userName).toEqual('Ada')
    expect(logic.values.userAge).toEqual(2)
    expect(logic.values.label).toEqual('name:Ada')
    expect(nameRuns).toEqual(1)
    expect(ageRuns).toEqual(2)
    expect(labelRuns).toEqual(1)
    expect(logic.selectorHealth().selectors.userAge.dirtyCause).toEqual('user.age')
    expect(logic.selectorHealth().selectors.userName.dirtyCause).toBeNull()

    logic.actions.setUser({ name: 'Bea', age: 2 })
    expect(logic.values.label).toEqual('name:Bea')
    expect(nameRuns).toEqual(2)
    expect(labelRuns).toEqual(2)
    expect(ageRuns).toEqual(2)
    expect(logic.selectorHealth().selectors.label.dirtyCause).toEqual('selector:userName')
    expect(logic.selectorHealth().selectors.userName.dirtyCause).toEqual('user.name')
  })

  test('one action recomputes a selector once', () => {
    let runs = 0
    const logic = kea({
      actions: () => ({
        setUser: (user) => ({ user }),
      }),
      reducers: ({ actions }) => ({
        user: [{ name: 'Ada', age: 1 }, { [actions.setUser]: (_, { user }) => user }],
      }),
      selectors: ({ selectors }) => ({
        summary: [
          () => [selectors.user],
          (user) => {
            runs += 1
            return `${user.name}:${user.age}`
          },
        ],
      }),
    })
    logic.mount()
    expect(logic.values.summary).toEqual('Ada:1')
    logic.actions.setUser({ name: 'Bea', age: 4 })
    expect(logic.values.summary).toEqual('Bea:4')
    expect(logic.values.summary).toEqual('Bea:4')
    expect(runs).toEqual(2)
    expect(logic.selectorHealth().selectors.summary.evaluations).toEqual(2)
    expect(logic.selectorHealth().selectors.summary.dependencies).toEqual(['user.name', 'user.age'])
  })

  test('tracks map, set, and array access', () => {
    const logic = kea({
      actions: () => ({
        setData: (data) => ({ data }),
        setFlags: (flags) => ({ flags }),
        setList: (list) => ({ list }),
      }),
      reducers: ({ actions }) => ({
        data: [new Map([['a', 1], ['b', 2]]), { [actions.setData]: (_, { data }) => data }],
        flags: [new Set(['a', 'b']), { [actions.setFlags]: (_, { flags }) => flags }],
        list: [['x', 'y'], { [actions.setList]: (_, { list }) => list }],
      }),
      selectors: ({ selectors }) => ({
        item: [() => [selectors.data], (data) => data.get('a')],
        hasA: [() => [selectors.flags], (flags) => flags.has('a')],
        hasX: [() => [selectors.list], (list) => list.includes('x')],
        first: [() => [selectors.list], (list) => list[0]],
      }),
    })
    logic.mount()
    expect(logic.values.item).toEqual(1)
    expect(logic.values.hasA).toEqual(true)
    expect(logic.values.hasX).toEqual(true)
    expect(logic.values.first).toEqual('x')
    const health = logic.selectorHealth()
    expect(health.selectors.item.dependencies).toEqual(['data.map:a'])
    expect(health.selectors.hasA.dependencies).toEqual(['flags.set:a'])
    expect(health.selectors.hasX.dependencies).toContain('list.0')
    expect(health.selectors.first.dependencies).toEqual(['list.0'])

    let itemRuns = health.selectors.item.evaluations
    logic.actions.setData(new Map([['a', 1], ['b', 9]]))
    expect(logic.values.item).toEqual(1)
    expect(logic.selectorHealth().selectors.item.evaluations).toEqual(itemRuns)

    logic.actions.setData(new Map([['a', 3], ['b', 9]]))
    expect(logic.values.item).toEqual(3)
    expect(logic.selectorHealth().selectors.item.evaluations).toEqual(itemRuns + 1)
    expect(logic.selectorHealth().selectors.item.dirtyCause).toEqual('data.map:a')
  })

  test('throws on circular selector inputs while building', () => {
    const logic = kea({
      selectors: ({ selectors }) => ({
        a: [() => [selectors.b], (b) => b],
        b: [() => [selectors.a], (a) => a],
      }),
    })
    expect(() => logic.mount()).toThrow('[KEA] Circular dependency detected')
  })

  test('components rerender only for accessed selectors', () => {
    const logic = kea({
      actions: () => ({
        setUser: (user) => ({ user }),
      }),
      reducers: ({ actions }) => ({
        user: [{ name: 'Ada', age: 1 }, { [actions.setUser]: (_, { user }) => user }],
      }),
      selectors: ({ selectors }) => ({
        userName: [() => [selectors.user], (user) => user.name],
        userAge: [() => [selectors.user], (user) => user.age],
      }),
    })

    let renders = 0
    function View() {
      renders += 1
      const { userName } = useValues(logic)
      return React.createElement('div', null, userName)
    }

    const view = render(React.createElement(View))
    expect(view.container.textContent).toEqual('Ada')
    const afterMount = renders

    act(() => {
      logic.actions.setUser({ name: 'Ada', age: 9 })
    })
    expect(renders).toEqual(afterMount)

    act(() => {
      logic.actions.setUser({ name: 'Bea', age: 9 })
    })
    expect(view.container.textContent).toEqual('Bea')
    expect(renders).toBeGreaterThan(afterMount)
  })
})
