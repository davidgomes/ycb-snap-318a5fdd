import { beforeEach, describe, expect, it, vi } from 'vitest';
import { $internal, createWorld, ordered, relation, trait } from '../src';

describe('Deferred commands', () => {
    const world = createWorld();

    const Position = trait({ x: 0, y: 0 });
    const Velocity = trait({ x: 0, y: 0 });
    const Tag = trait();

    beforeEach(() => {
        world.reset();
    });

    it('defers adds until updateEach exits', () => {
        const entity = world.spawn(Position);
        let queriedDuring = -1;

        world.query(Position).updateEach((_, e) => {
            world.deferred.add(e, Velocity({ x: 1 }));
            queriedDuring = world.query(Velocity).length;
        });

        expect(queriedDuring).toBe(0);
        expect(world.query(Velocity)).toContain(entity);
        expect(entity.get(Velocity)!.x).toBe(1);
    });

    it('reports post-flush state from has and get while pending', () => {
        const entity = world.spawn(Position({ x: 5 }));

        world.deferred.add(entity, Velocity({ x: 2 }));
        world.deferred.remove(entity, Position);

        expect(entity.has(Velocity)).toBe(true);
        expect(entity.get(Velocity)).toEqual({ x: 2, y: 0 });
        expect(entity.has(Position)).toBe(false);
        expect(entity.get(Position)).toBeUndefined();

        // Not executed yet.
        expect(world.query(Position)).toContain(entity);

        world.deferred.flush();
        expect(entity.has(Velocity)).toBe(true);
        expect(entity.has(Position)).toBe(false);
    });

    it('executes commands in the order they were deferred', () => {
        const entity = world.spawn();

        world.deferred.add(entity, Tag);
        world.deferred.remove(entity, Tag);
        world.deferred.flush();
        expect(entity.has(Tag)).toBe(false);

        world.deferred.remove(entity, Tag);
        world.deferred.add(entity, Tag);
        world.deferred.flush();
        expect(entity.has(Tag)).toBe(true);
    });

    it('replaces earlier values for the same trait with later ones', () => {
        const entity = world.spawn();

        world.deferred.add(entity, Position({ x: 1 }));
        world.deferred.add(entity, Position({ x: 2, y: 3 }));
        expect(entity.get(Position)).toEqual({ x: 2, y: 3 });

        world.deferred.flush();
        expect(entity.get(Position)).toEqual({ x: 2, y: 3 });
    });

    it('spawns entities on execution', () => {
        const addSpy = vi.fn();
        world.onAdd(Position, addSpy);

        const entity = world.deferred.spawn(Position({ x: 3 }));

        expect(entity.has(Position)).toBe(true);
        expect(entity.get(Position)!.x).toBe(3);
        expect(world.query(Position)).not.toContain(entity);
        expect(world.query()).not.toContain(entity);
        expect(addSpy).not.toHaveBeenCalled();

        world.deferred.flush();

        expect(world.query(Position)).toContain(entity);
        expect(world.query()).toContain(entity);
        expect(addSpy).toHaveBeenCalledTimes(1);
        expect(addSpy).toHaveBeenCalledWith(entity);
    });

    it('nullifies a spawn and destroy in the same buffer', () => {
        const addSpy = vi.fn();
        const removeSpy = vi.fn();
        world.onAdd(Position, addSpy);
        world.onRemove(Position, removeSpy);

        const entity = world.deferred.spawn(Position);
        world.deferred.add(entity, Velocity);
        world.deferred.destroy(entity);

        expect(entity.has(Position)).toBe(false);

        world.deferred.flush();

        expect(entity.isAlive()).toBe(false);
        expect(addSpy).not.toHaveBeenCalled();
        expect(removeSpy).not.toHaveBeenCalled();
        expect(world.query(Position).length).toBe(0);
    });

    it('silently skips commands on destroyed entities', () => {
        const entity = world.spawn(Position);

        world.deferred.destroy(entity);
        world.deferred.add(entity, Velocity);
        world.deferred.destroy(entity);
        expect(() => world.deferred.flush()).not.toThrow();
        expect(entity.isAlive()).toBe(false);

        expect(() => world.deferred.add(entity, Velocity)).not.toThrow();
        expect(() => world.deferred.destroy(entity)).not.toThrow();
        expect(() => world.deferred.flush()).not.toThrow();
    });

    it('throws when the world entity destruction executes', () => {
        const entity = world[$internal].worldEntity;

        expect(() => world.deferred.destroy(entity)).not.toThrow();
        expect(() => world.deferred.flush()).toThrow();
        expect(world.has(entity)).toBe(true);
    });

    it('fires subscriptions once based on the state difference', () => {
        const entity = world.spawn();
        const addSpy = vi.fn();
        const removeSpy = vi.fn();
        world.onAdd(Tag, addSpy);
        world.onRemove(Tag, removeSpy);

        world.deferred.add(entity, Tag);
        world.deferred.remove(entity, Tag);
        world.deferred.add(entity, Tag);
        world.deferred.flush();

        expect(addSpy).toHaveBeenCalledTimes(1);
        expect(removeSpy).not.toHaveBeenCalled();

        world.deferred.remove(entity, Tag);
        world.deferred.add(entity, Tag);
        world.deferred.flush();

        expect(addSpy).toHaveBeenCalledTimes(1);
        expect(removeSpy).not.toHaveBeenCalled();

        world.deferred.remove(entity, Tag);
        world.deferred.flush();
        expect(removeSpy).toHaveBeenCalledTimes(1);
    });

    it('fires query subscriptions once based on the state difference', () => {
        const entity = world.spawn();
        const addSpy = vi.fn();
        const removeSpy = vi.fn();
        world.onQueryAdd([Position, Velocity], addSpy);
        world.onQueryRemove([Position, Velocity], removeSpy);

        world.deferred.add(entity, Position, Velocity);
        world.deferred.remove(entity, Velocity);
        world.deferred.add(entity, Velocity);
        world.deferred.flush();

        expect(addSpy).toHaveBeenCalledTimes(1);
        expect(removeSpy).not.toHaveBeenCalled();
    });

    it('fires relation subscriptions once per pair', () => {
        const Likes = relation();
        const a = world.spawn();
        const b = world.spawn();
        const entity = world.spawn();
        const addSpy = vi.fn();
        world.onAdd(Likes, addSpy);

        world.deferred.add(entity, Likes(a), Likes(b));
        world.deferred.remove(entity, Likes(a));
        world.deferred.add(entity, Likes(a));
        world.deferred.flush();

        expect(addSpy).toHaveBeenCalledTimes(2);
        expect(addSpy).toHaveBeenCalledWith(entity, a);
        expect(addSpy).toHaveBeenCalledWith(entity, b);
    });

    it('executes pending commands before a non-deferred mutation on the entity', () => {
        const entity = world.spawn();
        const other = world.spawn();

        world.deferred.add(entity, Position({ x: 1 }));
        world.deferred.add(other, Tag);

        entity.set(Position, { x: 5 });
        expect(entity.get(Position)!.x).toBe(5);
        expect(world.query(Position)).toContain(entity);

        world.deferred.remove(entity, Position);
        entity.add(Position({ x: 9 }));
        expect(entity.get(Position)!.x).toBe(9);
    });

    it('does not flush other scopes when mutating an unrelated entity', () => {
        const entity = world.spawn();
        const other = world.spawn();

        world.deferred.add(entity, Tag);
        other.add(Tag);

        expect(world.query(Tag)).toContain(other);
        expect(world.query(Tag)).not.toContain(entity);
    });

    it('flushes inner scopes independently of outer buffers', () => {
        const outer = world.spawn(Position);
        const inner = world.spawn(Velocity);

        world.query(Position).updateEach(() => {
            world.deferred.add(outer, Tag);

            world.query(Velocity).updateEach(() => {
                world.deferred.add(inner, Tag);
            });

            expect(world.query(Tag)).toContain(inner);
            expect(world.query(Tag)).not.toContain(outer);

            world.query(Velocity).updateEach(() => {
                world.deferred.remove(inner, Tag);
                world.deferred.flush();
                expect(world.query(Tag)).not.toContain(inner);
                expect(world.query(Tag)).not.toContain(outer);
            });
        });

        expect(world.query(Tag)).toContain(outer);
    });

    it('keeps root commands pending across updateEach calls', () => {
        const entity = world.spawn(Position);
        world.deferred.add(entity, Tag);

        world.query(Position).updateEach(() => {});

        expect(world.query(Tag)).not.toContain(entity);
        world.deferred.flush();
        expect(world.query(Tag)).toContain(entity);
    });

    it('replaces relation pairs with addExclusive', () => {
        const Likes = relation({ store: { weight: 0 } });
        const a = world.spawn();
        const b = world.spawn();
        const c = world.spawn();
        const entity = world.spawn(Likes(a), Likes(b));

        world.deferred.addExclusive(entity, Likes(c, { weight: 3 }));

        expect(entity.has(Likes(a))).toBe(false);
        expect(entity.has(Likes(b))).toBe(false);
        expect(entity.has(Likes(c))).toBe(true);
        expect(entity.get(Likes(c))).toEqual({ weight: 3 });

        world.deferred.flush();

        expect(entity.targetsFor(Likes)).toEqual([c]);
        expect(entity.get(Likes(c))).toEqual({ weight: 3 });

        world.deferred.addExclusive(entity, Likes('*'));
        expect(entity.has(Likes('*'))).toBe(false);
        world.deferred.flush();
        expect(entity.targetsFor(Likes)).toEqual([]);
        expect(entity.has(Likes('*'))).toBe(false);
    });

    it('cascades autoDestroy relations on execution', () => {
        const ChildOf = relation({ autoDestroy: 'orphan' });
        const parent = world.spawn();
        const child = world.spawn(ChildOf(parent), Position);
        const grandchild = world.deferred.spawn(ChildOf(child), Position);

        world.deferred.destroy(parent);

        expect(child.has(Position)).toBe(false);
        expect(grandchild.has(Position)).toBe(false);

        world.deferred.flush();

        expect(parent.isAlive()).toBe(false);
        expect(child.isAlive()).toBe(false);
        expect(grandchild.isAlive()).toBe(false);
        expect(world.query(Position).length).toBe(0);
    });

    it('does not cascade through relations to a nullified spawn', () => {
        const ChildOf = relation({ autoDestroy: 'orphan' });
        const child = world.spawn(Position);

        const parent = world.deferred.spawn();
        world.deferred.add(child, ChildOf(parent));
        world.deferred.destroy(parent);

        expect(child.has(Position)).toBe(true);
        expect(child.has(ChildOf(parent))).toBe(false);

        world.deferred.flush();

        expect(child.isAlive()).toBe(true);
        expect(child.has(ChildOf('*'))).toBe(false);
    });

    it('skips pairs to targets destroyed earlier in the buffer', () => {
        const ChildOf = relation({ autoDestroy: 'orphan' });
        const parent = world.spawn();
        const child = world.spawn();

        world.deferred.destroy(parent);
        world.deferred.add(child, ChildOf(parent));
        world.deferred.flush();

        expect(child.isAlive()).toBe(true);
        expect(child.has(ChildOf('*'))).toBe(false);
    });

    it('defers destruction until updateEach exits', () => {
        const entity = world.spawn(Position);

        world.query(Position).updateEach(([position], e) => {
            position.x = 10;
            world.deferred.destroy(e);
            expect(e.isAlive()).toBe(true);
        });

        expect(entity.isAlive()).toBe(false);
    });

    it('respects exclusive relations when deferring adds', () => {
        const Targeting = relation({ exclusive: true });
        const a = world.spawn();
        const b = world.spawn();
        const entity = world.spawn(Targeting(a));
        const removeSpy = vi.fn();
        world.onRemove(Targeting, removeSpy);

        world.deferred.add(entity, Targeting(b));
        expect(entity.has(Targeting(a))).toBe(false);
        expect(entity.has(Targeting(b))).toBe(true);

        world.deferred.flush();
        expect(entity.targetFor(Targeting)).toBe(b);
        expect(removeSpy).toHaveBeenCalledTimes(1);
        expect(removeSpy).toHaveBeenCalledWith(entity, a);
    });

    it('predicts target autoDestroy cascades', () => {
        const Contains = relation({ autoDestroy: 'target' });
        const item = world.spawn(Tag);
        const container = world.spawn();

        world.deferred.add(container, Contains(item));
        world.deferred.destroy(container);

        expect(item.has(Tag)).toBe(false);
        world.deferred.flush();
        expect(item.isAlive()).toBe(false);
    });

    it('fires remove subscriptions once for destroyed entities', () => {
        const entity = world.spawn(Position, Tag);
        const removeSpy = vi.fn();
        world.onRemove(Position, removeSpy);

        world.deferred.remove(entity, Tag);
        world.deferred.destroy(entity);
        world.deferred.flush();

        expect(removeSpy).toHaveBeenCalledTimes(1);
        expect(removeSpy).toHaveBeenCalledWith(entity);
    });

    it('keeps ordered relations in sync', () => {
        const ChildOf = relation();
        const Children = ordered(ChildOf);
        const parent = world.spawn(Children);
        const a = world.spawn();
        const b = world.deferred.spawn(ChildOf(parent));

        world.deferred.add(a, ChildOf(parent));
        world.deferred.flush();

        expect([...parent.get(Children)!]).toEqual([b, a]);
    });

    it('returns deferred values for AoS traits', () => {
        const Mesh = trait(() => ({ name: 'default' }));
        const mesh = { name: 'box' };

        const entity = world.deferred.spawn(Mesh(mesh));
        expect(entity.get(Mesh)).toBe(mesh);

        world.deferred.flush();
        expect(entity.get(Mesh)).toBe(mesh);
    });

    it('defers commands issued by subscribers during a flush', () => {
        const entity = world.spawn();
        world.onAdd(Tag, (e) => world.deferred.add(e, Position));

        world.deferred.add(entity, Tag);
        world.deferred.flush();

        expect(world.query(Position)).not.toContain(entity);
        expect(entity.has(Position)).toBe(true);

        world.deferred.flush();
        expect(world.query(Position)).toContain(entity);
    });

    it('discards pending commands on reset', () => {
        const entity = world.spawn();
        world.deferred.add(entity, Tag);
        world.deferred.spawn(Tag);

        world.reset();
        world.deferred.flush();

        expect(world.query(Tag).length).toBe(0);
    });
});
