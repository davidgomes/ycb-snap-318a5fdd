import { describe, expect, it } from 'vitest';
import { $internal, createWorld, relation, trait } from '../src';

describe('Deferred command buffer', () => {
    const Position = trait({ x: 0, y: 0 });
    const Marker = trait();
    const Foo = trait();
    const Bar = trait();
    const Baz = trait();

    it('applies spawn, add, and remove on flush', () => {
        const world = createWorld();
        const existing = world.spawn(Position({ x: 1, y: 2 }));

        world.deferred.add(existing, Marker);
        world.deferred.remove(existing, Position);
        const spawned = world.deferred.spawn(Position({ x: 4, y: 5 }), Marker);

        expect(world.has(spawned)).toBe(false);
        expect(existing.has(Marker)).toBe(true);
        expect(existing.has(Position)).toBe(false);
        expect(spawned.has(Position)).toBe(true);
        expect(spawned.get(Position)).toEqual({ x: 4, y: 5 });
        expect(world.query(Marker).length).toBe(0);

        world.deferred.flush();

        expect(world.has(spawned)).toBe(true);
        expect(existing.has(Marker)).toBe(true);
        expect(existing.has(Position)).toBe(false);
        expect(spawned.get(Position)).toEqual({ x: 4, y: 5 });
        expect(world.query(Marker).length).toBe(2);
    });

    it('replaces earlier values for the same trait with later ones', () => {
        const world = createWorld();
        const entity = world.spawn(Position({ x: 5, y: 6 }));

        world.deferred.add(entity, Position({ x: 1, y: 2 }));
        world.deferred.add(entity, Position({ y: 4 }));
        expect(entity.get(Position)).toEqual({ x: 5, y: 4 });

        const created = world.deferred.spawn();
        world.deferred.add(created, Position({ x: 1, y: 2 }));
        world.deferred.add(created, Position({ x: 3 }));
        expect(created.get(Position)).toEqual({ x: 3, y: 0 });

        world.deferred.flush();
        expect(entity.get(Position)).toEqual({ x: 5, y: 4 });
        expect(created.get(Position)).toEqual({ x: 3, y: 0 });
    });

    it('runs earlier commands before later ones', () => {
        const ChildOf = relation({ autoDestroy: 'orphan' });
        const world = createWorld();
        const parent = world.spawn();
        const child = world.spawn();

        world.deferred.add(child, ChildOf(parent));
        world.deferred.destroy(parent);
        world.deferred.flush();

        expect(world.has(parent)).toBe(false);
        expect(world.has(child)).toBe(false);

        const parent2 = world.spawn();
        const child2 = world.spawn();
        world.deferred.destroy(parent2);
        world.deferred.add(child2, ChildOf(parent2));
        world.deferred.flush();

        expect(world.has(parent2)).toBe(false);
        expect(world.has(child2)).toBe(true);
        expect(child2.has(ChildOf(parent2))).toBe(true);
    });

    it('skips commands on destroyed entities', () => {
        const world = createWorld();
        const entity = world.spawn(Position);
        const adds: unknown[] = [];
        world.onAdd(Marker, (target) => adds.push(target));

        world.deferred.destroy(entity);
        world.deferred.add(entity, Marker);
        expect(() => world.deferred.flush()).not.toThrow();

        expect(world.has(entity)).toBe(false);
        expect(adds).toEqual([]);
    });

    it('nullifies spawn and destroy in the same buffer', () => {
        const world = createWorld();
        const adds: unknown[] = [];
        const removes: unknown[] = [];
        world.onAdd(Marker, (entity) => adds.push(entity));
        world.onRemove(Marker, (entity) => removes.push(entity));

        const entity = world.deferred.spawn(Marker);
        world.deferred.destroy(entity);
        expect(entity.has(Marker)).toBe(false);
        world.deferred.flush();

        expect(world.has(entity)).toBe(false);
        expect(world.entities.filter((candidate) => candidate === entity)).toEqual([]);
        expect(adds).toEqual([]);
        expect(removes).toEqual([]);
    });

    it('throws when a deferred command destroys the world entity', () => {
        const world = createWorld();
        const entity = world.spawn();
        const worldEntity = world[$internal].worldEntity;

        world.deferred.add(entity, Marker);
        world.deferred.destroy(worldEntity);
        world.deferred.add(entity, Foo);

        expect(() => world.deferred.flush()).toThrow(/world entity/);
        expect(world.has(worldEntity)).toBe(true);
        expect(entity.has(Marker)).toBe(true);
        expect(entity.has(Foo)).toBe(false);
    });

    it('flushes the current scope when updateEach exits', () => {
        const world = createWorld();
        const entity = world.spawn(Position);

        let during = -1;
        world.query(Position).updateEach((_, current) => {
            world.deferred.add(current, Marker);
            expect(current.has(Marker)).toBe(true);
            during = world.query(Marker).length;
        });

        expect(during).toBe(0);
        expect(entity.has(Marker)).toBe(true);
        expect(world.query(Marker).length).toBe(1);
    });

    it('flushes inner scopes without dropping outer commands', () => {
        const world = createWorld();
        const entity = world.spawn(Position);
        world.deferred.add(entity, Foo);

        world.query(Position).updateEach(() => {
            world.query(Position).updateEach(() => {
                world.deferred.add(entity, Bar);
            });
            expect(world.query(Bar).length).toBe(1);
            expect(world.query(Foo).length).toBe(0);
            world.deferred.add(entity, Baz);
        });

        expect(world.query(Bar).length).toBe(1);
        expect(world.query(Baz).length).toBe(1);
        expect(world.query(Foo).length).toBe(0);
        expect(entity.has(Foo)).toBe(true);

        world.deferred.flush();
        expect(entity.has(Foo)).toBe(true);
        expect(world.query(Foo).length).toBe(1);
    });

    it('flushes pending commands before a non-deferred mutation', () => {
        const world = createWorld();
        const entity = world.spawn(Position);

        world.deferred.add(entity, Foo);
        entity.add(Bar);

        expect(entity.has(Foo)).toBe(true);
        expect(entity.has(Bar)).toBe(true);
        expect(world.query(Foo).length).toBe(1);
        expect(world.query(Bar).length).toBe(1);
    });

    it('addExclusive replaces relation pairs and wildcard clears them', () => {
        const Likes = relation({ store: { amount: 0 } });
        const world = createWorld();
        const entity = world.spawn();
        const a = world.spawn();
        const b = world.spawn();
        const c = world.spawn();

        entity.add(Likes(a, { amount: 1 }), Likes(b, { amount: 2 }));
        world.deferred.addExclusive(entity, Likes(c, { amount: 9 }));

        expect(entity.has(Likes(a))).toBe(false);
        expect(entity.has(Likes(b))).toBe(false);
        expect(entity.has(Likes(c))).toBe(true);
        expect(entity.get(Likes(c))).toEqual({ amount: 9 });
        expect(entity.targetsFor(Likes)).toEqual([c]);

        world.deferred.flush();
        expect(entity.targetsFor(Likes)).toEqual([c]);
        expect(entity.get(Likes(c))!.amount).toBe(9);

        world.deferred.addExclusive(entity, Likes('*'));
        expect(entity.targetsFor(Likes)).toEqual([]);
        expect(entity.has(Likes('*'))).toBe(false);
        world.deferred.flush();
        expect(entity.has(Likes(c))).toBe(false);
        expect(entity.targetsFor(Likes)).toEqual([]);
    });

    it('fires subscriptions once per changed pair', () => {
        const Likes = relation();
        const world = createWorld();
        const entity = world.spawn();
        const a = world.spawn();
        const b = world.spawn();
        const c = world.spawn();

        const adds: EntityEvent[] = [];
        const removes: EntityEvent[] = [];
        world.onAdd(Likes, (source, target) => adds.push([source, target!]));
        world.onRemove(Likes, (source, target) => removes.push([source, target!]));

        entity.add(Likes(a));
        adds.length = 0;
        removes.length = 0;

        world.deferred.remove(entity, Likes(a));
        world.deferred.add(entity, Likes(a));
        world.deferred.add(entity, Likes(b));
        world.deferred.add(entity, Likes(b));
        world.deferred.add(entity, Likes(c));
        world.deferred.remove(entity, Likes(c));
        world.deferred.flush();

        expect(adds).toEqual([[entity, b]]);
        expect(removes).toEqual([]);

        const traitAdds: unknown[] = [];
        const traitRemoves: unknown[] = [];
        const changes: unknown[] = [];
        world.onAdd(Position, (target) => traitAdds.push(target));
        world.onRemove(Position, (target) => traitRemoves.push(target));
        world.onChange(Position, (target) => changes.push(target));

        world.deferred.add(entity, Position({ x: 1, y: 1 }));
        world.deferred.add(entity, Position({ x: 2, y: 1 }));
        world.deferred.flush();
        expect(traitAdds).toEqual([entity]);
        expect(traitRemoves).toEqual([]);
        expect(changes).toEqual([]);
        expect(entity.get(Position)).toEqual({ x: 2, y: 1 });

        world.deferred.add(entity, Position({ x: 8, y: 1 }));
        world.deferred.flush();
        expect(changes).toEqual([entity]);
        expect(traitAdds).toEqual([entity]);
    });

    it('fires query subscriptions once for the net membership change', () => {
        const world = createWorld();
        const entity = world.spawn();
        const seen: unknown[] = [];
        world.onQueryAdd([Marker], (target) => seen.push(['add', target]));
        world.onQueryRemove([Marker], (target) => seen.push(['remove', target]));

        world.deferred.add(entity, Marker);
        world.deferred.remove(entity, Marker);
        world.deferred.add(entity, Marker);
        world.deferred.flush();

        expect(seen).toEqual([['add', entity]]);
        expect(world.query(Marker)).toContain(entity);
    });

    it('cascades autoDestroy and respects spawn nullification', () => {
        const ChildOf = relation({ autoDestroy: 'orphan' });
        const Contains = relation({ autoDestroy: 'target' });
        const world = createWorld();

        const parent = world.spawn();
        const child = world.spawn(ChildOf(parent), Marker);
        const removes: unknown[] = [];
        world.onRemove(ChildOf, (source, target) => removes.push([source, target]));
        world.onRemove(Marker, (source) => removes.push(['marker', source]));

        world.deferred.destroy(parent);
        world.deferred.flush();

        expect(world.has(parent)).toBe(false);
        expect(world.has(child)).toBe(false);
        expect(removes).toContainEqual([child, parent]);
        expect(removes).toContainEqual(['marker', child]);
        expect(removes).toHaveLength(2);

        const extraAdds: unknown[] = [];
        world.onAdd(Foo, (entity) => extraAdds.push(entity));
        const kept = world.deferred.spawn(Foo);
        const transient = world.deferred.spawn(Contains(kept), Marker);
        world.deferred.destroy(transient);
        world.deferred.flush();

        expect(world.has(kept)).toBe(true);
        expect(kept.has(Foo)).toBe(true);
        expect(world.has(transient)).toBe(false);
        expect(extraAdds).toEqual([kept]);
    });

    it('commits a deferred spawn before a non-deferred add on that entity', () => {
        const world = createWorld();
        const entity = world.deferred.spawn(Position({ x: 2, y: 3 }));
        entity.add(Marker);

        expect(world.has(entity)).toBe(true);
        expect(entity.has(Marker)).toBe(true);
        expect(entity.get(Position)).toEqual({ x: 2, y: 3 });
    });

    it('flush inside updateEach does not apply outer commands', () => {
        const world = createWorld();
        const entity = world.spawn(Position);
        world.deferred.add(entity, Foo);

        world.query(Position).updateEach(() => {
            world.deferred.add(entity, Bar);
            world.deferred.flush();
            expect(world.query(Bar).length).toBe(1);
            expect(world.query(Foo).length).toBe(0);
        });

        expect(entity.has(Foo)).toBe(true);
        expect(world.query(Foo).length).toBe(0);
    });

    it('can reset while a spawn is still deferred', () => {
        const world = createWorld();
        world.deferred.spawn(Marker);
        expect(() => world.reset()).not.toThrow();
        expect(world.entities).toHaveLength(1);
    });
});

type EntityEvent = [number, number];
