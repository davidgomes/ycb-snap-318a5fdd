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

const Health = trait({ hp: 0 });
const Speed = trait({ value: 0 });
const Position = trait({ x: 0, y: 0 });
const Tag = trait();

describe('createPredicate', () => {
    const world = createWorld();
    world.init();

    beforeEach(() => {
        world.reset();
    });

    it('returns a distinct instance per call', () => {
        const a = createPredicate([Health], ([health]) => health.hp > 0);
        const b = createPredicate([Health], ([health]) => health.hp > 0);
        expect(a).not.toBe(b);
    });

    it('rejects tags and relations as dependencies', () => {
        const ChildOf = relation();
        expect(() => createPredicate([Tag], () => true)).toThrow(/tags or relations/);
        expect(() => createPredicate([ChildOf as never], () => true)).toThrow(/tags or relations/);
    });

    it('matches entities by dependency values and re-evaluates on add and set', () => {
        const isHurt = createPredicate([Health], ([health]) => health.hp > 0 && health.hp < 50);
        const entity = world.spawn(Health({ hp: 100 }));

        expect(world.query(isHurt)).toHaveLength(0);

        entity.set(Health, { hp: 10 });
        expect(world.query(isHurt)).toContain(entity);

        entity.set(Health, { hp: 80 });
        expect(world.query(isHurt)).toHaveLength(0);

        const other = world.spawn();
        other.add(Health({ hp: 20 }));
        expect(world.query(isHurt)).toContain(other);
    });

    it('passes dependency data in order and adds nothing to the callback tuple', () => {
        const seen: number[][] = [];
        const both = createPredicate([Health, Speed], ([health, speed]) => {
            seen.push([health.hp, speed.value]);
            return health.hp > 0 && speed.value > 0;
        });

        const entity = world.spawn(Position({ x: 1, y: 2 }), Health({ hp: 5 }), Speed({ value: 3 }));
        const tuples: number[][] = [];
        world.query(Position, both).updateEach(([pos]) => {
            tuples.push([pos.x, pos.y]);
        });

        expect(seen.at(-1)).toEqual([5, 3]);
        expect(tuples).toEqual([[1, 2]]);
        expect(world.query(Position, both)).toContain(entity);
    });

    it('Not matches a missing dependency or a false predicate', () => {
        const isFast = createPredicate([Speed], ([speed]) => speed.value > 10);
        const slow = world.spawn(Speed({ value: 1 }));
        const fast = world.spawn(Speed({ value: 20 }));
        const bare = world.spawn();

        const matched = world.query(Not(isFast));
        expect(matched).toContain(slow);
        expect(matched).toContain(bare);
        expect(matched).not.toContain(fast);

        fast.set(Speed, { value: 0 });
        expect(world.query(Not(isFast))).toContain(fast);
    });

    it('Or accepts predicates', () => {
        const isFast = createPredicate([Speed], ([speed]) => speed.value > 10);
        const isHurt = createPredicate([Health], ([health]) => health.hp < 10);
        const fast = world.spawn(Speed({ value: 12 }));
        const hurt = world.spawn(Health({ hp: 3 }));
        const neither = world.spawn(Speed({ value: 1 }), Health({ hp: 40 }));

        const matched = world.query(Or(isFast, isHurt));
        expect(matched).toContain(fast);
        expect(matched).toContain(hurt);
        expect(matched).not.toContain(neither);
    });

    it('tracks Added, Removed, and Changed predicate transitions', () => {
        const isFast = createPredicate([Speed], ([speed]) => speed.value > 10);
        const Added = createAdded();
        const Removed = createRemoved();
        const Changed = createChanged();
        const entity = world.spawn(Speed({ value: 0 }));

        entity.set(Speed, { value: 15 });
        expect(world.query(Added(isFast))).toContain(entity);
        expect(world.query(Added(isFast))).toHaveLength(0);

        // First observation of Changed sees the false → true transition.
        expect(world.query(Changed(isFast))).toContain(entity);
        expect(world.query(Changed(isFast))).toHaveLength(0);

        entity.set(Speed, { value: 16 });
        expect(world.query(Changed(isFast))).toHaveLength(0);
        expect(world.query(Removed(isFast))).toHaveLength(0);

        entity.set(Speed, { value: 1 });
        expect(world.query(Removed(isFast))).toContain(entity);
        expect(world.query(Removed(isFast))).toHaveLength(0);

        entity.set(Speed, { value: 1 });
        const changed = createPredicate([Speed], ([speed]) => speed.value > 10);
        const ChangedAgain = createChanged();
        entity.set(Speed, { value: 20 });
        expect(world.query(ChangedAgain(changed))).toContain(entity);
        entity.set(Speed, { value: 0 });
        expect(world.query(ChangedAgain(changed))).toContain(entity);
    });

    it('defers predicate re-evaluation until updateEach finishes', () => {
        const isFast = createPredicate([Speed], ([speed]) => speed.value > 10);
        const entity = world.spawn(Position({ x: 0, y: 0 }), Speed({ value: 0 }));
        expect(world.query(isFast)).toHaveLength(0);
        const lengths: number[] = [];

        world.query(Position).updateEach(([pos], current) => {
            expect(pos.x).toBe(0);
            current.set(Speed, { value: 50 });
            lengths.push(world.query(isFast).length);
        });

        expect(lengths).toEqual([0]);
        expect(world.query(isFast)).toContain(entity);
    });

    it('composes with relation pairs', () => {
        const ChildOf = relation();
        const isFast = createPredicate([Speed], ([speed]) => speed.value > 10);
        const parent = world.spawn();
        const child = world.spawn(ChildOf(parent), Speed({ value: 4 }));
        const other = world.spawn(Speed({ value: 40 }));

        expect(world.query(ChildOf(parent), isFast)).toHaveLength(0);
        child.set(Speed, { value: 30 });
        const matched = world.query(ChildOf(parent), isFast);
        expect(matched).toContain(child);
        expect(matched).not.toContain(other);
    });
});
