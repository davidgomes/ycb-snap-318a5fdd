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
const IsPlayer = trait();

describe('Predicates', () => {
    const world = createWorld();
    world.init();

    beforeEach(() => {
        world.reset();
    });

    const isLow = createPredicate([Health], ([health]) => health.value < 10);
    const isHigh = createPredicate([Health], ([health]) => health.value > 80);

    it('returns a distinct instance from each call', () => {
        const first = createPredicate([Health], ([health]) => health.value < 10);
        const second = createPredicate([Health], ([health]) => health.value < 10);
        expect(first).not.toBe(second);

        const entity = world.spawn(Health({ value: 1 }));
        expect(world.query(first)).toContain(entity);
        expect(world.query(second)).toContain(entity);
        expect(world.query(first)).not.toBe(world.query(second));
    });

    it('throws when a dependency is a tag or a relation', () => {
        expect(() => createPredicate([IsPlayer], () => true)).toThrow(/tags/);
        expect(() => createPredicate([relation()], () => true)).toThrow(/relations/);
        const ChildOf = relation();
        const parent = world.spawn();
        expect(() => createPredicate([ChildOf(parent) as never], () => true)).toThrow(/relations/);
    });

    it('passes dependency data in dependency order', () => {
        let received: { n: number }[] | undefined;
        const A = trait({ n: 0 });
        const B = trait({ n: 0 });
        const greater = createPredicate([B, A], ([b, a]) => {
            received = [b, a];
            return b.n > a.n;
        });

        const entity = world.spawn(A({ n: 1 }), B({ n: 5 }));
        expect(world.query(greater)).toContain(entity);
        expect(received?.[0].n).toBe(5);
        expect(received?.[1].n).toBe(1);
    });

    it('filters entities by predicate value and re-evaluates on set and add', () => {
        const low = world.spawn(Health({ value: 1 }));
        const high = world.spawn(Health({ value: 50 }));
        const empty = world.spawn();

        expect(world.query(isLow)).toContain(low);
        expect(world.query(isLow)).not.toContain(high);
        expect(world.query(isLow)).not.toContain(empty);

        high.set(Health, { value: 3 });
        expect(world.query(isLow)).toContain(high);

        low.set(Health, { value: 40 });
        expect(world.query(isLow)).not.toContain(low);

        empty.add(Health({ value: 2 }));
        expect(world.query(isLow)).toContain(empty);

        empty.remove(Health);
        expect(world.query(isLow)).not.toContain(empty);
    });

    it('Not(predicate) matches entities missing a dependency or failing the predicate', () => {
        const low = world.spawn(Health({ value: 1 }));
        const high = world.spawn(Health({ value: 50 }));
        const empty = world.spawn();

        const excluded = world.query(Not(isLow));
        expect(excluded).toContain(high);
        expect(excluded).toContain(empty);
        expect(excluded).not.toContain(low);

        high.set(Health, { value: 1 });
        expect(world.query(Not(isLow))).not.toContain(high);
        expect(world.query(isLow)).toContain(high);
    });

    it('Or accepts predicates and traits', () => {
        const low = world.spawn(Health({ value: 1 }));
        const high = world.spawn(Health({ value: 90 }));
        const mid = world.spawn(Health({ value: 50 }));
        const player = world.spawn(IsPlayer, Health({ value: 50 }));

        const either = world.query(Or(isLow, isHigh));
        expect(either).toContain(low);
        expect(either).toContain(high);
        expect(either).not.toContain(mid);

        const lowOrPlayer = world.query(Or(isLow, IsPlayer));
        expect(lowOrPlayer).toContain(low);
        expect(lowOrPlayer).toContain(player);
        expect(lowOrPlayer).not.toContain(mid);
    });

    it('tracks Added, Removed, and Changed against predicate truth', () => {
        const Added = createAdded();
        const Removed = createRemoved();
        const Changed = createChanged();
        const entity = world.spawn(Health({ value: 1 }));

        expect(world.query(Added(isLow))).toContain(entity);
        expect(world.query(Added(isLow))).toHaveLength(0);
        expect(world.query(Removed(isLow))).toHaveLength(0);
        expect(world.query(Changed(isLow))).toHaveLength(0);

        entity.set(Health, { value: 2 });
        expect(world.query(Changed(isLow))).toHaveLength(0);
        expect(world.query(Added(isLow))).toHaveLength(0);

        entity.set(Health, { value: 50 });
        expect(world.query(Removed(isLow))).toContain(entity);
        expect(world.query(Changed(isLow))).toContain(entity);
        expect(world.query(Added(isLow))).toHaveLength(0);
        expect(world.query(isLow)).not.toContain(entity);

        entity.set(Health, { value: 1 });
        expect(world.query(Added(isLow))).toContain(entity);
        expect(world.query(Changed(isLow))).toContain(entity);
        expect(world.query(Removed(isLow))).toHaveLength(0);
    });

    it('Changed(predicate) keeps a truthiness transition that flips back before it is read', () => {
        const Changed = createChanged();
        const entity = world.spawn(Health({ value: 1 }));

        world.query(Changed(isLow));
        entity.set(Health, { value: 50 });
        entity.set(Health, { value: 1 });
        expect(world.query(Changed(isLow))).toContain(entity);
        expect(world.query(Changed(isLow))).toHaveLength(0);
    });

    it('Added and Removed trackers are independent per predicate and per factory', () => {
        const Added = createAdded();
        const AddedLater = createAdded();
        const entity = world.spawn(Health({ value: 50 }));

        expect(world.query(Added(isLow))).toHaveLength(0);
        entity.set(Health, { value: 1 });
        expect(world.query(Added(isLow))).toContain(entity);
        expect(world.query(AddedLater(isLow))).toContain(entity);
        expect(world.query(Added(isHigh))).toHaveLength(0);
    });

    it('does not add predicate data to the callback tuple', () => {
        const entity = world.spawn(Health({ value: 1 }), Position({ x: 3, y: 4 }));

        world.query(isLow).updateEach((state, matched) => {
            expect(state).toEqual([]);
            expect(matched).toBe(entity);
        });

        world.query(Health, isLow).updateEach((state) => {
            expect(state).toHaveLength(1);
            expect(state[0].value).toBe(1);
        });

        world.query(Position, Not(isHigh)).updateEach((state) => {
            expect(state).toHaveLength(1);
            expect(state[0]).toMatchObject({ x: 3, y: 4 });
        });
    });

    it('defers predicate re-evaluation until updateEach finishes', () => {
        const a = world.spawn(Health({ value: 50 }));
        const b = world.spawn(Health({ value: 50 }));
        const seen: number[] = [];

        world.query(Health).updateEach(([health]) => {
            health.value = 1;
            seen.push(world.query(isLow).length);
        });

        expect(seen).toEqual([0, 0]);
        expect(world.query(isLow)).toContain(a);
        expect(world.query(isLow)).toContain(b);

        const anchor = world.spawn(Position({ x: 0, y: 0 }));
        const other = world.spawn(Position({ x: 1, y: 1 }), Health({ value: 50 }));
        let mid = -1;
        world.query(Position).updateEach((_, entity) => {
            if (entity === other) entity.set(Health, { value: 1 });
            mid = world.query(isLow).filter((candidate) => candidate === other).length;
        });
        expect(mid).toBe(0);
        expect(world.query(isLow)).toContain(other);
        expect(anchor).toBeDefined();
    });

    it('composes with relation pairs', () => {
        const ChildOf = relation();
        const parent = world.spawn();
        const otherParent = world.spawn();
        const child = world.spawn(ChildOf(parent), Health({ value: 1 }));
        const sibling = world.spawn(ChildOf(otherParent), Health({ value: 1 }));
        const healthyChild = world.spawn(ChildOf(parent), Health({ value: 50 }));

        const children = world.query(ChildOf(parent), isLow);
        expect(children).toContain(child);
        expect(children).not.toContain(sibling);
        expect(children).not.toContain(healthyChild);

        healthyChild.set(Health, { value: 4 });
        expect(world.query(ChildOf(parent), isLow)).toContain(healthyChild);

        child.remove(ChildOf(parent));
        expect(world.query(ChildOf(parent), isLow)).not.toContain(child);

        const Added = createAdded();
        const late = world.spawn(Health({ value: 1 }));
        expect(world.query(Added(isLow), ChildOf(parent))).not.toContain(late);
        late.add(ChildOf(parent));
        expect(world.query(Added(isLow), ChildOf(parent))).toContain(late);
    });

    it('supports AoS dependency data', () => {
        const Shield = trait(() => ({ value: 0 }));
        const exposed = createPredicate([Shield], ([shield]) => shield.value <= 0);
        const entity = world.spawn(Shield({ value: 0 }));
        expect(world.query(exposed)).toContain(entity);
        entity.set(Shield, { value: 5 });
        expect(world.query(exposed)).not.toContain(entity);
    });
});
