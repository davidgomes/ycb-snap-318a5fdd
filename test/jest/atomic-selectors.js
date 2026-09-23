import {
  kea,
  resetContext,
  getContext,
  activatePlugin,
  corePlugin,
  actions,
  reducers,
  selectors,
  connect,
  props,
  key,
  useValues,
} from '../../src'

import React from 'react'
import { render, screen, act } from '@testing-library/react'

const health = (logic) => logic.selectorHealth().selectors

function userLogic(
  extraSelectors = {},
  defaultUser = { name: 'Alice', age: 30, address: { city: 'Paris', zip: '75001' } },
) {
  return kea([
    actions({
      setName: (name) => ({ name }),
      setAge: (age) => ({ age }),
      setCity: (city) => ({ city }),
      setZip: (zip) => ({ zip }),
      setUser: (user) => ({ user }),
    }),
    reducers({
      user: [
        defaultUser,
        {
          setName: (state, { name }) => ({ ...state, name }),
          setAge: (state, { age }) => ({ ...state, age }),
          setCity: (state, { city }) => ({ ...state, address: { ...state.address, city } }),
          setZip: (state, { zip }) => ({ ...state, address: { ...state.address, zip } }),
          setUser: (_, { user }) => user,
        },
      ],
      other: [0, { setAge: (state) => state + 1 }],
    }),
    selectors(extraSelectors),
  ])
}

