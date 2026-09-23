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
const Armor = trait({ value: 0 });
const Tag = trait();

describe('Predicates', () => {
    const world = createWorld();
    world.init();

    beforeEach(() => {
        world.reset();
    });

    it('filters by value and re-evaluates on set/add', () => {
        const IsLow = createPredicate([Health], ([h]) => h.value < 20);
        const a = world.spawn(Health({ value: 10 }));
        const b = world.spawn(Health({ value: 50 }));
        const c = world.spawn();

        expect([...world.query(IsLow)]).toEqual([a]);
        b.set(Health, { value: 5 });
        expect(world.query(IsLow)).toContain(b);
        c.add(Health({ value: 1 }));
        expect(world.query(IsLow).length).toBe(3);
    });

    it('passes dependency data in order and adds nothing to the tuple', () => {
        const Tough = createPredicate([Health, Armor], ([h, a]) => h.value + a.value > 100);
        const e = world.spawn(Health({ value: 90 }), Armor({ value: 20 }));
        world.spawn(Health({ value: 90 }));
        const results: unknown[] = [];
        world.query(Health, Tough).updateEach((state) => results.push(state.length));
        expect(results).toEqual([1]);
        expect([...world.query(Tough)]).toEqual([e]);
    });

    it('returns distinct instances and rejects tags and relations', () => {
        const fn = () => true;
        expect(createPredicate([Health], fn)).not.toBe(createPredicate([Health], fn));
        expect(() => createPredicate([Tag], fn)).toThrow();
        expect(() => createPredicate([relation() as any], fn)).toThrow();
    });

    it('supports Not and Or', () => {
        const IsLow = createPredicate([Health], ([h]) => h.value < 20);
        const HasArmor = createPredicate([Armor], ([a]) => a.value > 0);
        const low = world.spawn(Health({ value: 10 }));
        const high = world.spawn(Health({ value: 50 }));
        const none = world.spawn();
        const armored = world.spawn(Armor({ value: 5 }));

        const notLow = world.query(Not(IsLow));
        expect(notLow).toContain(high);
        expect(notLow).toContain(none);
        expect(notLow).not.toContain(low);

        const either = world.query(Or(IsLow, HasArmor));
        expect([...either].sort()).toEqual([low, armored].sort());
    });

    it('supports Added, Removed and Changed', () => {
        const Added = createAdded();
        const Removed = createRemoved();
        const Changed = createChanged();
        const IsLow = createPredicate([Health], ([h]) => h.value < 20);
        const e = world.spawn(Health({ value: 10 }));

        expect([...world.query(Added(IsLow))]).toEqual([e]);
        expect([...world.query(Added(IsLow))]).toEqual([]);
        world.query(Removed(IsLow));
        world.query(Changed(IsLow));

        e.set(Health, { value: 50 });
        expect([...world.query(Removed(IsLow))]).toEqual([e]);
        expect([...world.query(Changed(IsLow))]).toEqual([e]);
        expect([...world.query(Changed(IsLow))]).toEqual([]);
        expect([...world.query(Added(IsLow))]).toEqual([]);

        e.set(Health, { value: 5 });
        expect([...world.query(Added(IsLow))]).toEqual([e]);
        expect([...world.query(Changed(IsLow))]).toEqual([e]);
    });

    it('defers re-evaluation during updateEach', () => {
        const IsLow = createPredicate([Health], ([h]) => h.value < 20);
        world.spawn(Health({ value: 10 }));
        world.spawn(Health({ value: 15 }));
        let count = 0;
        world.query(Health, IsLow).updateEach(([h]) => {
            h.value = 100;
            count++;
        });
        expect(count).toBe(2);
        expect([...world.query(IsLow)]).toEqual([]);
    });

    it('composes with relation pairs', () => {
        const ChildOf = relation();
        const IsLow = createPredicate([Health], ([h]) => h.value < 20);
        const parent = world.spawn();
        const a = world.spawn(ChildOf(parent), Health({ value: 10 }));
        world.spawn(ChildOf(parent), Health({ value: 50 }));
        world.spawn(Health({ value: 5 }));
        expect([...world.query(ChildOf(parent), IsLow)]).toEqual([a]);
    });
});
