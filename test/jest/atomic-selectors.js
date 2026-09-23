import { kea, resetContext, getContext } from '../../src'
import React from 'react'
import { render, act } from '@testing-library/react'
import { useValues } from '../../src'

describe('atomic selectors', () => {
  beforeEach(() => {
    resetContext({ atomicSelectors: true })
  })

  test('selectorHealth is absent unless atomicSelectors is enabled', () => {
    resetContext()
    const logic = kea({
      reducers: { count: [1] },
      selectors: { double: [(s) => [s.count], (count) => count * 2] },
    })
    logic.mount()
    expect(logic.selectorHealth).toBeUndefined()
    expect(logic.build().selectorHealth).toBeUndefined()
  })

  test('tracks object leaves and ignores sibling fields', () => {
    let userNameRuns = 0
    let userAgeRuns = 0
    let greetingRuns = 0
    const logic = kea({
      actions: {
        setName: (name) => ({ name }),
        setAge: (age) => ({ age }),
        setUser: (name, age) => ({ name, age }),
      },
      reducers: ({ actions }) => ({
        user: [
          { name: 'Ada', age: 36 },
          {
            [actions.setName]: (state, { name }) => ({ ...state, name }),
            [actions.setAge]: (state, { age }) => ({ ...state, age }),
            [actions.setUser]: (_, { name, age }) => ({ name, age }),
          },
        ],
      }),
      selectors: {
        userName: [
          (s) => [s.user],
          (user) => {
            userNameRuns += 1
            return user.name
          },
        ],
        userAge: [
          (s) => [s.user],
          (user) => {
            userAgeRuns += 1
            return user.age
          },
        ],
        greeting: [
          (s) => [s.userName],
          (userName) => {
            greetingRuns += 1
            return `hi ${userName}`
          },
        ],
      },
    })

    logic.mount()
    expect(logic.values.greeting).toEqual('hi Ada')
    expect(logic.values.userAge).toEqual(36)
    expect(userNameRuns).toEqual(1)
    expect(userAgeRuns).toEqual(1)
    expect(greetingRuns).toEqual(1)

    const health = logic.selectorHealth()
    expect(health.selectors.userName.dependencies).toEqual(['user.name'])
    expect(health.selectors.userAge.dependencies).toEqual(['user.age'])
    expect(health.selectors.greeting.dependencies).toEqual(['userName'])
    expect(health.selectors.userName.dependents).toEqual(['greeting'])
    expect(health.selectors.greeting.dependents).toEqual([])
    expect(health.selectors.userName.evaluations).toEqual(1)
    expect(health.selectors.userName.dirtyCause).toEqual(null)
    expect(health.topologicalOrder).toEqual(['userName', 'userAge', 'greeting'])

    logic.actions.setAge(37)
    expect(logic.values.greeting).toEqual('hi Ada')
    expect(logic.values.userName).toEqual('Ada')
    expect(logic.values.userAge).toEqual(37)
    expect(userNameRuns).toEqual(1)
    expect(greetingRuns).toEqual(1)
    expect(userAgeRuns).toEqual(2)
    expect(logic.selectorHealth().selectors.userAge.dirtyCause).toEqual('user.age')
    expect(logic.selectorHealth().selectors.userName.dirtyCause).toEqual(null)
    expect(logic.selectorHealth().selectors.greeting.evaluations).toEqual(1)

    logic.actions.setName('Bea')
    expect(logic.values.greeting).toEqual('hi Bea')
    expect(userNameRuns).toEqual(2)
    expect(greetingRuns).toEqual(2)
    expect(userAgeRuns).toEqual(2)
    expect(logic.selectorHealth().selectors.userName.dirtyCause).toEqual('user.name')
    expect(logic.selectorHealth().selectors.greeting.dirtyCause).toEqual('selector:userName')

    logic.actions.setUser('Cleo', 40)
    expect(logic.values.greeting).toEqual('hi Cleo')
    expect(logic.values.userAge).toEqual(40)
    expect(userNameRuns).toEqual(3)
    expect(userAgeRuns).toEqual(3)
    expect(greetingRuns).toEqual(3)
  })

  test('one action recomputes a multi-leaf selector once', () => {
    let runs = 0
    const logic = kea({
      actions: {
        setUser: (name, age) => ({ name, age }),
      },
      reducers: ({ actions }) => ({
        user: [
          { name: 'Ada', age: 1 },
          {
            [actions.setUser]: (_, { name, age }) => ({ name, age }),
          },
        ],
      }),
      selectors: {
        label: [
          (s) => [s.user],
          (user) => {
            runs += 1
            return `${user.name}:${user.age}`
          },
        ],
      },
    })
    logic.mount()
    expect(logic.values.label).toEqual('Ada:1')
    logic.actions.setUser('Bea', 2)
    expect(logic.values.label).toEqual('Bea:2')
    expect(runs).toEqual(2)
    expect(logic.selectorHealth().selectors.label.dependencies).toEqual(['user.name', 'user.age'])
    expect(logic.selectorHealth().selectors.label.evaluations).toEqual(2)
  })

  test('tracks map keys, set membership, and array indices', () => {
    let mapRuns = 0
    let setRuns = 0
    let listRuns = 0
    const logic = kea({
      actions: {
        setData: (data) => ({ data }),
        setFlags: (flags) => ({ flags }),
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
        flags: [new Set(['a']), { [actions.setFlags]: (_, { flags }) => flags }],
        list: [['a', 'b', 'c'], { [actions.setList]: (_, { list }) => list }],
      }),
      selectors: {
        itemA: [
          (s) => [s.data],
          (data) => {
            mapRuns += 1
            return data.get('a')
          },
        ],
        hasA: [
          (s) => [s.flags],
          (flags) => {
            setRuns += 1
            return flags.has('a')
          },
        ],
        hasB: [
          (s) => [s.list],
          (list) => {
            listRuns += 1
            return list.includes('b')
          },
        ],
        firstTwo: [(s) => [s.list], (list) => [list[0], list[1]]],
      },
    })

    logic.mount()
    expect(logic.values.itemA).toEqual(1)
    expect(logic.values.hasA).toEqual(true)
    expect(logic.values.hasB).toEqual(true)
    expect(logic.values.firstTwo).toEqual(['a', 'b'])
    expect(logic.selectorHealth().selectors.itemA.dependencies).toEqual(['data.map:a'])
    expect(logic.selectorHealth().selectors.hasA.dependencies).toEqual(['flags.set:a'])
    expect(logic.selectorHealth().selectors.hasB.dependencies).toEqual(['list.0', 'list.1'])
    expect(logic.selectorHealth().selectors.firstTwo.dependencies).toEqual(['list.0', 'list.1'])

    logic.actions.setData(
      new Map([
        ['a', 1],
        ['b', 9],
      ]),
    )
    expect(logic.values.itemA).toEqual(1)
    expect(mapRuns).toEqual(1)

    logic.actions.setData(
      new Map([
        ['a', 4],
        ['b', 9],
      ]),
    )
    expect(logic.values.itemA).toEqual(4)
    expect(mapRuns).toEqual(2)
    expect(logic.selectorHealth().selectors.itemA.dirtyCause).toEqual('data.map:a')

    logic.actions.setFlags(new Set(['a', 'z']))
    expect(logic.values.hasA).toEqual(true)
    expect(setRuns).toEqual(1)

    logic.actions.setFlags(new Set(['z']))
    expect(logic.values.hasA).toEqual(false)
    expect(setRuns).toEqual(2)
    expect(logic.selectorHealth().selectors.hasA.dirtyCause).toEqual('flags.set:a')

    logic.actions.setList(['a', 'b', 'z'])
    expect(logic.values.hasB).toEqual(true)
    expect(listRuns).toEqual(1)

    logic.actions.setList(['a', 'c'])
    expect(logic.values.hasB).toEqual(false)
    expect(listRuns).toEqual(2)

    logic.actions.setList(['a', 'c', 'b'])
    expect(logic.values.hasB).toEqual(true)
    expect(listRuns).toEqual(3)
  })

  test('tracks nested leaves and missed array scans', () => {
    let cityRuns = 0
    let missingRuns = 0
    const logic = kea({
      actions: {
        setCity: (city) => ({ city }),
        setZip: (zip) => ({ zip }),
        setList: (list) => ({ list }),
      },
      reducers: ({ actions }) => ({
        user: [
          { address: { city: 'Paris', zip: '75001' } },
          {
            [actions.setCity]: (state, { city }) => ({ address: { ...state.address, city } }),
            [actions.setZip]: (state, { zip }) => ({ address: { ...state.address, zip } }),
          },
        ],
        list: [['a', 'b'], { [actions.setList]: (_, { list }) => list }],
      }),
      selectors: {
        city: [
          (s) => [s.user],
          (user) => {
            cityRuns += 1
            return user.address.city
          },
        ],
        missing: [
          (s) => [s.list],
          (list) => {
            missingRuns += 1
            return list.includes('z')
          },
        ],
      },
    })
    logic.mount()
    expect(logic.values.city).toEqual('Paris')
    expect(logic.selectorHealth().selectors.city.dependencies).toEqual(['user.address.city'])
    logic.actions.setZip('75002')
    expect(logic.values.city).toEqual('Paris')
    expect(cityRuns).toEqual(1)
    logic.actions.setCity('Lyon')
    expect(logic.values.city).toEqual('Lyon')
    expect(cityRuns).toEqual(2)
    expect(logic.selectorHealth().selectors.city.dirtyCause).toEqual('user.address.city')

    expect(logic.values.missing).toEqual(false)
    expect(logic.selectorHealth().selectors.missing.dependencies).toEqual(['list.0', 'list.1'])
    logic.actions.setList(['a', 'b', 'c'])
    expect(logic.values.missing).toEqual(false)
    expect(missingRuns).toEqual(2)
    logic.actions.setList(['a', 'b', 'z'])
    expect(logic.values.missing).toEqual(true)
    expect(missingRuns).toEqual(3)
  })

  test('throws when selectors form a cycle', () => {
    const logic = kea({
      selectors: {
        left: [(s) => [s.right], (right) => right],
        right: [(s) => [s.left], (left) => left],
      },
    })
    expect(() => logic.mount()).toThrow('[KEA] Circular dependency detected')
  })

  test('afterMount still runs when atomic selectors are enabled', () => {
    const seen = []
    const logic = kea({
      path: () => ['scenes', 'atomicEvents'],
      events: {
        beforeMount: () => seen.push('beforeMount'),
        afterMount: () => seen.push('afterMount'),
        beforeUnmount: () => seen.push('beforeUnmount'),
        afterUnmount: () => seen.push('afterUnmount'),
      },
      reducers: { n: [1] },
      selectors: { double: [(s) => [s.n], (n) => n * 2] },
    })
    const unmount = logic.mount()
    expect(seen).toEqual(['beforeMount', 'afterMount'])
    expect(logic.values.double).toEqual(2)
    unmount()
    expect(seen).toEqual(['beforeMount', 'afterMount', 'beforeUnmount', 'afterUnmount'])
    expect(getContext().options.atomicSelectors).toEqual(true)
  })

  test('components rerender only for the leaves they read', () => {
    const logic = kea({
      actions: {
        setName: (name) => ({ name }),
        setAge: (age) => ({ age }),
        setOther: (other) => ({ other }),
      },
      reducers: ({ actions }) => ({
        user: [
          { name: 'Ada', age: 1 },
          {
            [actions.setName]: (state, { name }) => ({ ...state, name }),
            [actions.setAge]: (state, { age }) => ({ ...state, age }),
          },
        ],
        other: ['x', { [actions.setOther]: (_, { other }) => other }],
      }),
      selectors: {
        userName: [(s) => [s.user], (user) => user.name],
      },
    })

    let objectRenders = 0
    let primitiveRenders = 0

    function ObjectReader() {
      const { user } = useValues(logic)
      objectRenders += 1
      return <span>{user.name}</span>
    }

    function PrimitiveReader() {
      const { userName } = useValues(logic)
      primitiveRenders += 1
      return <span>{userName}</span>
    }

    render(
      <>
        <ObjectReader />
        <PrimitiveReader />
      </>,
    )
    expect(objectRenders).toEqual(1)
    expect(primitiveRenders).toEqual(1)

    act(() => {
      logic.actions.setAge(2)
    })
    expect(objectRenders).toEqual(1)
    expect(primitiveRenders).toEqual(1)

    act(() => {
      logic.actions.setOther('y')
    })
    expect(objectRenders).toEqual(1)
    expect(primitiveRenders).toEqual(1)

    act(() => {
      logic.actions.setName('Bea')
    })
    expect(objectRenders).toEqual(2)
    expect(primitiveRenders).toEqual(2)
  })
})