describe('atomic selectors', () => {
  describe('configuration', () => {
    test('is disabled by default and selectorHealth is undefined', () => {
      resetContext()
      expect(getContext().options.atomicSelectors).toBe(false)

      const logic = kea([reducers({ a: [1] }), selectors({ b: [(s) => [s.a], (a) => a + 1] })])
      expect(logic.selectorHealth).toBeUndefined()
      logic.mount()
      expect(logic.selectorHealth).toBeUndefined()
      expect(logic.build().selectorHealth).toBeUndefined()
      expect(logic.values.b).toEqual(2)
    })

    test('exposes selectorHealth() when enabled', () => {
      resetContext({ atomicSelectors: true })
      expect(getContext().options.atomicSelectors).toBe(true)

      const logic = kea([reducers({ a: [1] }), selectors({ b: [(s) => [s.a], (a) => a + 1] })])
      logic.mount()

      expect(typeof logic.selectorHealth).toBe('function')
      expect(typeof logic.build().selectorHealth).toBe('function')
      expect(logic.selectorHealth()).toEqual({
        selectors: { b: { dependencies: [], dependents: [], evaluations: 0, dirtyCause: null } },
        topologicalOrder: ['b'],
      })

      expect(logic.values.b).toEqual(2)
      expect(logic.selectorHealth()).toEqual({
        selectors: { b: { dependencies: ['a'], dependents: [], evaluations: 1, dirtyCause: null } },
        topologicalOrder: ['b'],
      })
    })

    test('logic without selectors reports an empty graph', () => {
      resetContext({ atomicSelectors: true })
      const logic = kea([reducers({ a: [1] })])
      logic.mount()
      expect(logic.selectorHealth()).toEqual({ selectors: {}, topologicalOrder: [] })
    })
  })

  describe('dependency tracking', () => {
    beforeEach(() => {
      resetContext({ atomicSelectors: true })
    })

    test('tracks the exact leaf that was read', () => {
      const logic = userLogic({
        userName: [(s) => [s.user], (user) => user.name],
        city: [(s) => [s.user], (user) => user.address.city],
      })
      logic.mount()

      expect(logic.values.userName).toEqual('Alice')
      expect(logic.values.city).toEqual('Paris')
      expect(health(logic).userName.dependencies).toEqual(['user.name'])
      expect(health(logic).city.dependencies).toEqual(['user.address.city'])

      logic.actions.setAge(31)
      logic.actions.setZip('75002')
      expect(logic.values.userName).toEqual('Alice')
      expect(logic.values.city).toEqual('Paris')
      expect(health(logic).userName.evaluations).toEqual(1)
      expect(health(logic).city.evaluations).toEqual(1)
      expect(health(logic).userName.dirtyCause).toEqual(null)

      logic.actions.setName('Bob')
      expect(logic.values.userName).toEqual('Bob')
      expect(health(logic).userName).toEqual({
        dependencies: ['user.name'],
        dependents: [],
        evaluations: 2,
        dirtyCause: 'user.name',
      })
      expect(health(logic).city.evaluations).toEqual(1)

      logic.actions.setCity('Tallinn')
      expect(logic.values.city).toEqual('Tallinn')
      expect(health(logic).city.evaluations).toEqual(2)
      expect(health(logic).city.dirtyCause).toEqual('user.address.city')
      expect(health(logic).userName.evaluations).toEqual(2)
    })

    test('lists every leaf read, in access order, without parent nodes', () => {
      const logic = userLogic({
        summary: [(s) => [s.user], (user) => `${user.name} (${user.age}) from ${user.address.city}`],
      })
      logic.mount()

      expect(logic.values.summary).toEqual('Alice (30) from Paris')
      expect(health(logic).summary.dependencies).toEqual(['user.name', 'user.age', 'user.address.city'])
    })

    test('primitive inputs and untouched objects depend on the whole value', () => {
      const logic = userLogic({
        otherPlusOne: [(s) => [s.other], (other) => other + 1],
        hasUser: [(s) => [s.user], (user) => !!user],
      })
      logic.mount()

      expect(logic.values.otherPlusOne).toEqual(1)
      expect(logic.values.hasUser).toEqual(true)
      expect(health(logic).otherPlusOne.dependencies).toEqual(['other'])
      expect(health(logic).hasUser.dependencies).toEqual(['user'])

      logic.actions.setUser(null)
      expect(logic.values.hasUser).toEqual(false)
      expect(health(logic).hasUser.dirtyCause).toEqual('user')
    })

    test('returned state objects are the originals and are tracked by identity', () => {
      const logic = userLogic({
        address: [(s) => [s.user], (user) => user.address],
        wrapped: [(s) => [s.user], (user) => ({ address: user.address, list: [user.address] })],
      })
      logic.mount()

      const { address } = logic.values.user
      expect(logic.values.address).toBe(address)
      expect(logic.values.wrapped.address).toBe(address)
      expect(logic.values.wrapped.list[0]).toBe(address)
      expect(health(logic).address.dependencies).toEqual(['user.address'])

      logic.actions.setName('Bob')
      expect(health(logic).address.evaluations).toEqual(1)

      logic.actions.setZip('75002')
      expect(logic.values.address).toEqual({ city: 'Paris', zip: '75002' })
      expect(logic.values.address).toBe(logic.values.user.address)
      expect(health(logic).address.evaluations).toEqual(2)
      expect(health(logic).address.dirtyCause).toEqual('user.address')
    })

    test('key enumeration and membership checks are tracked', () => {
      const logic = userLogic({
        keyCount: [(s) => [s.user], (user) => Object.keys(user).length],
        hasNickname: [(s) => [s.user], (user) => 'nickname' in user],
      })
      logic.mount()

      expect(logic.values.keyCount).toEqual(3)
      expect(logic.values.hasNickname).toEqual(false)
      expect(health(logic).keyCount.dependencies).toEqual(['user'])
      expect(health(logic).hasNickname.dependencies).toEqual(['user.nickname'])

      logic.actions.setAge(31)
      expect(health(logic).keyCount.evaluations).toEqual(1)
      expect(health(logic).hasNickname.evaluations).toEqual(1)

      logic.actions.setUser({ ...logic.values.user, nickname: 'Al' })
      expect(logic.values.keyCount).toEqual(4)
      expect(logic.values.hasNickname).toEqual(true)
      expect(health(logic).keyCount.dirtyCause).toEqual('user')
      expect(health(logic).hasNickname.dirtyCause).toEqual('user.nickname')
    })

    test('works with frozen state', () => {
      const frozenUser = Object.freeze({ name: 'Alice', age: 30, address: Object.freeze({ city: 'Paris', zip: '1' }) })
      const logic = userLogic({ city: [(s) => [s.user], (user) => user.address.city] }, frozenUser)
      logic.mount()

      expect(logic.values.city).toEqual('Paris')
      expect(health(logic).city.dependencies).toEqual(['user.address.city'])
      logic.actions.setZip('2')
      expect(health(logic).city.evaluations).toEqual(1)
      logic.actions.setCity('Rome')
      expect(logic.values.city).toEqual('Rome')
    })

    test('props used as selector inputs are tracked', () => {
      const logic = kea([
        props({ id: 1, suffix: '!' }),
        key((props) => props.id),
        reducers({ names: [{ 1: 'one', 2: 'two' }] }),
        selectors({
          label: [(s, p) => [p.suffix, s.names, p.id], (suffix, names, id) => names[id] + suffix],
        }),
      ])
      const builtLogic = logic({ id: 1, suffix: '!' })
      builtLogic.mount()

      expect(builtLogic.values.label).toEqual('one!')
      expect(health(builtLogic).label.dependencies).toEqual(['props.suffix', 'names.1', 'props.id'])

      logic({ id: 1, suffix: '?' })
      expect(builtLogic.values.label).toEqual('one?')
      expect(health(builtLogic).label.evaluations).toEqual(2)
      expect(health(builtLogic).label.dirtyCause).toEqual('props.suffix')
    })

    test('selectors can still be called with an explicit state', () => {
      const logic = userLogic({ userName: [(s) => [s.user], (user) => user.name] })
      logic.mount()

      const oldState = getContext().store.getState()
      logic.actions.setName('Bob')
      expect(logic.selectors.userName(oldState)).toEqual('Alice')
      expect(logic.selectors.userName()).toEqual('Bob')
      expect(logic.values.userName).toEqual('Bob')
    })

    test('honours custom memoization options', () => {
      const logic = kea([
        actions({ addValue: (value) => ({ value }), setValue: (index, value) => ({ index, value }) }),
        reducers({
          values: [
            [],
            {
              addValue: (state, { value }) => [...state, value],
              setValue: (state, { index, value }) => state.map((s, i) => (i === index ? value : s)),
            },
          ],
        }),
        selectors({
          reversedValuesIfLengthChanges: [
            (s) => [s.values],
            (values) => [...values].reverse(),
            { resultEqualityCheck: (a, b) => a.length === b.length },
          ],
          firstUpperCase: [
            (s) => [s.values],
            (values) => values[0]?.toUpperCase(),
            { equalityCheck: (a, b) => a?.toLowerCase() === b?.toLowerCase() },
          ],
        }),
      ])
      logic.mount()

      logic.actions.addValue('first')
      logic.actions.addValue('second')
      expect(logic.values.reversedValuesIfLengthChanges).toEqual(['second', 'first'])
      logic.actions.setValue(1, 'SECOND')
      expect(logic.values.reversedValuesIfLengthChanges).toEqual(['second', 'first'])
      logic.actions.addValue('third')
      expect(logic.values.reversedValuesIfLengthChanges).toEqual(['third', 'SECOND', 'first'])

      expect(logic.values.firstUpperCase).toEqual('FIRST')
      const evaluations = health(logic).firstUpperCase.evaluations
      logic.actions.setValue(0, 'FIRST')
      expect(logic.values.firstUpperCase).toEqual('FIRST')
      expect(health(logic).firstUpperCase.evaluations).toEqual(evaluations)
    })
  })

  describe('collections', () => {
    beforeEach(() => {
      resetContext({ atomicSelectors: true })
    })

    test('tracks Map keys', () => {
      const logic = kea([
        actions({ setEntry: (key, value) => ({ key, value }) }),
        reducers({
          data: [
            new Map([
              ['a', 1],
              ['b', 2],
            ]),
            { setEntry: (state, { key, value }) => new Map(state).set(key, value) },
          ],
        }),
        selectors({
          valueOfA: [(s) => [s.data], (data) => data.get('a')],
          hasC: [(s) => [s.data], (data) => data.has('c')],
        }),
      ])
      logic.mount()

      expect(logic.values.valueOfA).toEqual(1)
      expect(logic.values.hasC).toEqual(false)
      expect(health(logic).valueOfA.dependencies).toEqual(['data.map:a'])
      expect(health(logic).hasC.dependencies).toEqual(['data.map:c'])

      logic.actions.setEntry('b', 3)
      expect(health(logic).valueOfA.evaluations).toEqual(1)
      expect(health(logic).hasC.evaluations).toEqual(1)

      logic.actions.setEntry('a', 5)
      expect(logic.values.valueOfA).toEqual(5)
      expect(health(logic).valueOfA.evaluations).toEqual(2)
      expect(health(logic).valueOfA.dirtyCause).toEqual('data.map:a')
      expect(health(logic).hasC.evaluations).toEqual(1)

      logic.actions.setEntry('c', 0)
      expect(logic.values.hasC).toEqual(true)
      expect(health(logic).hasC.dirtyCause).toEqual('data.map:c')
    })

    test('tracks nested reads through Map values', () => {
      const logic = kea([
        actions({ setEntry: (key, value) => ({ key, value }) }),
        reducers({
          data: [
            new Map([['a', { label: 'A', count: 1 }]]),
            { setEntry: (state, { key, value }) => new Map(state).set(key, value) },
          ],
        }),
        selectors({ labelOfA: [(s) => [s.data], (data) => data.get('a').label] }),
      ])
      logic.mount()

      expect(logic.values.labelOfA).toEqual('A')
      expect(health(logic).labelOfA.dependencies).toEqual(['data.map:a.label'])
      logic.actions.setEntry('a', { label: 'A', count: 2 })
      expect(health(logic).labelOfA.evaluations).toEqual(1)
    })

    test('tracks Set membership', () => {
      const logic = kea([
        actions({ add: (value) => ({ value }), remove: (value) => ({ value }) }),
        reducers({
          data: [
            new Set(['a', 'b']),
            {
              add: (state, { value }) => new Set(state).add(value),
              remove: (state, { value }) => {
                const next = new Set(state)
                next.delete(value)
                return next
              },
            },
          ],
        }),
        selectors({ hasA: [(s) => [s.data], (data) => data.has('a')] }),
      ])
      logic.mount()

      expect(logic.values.hasA).toEqual(true)
      expect(health(logic).hasA.dependencies).toEqual(['data.set:a'])

      logic.actions.add('c')
      logic.actions.remove('b')
      expect(health(logic).hasA.evaluations).toEqual(1)

      logic.actions.remove('a')
      expect(logic.values.hasA).toEqual(false)
      expect(health(logic).hasA.evaluations).toEqual(2)
      expect(health(logic).hasA.dirtyCause).toEqual('data.set:a')
    })

    test('iterating a collection depends on the whole collection', () => {
      const logic = kea([
        actions({ add: (value) => ({ value }) }),
        reducers({ data: [new Set(['a']), { add: (state, { value }) => new Set(state).add(value) }] }),
        selectors({ size: [(s) => [s.data], (data) => [...data].length] }),
      ])
      logic.mount()

      expect(logic.values.size).toEqual(1)
      expect(health(logic).size.dependencies).toEqual(['data'])
      logic.actions.add('b')
      expect(logic.values.size).toEqual(2)
    })

    test('tracks array indices and the elements checked by includes()', () => {
      const logic = kea([
        actions({ setItem: (index, value) => ({ index, value }), push: (value) => ({ value }) }),
        reducers({
          list: [
            ['a', 'b', 'c'],
            {
              setItem: (state, { index, value }) => state.map((item, i) => (i === index ? value : item)),
              push: (state, { value }) => [...state, value],
            },
          ],
        }),
        selectors({
          firstTwo: [(s) => [s.list], (list) => list[0] + list[1]],
          hasB: [(s) => [s.list], (list) => list.includes('b')],
        }),
      ])
      logic.mount()

      expect(logic.values.firstTwo).toEqual('ab')
      expect(logic.values.hasB).toEqual(true)
      expect(health(logic).firstTwo.dependencies).toEqual(['list.0', 'list.1'])
      expect(health(logic).hasB.dependencies).toEqual(['list.0', 'list.1'])

      logic.actions.setItem(2, 'z')
      logic.actions.push('d')
      expect(health(logic).firstTwo.evaluations).toEqual(1)
      expect(health(logic).hasB.evaluations).toEqual(1)

      logic.actions.setItem(1, 'x')
      expect(logic.values.hasB).toEqual(false)
      expect(health(logic).hasB.dirtyCause).toEqual('list.1')
      // not found: every element and the length matter now
      expect(health(logic).hasB.dependencies).toEqual(['list.0', 'list.1', 'list.2', 'list.3', 'list.length'])

      logic.actions.push('b')
      expect(logic.values.hasB).toEqual(true)
      expect(health(logic).hasB.dirtyCause).toEqual('list.length')
    })

    test('indexOf and includes compare against the original state objects', () => {
      const first = { id: 1 }
      const second = { id: 2 }
      const logic = kea([
        reducers({ list: [[first, second]], selected: [second] }),
        selectors({
          selectedIndex: [(s) => [s.list, s.selected], (list, selected) => list.indexOf(selected)],
          containsSecond: [(s) => [s.list], (list) => list.includes(second)],
        }),
      ])
      logic.mount()

      expect(logic.values.selectedIndex).toEqual(1)
      expect(logic.values.containsSecond).toEqual(true)
      expect(health(logic).containsSecond.dependencies).toEqual(['list.0', 'list.1'])
    })

    test('array search methods match native results', () => {
      const lists = [[1, 2, 1, NaN, undefined], [], [, 'hole', undefined], ['a', 'b', 'a']]
      const calls = [
        ['includes', [1]],
        ['includes', [NaN]],
        ['includes', [undefined]],
        ['includes', ['a', -1]],
        ['indexOf', [1, 1]],
        ['indexOf', [undefined]],
        ['indexOf', ['a', -2]],
        ['lastIndexOf', [1]],
        ['lastIndexOf', [1, undefined]],
        ['lastIndexOf', ['a', -2]],
        ['lastIndexOf', ['a', 10]],
      ]
      const logic = kea([
        reducers({ lists: [lists] }),
        selectors({
          results: [
            (s) => [s.lists],
            (tracked) => tracked.map((list) => calls.map(([method, args]) => list[method](...args))),
          ],
        }),
      ])
      logic.mount()

      expect(logic.values.results).toEqual(lists.map((list) => calls.map(([method, args]) => list[method](...args))))
    })

    test('find() and some() only depend on the elements they visit', () => {
      const logic = kea([
        actions({ setTodo: (index, todo) => ({ index, todo }) }),
        reducers({
          todos: [
            [
              { id: 1, done: false, title: 'a' },
              { id: 2, done: true, title: 'b' },
              { id: 3, done: false, title: 'c' },
            ],
            { setTodo: (state, { index, todo }) => state.map((t, i) => (i === index ? { ...t, ...todo } : t)) },
          ],
        }),
        selectors({
          firstDoneTitle: [(s) => [s.todos], (todos) => todos.find((todo) => todo.done)?.title],
          anyDone: [(s) => [s.todos], (todos) => todos.some((todo) => todo.done)],
          doneTodos: [(s) => [s.todos], (todos) => todos.filter((todo) => todo.done)],
        }),
      ])
      logic.mount()

      expect(logic.values.firstDoneTitle).toEqual('b')
      expect(logic.values.anyDone).toEqual(true)
      expect(logic.values.doneTodos).toEqual([logic.values.todos[1]])
      expect(logic.values.doneTodos[0]).toBe(logic.values.todos[1])
      expect(health(logic).firstDoneTitle.dependencies).toEqual(['todos.0.done', 'todos.1.done', 'todos.1.title'])
      expect(health(logic).anyDone.dependencies).toEqual(['todos.0.done', 'todos.1.done'])

      logic.actions.setTodo(2, { title: 'changed' })
      logic.actions.setTodo(0, { title: 'changed too' })
      expect(health(logic).firstDoneTitle.evaluations).toEqual(1)
      expect(health(logic).anyDone.evaluations).toEqual(1)

      logic.actions.setTodo(1, { title: 'B' })
      expect(logic.values.firstDoneTitle).toEqual('B')
      expect(health(logic).firstDoneTitle.dirtyCause).toEqual('todos.1.title')
      expect(health(logic).anyDone.evaluations).toEqual(1)
    })
  })

  describe('propagation', () => {
    beforeEach(() => {
      resetContext({ atomicSelectors: true })
    })

    test('updates flow through multi-level chains only to affected selectors', () => {
      const logic = userLogic({
        userName: [(s) => [s.user], (user) => user.name],
        upperName: [(s) => [s.userName], (userName) => userName.toUpperCase()],
        nameLength: [(s) => [s.userName], (userName) => userName.length],
        isLongName: [(s) => [s.nameLength], (nameLength) => nameLength > 5],
        userAge: [(s) => [s.user], (user) => user.age],
      })
      logic.mount()

      for (const key of ['upperName', 'isLongName', 'userAge']) {
        logic.values[key]
      }
      const report = logic.selectorHealth()
      expect(report.topologicalOrder).toEqual(['userName', 'upperName', 'nameLength', 'isLongName', 'userAge'])
      expect(report.selectors.userName.dependents).toEqual(['upperName', 'nameLength'])
      expect(report.selectors.nameLength.dependents).toEqual(['isLongName'])
      expect(report.selectors.upperName.dependencies).toEqual(['userName'])
      expect(report.selectors.isLongName.dependencies).toEqual(['nameLength'])

      logic.actions.setAge(31)
      expect(health(logic).userAge.evaluations).toEqual(2)
      for (const key of ['userName', 'upperName', 'nameLength', 'isLongName']) {
        expect(health(logic)[key].evaluations).toEqual(1)
      }

      // same length, so isLongName's only input does not change
      logic.actions.setName('Carol')
      expect(logic.values.upperName).toEqual('CAROL')
      expect(health(logic).userName.dirtyCause).toEqual('user.name')
      expect(health(logic).upperName.dirtyCause).toEqual('selector:userName')
      expect(health(logic).nameLength.evaluations).toEqual(2)
      expect(health(logic).isLongName.evaluations).toEqual(1)
      expect(health(logic).isLongName.dirtyCause).toEqual(null)

      logic.actions.setName('Bartholomew')
      expect(logic.values.isLongName).toEqual(true)
      expect(health(logic).isLongName.evaluations).toEqual(2)
      expect(health(logic).isLongName.dirtyCause).toEqual('selector:nameLength')
    })

    test('selectors referenced before they are defined and legacy closures resolve to local names', () => {
      const logic = kea({
        reducers: () => ({ name: ['chirpy', { setName: (_, { name }) => name }] }),
        actions: () => ({ setName: (name) => ({ name }) }),
        selectors: ({ selectors }) => ({
          upperCaseName: [() => [selectors.capitalizedName], (capitalizedName) => capitalizedName.toUpperCase()],
          capitalizedName: [() => [selectors.name], (name) => name.charAt(0).toUpperCase() + name.slice(1)],
        }),
      })
      logic.mount()

      expect(logic.values.upperCaseName).toEqual('CHIRPY')
      expect(logic.selectorHealth()).toEqual({
        selectors: {
          upperCaseName: {
            dependencies: ['capitalizedName'],
            dependents: [],
            evaluations: 1,
            dirtyCause: null,
          },
          capitalizedName: { dependencies: ['name'], dependents: ['upperCaseName'], evaluations: 1, dirtyCause: null },
        },
        topologicalOrder: ['capitalizedName', 'upperCaseName'],
      })
    })

    test('selectors added in later builders depend on earlier ones by name', () => {
      const logic = userLogic({ userName: [(s) => [s.user], (user) => user.name] })
      logic.build().extend(selectors({ greeting: [(s) => [s.userName], (userName) => `Hi ${userName}`] }))
      logic.mount()

      expect(logic.values.greeting).toEqual('Hi Alice')
      expect(health(logic).greeting.dependencies).toEqual(['userName'])
      expect(health(logic).userName.dependents).toEqual(['greeting'])
      expect(logic.selectorHealth().topologicalOrder).toEqual(['userName', 'greeting'])
    })

    test('connected logic only reports its own selectors', () => {
      const sourceLogic = userLogic({ userName: [(s) => [s.user], (user) => user.name] })
      const consumerLogic = kea([
        connect({ values: [sourceLogic, ['userName', 'user']], actions: [sourceLogic, ['setName', 'setAge']] }),
        selectors({
          greeting: [(s) => [s.userName], (userName) => `Hi ${userName}`],
          age: [(s) => [s.user], (user) => user.age],
        }),
      ])
      consumerLogic.mount()

      expect(consumerLogic.values.greeting).toEqual('Hi Alice')
      expect(consumerLogic.values.age).toEqual(30)
      expect(Object.keys(health(consumerLogic))).toEqual(['greeting', 'age'])
      expect(health(consumerLogic).greeting.dependencies).toEqual(['userName'])
      expect(health(consumerLogic).age.dependencies).toEqual(['user.age'])
      expect(Object.keys(health(sourceLogic))).toEqual(['userName'])

      consumerLogic.actions.setAge(40)
      expect(consumerLogic.values.age).toEqual(40)
      expect(health(consumerLogic).greeting.evaluations).toEqual(1)
      expect(health(sourceLogic).userName.evaluations).toEqual(1)

      consumerLogic.actions.setName('Bob')
      expect(consumerLogic.values.greeting).toEqual('Hi Bob')
      expect(health(consumerLogic).greeting.dirtyCause).toEqual('userName')
    })
  })

  describe('atomic updates', () => {
    beforeEach(() => {
      resetContext({ atomicSelectors: true })
    })

    function namesLogic(extraSelectors) {
      return kea([
        actions({ setNames: (first, last) => ({ first, last }), setUnrelated: true }),
        reducers({
          user: [{ first: 'Ada', last: 'Lovelace' }, { setNames: (_, { first, last }) => ({ first, last }) }],
          unrelated: [0, { setUnrelated: (state) => state + 1 }],
        }),
        selectors(extraSelectors),
      ])
    }

    test('several dependency changes in one action cause one re-evaluation', () => {
      const logic = namesLogic({ fullName: [(s) => [s.user], (user) => `${user.first} ${user.last}`] })
      logic.mount()

      expect(logic.values.fullName).toEqual('Ada Lovelace')
      logic.actions.setNames('Grace', 'Hopper')
      expect(logic.values.fullName).toEqual('Grace Hopper')
      expect(health(logic).fullName.evaluations).toEqual(2)
      expect(health(logic).fullName.dirtyCause).toEqual('user.first')
    })

    test('diamond dependencies re-evaluate once per action', () => {
      const logic = namesLogic({
        first: [(s) => [s.user], (user) => user.first],
        last: [(s) => [s.user], (user) => user.last],
        initials: [(s) => [s.first, s.last], (first, last) => first[0] + last[0]],
      })
      logic.mount()

      expect(logic.values.initials).toEqual('AL')
      logic.actions.setNames('Grace', 'Hopper')
      expect(logic.values.initials).toEqual('GH')
      expect(health(logic).initials.evaluations).toEqual(2)
      expect(health(logic).initials.dirtyCause).toEqual('selector:first')
      expect(logic.selectorHealth().topologicalOrder).toEqual(['first', 'last', 'initials'])
    })

    test('evaluated selectors are updated when the action is dispatched', () => {
      const logic = namesLogic({ fullName: [(s) => [s.user], (user) => `${user.first} ${user.last}`] })
      logic.mount()

      logic.actions.setNames('Grace', 'Hopper')
      expect(health(logic).fullName.evaluations).toEqual(0)

      expect(logic.values.fullName).toEqual('Grace Hopper')
      logic.actions.setNames('Alan', 'Turing')
      expect(health(logic).fullName.evaluations).toEqual(2)
      expect(health(logic).fullName.dirtyCause).toEqual('user.first')

      logic.actions.setUnrelated()
      expect(health(logic).fullName.evaluations).toEqual(2)
      expect(logic.values.fullName).toEqual('Alan Turing')
    })

    test('errors thrown while refreshing surface on the next read, not on dispatch', () => {
      const logic = namesLogic({
        fullName: [
          (s) => [s.user],
          (user) => {
            if (user.first === 'boom') {
              throw new Error('exploded')
            }
            return user.first
          },
        ],
      })
      logic.mount()

      expect(logic.values.fullName).toEqual('Ada')
      expect(() => logic.actions.setNames('boom', 'x')).not.toThrow()
      expect(() => logic.values.fullName).toThrow('exploded')
      logic.actions.setNames('Ok', 'x')
      expect(logic.values.fullName).toEqual('Ok')
    })
  })

  describe('circular dependencies', () => {
    beforeEach(() => {
      resetContext({ atomicSelectors: true })
    })

    test('are detected while mounting', () => {
      const logic = kea([
        reducers({ a: [1] }),
        selectors({
          x: [(s) => [s.a, s.y], (a, y) => a + y],
          y: [(s) => [s.z], (z) => z],
          z: [(s) => [s.x], (x) => x],
        }),
      ])

      expect(() => logic.mount()).toThrow('[KEA] Circular dependency detected')
      expect(() => logic.build()).toThrow('x -> y -> z -> x')
      expect(logic.isMounted()).toEqual(false)
    })

    test('self references are detected while building', () => {
      const logic = kea({
        selectors: ({ selectors }) => ({ loop: [() => [selectors.loop], (loop) => loop] }),
      })
      expect(() => logic.build()).toThrow('[KEA] Circular dependency detected')
    })

    test('are detected at evaluation time when hidden in inline inputs', () => {
      const logic = kea([
        reducers({ a: [1] }),
        selectors(({ selectors }) => ({
          x: [() => [(state) => selectors.y(state)], (y) => y],
          y: [(s) => [s.x], (x) => x],
        })),
      ])
      logic.mount()
      expect(() => logic.values.y).toThrow('[KEA] Circular dependency detected')
    })

    test('are not reported without atomic selectors', () => {
      resetContext()
      const logic = kea([selectors({ x: [(s) => [s.y], (y) => y], y: [(s) => [s.x], (x) => x] })])
      expect(() => logic.mount()).not.toThrow()
    })
  })

  describe('compatibility', () => {
    test('does not change plugin registration or lifecycle event order', () => {
      resetContext({ atomicSelectors: true })
      const events = []
      const testPlugin = {
        name: 'test',
        events: {
          afterLogic: () => events.push('plugin.afterLogic'),
          beforeMount: () => events.push('plugin.beforeMount'),
          afterMount: () => events.push('plugin.afterMount'),
          beforeUnmount: () => events.push('plugin.beforeUnmount'),
          afterUnmount: () => events.push('plugin.afterUnmount'),
        },
      }
      activatePlugin(testPlugin)

      const { plugins } = getContext()
      expect(plugins.activated).toEqual([corePlugin, testPlugin])
      expect(Object.keys(plugins.events)).toEqual([
        'afterPlugin',
        'beforeReduxStore',
        'legacyBuild',
        'afterLogic',
        'beforeMount',
        'afterMount',
        'beforeUnmount',
        'afterUnmount',
      ])

      const connectedLogic = kea({
        reducers: () => ({ value: [true] }),
        selectors: ({ selectors }) => ({ notValue: [() => [selectors.value], (value) => !value] }),
        events: () => ({
          beforeMount: () => events.push('connectedLogic.beforeMount'),
          afterMount: () => events.push('connectedLogic.afterMount'),
          beforeUnmount: () => events.push('connectedLogic.beforeUnmount'),
          afterUnmount: () => events.push('connectedLogic.afterUnmount'),
        }),
      })
      const logic = kea({
        connect: { values: [connectedLogic, ['notValue']] },
        events: () => ({
          beforeMount: () => events.push('logic.beforeMount'),
          afterMount: () => events.push('logic.afterMount'),
          beforeUnmount: () => events.push('logic.beforeUnmount'),
          afterUnmount: () => events.push('logic.afterUnmount'),
        }),
      })

      const unmount = logic.mount()
      expect(logic.values.notValue).toEqual(false)
      unmount()

      expect(events).toEqual([
        'plugin.afterLogic',
        'plugin.afterLogic',
        'plugin.beforeMount',
        'connectedLogic.beforeMount',
        'plugin.afterMount',
        'connectedLogic.afterMount',
        'plugin.beforeMount',
        'logic.beforeMount',
        'plugin.afterMount',
        'logic.afterMount',
        'plugin.beforeUnmount',
        'logic.beforeUnmount',
        'plugin.afterUnmount',
        'logic.afterUnmount',
        'plugin.beforeUnmount',
        'connectedLogic.beforeUnmount',
        'plugin.afterUnmount',
        'connectedLogic.afterUnmount',
      ])
    })

    test('keeps working after remounting', () => {
      resetContext({ atomicSelectors: true })
      const logic = userLogic({ userName: [(s) => [s.user], (user) => user.name] })

      const unmount = logic.mount()
      expect(logic.values.userName).toEqual('Alice')
      logic.actions.setName('Bob')
      expect(logic.values.userName).toEqual('Bob')
      unmount()

      logic.mount()
      expect(logic.values.userName).toEqual('Alice')
      expect(health(logic).userName.evaluations).toEqual(1)
    })
  })

  describe('react', () => {
    beforeEach(() => {
      resetContext({ atomicSelectors: true })
    })

    test('components re-render only when the selectors they use change', () => {
      const logic = userLogic({
        userName: [(s) => [s.user], (user) => user.name],
        userInfo: [(s) => [s.user], (user) => ({ name: user.name, city: user.address.city })],
        userAge: [(s) => [s.user], (user) => user.age],
      })

      const renders = { name: 0, info: 0, age: 0 }

      function Name() {
        const { userName } = useValues(logic)
        renders.name += 1
        return <div data-testid="name">{userName}</div>
      }
      function Info() {
        const { userInfo } = useValues(logic)
        renders.info += 1
        return <div data-testid="info">{`${userInfo.name} ${userInfo.city}`}</div>
      }
      function Age() {
        const { userAge } = useValues(logic)
        renders.age += 1
        return <div data-testid="age">{userAge}</div>
      }

      render(
        <>
          <Name />
          <Info />
          <Age />
        </>,
      )
      expect(renders).toEqual({ name: 1, info: 1, age: 1 })

      act(() => logic.actions.setAge(31))
      expect(screen.getByTestId('age')).toHaveTextContent('31')
      expect(renders).toEqual({ name: 1, info: 1, age: 2 })
      expect(health(logic).userInfo.evaluations).toEqual(1)

      act(() => getContext().store.dispatch({ type: 'unrelated action' }))
      expect(renders).toEqual({ name: 1, info: 1, age: 2 })

      act(() => logic.actions.setName('Bob'))
      expect(screen.getByTestId('name')).toHaveTextContent('Bob')
      expect(screen.getByTestId('info')).toHaveTextContent('Bob Paris')
      expect(renders).toEqual({ name: 2, info: 2, age: 2 })

      act(() => logic.actions.setZip('75002'))
      expect(renders).toEqual({ name: 2, info: 2, age: 2 })
    })
  })
})
