import { beforeEach, describe, expect, it, vi } from 'vitest';
import { $internal, createWorld, relation, trait, type Entity } from '../src';

const Position = trait({ x: 0, y: 0 });
const Velocity = trait({ x: 0, y: 0 });
const Name = trait({ value: '' });
const IsEnemy = trait();

describe('Deferred commands', () => {
    const world = createWorld();

    beforeEach(() => {
        world.reset();
    });

    it('defers structural changes until flush', () => {
        const entity = world.spawn();
        world.deferred.add(entity, Position({ x: 1, y: 2 }), IsEnemy);
        world.deferred.add(entity, Name({ value: 'ada' }));

        expect(entity.has(Position)).toBe(true);
        expect(entity.has(IsEnemy)).toBe(true);
        expect(entity.get(Position)).toEqual({ x: 1, y: 2 });
        expect(entity.get(Name)).toEqual({ value: 'ada' });
        expect(world.query(Position).length).toBe(0);

        world.deferred.flush();

        expect(entity.has(Position)).toBe(true);
        expect(entity.get(Position)).toEqual({ x: 1, y: 2 });
        expect(entity.get(Name)?.value).toBe('ada');
        expect(world.query(Position)).toContain(entity);
        expect(world.query(IsEnemy)).toContain(entity);
    });

    it('replaces earlier values for the same trait', () => {
        const entity = world.spawn();
        world.deferred.add(entity, Position({ x: 1, y: 1 }));
        world.deferred.add(entity, Position({ x: 4, y: 5 }));

        expect(entity.get(Position)).toEqual({ x: 4, y: 5 });
        world.deferred.flush();
        expect(entity.get(Position)).toEqual({ x: 4, y: 5 });
    });

    it('runs earlier commands before later ones', () => {
        const first = world.spawn();
        const second = world.spawn();
        const seen: Entity[] = [];
        world.onAdd(Name, (entity) => seen.push(entity));

        world.deferred.add(first, Name({ value: 'a' }));
        world.deferred.add(second, Name({ value: 'b' }));
        world.deferred.flush();

        expect(seen).toEqual([first, second]);
        expect(first.get(Name)?.value).toBe('a');
        expect(second.get(Name)?.value).toBe('b');
    });

    it('flushes when updateEach exits', () => {
        const marker = world.spawn(IsEnemy);
        const entity = world.spawn(Position);
        let during = false;

        world.query(IsEnemy).updateEach(() => {
            world.deferred.add(entity, Name({ value: 'flush-me' }));
            during = entity.has(Name);
            expect(world.query(Name).length).toBe(0);
        });

        expect(during).toBe(true);
        expect(entity.get(Name)?.value).toBe('flush-me');
        expect(world.query(Name)).toContain(entity);
        expect(marker.has(IsEnemy)).toBe(true);
    });

    it('flushes inner scopes without dropping the outer buffer', () => {
        const entity = world.spawn();
        const events: string[] = [];
        world.onAdd(Name, () => events.push('name'));
        world.onAdd(Velocity, () => events.push('velocity'));

        world.deferred.add(entity, Name({ value: 'outer' }));
        world.query(IsEnemy).updateEach(() => {
            world.deferred.add(entity, Velocity({ x: 1, y: 0 }));
        });

        expect(events).toEqual([]);
        expect(entity.has(Name)).toBe(true);
        expect(entity.has(Velocity)).toBe(false);

        const inner = world.spawn(IsEnemy);
        world.query(IsEnemy).updateEach(() => {
            world.deferred.add(entity, Velocity({ x: 2, y: 0 }));
        });

        expect(events).toEqual(['velocity']);
        expect(entity.has(Velocity)).toBe(true);
        expect(entity.get(Name)?.value).toBe('outer');
        expect(world.query(Name).length).toBe(0);

        world.deferred.flush();
        expect(events).toEqual(['velocity', 'name']);
        expect(entity.get(Name)?.value).toBe('outer');
        expect(inner.has(IsEnemy)).toBe(true);
    });

    it('flushes pending commands before a non-deferred mutation', () => {
        const entity = world.spawn();
        world.deferred.add(entity, Position({ x: 3, y: 4 }));
        entity.add(Name({ value: 'now' }));

        expect(entity.get(Position)).toEqual({ x: 3, y: 4 });
        expect(entity.get(Name)?.value).toBe('now');
        expect(world.query(Position)).toContain(entity);
    });

    it('skips commands on destroyed entities', () => {
        const entity = world.spawn(Position);
        world.deferred.destroy(entity);
        world.deferred.add(entity, Name({ value: 'nope' }));

        expect(world.has(entity)).toBe(false);
        expect(entity.has(Position)).toBe(false);
        expect(() => world.deferred.flush()).not.toThrow();
        expect(world.has(entity)).toBe(false);
        expect(entity.has(Name)).toBe(false);
    });

    it('nullifies spawn and destroy in the same buffer', () => {
        const adds = vi.fn();
        const removes = vi.fn();
        world.onAdd(Position, adds);
        world.onRemove(Position, removes);

        const entity = world.deferred.spawn(Position({ x: 1, y: 2 }), Name);
        world.deferred.add(entity, Velocity);
        world.deferred.destroy(entity);

        expect(world.has(entity)).toBe(false);
        expect(entity.has(Position)).toBe(false);

        world.deferred.flush();

        expect(world.has(entity)).toBe(false);
        expect(adds).not.toHaveBeenCalled();
        expect(removes).not.toHaveBeenCalled();
        expect(world.query(Position).length).toBe(0);
    });

    it('fires subscriptions once from the before/after difference', () => {
        const entity = world.spawn();
        const adds: string[] = [];
        const removes: string[] = [];
        world.onAdd(Position, () => adds.push('position'));
        world.onRemove(Position, () => removes.push('position'));
        world.onAdd(Name, () => adds.push('name'));

        world.deferred.add(entity, Position({ x: 1, y: 1 }));
        world.deferred.add(entity, Position({ x: 2, y: 2 }));
        world.deferred.add(entity, Name({ value: 'a' }));
        world.deferred.remove(entity, Name);
        world.deferred.flush();

        expect(adds).toEqual(['position']);
        expect(removes).toEqual([]);
        expect(entity.get(Position)).toEqual({ x: 2, y: 2 });
        expect(entity.has(Name)).toBe(false);
    });

    it('throws when a deferred command destroys the world entity', () => {
        const worldEntity = world[$internal].worldEntity;
        const entity = world.spawn();
        world.deferred.add(entity, IsEnemy);
        world.deferred.destroy(worldEntity);

        expect(() => world.deferred.flush()).toThrow(/world entity/);
        expect(world.has(worldEntity)).toBe(true);
        expect(entity.has(IsEnemy)).toBe(true);
    });

    it('addExclusive replaces relation pairs and wildcard clears them', () => {
        const Likes = relation({ store: { score: 0 } });
        const entity = world.spawn();
        const a = world.spawn();
        const b = world.spawn();
        const c = world.spawn();
        entity.add(Likes(a, { score: 1 }), Likes(b, { score: 2 }));

        world.deferred.addExclusive(entity, Likes(c, { score: 9 }));
        expect(entity.has(Likes(a))).toBe(false);
        expect(entity.has(Likes(b))).toBe(false);
        expect(entity.has(Likes(c))).toBe(true);
        expect(entity.get(Likes(c))).toEqual({ score: 9 });
        expect(entity.targetsFor(Likes)).toEqual([c]);

        world.deferred.flush();
        expect(entity.targetsFor(Likes)).toEqual([c]);
        expect(entity.has(Likes(a))).toBe(false);

        world.deferred.addExclusive(entity, Likes('*'));
        expect(entity.has(Likes('*'))).toBe(false);
        world.deferred.flush();
        expect(entity.targetsFor(Likes)).toEqual([]);
        expect(entity.has(Likes(c))).toBe(false);
    });

    it('fires relation subscriptions once per pair', () => {
        const Likes = relation();
        const entity = world.spawn();
        const a = world.spawn();
        const b = world.spawn();
        const c = world.spawn();
        entity.add(Likes(a), Likes(b));

        const adds: Entity[] = [];
        const removes: Entity[] = [];
        world.onAdd(Likes, (_entity, target) => adds.push(target!));
        world.onRemove(Likes, (_entity, target) => removes.push(target!));

        world.deferred.addExclusive(entity, Likes(c));
        world.deferred.add(entity, Likes(c));
        world.deferred.flush();

        expect(removes.sort()).toEqual([a, b].sort());
        expect(adds).toEqual([c]);
        expect(entity.targetsFor(Likes)).toEqual([c]);
    });

    it('cascades autoDestroy relations and ignores nullified spawns', () => {
        const ChildOf = relation({ autoDestroy: 'orphan' });
        const parent = world.spawn();
        const child = world.spawn(ChildOf(parent));
        const grandchild = world.spawn(ChildOf(child));
        const ghost = world.deferred.spawn(ChildOf(parent), Position);
        world.deferred.destroy(ghost);
        world.deferred.destroy(parent);

        expect(world.has(parent)).toBe(false);
        expect(world.has(child)).toBe(false);
        expect(world.has(grandchild)).toBe(false);
        expect(world.has(ghost)).toBe(false);

        expect(() => world.deferred.flush()).not.toThrow();
        expect(world.has(parent)).toBe(false);
        expect(world.has(child)).toBe(false);
        expect(world.has(grandchild)).toBe(false);
        expect(world.has(ghost)).toBe(false);
    });

    it('cascades autoDestroy target relations from a deferred destroy', () => {
        const Contains = relation({ autoDestroy: 'target' });
        const container = world.spawn();
        const item = world.spawn();
        container.add(Contains(item));

        world.deferred.destroy(container);
        expect(world.has(item)).toBe(false);
        world.deferred.flush();
        expect(world.has(container)).toBe(false);
        expect(world.has(item)).toBe(false);
    });

    it('spawns with traits visible before flush and committed after', () => {
        const parent = world.spawn();
        const ChildOf = relation({ exclusive: true });
        const child = world.deferred.spawn(Position({ x: 8, y: 1 }), ChildOf(parent));

        expect(world.has(child)).toBe(true);
        expect(child.has(Position)).toBe(true);
        expect(child.get(Position)).toEqual({ x: 8, y: 1 });
        expect(child.has(ChildOf(parent))).toBe(true);
        expect(world.query(Position)).not.toContain(child);

        world.deferred.flush();
        expect(world.query(Position)).toContain(child);
        expect(child.targetFor(ChildOf)).toBe(parent);
    });
});
