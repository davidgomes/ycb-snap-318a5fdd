import { beforeEach, describe, expect, it } from 'vitest';
import {
    Not,
    Or,
    createAdded,
    createChanged,
    createPredicate,
    createRemoved,
    createWorld,
    relation,
    trait,
} from '../src';

const ids = (entities: readonly number[]) => [...entities];

const Health = trait({ hp: 0 });
const Mana = trait({ mp: 0 });
const Marker = trait({ n: 0 });
const IsBoss = trait();

describe('createPredicate', () => {
    const world = createWorld();
    world.init();

    beforeEach(() => {
        world.reset();
    });

    it('returns a distinct predicate from each call', () => {
        const first = createPredicate([Health], ([health]) => health.hp > 0);
        const second = createPredicate([Health], ([health]) => health.hp > 0);
        expect(first).not.toBe(second);
    });

    it('throws when a dependency is a tag or a relation', () => {
        expect(() => createPredicate([IsBoss], () => true)).toThrow(/tag/i);

        const ChildOf = relation();
        const parent = world.spawn();
        expect(() => createPredicate([ChildOf as never], () => true)).toThrow(/relation/i);
        expect(() => createPredicate([ChildOf(parent) as never], () => true)).toThrow(/relation/i);
    });

    it('passes dependency data as one array, in order', () => {
        const seen: unknown[] = [];
        const ready = createPredicate([Health, Mana], (data) => {
            seen.push(data);
            return data[0].hp > 0 && data[1].mp > 0;
        });

        const entity = world.spawn(Health({ hp: 4 }), Mana({ mp: 9 }));
        const matched = world.query(ready);

        expect(ids(matched)).toEqual([entity]);
        expect(seen).toEqual([[{ hp: 4 }, { mp: 9 }]]);
        expect(Array.isArray(seen[0])).toBe(true);
    });

    it('matches on add and set, and is false when a dependency is missing', () => {
        const alive = createPredicate([Health], ([health]) => health.hp > 0);
        const entity = world.spawn();

        expect(ids(world.query(alive))).toEqual([]);
        expect(ids(world.query(Not(alive)))).toEqual([entity]);

        entity.add(Health({ hp: 0 }));
        expect(ids(world.query(alive))).toEqual([]);
        expect(ids(world.query(Not(alive)))).toEqual([entity]);

        entity.set(Health, { hp: 12 });
        expect(ids(world.query(alive))).toEqual([entity]);
        expect(ids(world.query(Not(alive)))).toEqual([]);

        entity.set(Health, { hp: 0 });
        expect(ids(world.query(alive))).toEqual([]);
        expect(ids(world.query(Not(alive)))).toEqual([entity]);

        entity.remove(Health);
        expect(ids(world.query(Not(alive)))).toEqual([entity]);
    });

    it('accepts predicates in Or, including alongside traits', () => {
        const alive = createPredicate([Health], ([health]) => health.hp > 0);
        const boss = world.spawn(IsBoss);
        const living = world.spawn(Health({ hp: 3 }));
        const dead = world.spawn(Health({ hp: 0 }));

        expect(ids(world.query(Or(IsBoss, alive)))).toEqual([boss, living]);
        expect(ids(world.query(Or(alive, IsBoss)))).toEqual([boss, living]);
        expect(world.query(Or(IsBoss, alive)).includes(dead)).toBe(false);
    });

    it('tracks Added, Removed, and Changed against predicate truth', () => {
        const alive = createPredicate([Health], ([health]) => health.hp > 0);
        const Added = createAdded();
        const Removed = createRemoved();
        const Changed = createChanged();
        const entity = world.spawn(Health({ hp: 0 }));

        expect(ids(world.query(Added(alive)))).toEqual([]);
        expect(ids(world.query(Removed(alive)))).toEqual([]);
        expect(ids(world.query(Changed(alive)))).toEqual([]);

        entity.set(Health, { hp: 5 });
        expect(ids(world.query(Added(alive)))).toEqual([entity]);
        expect(ids(world.query(Changed(alive)))).toEqual([entity]);
        expect(ids(world.query(Removed(alive)))).toEqual([]);

        // Drained on read.
        expect(ids(world.query(Added(alive)))).toEqual([]);
        expect(ids(world.query(Changed(alive)))).toEqual([]);

        entity.set(Health, { hp: 0 });
        expect(ids(world.query(Removed(alive)))).toEqual([entity]);
        expect(ids(world.query(Changed(alive)))).toEqual([entity]);
        expect(ids(world.query(Added(alive)))).toEqual([]);

        expect(ids(world.query(Removed(alive)))).toEqual([]);
    });

    it('includes entities that already satisfy Added(predicate) on the first result', () => {
        const alive = createPredicate([Health], ([health]) => health.hp > 0);
        const Added = createAdded();
        const entity = world.spawn(Health({ hp: 8 }));

        expect(ids(world.query(Added(alive)))).toEqual([entity]);
        expect(ids(world.query(Added(alive)))).toEqual([]);
    });

    it('does not add predicate data to updateEach or readEach', () => {
        const alive = createPredicate([Health], ([health]) => health.hp > 0);
        const entity = world.spawn(Health({ hp: 6 }));

        world.query(Health, alive, Not(IsBoss)).readEach((state, matched) => {
            expect(matched).toBe(entity);
            expect(state).toEqual([{ hp: 6 }]);
        });

        world.query(alive).updateEach((state) => {
            expect(state).toEqual([]);
        });
    });

    it('defers predicate re-evaluation until updateEach finishes', () => {
        const alive = createPredicate([Health], ([health]) => health.hp > 0);
        const entity = world.spawn(Marker({ n: 1 }), Health({ hp: 4 }));
        expect(world.query(alive).includes(entity)).toBe(true);
        const seen: boolean[] = [];

        world.query(Marker).updateEach((_, matched) => {
            matched.set(Health, { hp: 0 });
            seen.push(world.query(alive).includes(matched));
        });

        expect(seen).toEqual([true]);
        expect(world.query(alive).includes(entity)).toBe(false);

        const spawned = world.spawn(Marker({ n: 2 }));
        const addedDuring: boolean[] = [];
        world.query(Marker).updateEach((_, matched) => {
            if (matched !== spawned) return;
            matched.add(Health({ hp: 7 }));
            addedDuring.push(world.query(alive).includes(matched));
        });

        expect(addedDuring).toEqual([false]);
        expect(world.query(alive).includes(spawned)).toBe(true);
    });

    it('re-evaluates tuple writes when updateEach commits dependency data', () => {
        const alive = createPredicate([Health], ([health]) => health.hp > 0);
        const entity = world.spawn(Health({ hp: 4 }));
        let during = true;

        world.query(Health).updateEach(([health], matched) => {
            health.hp = 0;
            during = world.query(alive).includes(matched);
        });

        expect(during).toBe(true);
        expect(entity.get(Health)!.hp).toBe(0);
        expect(ids(world.query(alive))).toEqual([]);
    });

    it('composes with relation pairs', () => {
        const alive = createPredicate([Health], ([health]) => health.hp > 0);
        const ChildOf = relation();
        const parent = world.spawn();
        const other = world.spawn();
        const child = world.spawn(ChildOf(parent), Health({ hp: 2 }));
        world.spawn(ChildOf(other), Health({ hp: 2 }));
        world.spawn(ChildOf(parent), Health({ hp: 0 }));

        expect(ids(world.query(ChildOf(parent), alive))).toEqual([child]);

        child.set(Health, { hp: 0 });
        expect(ids(world.query(ChildOf(parent), alive))).toEqual([]);

        child.set(Health, { hp: 1 });
        expect(ids(world.query(ChildOf(parent), alive))).toEqual([child]);
    });

    it('combines a required trait with a predicate', () => {
        const alive = createPredicate([Health], ([health]) => health.hp > 0);
        const tagged = world.spawn(Marker({ n: 1 }), Health({ hp: 3 }));
        world.spawn(Health({ hp: 3 }));
        world.spawn(Marker({ n: 2 }), Health({ hp: 0 }));

        expect(ids(world.query(Marker, alive))).toEqual([tagged]);
    });

    it('accepts several predicates in one Or', () => {
        const alive = createPredicate([Health], ([health]) => health.hp > 0);
        const rich = createPredicate([Mana], ([mana]) => mana.mp > 10);
        const healthy = world.spawn(Health({ hp: 2 }));
        const wealthy = world.spawn(Mana({ mp: 20 }));
        world.spawn(Health({ hp: 0 }), Mana({ mp: 1 }));

        expect(ids(world.query(Or(alive, rich)))).toEqual([healthy, wealthy]);
    });

    it('does not treat a same-truth set as a change', () => {
        const alive = createPredicate([Health], ([health]) => health.hp > 0);
        const Changed = createChanged();
        const entity = world.spawn(Health({ hp: 4 }));

        expect(ids(world.query(Changed(alive)))).toEqual([]);
        entity.set(Health, { hp: 9 });
        expect(ids(world.query(Changed(alive)))).toEqual([]);
        entity.set(Health, { hp: 0 });
        expect(ids(world.query(Changed(alive)))).toEqual([entity]);
    });

    it('re-evaluates a set that opts out of change events', () => {
        const alive = createPredicate([Health], ([health]) => health.hp > 0);
        const entity = world.spawn(Health({ hp: 4 }));
        expect(world.query(alive).includes(entity)).toBe(true);

        entity.set(Health, { hp: 0 }, false);
        expect(ids(world.query(alive))).toEqual([]);
    });

    it('keeps Added pending when another filter excluded the entity', () => {
        const alive = createPredicate([Health], ([health]) => health.hp > 0);
        const Added = createAdded();
        const waiting = world.spawn(Health({ hp: 4 }));
        const ready = world.spawn(Marker({ n: 1 }), Health({ hp: 4 }));

        expect(ids(world.query(Marker, Added(alive)))).toEqual([ready]);
        expect(ids(world.query(Marker, Added(alive)))).toEqual([]);

        waiting.add(Marker({ n: 2 }));
        expect(ids(world.query(Marker, Added(alive)))).toEqual([waiting]);
    });

    it('matches Or of a trait Added and a predicate', () => {
        const alive = createPredicate([Health], ([health]) => health.hp > 0);
        const Added = createAdded();
        const living = world.spawn(Health({ hp: 2 }));
        const spawned = world.spawn();

        expect(ids(world.query(Or(Added(Marker), alive)))).toEqual([living]);

        spawned.add(Marker({ n: 1 }));
        expect(ids(world.query(Or(Added(Marker), alive)))).toEqual([living, spawned]);
        expect(ids(world.query(Or(Added(Marker), alive)))).toEqual([living]);
    });

    it('defers predicate updates from a relation-only updateEach', () => {
        const alive = createPredicate([Health], ([health]) => health.hp > 0);
        const ChildOf = relation();
        const parent = world.spawn();
        const child = world.spawn(ChildOf(parent), Health({ hp: 3 }));
        expect(world.query(alive).includes(child)).toBe(true);

        let during = false;
        world.query(ChildOf(parent)).updateEach((_, matched) => {
            matched.set(Health, { hp: 0 });
            during = world.query(alive).includes(matched);
        });

        expect(during).toBe(true);
        expect(world.query(alive).includes(child)).toBe(false);
    });

    it('composes Added(predicate) with a relation pair', () => {
        const alive = createPredicate([Health], ([health]) => health.hp > 0);
        const Added = createAdded();
        const ChildOf = relation();
        const parent = world.spawn();
        const child = world.spawn(ChildOf(parent), Health({ hp: 4 }));
        world.spawn(Health({ hp: 4 }));

        expect(ids(world.query(ChildOf(parent), Added(alive)))).toEqual([child]);

        // The previous result still contains child, so a flicker is not Added.
        child.set(Health, { hp: 0 });
        child.set(Health, { hp: 2 });
        expect(ids(world.query(ChildOf(parent), Added(alive)))).toEqual([]);

        // That read did not contain child. The next false -> true is Added.
        child.set(Health, { hp: 0 });
        child.set(Health, { hp: 2 });
        expect(ids(world.query(ChildOf(parent), Added(alive)))).toEqual([child]);

        const born = world.spawn(ChildOf(parent));
        born.add(Health({ hp: 1 }));
        expect(ids(world.query(ChildOf(parent), Added(alive)))).toEqual([born]);
    });

    it('includes a destroyed entity in Removed when the predicate becomes false', () => {
        const alive = createPredicate([Health], ([health]) => health.hp > 0);
        const Removed = createRemoved();
        const entity = world.spawn(Health({ hp: 5 }));

        expect(ids(world.query(Removed(alive)))).toEqual([]);
        entity.destroy();
        expect(ids(world.query(Removed(alive)))).toEqual([entity]);
        expect(world.has(entity)).toBe(false);
    });
});
