import React from 'react'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { kea, resetContext, useValues, useActions } from '../../src'

describe('atomic selectors', () => {
  beforeEach(() => {
    resetContext({ createStore: true, atomicSelectors: true })
  })

  function counterLogic() {
    const calls = { userName: 0, label: 0, ageLabel: 0 }
    const logic = kea([
      {
        actions: {
          setName: (name) => ({ name }),
          setAge: (age) => ({ age }),
          setBoth: (name, age) => ({ name, age }),
        },
        reducers: ({ actions }) => ({
          user: [
            { name: 'Ada', age: 30 },
            {
              [actions.setName]: (state, { name }) => ({ ...state, name }),
              [actions.setAge]: (state, { age }) => ({ ...state, age }),
              [actions.setBoth]: (_, { name, age }) => ({ name, age }),
            },
          ],
        }),
        selectors: ({ selectors }) => ({
          userName: [
            () => [selectors.user],
            (user) => {
              calls.userName += 1
              return user.name
            },
          ],
          ageLabel: [
            () => [selectors.user],
            (user) => {
              calls.ageLabel += 1
              return user.age
            },
          ],
          label: [
            () => [selectors.userName],
            (userName) => {
              calls.label += 1
              return `Name:${userName}`
            },
          ],
        }),
      },
    ])
    return { logic, calls }
  }

  test('selectorHealth is absent unless atomicSelectors is enabled', () => {
    resetContext({ createStore: true })
    const { logic } = counterLogic()
    logic.mount()
    expect(logic.selectorHealth).toBeUndefined()
  })

  test('tracks leaf dependencies and skips unrelated field changes', () => {
    const { logic, calls } = counterLogic()
    logic.mount()

    expect(logic.values.userName).toEqual('Ada')
    expect(logic.values.label).toEqual('Name:Ada')
    expect(calls.userName).toEqual(1)
    expect(calls.label).toEqual(1)

    const health = logic.selectorHealth()
    expect(health.selectors.userName.dependencies).toEqual(['user.name'])
    expect(health.selectors.userName.dependents).toEqual(['label'])
    expect(health.selectors.label.dependencies).toEqual(['userName'])
    expect(health.selectors.label.dependents).toEqual([])
    expect(health.selectors.userName.dirtyCause).toBeNull()
    expect(health.topologicalOrder.indexOf('userName')).toBeLessThan(health.topologicalOrder.indexOf('label'))

    logic.actions.setAge(31)
    expect(calls.userName).toEqual(1)
    expect(calls.label).toEqual(1)
    expect(logic.values.userName).toEqual('Ada')
    expect(logic.selectorHealth().selectors.userName.evaluations).toEqual(1)

    logic.actions.setName('Grace')
    expect(calls.userName).toEqual(2)
    expect(calls.label).toEqual(2)
    expect(logic.values.label).toEqual('Name:Grace')
    expect(logic.selectorHealth().selectors.userName.dirtyCause).toEqual('user.name')
    expect(logic.selectorHealth().selectors.label.dirtyCause).toEqual('selector:userName')
    expect(logic.selectorHealth().selectors.ageLabel.evaluations).toEqual(0)
  })

  test('re-evaluates a selector once when several leaves change in one action', () => {
    const { logic, calls } = counterLogic()
    logic.mount()
    expect(logic.values.userName).toEqual('Ada')
    expect(logic.values.ageLabel).toEqual(30)

    const both = kea([
      {
        actions: { setBoth: (name, age) => ({ name, age }) },
        reducers: ({ actions }) => ({
          user: [{ name: 'Ada', age: 30 }, { [actions.setBoth]: (_, { name, age }) => ({ name, age }) }],
        }),
        selectors: ({ selectors }) => ({
          card: [
            () => [selectors.user],
            (user) => {
              calls.userName += 1
              return `${user.name}:${user.age}`
            },
          ],
        }),
      },
    ])
    calls.userName = 0
    both.mount()
    expect(both.values.card).toEqual('Ada:30')
    expect(calls.userName).toEqual(1)
    both.actions.setBoth('Grace', 40)
    expect(calls.userName).toEqual(2)
    expect(both.values.card).toEqual('Grace:40')
    expect(both.selectorHealth().selectors.card.dependencies.sort()).toEqual(['user.age', 'user.name'])
    expect(both.selectorHealth().selectors.card.evaluations).toEqual(2)
  })

  test('tracks map keys, set membership, and array elements that were checked', () => {
    let reads = 0
    const logic = kea([
      {
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
          list: [['a', 'b', 'c'], { [actions.setList]: (_, { list }) => list }],
        }),
        selectors: ({ selectors }) => ({
          itemA: [() => [selectors.data], (data) => data.get('a')],
          hasA: [() => [selectors.tags], (tags) => tags.has('a')],
          hasX: [
            () => [selectors.list],
            (list) => {
              reads += 1
              return list.includes('b')
            },
          ],
        }),
      },
    ])
    logic.mount()
    expect(logic.values.itemA).toEqual(1)
    expect(logic.values.hasA).toEqual(true)
    expect(logic.values.hasX).toEqual(true)
    expect(logic.selectorHealth().selectors.itemA.dependencies).toEqual(['data.map:a'])
    expect(logic.selectorHealth().selectors.hasA.dependencies).toEqual(['tags.set:a'])
    expect(logic.selectorHealth().selectors.hasX.dependencies).toEqual(['list.0', 'list.1'])

    reads = logic.selectorHealth().selectors.hasX.evaluations
    logic.actions.setData(
      new Map([
        ['a', 1],
        ['b', 5],
      ]),
    )
    expect(logic.selectorHealth().selectors.itemA.evaluations).toEqual(1)
    logic.actions.setData(
      new Map([
        ['a', 9],
        ['b', 5],
      ]),
    )
    expect(logic.values.itemA).toEqual(9)
    expect(logic.selectorHealth().selectors.itemA.dirtyCause).toEqual('data.map:a')

    logic.actions.setTags(new Set(['b', 'c']))
    expect(logic.values.hasA).toEqual(false)
    expect(logic.selectorHealth().selectors.hasA.dirtyCause).toEqual('tags.set:a')

    const before = logic.selectorHealth().selectors.hasX.evaluations
    logic.actions.setList(['a', 'b', 'z'])
    expect(logic.selectorHealth().selectors.hasX.evaluations).toEqual(before)
    logic.actions.setList(['a', 'z', 'c'])
    expect(logic.values.hasX).toEqual(false)
    expect(logic.selectorHealth().selectors.hasX.evaluations).toEqual(before + 1)
  })

  test('throws when selectors form a cycle during build', () => {
    expect(() => {
      kea([
        {
          selectors: ({ selectors }) => ({
            a: [() => [selectors.b], (b) => b],
            b: [() => [selectors.a], (a) => a],
          }),
        },
      ]).mount()
    }).toThrow('[KEA] Circular dependency detected')
  })

  test('does not reorder mount events', () => {
    const seen = []
    const plugin = {
      name: 'atomic-order-probe',
      events: {
        afterMount() {
          seen.push('plugin.afterMount')
        },
      },
    }
    const { activatePlugin } = require('../../src')
    activatePlugin(plugin)

    const logic = kea({
      reducers: () => ({
        value: [1],
      }),
      events: () => ({
        afterMount() {
          seen.push('logic.afterMount')
        },
      }),
    })
    logic.mount()
    expect(seen).toEqual(['plugin.afterMount', 'logic.afterMount'])
  })

  test('react subscribers rerender only for the selectors they read', () => {
    const logic = kea([
      {
        actions: {
          setName: (name) => ({ name }),
          setAge: (age) => ({ age }),
        },
        reducers: ({ actions }) => ({
          user: [
            { name: 'Ada', age: 30 },
            {
              [actions.setName]: (state, { name }) => ({ ...state, name }),
              [actions.setAge]: (state, { age }) => ({ ...state, age }),
            },
          ],
        }),
        selectors: ({ selectors }) => ({
          userName: [() => [selectors.user], (user) => user.name],
        }),
      },
    ])

    let renders = 0
    function Screen() {
      const { userName } = useValues(logic)
      const { setName, setAge } = useActions(logic)
      renders += 1
      return (
        <div>
          <span data-testid="name">{userName}</span>
          <button data-testid="age" onClick={() => setAge(99)}>
            age
          </button>
          <button data-testid="rename" onClick={() => setName('Grace')}>
            rename
          </button>
        </div>
      )
    }

    render(<Screen />)
    expect(screen.getByTestId('name').textContent).toEqual('Ada')
    const afterMount = renders

    act(() => {
      fireEvent.click(screen.getByTestId('age'))
    })
    expect(renders).toEqual(afterMount)
    expect(screen.getByTestId('name').textContent).toEqual('Ada')

    act(() => {
      fireEvent.click(screen.getByTestId('rename'))
    })
    expect(screen.getByTestId('name').textContent).toEqual('Grace')
    expect(renders).toBeGreaterThan(afterMount)
  })

  test('an unused sibling field on the same reducer does not rerender', () => {
    const logic = kea([
      {
        actions: {
          setName: (name) => ({ name }),
          setOther: (other) => ({ other }),
        },
        reducers: ({ actions }) => ({
          user: [
            { name: 'Ada', age: 1 },
            {
              [actions.setName]: (state, { name }) => ({ ...state, name }),
            },
          ],
          other: [0, { [actions.setOther]: (_, { other }) => other }],
        }),
        selectors: ({ selectors }) => ({
          userName: [() => [selectors.user], (user) => user.name],
        }),
      },
    ])

    let renders = 0
    function Screen() {
      const { userName } = useValues(logic)
      const { setOther } = useActions(logic)
      renders += 1
      return (
        <div>
          <span data-testid="name">{userName}</span>
          <button data-testid="other" onClick={() => setOther(1)}>
            other
          </button>
        </div>
      )
    }

    render(<Screen />)
    const baseline = renders
    act(() => {
      fireEvent.click(screen.getByTestId('other'))
    })
    expect(renders).toEqual(baseline)
    expect(screen.getByTestId('name').textContent).toEqual('Ada')
  })
})
