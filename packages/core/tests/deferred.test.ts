import { beforeEach, describe, expect, it, vi } from 'vitest';
import { $internal, createWorld, Not, relation, trait, universe } from '../src';

const Position = trait({ x: 0, y: 0 });
const Velocity = trait({ x: 0, y: 0 });
const Tag = trait();
const Meta = trait(() => ({ name: 'default' }));

describe('Deferred', () => {
    beforeEach(() => {
        universe.reset();
    });

    describe('spawn', () => {
        it('defers spawning until flush', () => {
            const world = createWorld();
            const onAdd = vi.fn();
            world.onAdd(Position, onAdd);

            const entity = world.deferred.spawn(Position({ x: 1 }));

            expect(world.query(Position).length).toBe(0);
            expect(onAdd).not.toHaveBeenCalled();
            expect(entity.has(Position)).toBe(true);
            expect(entity.get(Position)).toEqual({ x: 1, y: 0 });

            world.deferred.flush();

            expect([...world.query(Position)]).toEqual([entity]);
            expect(onAdd).toHaveBeenCalledTimes(1);
            expect(onAdd).toHaveBeenCalledWith(entity);
            expect(entity.get(Position)).toEqual({ x: 1, y: 0 });
        });

        it('keeps AoS values returned before flush', () => {
            const world = createWorld();
            const entity = world.deferred.spawn(Meta);

            const meta = entity.get(Meta)!;
            expect(meta).toEqual({ name: 'default' });

            world.deferred.flush();
            expect(entity.get(Meta)).toBe(meta);
        });

        it('nullifies a spawn destroyed in the same buffer', () => {
            const world = createWorld();
            const onAdd = vi.fn();
            const onRemove = vi.fn();
            const onQueryAdd = vi.fn();
            world.onAdd(Position, onAdd);
            world.onRemove(Position, onRemove);
            world.onQueryAdd([Position], onQueryAdd);

            const entity = world.deferred.spawn(Position);
            world.deferred.destroy(entity);

            expect(entity.has(Position)).toBe(false);

            world.deferred.flush();

            expect(onAdd).not.toHaveBeenCalled();
            expect(onRemove).not.toHaveBeenCalled();
            expect(onQueryAdd).not.toHaveBeenCalled();
            expect(world.has(entity)).toBe(false);
            expect(world.entities.length).toBe(1);
        });
    });

    describe('add and remove', () => {
        it('defers adding and removing traits', () => {
            const world = createWorld();
            const entity = world.spawn(Position);

            world.deferred.add(entity, Velocity({ x: 2 }));
            world.deferred.remove(entity, Position);

            expect(world.query(Velocity).length).toBe(0);
            expect([...world.query(Position)]).toEqual([entity]);
            expect(entity.has(Velocity)).toBe(true);
            expect(entity.get(Velocity)).toEqual({ x: 2, y: 0 });
            expect(entity.has(Position)).toBe(false);
            expect(entity.get(Position)).toBeUndefined();

            world.deferred.flush();

            expect([...world.query(Velocity)]).toEqual([entity]);
            expect(world.query(Position).length).toBe(0);
        });

        it('replaces earlier values for the same trait with later ones', () => {
            const world = createWorld();
            const onAdd = vi.fn();
            world.onAdd(Position, onAdd);
            const entity = world.spawn();

            world.deferred.add(entity, Position({ x: 1, y: 1 }));
            world.deferred.add(entity, Position({ x: 2 }));

            expect(entity.get(Position)).toEqual({ x: 2, y: 0 });

            world.deferred.flush();

            expect(entity.get(Position)).toEqual({ x: 2, y: 0 });
            expect(onAdd).toHaveBeenCalledTimes(1);
        });

        it('keeps existing values when adding a trait the entity already has', () => {
            const world = createWorld();
            const entity = world.spawn(Position({ x: 5 }));

            world.deferred.add(entity, Position({ x: 1 }));
            expect(entity.get(Position)).toEqual({ x: 5, y: 0 });

            world.deferred.flush();
            expect(entity.get(Position)).toEqual({ x: 5, y: 0 });
        });

        it('overwrites the value when a trait is removed and added again', () => {
            const world = createWorld();
            const onAdd = vi.fn();
            const onRemove = vi.fn();
            const entity = world.spawn(Position({ x: 5 }));
            world.onAdd(Position, onAdd);
            world.onRemove(Position, onRemove);

            world.deferred.remove(entity, Position);
            world.deferred.add(entity, Position({ y: 3 }));

            expect(entity.get(Position)).toEqual({ x: 0, y: 3 });

            world.deferred.flush();

            expect(entity.get(Position)).toEqual({ x: 0, y: 3 });
            expect(onAdd).not.toHaveBeenCalled();
            expect(onRemove).not.toHaveBeenCalled();
        });

        it('fires no subscriptions when an add is cancelled by a remove', () => {
            const world = createWorld();
            const onAdd = vi.fn();
            const onRemove = vi.fn();
            world.onAdd(Tag, onAdd);
            world.onRemove(Tag, onRemove);
            const entity = world.spawn();

            world.deferred.add(entity, Tag);
            world.deferred.remove(entity, Tag);
            world.deferred.flush();

            expect(entity.has(Tag)).toBe(false);
            expect(onAdd).not.toHaveBeenCalled();
            expect(onRemove).not.toHaveBeenCalled();
        });
    });

    describe('destroy', () => {
        it('defers destroying entities', () => {
            const world = createWorld();
            const entity = world.spawn(Position);

            world.deferred.destroy(entity);

            expect(world.has(entity)).toBe(true);
            expect([...world.query(Position)]).toEqual([entity]);
            expect(entity.has(Position)).toBe(false);

            world.deferred.flush();

            expect(world.has(entity)).toBe(false);
            expect(world.query(Position).length).toBe(0);
        });

        it('silently skips commands on destroyed entities', () => {
            const world = createWorld();
            const onAdd = vi.fn();
            world.onAdd(Velocity, onAdd);
            const entity = world.spawn(Position);
            const dead = world.spawn();
            dead.destroy();

            world.deferred.destroy(entity);
            world.deferred.add(entity, Velocity);
            world.deferred.remove(entity, Position);
            world.deferred.destroy(entity);
            world.deferred.add(dead, Velocity);
            world.deferred.destroy(dead);

            expect(() => world.deferred.flush()).not.toThrow();
            expect(onAdd).not.toHaveBeenCalled();
            expect(world.has(entity)).toBe(false);
        });

        it('drops pending changes on an entity that is later destroyed', () => {
            const world = createWorld();
            const onAdd = vi.fn();
            const onRemove = vi.fn();
            world.onAdd(Velocity, onAdd);
            world.onRemove(Position, onRemove);
            const entity = world.spawn(Position);

            world.deferred.add(entity, Velocity);
            world.deferred.destroy(entity);
            world.deferred.flush();

            expect(onAdd).not.toHaveBeenCalled();
            expect(onRemove).toHaveBeenCalledTimes(1);
        });

        it('throws when a deferred world entity destruction executes', () => {
            const world = createWorld();
            const worldEntity = world[$internal].worldEntity;
            const entity = world.spawn();

            world.deferred.destroy(worldEntity);
            world.deferred.add(entity, Tag);

            expect(() => world.deferred.flush()).toThrow();
            expect(world.has(worldEntity)).toBe(true);
            expect(entity.has(Tag)).toBe(true);
        });
    });

    describe('ordering', () => {
        it('executes earlier commands before later ones', () => {
            const world = createWorld();
            const order: string[] = [];
            world.onAdd(Position, (e) => order.push(`position ${e}`));
            world.onAdd(Velocity, (e) => order.push(`velocity ${e}`));
            world.onRemove(Tag, (e) => order.push(`tag ${e}`));

            const a = world.spawn(Tag);
            const b = world.spawn();

            world.deferred.add(b, Velocity);
            world.deferred.add(a, Position);
            world.deferred.remove(a, Tag);
            world.deferred.add(b, Position);
            world.deferred.flush();

            expect(order).toEqual([`velocity ${b}`, `position ${a}`, `tag ${a}`, `position ${b}`]);
        });

        it('adds relations to entities spawned earlier in the buffer', () => {
            const world = createWorld();
            const ChildOf = relation();

            const parent = world.deferred.spawn();
            const child = world.deferred.spawn(ChildOf(parent));

            expect(child.has(ChildOf(parent))).toBe(true);

            world.deferred.flush();

            expect(child.has(ChildOf(parent))).toBe(true);
            expect([...world.query(ChildOf(parent))]).toEqual([child]);
        });
    });

    describe('execution triggers', () => {
        it('flushes when updateEach exits', () => {
            const world = createWorld();
            const entities = [world.spawn(Position), world.spawn(Position)];

            world.query(Position).updateEach((_, entity) => {
                world.deferred.add(entity, Velocity);
                world.deferred.spawn(Position);
                expect(world.query(Velocity).length).toBe(0);
                expect(entity.has(Velocity)).toBe(true);
            });

            expect([...world.query(Velocity)]).toEqual(entities);
            expect(world.query(Position).length).toBe(4);
        });

        it('flushes when updateEach exits with a relation-only query', () => {
            const world = createWorld();
            const ChildOf = relation();
            const parent = world.spawn();
            const child = world.spawn(ChildOf(parent));

            world.query(ChildOf(parent)).updateEach((_, entity) => {
                world.deferred.add(entity, Tag);
            });

            expect([...world.query(Tag)]).toEqual([child]);
        });

        it('flushes when updateEach exits by throwing', () => {
            const world = createWorld();
            const entity = world.spawn(Position);

            expect(() =>
                world.query(Position).updateEach(() => {
                    world.deferred.add(entity, Tag);
                    throw new Error('boom');
                })
            ).toThrow('boom');

            expect([...world.query(Tag)]).toEqual([entity]);
            expect(world[$internal].deferredScopes.length).toBe(1);
        });

        it('commits updateEach state before executing deferred commands', () => {
            const world = createWorld();
            const entity = world.spawn(Position);

            world.query(Position).updateEach(([position], e) => {
                position.x = 10;
                world.deferred.destroy(e);
            });

            expect(world.has(entity)).toBe(false);
        });

        it('flushes pending commands before a non-deferred mutation on the entity', () => {
            const world = createWorld();
            const entity = world.spawn();
            const other = world.spawn();

            world.deferred.add(entity, Position({ x: 1 }));
            world.deferred.add(other, Tag);

            entity.set(Position, { y: 2 });

            expect([...world.query(Position)]).toEqual([entity]);
            expect(entity.get(Position)).toEqual({ x: 1, y: 2 });
            expect([...world.query(Tag)]).toEqual([other]);
        });

        it('does not flush for mutations on entities without pending commands', () => {
            const world = createWorld();
            const entity = world.spawn();
            const other = world.spawn();

            world.deferred.add(entity, Tag);
            other.add(Position);

            expect(world.query(Tag).length).toBe(0);
        });

        it('flushes before a non-deferred destroy of an entity with pending commands', () => {
            const world = createWorld();
            const onAdd = vi.fn();
            world.onAdd(Tag, onAdd);
            const entity = world.spawn();

            world.deferred.add(entity, Tag);
            entity.destroy();

            expect(onAdd).toHaveBeenCalledTimes(1);
            expect(world.has(entity)).toBe(false);
        });

        it('flushes inner scopes independently and preserves outer buffers', () => {
            const world = createWorld();
            const outer = world.spawn(Position);
            const inner = world.spawn(Velocity);

            world.query(Position).updateEach(() => {
                world.deferred.add(outer, Tag);

                world.query(Velocity).updateEach(() => {
                    world.deferred.add(inner, Tag);
                });

                expect([...world.query(Tag)]).toEqual([inner]);
                expect(outer.has(Tag)).toBe(true);
            });

            expect([...world.query(Tag)]).toEqual([inner, outer]);
        });

        it('lets inner scopes change entities spawned in an outer buffer', () => {
            const world = createWorld();
            const onAdd = vi.fn();
            world.onAdd(Position, onAdd);
            world.spawn(Velocity);
            let spawned!: ReturnType<typeof world.spawn>;

            world.query(Velocity).updateEach(() => {
                spawned = world.deferred.spawn(Position);

                world.query(Velocity).updateEach(() => {
                    world.deferred.add(spawned, Tag);
                });

                expect([...world.query(Tag)]).toEqual([spawned]);
                expect(spawned.has(Position)).toBe(true);
                expect(onAdd).not.toHaveBeenCalled();
            });

            expect([...world.query(Position, Tag)]).toEqual([spawned]);
            expect(onAdd).toHaveBeenCalledTimes(1);
        });

        it('flush only executes the current scope', () => {
            const world = createWorld();
            const a = world.spawn(Position);
            const b = world.spawn();

            world.deferred.add(b, Tag);

            world.query(Position).updateEach(() => {
                world.deferred.add(a, Tag);
                world.deferred.flush();
                expect([...world.query(Tag)]).toEqual([a]);
            });

            expect([...world.query(Tag)]).toEqual([a]);
            world.deferred.flush();
            expect([...world.query(Tag)]).toEqual([a, b]);
        });

        it('executes commands deferred by subscriptions during flush', () => {
            const world = createWorld();
            world.onAdd(Position, (entity) => world.deferred.add(entity, Tag));

            const entity = world.deferred.spawn(Position);
            world.deferred.flush();

            expect([...world.query(Tag)]).toEqual([entity]);
        });
    });

    describe('relations', () => {
        it('defers adding and removing relation pairs', () => {
            const world = createWorld();
            const Likes = relation({ store: { weight: 1 } });
            const entity = world.spawn();
            const a = world.spawn();
            const b = world.spawn();
            entity.add(Likes(a));

            world.deferred.add(entity, Likes(b, { weight: 5 }));
            world.deferred.remove(entity, Likes(a));

            expect(entity.has(Likes(b))).toBe(true);
            expect(entity.get(Likes(b))).toEqual({ weight: 5 });
            expect(entity.has(Likes(a))).toBe(false);
            expect(world.query(Likes(b)).length).toBe(0);

            world.deferred.flush();

            expect(entity.targetsFor(Likes)).toEqual([b]);
            expect(entity.get(Likes(b))).toEqual({ weight: 5 });
        });

        it('replaces earlier pair values with later ones', () => {
            const world = createWorld();
            const Likes = relation({ store: { weight: 1 } });
            const onAdd = vi.fn();
            world.onAdd(Likes, onAdd);
            const entity = world.spawn();
            const target = world.spawn();

            world.deferred.add(entity, Likes(target, { weight: 2 }));
            world.deferred.add(entity, Likes(target, { weight: 3 }));
            world.deferred.flush();

            expect(entity.get(Likes(target))).toEqual({ weight: 3 });
            expect(onAdd).toHaveBeenCalledTimes(1);
        });

        it('addExclusive replaces existing pairs with one', () => {
            const world = createWorld();
            const Likes = relation();
            const onAdd = vi.fn();
            const onRemove = vi.fn();
            const entity = world.spawn();
            const a = world.spawn();
            const b = world.spawn();
            const c = world.spawn();
            entity.add(Likes(a), Likes(b));
            world.onAdd(Likes, onAdd);
            world.onRemove(Likes, onRemove);

            world.deferred.addExclusive(entity, Likes(c));

            expect(entity.has(Likes(a))).toBe(false);
            expect(entity.has(Likes(b))).toBe(false);
            expect(entity.has(Likes(c))).toBe(true);

            world.deferred.flush();

            expect(entity.targetsFor(Likes)).toEqual([c]);
            expect(onAdd).toHaveBeenCalledTimes(1);
            expect(onAdd).toHaveBeenCalledWith(entity, c);
            expect(onRemove).toHaveBeenCalledTimes(2);
        });

        it('addExclusive keeps an existing pair without firing subscriptions', () => {
            const world = createWorld();
            const Likes = relation();
            const onAdd = vi.fn();
            const onRemove = vi.fn();
            const entity = world.spawn();
            const a = world.spawn();
            const b = world.spawn();
            entity.add(Likes(a), Likes(b));
            world.onAdd(Likes, onAdd);
            world.onRemove(Likes, onRemove);

            world.deferred.addExclusive(entity, Likes(a));
            world.deferred.flush();

            expect(entity.targetsFor(Likes)).toEqual([a]);
            expect(onAdd).not.toHaveBeenCalled();
            expect(onRemove).toHaveBeenCalledTimes(1);
            expect(onRemove).toHaveBeenCalledWith(entity, b);
        });

        it('wildcard clears all pairs', () => {
            const world = createWorld();
            const Likes = relation();
            const entity = world.spawn();
            const a = world.spawn();
            const b = world.spawn();
            entity.add(Likes(a), Likes(b));

            world.deferred.addExclusive(entity, Likes('*'));
            expect(entity.has(Likes('*'))).toBe(false);
            world.deferred.flush();
            expect(entity.has(Likes('*'))).toBe(false);

            entity.add(Likes(a), Likes(b));
            world.deferred.remove(entity, Likes('*'));
            world.deferred.add(entity, Likes(b));
            expect(entity.has(Likes(a))).toBe(false);
            expect(entity.has(Likes(b))).toBe(true);
            world.deferred.flush();
            expect(entity.targetsFor(Likes)).toEqual([b]);
        });

        it('fires subscriptions once per pair based on the net change', () => {
            const world = createWorld();
            const ChildOf = relation({ exclusive: true });
            const onAdd = vi.fn();
            const onRemove = vi.fn();
            const entity = world.spawn();
            const a = world.spawn();
            const b = world.spawn();
            entity.add(ChildOf(a));
            world.onAdd(ChildOf, onAdd);
            world.onRemove(ChildOf, onRemove);

            world.deferred.add(entity, ChildOf(b));
            world.deferred.add(entity, ChildOf(a));
            expect(entity.has(ChildOf(a))).toBe(true);
            expect(entity.has(ChildOf(b))).toBe(false);
            world.deferred.flush();

            expect(entity.targetFor(ChildOf)).toBe(a);
            expect(onAdd).not.toHaveBeenCalled();
            expect(onRemove).not.toHaveBeenCalled();

            world.deferred.add(entity, ChildOf(b));
            world.deferred.flush();

            expect(entity.targetFor(ChildOf)).toBe(b);
            expect(onRemove).toHaveBeenCalledTimes(1);
            expect(onRemove).toHaveBeenCalledWith(entity, a);
            expect(onAdd).toHaveBeenCalledTimes(1);
            expect(onAdd).toHaveBeenCalledWith(entity, b);
        });

        it('skips pairs targeting entities destroyed in the buffer', () => {
            const world = createWorld();
            const Likes = relation();
            const onAdd = vi.fn();
            world.onAdd(Likes, onAdd);
            const entity = world.spawn();
            const target = world.spawn();

            world.deferred.add(entity, Likes(target));
            world.deferred.destroy(target);

            expect(entity.has(Likes(target))).toBe(false);

            world.deferred.flush();

            expect(onAdd).not.toHaveBeenCalled();
            expect(entity.has(Likes('*'))).toBe(false);
        });
    });

    describe('autoDestroy', () => {
        it('cascades to existing sources', () => {
            const world = createWorld();
            const ChildOf = relation({ autoDestroy: 'orphan' });
            const parent = world.spawn();
            const child = world.spawn(ChildOf(parent));
            const grandchild = world.spawn(ChildOf(child));

            world.deferred.destroy(parent);

            expect(world.has(child)).toBe(true);
            expect(grandchild.has(ChildOf(child))).toBe(false);

            world.deferred.flush();

            expect(world.has(parent)).toBe(false);
            expect(world.has(child)).toBe(false);
            expect(world.has(grandchild)).toBe(false);
        });

        it('nullifies cascaded entities spawned in the same buffer', () => {
            const world = createWorld();
            const ChildOf = relation({ autoDestroy: 'orphan' });
            const onAdd = vi.fn();
            const onQueryAdd = vi.fn();
            world.onAdd(ChildOf, onAdd);
            world.onQueryAdd([Position], onQueryAdd);

            const parent = world.spawn();
            const child = world.deferred.spawn(Position, ChildOf(parent));
            const grandchild = world.deferred.spawn(Position, ChildOf(child));

            world.deferred.destroy(parent);
            world.deferred.flush();

            expect(onAdd).not.toHaveBeenCalled();
            expect(onQueryAdd).not.toHaveBeenCalled();
            expect(world.has(parent)).toBe(false);
            expect(world.has(child)).toBe(false);
            expect(world.has(grandchild)).toBe(false);
            expect(world.entities.length).toBe(1);
        });

        it('cascades to targets spawned in the same buffer', () => {
            const world = createWorld();
            const Contains = relation({ autoDestroy: 'target' });
            const onAdd = vi.fn();
            world.onAdd(Tag, onAdd);

            const container = world.spawn();
            const item = world.deferred.spawn(Tag);
            world.deferred.add(container, Contains(item));
            world.deferred.destroy(container);
            world.deferred.flush();

            expect(onAdd).not.toHaveBeenCalled();
            expect(world.has(container)).toBe(false);
            expect(world.has(item)).toBe(false);
        });

        it('does not cascade to sources whose pair was removed earlier in the buffer', () => {
            const world = createWorld();
            const ChildOf = relation({ autoDestroy: 'orphan' });
            const parent = world.spawn();
            const child = world.spawn(ChildOf(parent));

            world.deferred.remove(child, ChildOf(parent));
            world.deferred.destroy(parent);
            world.deferred.flush();

            expect(world.has(parent)).toBe(false);
            expect(world.has(child)).toBe(true);
        });
    });

    it('matches queries with Not modifiers once spawned entities are flushed', () => {
        const world = createWorld();
        const entity = world.deferred.spawn(Position);

        expect(world.query(Position, Not(Velocity)).length).toBe(0);
        world.deferred.flush();
        expect([...world.query(Position, Not(Velocity))]).toEqual([entity]);
    });

    it('clears pending commands on reset', () => {
        const world = createWorld();
        const entity = world.spawn();
        world.deferred.add(entity, Tag);
        world.deferred.spawn(Position);

        world.reset();
        world.deferred.flush();

        expect(world.query(Tag).length).toBe(0);
        expect(world.query(Position).length).toBe(0);
        expect(world.entities.length).toBe(1);
    });
});
