import { beforeEach, describe, expect, it } from 'vitest';
import {
    createAdded,
    createChanged,
    createPredicate,
    createRemoved,
    createWorld,
    Not,
    Or,
    relation,
    trait,
} from '../src';

const Health = trait({ value: 100 });
const Position = trait({ x: 0, y: 0 });
const Name = trait({ name: '' });
const IsPlayer = trait();

describe('Query predicates', () => {
    const world = createWorld();
    world.init();

    beforeEach(() => {
        world.reset();
    });

    it('filters entities by trait values', () => {
        const IsHurt = createPredicate([Health], ([health]) => health.value < 50);

        const healthy = world.spawn(Health({ value: 80 }));
        const hurt = world.spawn(Health({ value: 10 }));
        world.spawn(Position);

        const entities = world.query(IsHurt);
        expect(entities).toContain(hurt);
        expect(entities).not.toContain(healthy);
        expect(entities.length).toBe(1);
    });

    it('returns a distinct instance on every call', () => {
        const first = createPredicate([Health], ([health]) => health.value < 50);
        const second = createPredicate([Health], ([health]) => health.value < 50);

        expect(first).not.toBe(second);
        expect(first.id).not.toBe(second.id);

        const entity = world.spawn(Health({ value: 10 }));
        expect(world.query(first)).toContain(entity);
        expect(world.query(second)).toContain(entity);
        expect(world.query(first, second)).toContain(entity);
    });

    it('throws when a tag is used as a dependency', () => {
        expect(() => createPredicate([IsPlayer], () => true)).toThrow(
            /Predicates cannot depend on tag traits/
        );
    });

    it('throws when a relation is used as a dependency', () => {
        const ChildOf = relation();
        expect(() => createPredicate([ChildOf as any], () => true)).toThrow(
            /Predicates cannot depend on relations/
        );

        const parent = world.spawn();
        expect(() => createPredicate([ChildOf(parent) as any], () => true)).toThrow(
            /Predicates cannot depend on relations/
        );
    });

    it('receives dependency trait data in declaration order', () => {
        const seen: unknown[] = [];
        const Match = createPredicate([Position, Health], (data) => {
            seen.push(data);
            return data[0].x > 0 && data[1].value === 3;
        });

        world.spawn(Position({ x: 5, y: 1 }), Health({ value: 3 }));
        expect(world.query(Match).length).toBe(1);
        expect(seen[0]).toEqual([{ x: 5, y: 1 }, { value: 3 }]);
    });

    it('re-evaluates when a dependency is added or set', () => {
        const IsHurt = createPredicate([Health], ([health]) => health.value < 50);
        const entity = world.spawn(Position);

        expect(world.query(IsHurt).length).toBe(0);

        entity.add(Health({ value: 10 }));
        expect(world.query(IsHurt)).toEqual(expect.arrayContaining([entity]));

        entity.set(Health, { value: 90 });
        expect(world.query(IsHurt).length).toBe(0);

        entity.set(Health, { value: 1 });
        expect(world.query(IsHurt)).toEqual(expect.arrayContaining([entity]));
    });

    it('Not(predicate) matches missing dependencies or a false result', () => {
        const IsHurt = createPredicate([Health], ([health]) => health.value < 50);

        const missing = world.spawn(Position);
        const healthy = world.spawn(Health({ value: 80 }));
        const hurt = world.spawn(Health({ value: 10 }));

        const entities = world.query(Not(IsHurt));
        expect(entities).toContain(missing);
        expect(entities).toContain(healthy);
        expect(entities).not.toContain(hurt);
    });

    it('Or accepts predicates', () => {
        const IsHurt = createPredicate([Health], ([health]) => health.value < 50);
        const IsFar = createPredicate([Position], ([position]) => position.x > 10);

        const hurt = world.spawn(Health({ value: 1 }));
        const far = world.spawn(Position({ x: 20, y: 0 }));
        const neither = world.spawn(Health({ value: 100 }), Position({ x: 0, y: 0 }));

        const entities = world.query(Or(IsHurt, IsFar));
        expect(entities).toContain(hurt);
        expect(entities).toContain(far);
        expect(entities).not.toContain(neither);
    });

    it('Added(predicate) matches newly satisfying entities', () => {
        const Added = createAdded();
        const IsHurt = createPredicate([Health], ([health]) => health.value < 50);
        const entity = world.spawn(Health({ value: 80 }));

        expect(world.query(Added(IsHurt)).length).toBe(0);

        entity.set(Health, { value: 10 });
        expect(world.query(Added(IsHurt))).toEqual(expect.arrayContaining([entity]));
        expect(world.query(Added(IsHurt)).length).toBe(0);
    });

    it('Removed(predicate) matches a transition to false', () => {
        const Removed = createRemoved();
        const IsHurt = createPredicate([Health], ([health]) => health.value < 50);
        const entity = world.spawn(Health({ value: 10 }));

        expect(world.query(Removed(IsHurt)).length).toBe(0);

        entity.set(Health, { value: 80 });
        expect(world.query(Removed(IsHurt))).toEqual(expect.arrayContaining([entity]));
        expect(world.query(Removed(IsHurt)).length).toBe(0);
    });

    it('Changed(predicate) matches any truthiness transition', () => {
        const Changed = createChanged();
        const IsHurt = createPredicate([Health], ([health]) => health.value < 50);
        const entity = world.spawn(Health({ value: 80 }));

        expect(world.query(Changed(IsHurt)).length).toBe(0);

        entity.set(Health, { value: 10 });
        expect(world.query(Changed(IsHurt))).toEqual(expect.arrayContaining([entity]));
        expect(world.query(Changed(IsHurt)).length).toBe(0);

        entity.set(Health, { value: 90 });
        expect(world.query(Changed(IsHurt))).toEqual(expect.arrayContaining([entity]));
    });

    it('does not add predicate data to the updateEach callback tuple', () => {
        const IsHurt = createPredicate([Health], ([health]) => health.value < 50);
        world.spawn(Health({ value: 10 }), Name({ name: 'hero' }));

        let tupleLength = -1;
        world.query(IsHurt, Name).updateEach((state) => {
            tupleLength = state.length;
            expect(state[0]).toHaveProperty('name', 'hero');
        });

        expect(tupleLength).toBe(1);
    });

    it('defers re-evaluation until updateEach finishes', () => {
        const IsFar = createPredicate([Position], ([position]) => position.x > 10);
        const entity = world.spawn(Position({ x: 0, y: 0 }));

        expect(world.query(IsFar).length).toBe(0);

        world.query(Position).updateEach(([position]) => {
            position.x = 20;
            expect(world.query(IsFar).length).toBe(0);
            expect(world.query(IsFar)).not.toContain(entity);
        });

        expect(world.query(IsFar)).toEqual(expect.arrayContaining([entity]));
    });

    it('defers add() during updateEach', () => {
        const IsHurt = createPredicate([Health], ([health]) => health.value < 50);
        const entity = world.spawn(Position);

        world.query(Position).updateEach((_, current) => {
            current.add(Health({ value: 10 }));
            expect(world.query(IsHurt).length).toBe(0);
        });

        expect(world.query(IsHurt)).toContain(entity);
    });

    it('composes with relation pairs', () => {
        const ChildOf = relation();
        const IsHurt = createPredicate([Health], ([health]) => health.value < 50);

        const parentA = world.spawn();
        const parentB = world.spawn();
        const childA = world.spawn(Health({ value: 10 }), ChildOf(parentA));
        const childB = world.spawn(Health({ value: 10 }), ChildOf(parentB));
        const healthy = world.spawn(Health({ value: 90 }), ChildOf(parentA));

        const entities = world.query(IsHurt, ChildOf(parentA));
        expect(entities).toContain(childA);
        expect(entities).not.toContain(childB);
        expect(entities).not.toContain(healthy);
    });

    it('leaves the query when a dependency is removed', () => {
        const IsHurt = createPredicate([Health], ([health]) => health.value < 50);
        const entity = world.spawn(Health({ value: 10 }));

        expect(world.query(IsHurt)).toContain(entity);

        entity.remove(Health);
        expect(world.query(IsHurt).length).toBe(0);
        expect(world.query(Not(IsHurt))).toContain(entity);
    });

    it('supports multiple dependencies', () => {
        const InRange = createPredicate(
            [Position, Health],
            ([position, health]) => position.x < 5 && health.value > 0
        );

        const match = world.spawn(Position({ x: 1, y: 0 }), Health({ value: 10 }));
        const tooFar = world.spawn(Position({ x: 9, y: 0 }), Health({ value: 10 }));
        const dead = world.spawn(Position({ x: 1, y: 0 }), Health({ value: 0 }));

        const entities = world.query(InRange);
        expect(entities).toContain(match);
        expect(entities).not.toContain(tooFar);
        expect(entities).not.toContain(dead);
    });
});
