import { beforeEach, describe, expect, it } from 'vitest';
import { $internal, createWorld, relation, trait } from '../src';

describe('Deferred commands', () => {
    beforeEach(() => {
        // Worlds are reset per test.
    });

    it('flushes queued adds in order and lets later values replace earlier ones', () => {
        const world = createWorld();
        const Position = trait({ x: 0, y: 0 });
        const Health = trait({ amount: 1 });
        const entity = world.spawn();

        world.deferred.add(entity, Position({ x: 1, y: 2 }));
        world.deferred.add(entity, Health({ amount: 4 }));
        world.deferred.add(entity, Position({ x: 3 }));

        expect(entity.has(Position)).toBe(true);
        expect(entity.get(Position)).toEqual({ x: 3, y: 0 });
        expect(entity.has(Health)).toBe(true);
        expect(world.has(Position)).toBe(false);

        world.deferred.flush();

        expect(entity.get(Position)).toEqual({ x: 3, y: 0 });
        expect(entity.get(Health)).toEqual({ amount: 4 });
    });

    it('flushes when updateEach exits', () => {
        const world = createWorld();
        const Position = trait({ x: 0 });
        const entity = world.spawn(Position);
        let during = 0;

        world.query(Position).updateEach(() => {
            world.deferred.add(entity, Position({ x: 5 }));
            during = entity.get(Position)!.x;
        });

        expect(during).toBe(5);
        expect(entity.get(Position)).toEqual({ x: 5 });
    });

    it('flushes an entity before a non-deferred mutation', () => {
        const world = createWorld();
        const Health = trait({ amount: 0 });
        const Name = trait({ value: '' });
        const entity = world.spawn();

        world.deferred.add(entity, Health({ amount: 2 }));
        entity.add(Name({ value: 'a' }));

        expect(entity.get(Health)).toEqual({ amount: 2 });
        expect(entity.get(Name)).toEqual({ value: 'a' });
    });

    it('flushes inner scopes without dropping the outer buffer', () => {
        const world = createWorld();
        const Outer = trait({ n: 0 });
        const Inner = trait({ n: 0 });
        const entity = world.spawn();

        world.deferred.add(entity, Outer({ n: 1 }));
        world.query().updateEach(() => {
            world.deferred.add(entity, Inner({ n: 2 }));
        });

        expect(entity.has(Inner)).toBe(true);
        expect(entity.get(Inner)).toEqual({ n: 2 });
        expect(entity.has(Outer)).toBe(true);
        expect(world.query(Outer).length).toBe(0);

        world.deferred.flush();
        expect(entity.get(Outer)).toEqual({ n: 1 });
    });

    it('skips commands that target entities destroyed earlier in the buffer', () => {
        const world = createWorld();
        const Tag = trait();
        const entity = world.spawn(Tag);
        const added: string[] = [];
        world.onAdd(Tag, () => added.push('add'));
        world.onRemove(Tag, () => added.push('remove'));

        world.deferred.destroy(entity);
        world.deferred.add(entity, Tag);
        world.deferred.flush();

        expect(world.has(entity)).toBe(false);
        expect(added).toEqual(['remove']);
    });

    it('nullifies spawn and destroy in the same buffer', () => {
        const world = createWorld();
        const Tag = trait();
        const events: string[] = [];
        world.onAdd(Tag, () => events.push('add'));

        const entity = world.deferred.spawn(Tag);
        expect(entity.has(Tag)).toBe(true);
        world.deferred.destroy(entity);
        expect(entity.has(Tag)).toBe(false);
        world.deferred.flush();

        expect(events).toEqual([]);
        expect(world.has(entity)).toBe(false);
    });

    it('throws when a deferred destroy of the world entity executes', () => {
        const world = createWorld();
        const worldEntity = world[$internal].worldEntity;
        world.deferred.destroy(worldEntity);
        expect(() => world.deferred.flush()).toThrow(/world entity/);
        expect(world.has(worldEntity)).toBe(true);
    });

    it('fires relation subscriptions once for the net pair diff', () => {
        const world = createWorld();
        const Likes = relation();
        const a = world.spawn();
        const b = world.spawn();
        const c = world.spawn();
        const added: number[] = [];
        const removed: number[] = [];
        world.onAdd(Likes, (_entity, target) => added.push(target));
        world.onRemove(Likes, (_entity, target) => removed.push(target));

        world.deferred.add(a, Likes(b));
        world.deferred.remove(a, Likes(b));
        world.deferred.add(a, Likes(c));
        world.deferred.flush();

        expect(added).toEqual([c]);
        expect(removed).toEqual([]);
        expect(a.has(Likes(c))).toBe(true);
        expect(a.has(Likes(b))).toBe(false);
    });

    it('addExclusive replaces pairs and wildcard clears them', () => {
        const world = createWorld();
        const Likes = relation();
        const a = world.spawn();
        const b = world.spawn();
        const c = world.spawn();
        a.add(Likes(b), Likes(c));

        const d = world.spawn();
        world.deferred.addExclusive(a, Likes(d));
        expect(a.has(Likes(b))).toBe(false);
        expect(a.has(Likes(d))).toBe(true);
        world.deferred.flush();
        expect(a.targetsFor(Likes)).toEqual([d]);

        world.deferred.addExclusive(a, Likes('*'));
        world.deferred.flush();
        expect(a.has(Likes('*'))).toBe(false);
        expect(a.targetsFor(Likes)).toEqual([]);
    });

    it('cascades autoDestroy without resurrecting a nullified spawn', () => {
        const world = createWorld();
        const ChildOf = relation({ autoDestroy: 'orphan' });
        const parent = world.spawn();
        const child = world.deferred.spawn(ChildOf(parent));
        world.deferred.destroy(child);
        world.deferred.destroy(parent);
        world.deferred.flush();

        expect(world.has(parent)).toBe(false);
        expect(world.has(child)).toBe(false);
    });

    it('cascades autoDestroy for relations added earlier in the buffer', () => {
        const world = createWorld();
        const ChildOf = relation({ autoDestroy: 'orphan' });
        const parent = world.spawn();
        const child = world.deferred.spawn(ChildOf(parent));
        world.deferred.destroy(parent);
        world.deferred.flush();

        expect(world.has(parent)).toBe(false);
        expect(world.has(child)).toBe(false);
    });
});
