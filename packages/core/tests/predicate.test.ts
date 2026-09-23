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
const Velocity = trait(() => ({ x: 0, y: 0 }));
const Tag = trait();
const ChildOf = relation();

describe('Predicates', () => {
    const world = createWorld();
    world.init();

    beforeEach(() => {
        world.reset();
    });

    it('filters entities by trait values', () => {
        const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);

        const a = world.spawn(Health({ value: 10 }));
        world.spawn(Health({ value: 50 }));
        world.spawn();

        expect([...world.query(IsLowHealth)]).toEqual([a]);
    });

    it('passes each dependency value in order', () => {
        const seen: unknown[][] = [];
        const Check = createPredicate([Health, Velocity], (values) => {
            seen.push(values.map((v) => ({ ...v })));
            return true;
        });

        world.spawn(Health({ value: 5 }), Velocity({ x: 1, y: 2 }));
        world.query(Check);

        expect(seen.at(-1)).toEqual([{ value: 5 }, { x: 1, y: 2 }]);
    });

    it('returns a distinct predicate for every call', () => {
        const fn = ([h]: [{ value: number }]) => h.value < 20;
        const A = createPredicate([Health], fn);
        const B = createPredicate([Health], fn);
        expect(A).not.toBe(B);

        world.spawn(Health({ value: 10 }));
        expect(world.query(A).length).toBe(1);
        expect(world.query(B).length).toBe(1);
    });

    it('throws for tag and relation dependencies', () => {
        expect(() => createPredicate([Tag] as any, () => true)).toThrow();
        expect(() => createPredicate([ChildOf] as any, () => true)).toThrow();
    });

    it('re-evaluates on set and add', () => {
        const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);
        const e = world.spawn();

        expect([...world.query(IsLowHealth)]).toEqual([]);

        e.add(Health({ value: 5 }));
        expect([...world.query(IsLowHealth)]).toEqual([e]);

        e.set(Health, { value: 50 });
        expect([...world.query(IsLowHealth)]).toEqual([]);

        e.set(Health, (prev) => ({ value: prev.value - 40 }));
        expect([...world.query(IsLowHealth)]).toEqual([e]);

        e.remove(Health);
        expect([...world.query(IsLowHealth)]).toEqual([]);
    });

    it('requires every dependency to be present', () => {
        const Tanky = createPredicate([Health, Armor], ([h, a]) => h.value + a.value > 100);
        const e = world.spawn(Health({ value: 90 }));

        expect([...world.query(Tanky)]).toEqual([]);
        e.add(Armor({ value: 20 }));
        expect([...world.query(Tanky)]).toEqual([e]);
    });

    it('supports Not with missing dependencies', () => {
        const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);

        const low = world.spawn(Health({ value: 5 }));
        const high = world.spawn(Health({ value: 50 }));
        const none = world.spawn();

        expect([...world.query(Not(IsLowHealth))]).toEqual([high, none]);

        low.set(Health, { value: 60 });
        expect([...world.query(Not(IsLowHealth))]).toEqual([high, none, low]);
    });

    it('supports Or', () => {
        const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);
        const IsArmored = createPredicate([Armor], ([armor]) => armor.value > 10);

        const a = world.spawn(Health({ value: 5 }));
        const b = world.spawn(Armor({ value: 50 }));
        world.spawn(Health({ value: 50 }), Armor({ value: 0 }));

        expect([...world.query(Or(IsLowHealth, IsArmored))]).toEqual([a, b]);
    });

    it('supports Added, Removed and Changed', () => {
        const Added = createAdded();
        const Removed = createRemoved();
        const Changed = createChanged();
        const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);

        const e = world.spawn(Health({ value: 50 }));

        expect([...world.query(Added(IsLowHealth))]).toEqual([]);
        expect([...world.query(Removed(IsLowHealth))]).toEqual([]);
        expect([...world.query(Changed(IsLowHealth))]).toEqual([]);

        e.set(Health, { value: 10 });
        expect([...world.query(Added(IsLowHealth))]).toEqual([e]);
        expect([...world.query(Added(IsLowHealth))]).toEqual([]);
        expect([...world.query(Removed(IsLowHealth))]).toEqual([]);
        expect([...world.query(Changed(IsLowHealth))]).toEqual([e]);
        expect([...world.query(Changed(IsLowHealth))]).toEqual([]);

        // Staying true is not a transition.
        e.set(Health, { value: 5 });
        expect([...world.query(Added(IsLowHealth))]).toEqual([]);
        expect([...world.query(Changed(IsLowHealth))]).toEqual([]);

        e.set(Health, { value: 80 });
        expect([...world.query(Added(IsLowHealth))]).toEqual([]);
        expect([...world.query(Removed(IsLowHealth))]).toEqual([e]);
        expect([...world.query(Removed(IsLowHealth))]).toEqual([]);
        expect([...world.query(Changed(IsLowHealth))]).toEqual([e]);
        expect([...world.query(Changed(IsLowHealth))]).toEqual([]);

        // Losing a dependency is a transition to false.
        e.set(Health, { value: 1 });
        world.query(Removed(IsLowHealth));
        world.query(Changed(IsLowHealth));
        e.remove(Health);
        expect([...world.query(Removed(IsLowHealth))]).toEqual([e]);
        expect([...world.query(Changed(IsLowHealth))]).toEqual([e]);
    });

    it('adds no data to the callback tuple', () => {
        const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);
        world.spawn(Health({ value: 5 }), Armor({ value: 3 }));

        world.query(IsLowHealth, Armor).readEach((state) => {
            expect(state.length).toBe(1);
            expect(state[0]).toEqual({ value: 3 });
        });
    });

    it('defers re-evaluation during updateEach until iteration ends', () => {
        const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);
        const a = world.spawn(Health({ value: 50 }));
        const b = world.spawn(Health({ value: 50 }));

        world.query(Health).updateEach(([health], entity) => {
            health.value = 10;
            if (entity === a) {
                b.set(Health, { value: 5 });
                expect([...world.query(IsLowHealth)]).toEqual([]);
            }
        });

        expect([...world.query(IsLowHealth)]).toEqual([a, b]);
    });

    it('composes with relation pairs', () => {
        const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);
        const parent = world.spawn();
        const child = world.spawn(Health({ value: 5 }), ChildOf(parent));
        world.spawn(Health({ value: 5 }));
        world.spawn(Health({ value: 50 }), ChildOf(parent));

        expect([...world.query(IsLowHealth, ChildOf(parent))]).toEqual([child]);
    });

    it('handles entity destruction', () => {
        const Removed = createRemoved();
        const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);
        const e = world.spawn(Health({ value: 5 }));

        expect([...world.query(IsLowHealth)]).toEqual([e]);
        world.query(Removed(IsLowHealth));

        e.destroy();
        expect([...world.query(IsLowHealth)]).toEqual([]);
        expect([...world.query(Removed(IsLowHealth))]).toEqual([e]);
    });
});
