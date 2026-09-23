import { beforeEach, describe, expect, it } from 'vitest';
import { createWorld, relation, trait } from '../src';

describe('Deferred', () => {
    const world = createWorld();
    const Position = trait({ x: 0, y: 0 });
    const Tag = trait();

    beforeEach(() => world.reset());

    it('batches mutations during updateEach', () => {
        const a = world.spawn(Position);
        const b = world.spawn(Position);
        let spawned: any;
        world.query(Position).updateEach((_, e) => {
            world.deferred.add(e, Tag);
            if (e === a) spawned = world.deferred.spawn(Position({ x: 5 }));
            expect(world.query(Tag).length).toBe(0);
        });
        expect(a.has(Tag) && b.has(Tag)).toBe(true);
        expect(spawned.get(Position).x).toBe(5);
        expect(world.query(Position).length).toBe(3);
    });

    it('has/get reflect pending state and later values win', () => {
        const e = world.spawn();
        world.deferred.add(e, Position({ x: 1 }), Position({ x: 2 }));
        expect(e.has(Position)).toBe(true);
        expect(e.get(Position)).toEqual({ x: 2, y: 0 });
        expect(world.query(Position).length).toBe(0);
        world.deferred.flush();
        expect(e.get(Position)).toEqual({ x: 2, y: 0 });
    });

    it('fires subscriptions by net state difference', () => {
        let added = 0;
        let removed = 0;
        world.onAdd(Tag, () => added++);
        world.onRemove(Tag, () => removed++);
        const e = world.spawn();
        world.deferred.add(e, Tag);
        world.deferred.remove(e, Tag);
        world.deferred.add(e, Tag);
        world.deferred.flush();
        expect([added, removed]).toEqual([1, 0]);
    });

    it('nullifies spawn-destroy and skips destroyed entities', () => {
        const count = world.entities.length;
        const e = world.deferred.spawn(Position);
        world.deferred.destroy(e);
        const f = world.spawn();
        world.deferred.add(f, Tag);
        f.destroy();
        world.deferred.flush();
        expect(world.entities.length).toBe(count);
    });

    it('flushes on non-deferred mutation', () => {
        const e = world.spawn();
        world.deferred.add(e, Position({ x: 3 }));
        e.add(Tag);
        expect(world.query(Position, Tag).length).toBe(1);
    });

    it('addExclusive replaces pairs and wildcard clears', () => {
        const Likes = relation();
        const x = world.spawn();
        const y = world.spawn();
        const z = world.spawn();
        const e = world.spawn(Likes(x), Likes(y));
        let removed = 0;
        world.onRemove(Likes, () => removed++);
        world.deferred.addExclusive(e, Likes(y));
        world.deferred.add(e, Likes(z));
        world.deferred.addExclusive(e, Likes(y));
        expect(e.has(Likes(x))).toBe(false);
        expect(e.has(Likes(y))).toBe(true);
        world.deferred.flush();
        expect(e.targetsFor(Likes)).toEqual([y]);
        expect(removed).toBe(1);
        world.deferred.remove(e, Likes('*'));
        world.deferred.flush();
        expect(e.targetsFor(Likes)).toEqual([]);
    });

    it('throws when destroying the world entity on execution', () => {
        world.deferred.destroy(world.entities[0]);
        expect(() => world.deferred.flush()).toThrow();
    });

    it('inner scopes flush independently', () => {
        const outer = world.spawn(Position);
        const inner = world.spawn(Tag);
        world.query(Position).updateEach((_, e) => {
            world.deferred.add(e, Tag);
            world.query(Tag).updateEach((_, t) => {
                world.deferred.add(t, Position);
            });
            expect(inner.has(Position)).toBe(true);
            expect(world.query(Position, Tag).length).toBe(1);
        });
        expect(outer.has(Tag)).toBe(true);
        expect(world.query(Position, Tag).length).toBe(2);
    });

    it('cascades autoDestroy relations', () => {
        const ChildOf = relation({ autoDestroy: 'orphan' });
        const parent = world.spawn();
        const child = world.deferred.spawn(ChildOf(parent));
        world.deferred.destroy(parent);
        world.deferred.flush();
        expect(world.has(child)).toBe(false);
    });
});
