import { beforeEach, describe, expect, it } from 'vitest';
import {
    createTraitRegistry,
    createWorld,
    diffEntitySnapshots,
    diffWorldSnapshots,
    relation,
    trait,
    type World,
} from '../src';

const Position = trait({ x: 0, y: 0 });
const Tag = trait();
const Likes = relation({ store: { amount: 0 } });
const ChildOf = relation();
const registry = createTraitRegistry(
    ['position', Position],
    ['tag', Tag],
    ['likes', Likes],
    ['childOf', ChildOf]
);

describe('Snapshot', () => {
    let world: World;
    beforeEach(() => {
        world = createWorld();
    });

    it('rejects duplicates', () => {
        expect(() => createTraitRegistry(['a', Tag], ['a', Position])).toThrow();
        expect(() => createTraitRegistry(['a', Tag], ['b', Tag])).toThrow();
        expect(() => createTraitRegistry(['a', Likes], ['b', Likes])).toThrow();
    });

    it('snapshots and rolls back entities', () => {
        const a = world.spawn(Position({ x: 1 }), Tag);
        const b = world.spawn(Likes(a, { amount: 3 }), ChildOf(a));
        expect(a.snapshot(registry)).toEqual({ id: a.id(), traits: { position: { x: 1, y: 0 }, tag: true } });
        const snap = b.snapshot(registry);
        expect(snap.relations).toEqual({
            likes: [{ targetId: a.id(), data: { amount: 3 } }],
            childOf: [{ targetId: a.id() }],
        });
        b.remove(ChildOf(a));
        b.set(Likes(a), { amount: 9 });
        b.add(Position);
        b.rollback(registry, snap);
        expect(b.snapshot(registry)).toEqual(snap);
        expect(b.has(Position)).toBe(false);
    });

    it('rolls back the world with the same ids', () => {
        const a = world.spawn(Position({ x: 1 }));
        world.spawn(Tag);
        const c = world.spawn(Likes(a, { amount: 2 }));
        const checkpoint = world.snapshot(registry);
        a.destroy();
        world.spawn(Position);
        world.spawn(Position);
        world.rollback(registry, checkpoint);
        expect(world.snapshot(registry)).toEqual(checkpoint);
        expect(world.query(Position).length).toBe(1);
        expect(diffWorldSnapshots(checkpoint, world.snapshot(registry))).toEqual({
            added: [],
            removed: [],
            changed: [],
        });
        expect(checkpoint.entities.map((e) => e.id)).toContain(c.id());
        expect(() =>
            world.rollback(registry, { entities: [{ id: 5, traits: {}, relations: { likes: [{ targetId: 99 }] } }] })
        ).toThrow();
    });

    it('diffs', () => {
        expect(
            diffEntitySnapshots(
                { id: 1, traits: { a: true, b: { x: 1 } } },
                { id: 1, traits: { b: { x: 2 }, c: true } }
            )
        ).toEqual({ addedTraits: ['c'], removedTraits: ['a'], changedTraits: ['b'] });
        expect(
            diffWorldSnapshots(
                { entities: [{ id: 1, traits: {}, relations: {} }, { id: 2, traits: {} }] },
                { entities: [{ id: 1, traits: {} }, { id: 3, traits: {} }] }
            )
        ).toEqual({ added: [3], removed: [2], changed: [] });
        expect(() => diffWorldSnapshots(null as any, { entities: [] })).toThrow();
    });
});
