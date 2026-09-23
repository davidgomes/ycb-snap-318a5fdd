import { beforeEach, describe, expect, it, vi } from 'vitest';
import { $internal, createWorld, relation, trait, universe } from '../src';

const Position = trait({ x: 0, y: 0 });
const Health = trait({ amount: 0 });
const Marker = trait();
const Tag = trait();

describe('deferred command buffer', () => {
    beforeEach(() => {
        universe.reset();
    });

    it('flushes spawn, add, remove, and destroy in order', () => {
        const world = createWorld();
        const parent = world.spawn(Marker);
        const ChildOf = relation({ autoDestroy: 'orphan' });

        const early = world.deferred.spawn(Tag);
        world.deferred.destroy(parent);
        const child = world.deferred.spawn(Marker, ChildOf(parent));

        world.deferred.flush();

        expect(early.has(Tag)).toBe(true);
        expect(world.has(parent)).toBe(false);
        // Destroy ran before this spawn, so the child is not cascaded away.
        expect(world.has(child)).toBe(true);
        expect(child.has(ChildOf(parent))).toBe(true);
    });

    it('replaces earlier values of the same trait with later ones', () => {
        const world = createWorld();
        const entity = world.spawn(Marker);

        world.deferred.add(entity, Position({ x: 1, y: 2 }));
        world.deferred.add(entity, Position({ x: 3 }));
        expect(entity.get(Position)).toMatchObject({ x: 3, y: 0 });
        expect(entity.has(Position)).toBe(true);

        world.deferred.flush();

        expect(entity.get(Position)).toMatchObject({ x: 3, y: 0 });
        expect(entity.has(Health)).toBe(false);
    });

    it('previews has and get as they will be after flush', () => {
        const world = createWorld();
        const entity = world.spawn(Position({ x: 4, y: 5 }), Health({ amount: 3 }));

        world.deferred.add(entity, Position({ x: 8, y: 9 }));
        world.deferred.remove(entity, Health);

        expect(entity.has(Position)).toBe(true);
        expect(entity.get(Position)).toMatchObject({ x: 8, y: 9 });
        expect(entity.has(Health)).toBe(false);
        expect(entity.get(Health)).toBeUndefined();

        world.deferred.flush();

        expect(entity.get(Position)).toMatchObject({ x: 8, y: 9 });
        expect(entity.has(Health)).toBe(false);
    });

    it('flushes the updateEach scope on exit and keeps the outer buffer', () => {
        const world = createWorld();
        const entity = world.spawn(Marker);
        const added: string[] = [];
        world.onAdd(Health, () => added.push('health'));
        world.onAdd(Tag, () => added.push('tag'));

        world.deferred.add(entity, Health({ amount: 1 }));

        world.query(Marker).updateEach(() => {
            world.deferred.add(entity, Tag);
            expect(added).toEqual([]);
            expect(entity.has(Tag)).toBe(true);
            expect(entity.has(Health)).toBe(true);
        });

        expect(added).toEqual(['tag']);
        expect(entity.has(Tag)).toBe(true);
        expect(entity.has(Health)).toBe(true);

        world.deferred.flush();
        expect(added).toEqual(['tag', 'health']);
    });

    it('flushes an inner updateEach without flushing the outer scope', () => {
        const world = createWorld();
        const entity = world.spawn(Marker);
        const added: string[] = [];
        world.onAdd(Health, () => added.push('health'));
        world.onAdd(Tag, () => added.push('tag'));

        world.query(Marker).updateEach(() => {
            world.deferred.add(entity, Health({ amount: 2 }));
            world.query(Marker).updateEach(() => {
                world.deferred.add(entity, Tag);
            });
            expect(added).toEqual(['tag']);
            expect(entity.has(Health)).toBe(true);
        });

        expect(added).toEqual(['tag', 'health']);
    });

    it('runs deferred commands before a non-deferred mutation on that entity', () => {
        const world = createWorld();
        const entity = world.spawn(Marker);
        const order: string[] = [];
        world.onAdd(Health, () => order.push('health'));
        world.onAdd(Tag, () => order.push('tag'));

        world.deferred.add(entity, Health({ amount: 4 }));
        entity.add(Tag);

        expect(order).toEqual(['health', 'tag']);
        expect(entity.has(Health)).toBe(true);
        expect(entity.has(Tag)).toBe(true);
        expect(entity.get(Health)).toMatchObject({ amount: 4 });
    });

    it('flushes outer commands for an entity mutated inside updateEach', () => {
        const world = createWorld();
        const entity = world.spawn(Marker);
        const order: string[] = [];
        world.onAdd(Tag, () => order.push('tag'));
        world.onAdd(Position, () => order.push('position'));
        world.onAdd(Health, () => order.push('health'));

        world.deferred.add(entity, Position({ x: 1, y: 2 }));

        world.query(Marker).updateEach(() => {
            world.deferred.add(entity, Tag);
            entity.add(Health({ amount: 1 }));
            expect(order).toEqual(['tag', 'position', 'health']);
        });

        expect(entity.get(Position)).toMatchObject({ x: 1, y: 2 });
        expect(entity.has(Tag)).toBe(true);
        expect(entity.get(Health)).toMatchObject({ amount: 1 });
    });

    it('keeps outer commands when an inner entity is mutated', () => {
        const world = createWorld();
        const entity = world.spawn(Marker);
        const other = world.spawn();
        const added = vi.fn();
        world.onAdd(Tag, added);

        world.deferred.add(other, Tag);

        world.query(Marker).updateEach(() => {
            world.deferred.add(entity, Health({ amount: 2 }));
            entity.add(Position({ x: 3, y: 4 }));
        });

        expect(entity.has(Health)).toBe(true);
        expect(entity.get(Position)).toMatchObject({ x: 3, y: 4 });
        expect(other.has(Tag)).toBe(true);
        expect(added).not.toHaveBeenCalled();

        world.deferred.flush();
        expect(added).toHaveBeenCalledTimes(1);
        expect(other.has(Tag)).toBe(true);
    });

    it('nullifies a spawn when a later scope destroys it', () => {
        const world = createWorld();
        const added = vi.fn();
        world.onAdd(Tag, added);

        world.spawn(Marker);
        const entity = world.deferred.spawn(Tag);
        world.query(Marker).updateEach(() => {
            world.deferred.destroy(entity);
            world.deferred.add(entity, Health({ amount: 1 }));
        });

        expect(entity.isAlive()).toBe(false);
        expect(world.has(entity)).toBe(false);
        expect(entity.has(Tag)).toBe(false);

        world.deferred.flush();
        expect(world.has(entity)).toBe(false);
        expect(added).not.toHaveBeenCalled();
    });

    it('previews destruction on has, isAlive, and world accessors', () => {
        const world = createWorld();
        const entity = world.spawn(Marker, Position({ x: 1, y: 2 }));
        const worldEntity = world[$internal].worldEntity;

        world.deferred.destroy(entity);
        world.deferred.add(worldEntity, Health({ amount: 6 }));

        expect(entity.isAlive()).toBe(false);
        expect(world.has(entity)).toBe(false);
        expect(entity.has(Marker)).toBe(false);
        expect(entity.get(Position)).toBeUndefined();
        expect(world.has(Health)).toBe(true);
        expect(world.get(Health)).toMatchObject({ amount: 6 });
        expect(worldEntity.get(Health)).toMatchObject({ amount: 6 });

        world.deferred.flush();

        expect(world.has(entity)).toBe(false);
        expect(world.get(Health)).toMatchObject({ amount: 6 });
    });

    it('addExclusive keeps one pair and wildcard clears every pair', () => {
        const world = createWorld();
        const Likes = relation({ store: { rank: 0 } });
        const entity = world.spawn(Marker);
        const a = world.spawn();
        const b = world.spawn();
        const c = world.spawn();

        entity.add(Likes(a, { rank: 1 }), Likes(b, { rank: 2 }));
        world.deferred.addExclusive(entity, Likes(c, { rank: 3 }));

        expect(entity.has(Likes(a))).toBe(false);
        expect(entity.has(Likes(b))).toBe(false);
        expect(entity.has(Likes(c))).toBe(true);
        expect(entity.get(Likes(c))).toMatchObject({ rank: 3 });
        expect(entity.targetsFor(Likes)).toEqual([c]);

        world.deferred.flush();
        expect(entity.targetsFor(Likes)).toEqual([c]);

        world.deferred.addExclusive(entity, Likes('*'));
        expect(entity.has(Likes(c))).toBe(false);
        expect(entity.targetsFor(Likes)).toEqual([]);
        world.deferred.flush();
        expect(entity.has(Likes('*'))).toBe(false);
    });

    it('throws when a deferred world-entity destroy executes', () => {
        const world = createWorld();
        const entity = world.spawn(Marker);
        const worldEntity = world[$internal].worldEntity;

        world.deferred.add(entity, Tag);
        world.deferred.destroy(worldEntity);
        world.deferred.add(entity, Health({ amount: 1 }));

        expect(() => world.deferred.flush()).toThrow(/world entity/i);
        expect(world.has(worldEntity)).toBe(true);
        expect(entity.has(Tag)).toBe(true);
        expect(entity.has(Health)).toBe(false);
    });

    it('skips commands that target entities destroyed earlier in the buffer', () => {
        const world = createWorld();
        const entity = world.spawn(Position({ x: 1, y: 1 }));
        const dead = world.spawn();
        dead.destroy();

        world.deferred.destroy(entity);
        world.deferred.add(entity, Health({ amount: 5 }));
        world.deferred.add(dead, Tag);
        world.deferred.destroy(dead);

        expect(() => world.deferred.flush()).not.toThrow();
        expect(world.has(entity)).toBe(false);
        expect(world.has(dead)).toBe(false);
    });

    it('nullifies a spawn and destroy in the same buffer', () => {
        const world = createWorld();
        const Contains = relation({ autoDestroy: 'target' });
        const item = world.spawn(Marker);
        const added = vi.fn();
        const removed = vi.fn();
        world.onAdd(Tag, added);
        world.onRemove(Marker, removed);

        const container = world.deferred.spawn(Tag, Contains(item));
        world.deferred.add(container, Health({ amount: 1 }));
        world.deferred.destroy(container);

        expect(container.has(Tag)).toBe(false);
        expect(world.has(container)).toBe(false);

        world.deferred.flush();

        expect(world.has(item)).toBe(true);
        expect(world.has(container)).toBe(false);
        expect(added).not.toHaveBeenCalled();
        expect(removed).not.toHaveBeenCalled();
    });

    it('fires each subscription once from the net diff', () => {
        const world = createWorld();
        const entity = world.spawn(Marker);
        const added = vi.fn();
        const removed = vi.fn();
        const changed = vi.fn();
        const queryAdded = vi.fn();
        const queryRemoved = vi.fn();

        world.onAdd(Position, added);
        world.onRemove(Position, removed);
        world.onChange(Position, changed);
        world.onQueryAdd([Position], queryAdded);
        world.onQueryRemove([Position], queryRemoved);

        world.deferred.add(entity, Position({ x: 1, y: 0 }));
        world.deferred.add(entity, Position({ x: 2, y: 0 }));
        world.deferred.remove(entity, Position);
        world.deferred.add(entity, Position({ x: 3, y: 4 }));
        world.deferred.flush();

        expect(added).toHaveBeenCalledTimes(1);
        expect(added).toHaveBeenCalledWith(entity);
        expect(removed).not.toHaveBeenCalled();
        expect(changed).not.toHaveBeenCalled();
        expect(queryAdded).toHaveBeenCalledTimes(1);
        expect(queryRemoved).not.toHaveBeenCalled();
        expect(entity.get(Position)).toMatchObject({ x: 3, y: 4 });

        added.mockClear();
        changed.mockClear();
        world.deferred.add(entity, Position({ x: 5, y: 4 }));
        world.deferred.add(entity, Position({ x: 6, y: 7 }));
        world.deferred.flush();

        expect(added).not.toHaveBeenCalled();
        expect(changed).toHaveBeenCalledTimes(1);
        expect(changed).toHaveBeenCalledWith(entity);
        expect(entity.get(Position)).toMatchObject({ x: 6, y: 7 });
    });

    it('fires relation subscriptions once per pair', () => {
        const world = createWorld();
        const Likes = relation({ store: { rank: 0 } });
        const entity = world.spawn(Marker);
        const a = world.spawn();
        const b = world.spawn();
        const c = world.spawn();
        entity.add(Likes(a, { rank: 1 }));

        const added = vi.fn();
        const removed = vi.fn();
        const changed = vi.fn();
        world.onAdd(Likes, added);
        world.onRemove(Likes, removed);
        world.onChange(Likes, changed);

        world.deferred.add(entity, Likes(b, { rank: 2 }));
        world.deferred.addExclusive(entity, Likes(c, { rank: 3 }));
        world.deferred.flush();

        expect(removed).toHaveBeenCalledTimes(1);
        expect(removed).toHaveBeenCalledWith(entity, a);
        expect(added).toHaveBeenCalledTimes(1);
        expect(added).toHaveBeenCalledWith(entity, c);
        expect(changed).not.toHaveBeenCalled();
        expect(entity.has(Likes(b))).toBe(false);

        added.mockClear();
        changed.mockClear();
        world.deferred.add(entity, Likes(c, { rank: 9 }));
        world.deferred.flush();
        expect(added).not.toHaveBeenCalled();
        expect(changed).toHaveBeenCalledTimes(1);
        expect(changed).toHaveBeenCalledWith(entity, c);
    });

    it('cascades autoDestroy and ignores nullified spawns', () => {
        const world = createWorld();
        const ChildOf = relation({ autoDestroy: 'orphan' });
        const Contains = relation({ autoDestroy: 'target' });
        const parent = world.spawn(Marker);
        const child = world.spawn(Marker, ChildOf(parent));
        const item = world.spawn(Marker);
        const removed = vi.fn();
        world.onRemove(Marker, removed);

        const ghost = world.deferred.spawn(Marker, Contains(item));
        world.deferred.destroy(ghost);
        world.deferred.destroy(parent);
        world.deferred.flush();

        expect(world.has(parent)).toBe(false);
        expect(world.has(child)).toBe(false);
        expect(world.has(item)).toBe(true);
        expect(world.has(ghost)).toBe(false);
        expect(removed).toHaveBeenCalledTimes(2);
    });

    it('cascades a deferred spawn when its parent is destroyed later in the buffer', () => {
        const world = createWorld();
        const ChildOf = relation({ autoDestroy: 'orphan' });
        const parent = world.spawn(Marker);
        const added = vi.fn();
        const removed = vi.fn();
        world.onAdd(Marker, added);
        world.onRemove(Marker, removed);

        const child = world.deferred.spawn(Marker, ChildOf(parent));
        world.deferred.destroy(parent);
        expect(child.isAlive()).toBe(false);
        expect(parent.isAlive()).toBe(false);

        world.deferred.flush();

        expect(world.has(parent)).toBe(false);
        expect(world.has(child)).toBe(false);
        expect(added).not.toHaveBeenCalled();
        expect(removed).toHaveBeenCalledTimes(1);
    });

    it('spawns into queries only after flush', () => {
        const world = createWorld();
        const entity = world.deferred.spawn(Position({ x: 2, y: 3 }));

        expect(entity.has(Position)).toBe(true);
        expect(entity.get(Position)).toMatchObject({ x: 2, y: 3 });
        expect(world.query(Position).length).toBe(0);

        world.deferred.flush();

        expect(world.query(Position)).toContain(entity);
        expect(entity.get(Position)).toMatchObject({ x: 2, y: 3 });
    });

    it('reset drops deferred spawns', () => {
        const world = createWorld();
        world.deferred.spawn(Position({ x: 1, y: 1 }));
        world.reset();
        expect(world.entities.length).toBe(1);
        expect(() => world.deferred.flush()).not.toThrow();
    });
});
