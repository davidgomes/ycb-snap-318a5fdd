import { kea, resetContext, actions, reducers, selectors } from '../../src'

describe('atomic selectors', () => {
  beforeEach(() => {
    resetContext({ atomicSelectors: true })
  })

  test('selectorHealth is undefined when disabled', () => {
    resetContext()
    const logic = kea([reducers({ a: [1, {}] })])
    logic.mount()
    expect(logic.selectorHealth).toBeUndefined()
  })

  test('tracks leaf dependencies and propagates through chains', () => {
    const logic = kea([
      actions({ setName: (name) => ({ name }), setAge: (age) => ({ age }), setBoth: true }),
      reducers({
        user: [
          { name: 'a', age: 1 },
          {
            setName: (s, { name }) => ({ ...s, name }),
            setAge: (s, { age }) => ({ ...s, age }),
            setBoth: (s) => ({ name: s.name + '!', age: s.age + 1 }),
          },
        ],
      }),
      selectors({
        userName: [(s) => [s.user], (user) => user.name],
        both: [(s) => [s.user], (user) => `${user.name}${user.age}`],
        shout: [(s) => [s.userName], (name) => name.toUpperCase()],
      }),
    ])
    logic.mount()
    expect(logic.values.shout).toBe('A')
    expect(logic.values.both).toBe('a1')
    let health = logic.selectorHealth()
    expect(health.selectors.userName.dependencies).toEqual(['user.name'])
    expect(health.selectors.userName.dependents).toEqual(['shout'])
    expect(health.selectors.shout.dependencies).toEqual(['userName'])
    expect(health.topologicalOrder.indexOf('userName')).toBeLessThan(health.topologicalOrder.indexOf('shout'))

    logic.actions.setAge(2)
    expect(logic.values.shout).toBe('A')
    expect(logic.values.both).toBe('a2')
    health = logic.selectorHealth()
    expect(health.selectors.userName.evaluations).toBe(1)
    expect(health.selectors.shout.evaluations).toBe(1)

    logic.actions.setName('b')
    expect(logic.values.shout).toBe('B')
    health = logic.selectorHealth()
    expect(health.selectors.userName.dirtyCause).toBe('user.name')
    expect(health.selectors.shout.dirtyCause).toBe('selector:userName')

    logic.actions.setBoth()
    expect(logic.values.both).toBe('b!3')
    expect(logic.selectorHealth().selectors.both.evaluations).toBe(3)
  })

  test('collections', () => {
    const logic = kea([
      actions({ set: (data) => ({ data }) }),
      reducers({
        data: [new Map([['a', 1], ['b', 2]]), { set: (_, { data }) => data }],
        tags: [new Set(['a']), {}],
        list: [['x', 'y', 'z'], {}],
      }),
      selectors({
        fromMap: [(s) => [s.data], (d) => d.get('a')],
        fromSet: [(s) => [s.tags], (t) => t.has('a')],
        hasY: [(s) => [s.list], (l) => l.includes('y')],
      }),
    ])
    logic.mount()
    expect(logic.values.fromMap).toBe(1)
    expect(logic.values.fromSet).toBe(true)
    expect(logic.values.hasY).toBe(true)
    const health = logic.selectorHealth()
    expect(health.selectors.fromMap.dependencies).toEqual(['data.map:a'])
    expect(health.selectors.fromSet.dependencies).toEqual(['tags.set:a'])
    expect(health.selectors.hasY.dependencies).toEqual(['list.0', 'list.1'])
    logic.actions.set(new Map([['a', 1], ['b', 3]]))
    expect(logic.values.fromMap).toBe(1)
    expect(logic.selectorHealth().selectors.fromMap.evaluations).toBe(1)
  })

  test('circular dependencies throw at build', () => {
    const logic = kea([
      selectors({
        a: [(s) => [s.b], (b) => b],
        b: [(s) => [s.a], (a) => a],
      }),
    ])
    expect(() => logic.mount()).toThrow('[KEA] Circular dependency detected')
  })
})
