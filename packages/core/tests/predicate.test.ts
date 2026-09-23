import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
    createAdded,
    createChanged,
    createPredicate,
    createQuery,
    createRemoved,
    createWorld,
    Not,
    Or,
    relation,
    trait,
} from '../src';

const Position = trait({ x: 0, y: 0 });
const Velocity = trait({ x: 0, y: 0 });
const Health = trait(() => ({ amount: 100 }));
const Foo = trait();

describe('Predicates', () => {
    const world = createWorld();

    beforeEach(() => {
        world.reset();
    });

    it('filters entities by trait values', () => {
        const isFarRight = createPredicate([Position], ([position]) => position.x > 10);

        const entityA = world.spawn(Position({ x: 20 }));
        world.spawn(Position({ x: 5 }));
        world.spawn();

        const entities = world.query(isFarRight);
        expect([...entities]).toEqual([entityA]);
    });

    it('passes the data of each dependency in order', () => {
        const fn = vi.fn(([position, health]) => position.x > 0 && health.amount > 50);
        const isHealthyAndRight = createPredicate([Position, Health], fn);

        const entity = world.spawn(Position({ x: 1, y: 2 }), Health({ amount: 80 }));

        expect([...world.query(isHealthyAndRight)]).toEqual([entity]);
        expect(fn).toHaveBeenLastCalledWith([{ x: 1, y: 2 }, { amount: 80 }]);
    });

    it('re-evaluates when a dependency is set', () => {
        const isFarRight = createPredicate([Position], ([position]) => position.x > 10);
        const entity = world.spawn(Position);

        expect(world.query(isFarRight)).toHaveLength(0);

        entity.set(Position, { x: 20 });
        expect([...world.query(isFarRight)]).toEqual([entity]);

        entity.set(Position, { x: 0 }, false);
        expect(world.query(isFarRight)).toHaveLength(0);

        world.query(Position).updateEach(([position]) => {
            position.x = 50;
        });
        expect([...world.query(isFarRight)]).toEqual([entity]);
    });

    it('re-evaluates when a dependency is added or removed', () => {
        const isFarRight = createPredicate([Position], ([position]) => position.x > 10);
        const entity = world.spawn();

        expect(world.query(isFarRight)).toHaveLength(0);

        entity.add(Position({ x: 20 }));
        expect([...world.query(isFarRight)]).toEqual([entity]);

        entity.remove(Position);
        expect(world.query(isFarRight)).toHaveLength(0);

        entity.add(Position({ x: 20 }));
        entity.destroy();
        expect(world.query(isFarRight)).toHaveLength(0);
    });

    it('requires every dependency to be present', () => {
        const isMoving = createPredicate(
            [Position, Velocity],
            ([, velocity]) => velocity.x !== 0 || velocity.y !== 0
        );

        const entity = world.spawn(Velocity({ x: 1 }));
        expect(world.query(isMoving)).toHaveLength(0);

        entity.add(Position);
        expect([...world.query(isMoving)]).toEqual([entity]);
    });

    it('returns a distinct instance for each call', () => {
        const fn = ([position]: [{ x: number }]) => position.x > 10;
        const a = createPredicate([Position], fn);
        const b = createPredicate([Position], fn);

        expect(a).not.toBe(b);
        expect(createQuery(a).hash).not.toBe(createQuery(b).hash);
    });

    it('throws when a dependency is a tag or relation', () => {
        const ChildOf = relation();

        expect(() => createPredicate([Foo], () => true)).toThrow();
        expect(() => createPredicate([ChildOf as any], () => true)).toThrow();
        expect(() => createPredicate([ChildOf('*') as any], () => true)).toThrow();
    });

    it('adds no data to the callback tuple', () => {
        const isFarRight = createPredicate([Position], ([position]) => position.x > 10);
        world.spawn(Position({ x: 20 }), Velocity({ x: 3 }));

        world.query(isFarRight).readEach((state) => {
            expect(state).toHaveLength(0);
        });

        world.query(Velocity, isFarRight).updateEach((state) => {
            expect(state).toHaveLength(1);
            expect(state[0]).toEqual({ x: 3, y: 0 });
        });
    });

    it('matches Not for entities missing a dependency or failing the predicate', () => {
        const isFarRight = createPredicate([Position], ([position]) => position.x > 10);

        const passing = world.spawn(Position({ x: 20 }));
        const failing = world.spawn(Position({ x: 0 }));
        const missing = world.spawn();

        let entities = world.query(Not(isFarRight));
        expect(entities).toContain(failing);
        expect(entities).toContain(missing);
        expect(entities).not.toContain(passing);

        failing.set(Position, { x: 30 });
        passing.set(Position, { x: 0 });

        entities = world.query(Not(isFarRight));
        expect(entities).toContain(passing);
        expect(entities).not.toContain(failing);

        expect([...world.query(Position, Not(isFarRight))]).toEqual([passing]);
    });

    it('accepts predicates in Or', () => {
        const isFarRight = createPredicate([Position], ([position]) => position.x > 10);
        const isFarLeft = createPredicate([Position], ([position]) => position.x < -10);

        const right = world.spawn(Position({ x: 20 }));
        const left = world.spawn(Position({ x: -20 }));
        const tagged = world.spawn(Position, Foo);
        world.spawn(Position);

        let entities = world.query(Or(isFarRight, isFarLeft));
        expect(entities).toHaveLength(2);
        expect(entities).toContain(right);
        expect(entities).toContain(left);

        entities = world.query(Or(isFarRight, Foo));
        expect(entities).toHaveLength(2);
        expect(entities).toContain(right);
        expect(entities).toContain(tagged);
    });

    it('tracks entities that start satisfying the predicate with Added', () => {
        const Added = createAdded();
        const isFarRight = createPredicate([Position], ([position]) => position.x > 10);

        const entityA = world.spawn(Position({ x: 20 }));
        const entityB = world.spawn(Position);

        expect([...world.query(Added(isFarRight))]).toEqual([entityA]);
        expect(world.query(Added(isFarRight))).toHaveLength(0);

        // Still satisfied, so it is not added again.
        entityA.set(Position, { x: 30 });
        expect(world.query(Added(isFarRight))).toHaveLength(0);

        entityB.set(Position, { x: 20 });
        expect([...world.query(Added(isFarRight))]).toEqual([entityB]);

        // Satisfied and unsatisfied before reading is not an addition.
        entityA.set(Position, { x: 0 });
        world.query(Added(isFarRight));
        entityA.set(Position, { x: 20 });
        entityA.set(Position, { x: 0 });
        expect(world.query(Added(isFarRight))).toHaveLength(0);
    });

    it('tracks transitions to false with Removed', () => {
        const Removed = createRemoved();
        const isFarRight = createPredicate([Position], ([position]) => position.x > 10);

        const entityA = world.spawn(Position({ x: 20 }));
        const entityB = world.spawn(Position({ x: 20 }));

        expect(world.query(Removed(isFarRight))).toHaveLength(0);

        entityA.set(Position, { x: 0 });
        expect([...world.query(Removed(isFarRight))]).toEqual([entityA]);
        expect(world.query(Removed(isFarRight))).toHaveLength(0);

        entityB.remove(Position);
        expect([...world.query(Removed(isFarRight))]).toEqual([entityB]);

        // Unchanged falsy values are not removals.
        entityA.set(Position, { x: 5 });
        expect(world.query(Removed(isFarRight))).toHaveLength(0);
    });

    it('tracks any truthiness transition with Changed', () => {
        const Changed = createChanged();
        const isFarRight = createPredicate([Position], ([position]) => position.x > 10);

        const entity = world.spawn(Position);

        expect(world.query(Changed(isFarRight))).toHaveLength(0);

        entity.set(Position, { x: 20 });
        expect([...world.query(Changed(isFarRight))]).toEqual([entity]);
        expect(world.query(Changed(isFarRight))).toHaveLength(0);

        // Same truthiness is not a change.
        entity.set(Position, { x: 30 });
        expect(world.query(Changed(isFarRight))).toHaveLength(0);

        entity.set(Position, { x: 0 });
        expect([...world.query(Changed(isFarRight))]).toEqual([entity]);

        entity.set(Position, { x: 20 });
        entity.set(Position, { x: 0 });
        expect([...world.query(Changed(isFarRight))]).toEqual([entity]);
    });

    it('combines Changed on predicates and on their dependencies', () => {
        const Changed = createChanged();
        const isFarRight = createPredicate([Position], ([position]) => position.x > 10);
        const entity = world.spawn(Position({ x: 20 }));

        world.query(Changed(isFarRight));
        world.query(Changed(isFarRight), Changed(Position));

        entity.set(Position, { x: 0 });
        expect([...world.query(Changed(isFarRight), Changed(Position))]).toEqual([entity]);
    });

    it('accepts tracked predicates in Or', () => {
        const Added = createAdded();
        const isFarRight = createPredicate([Position], ([position]) => position.x > 10);

        const entityA = world.spawn(Position);
        const entityB = world.spawn();

        expect(world.query(Or(Added(isFarRight), Added(Foo)))).toHaveLength(0);

        entityA.set(Position, { x: 20 });
        entityB.add(Foo);

        const entities = world.query(Or(Added(isFarRight), Added(Foo)));
        expect(entities).toHaveLength(2);
        expect(entities).toContain(entityA);
        expect(entities).toContain(entityB);
    });

    it('defers re-evaluation during updateEach until iteration ends', () => {
        const isFarRight = createPredicate([Position], ([position]) => position.x > 10);
        const entityA = world.spawn(Position);
        const entityB = world.spawn(Position);
        const other = world.spawn(Position);
        expect(world.query(isFarRight)).toHaveLength(0);

        for (const changeDetection of ['auto', 'always', 'never'] as const) {
            entityA.set(Position, { x: 0 });
            entityB.set(Position, { x: 0 });
            other.set(Position, { x: 0 });

            world.query(Position).updateEach(
                ([position], entity) => {
                    position.x = 20;
                    if (entity === entityA) other.set(Position, { x: 20 });
                    expect(world.query(isFarRight)).toHaveLength(0);
                },
                { changeDetection }
            );

            expect(world.query(isFarRight)).toHaveLength(3);
        }
    });

    it('flushes deferred re-evaluation when updateEach throws', () => {
        const isFarRight = createPredicate([Position], ([position]) => position.x > 10);
        const entity = world.spawn(Position);

        expect(() =>
            world.query(Position).updateEach(() => {
                entity.set(Position, { x: 20 });
                throw new Error('boom');
            })
        ).toThrow('boom');

        expect([...world.query(isFarRight)]).toEqual([entity]);

        entity.set(Position, { x: 0 });
        expect(world.query(isFarRight)).toHaveLength(0);
    });

    it('composes with relation pairs', () => {
        const ChildOf = relation();
        const isFarRight = createPredicate([Position], ([position]) => position.x > 10);

        const parentA = world.spawn();
        const parentB = world.spawn();
        const childA = world.spawn(Position({ x: 20 }), ChildOf(parentA));
        const childB = world.spawn(Position({ x: 0 }), ChildOf(parentA));
        world.spawn(Position({ x: 20 }), ChildOf(parentB));

        expect([...world.query(isFarRight, ChildOf(parentA))]).toEqual([childA]);

        childB.set(Position, { x: 30 });
        const entities = world.query(isFarRight, ChildOf(parentA));
        expect(entities).toHaveLength(2);
        expect(entities).toContain(childB);

        expect(world.query(Not(isFarRight), ChildOf(parentA))).toHaveLength(0);
    });

    it('notifies query subscriptions when truthiness changes', () => {
        const isFarRight = createPredicate([Position], ([position]) => position.x > 10);
        const onAdd = vi.fn();
        const onRemove = vi.fn();

        world.onQueryAdd([isFarRight], onAdd);
        world.onQueryRemove([isFarRight], onRemove);

        const entity = world.spawn(Position);
        expect(onAdd).not.toHaveBeenCalled();

        entity.set(Position, { x: 20 });
        expect(onAdd).toHaveBeenCalledWith(entity);

        entity.set(Position, { x: 0 });
        expect(onRemove).toHaveBeenCalledWith(entity);
    });

    it('works across worlds', () => {
        const isFarRight = createPredicate([Position], ([position]) => position.x > 10);
        const otherWorld = createWorld();

        const entityA = world.spawn(Position({ x: 20 }));
        const entityB = otherWorld.spawn(Position({ x: 0 }));

        expect([...world.query(isFarRight)]).toEqual([entityA]);
        expect(otherWorld.query(isFarRight)).toHaveLength(0);

        entityB.set(Position, { x: 20 });
        expect([...otherWorld.query(isFarRight)]).toEqual([entityB]);

        otherWorld.destroy();
    });
});
