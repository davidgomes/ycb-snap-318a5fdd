import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
    $internal,
    createAdded,
    createRemoved,
    createWorld,
    type Entity,
    ordered,
    relation,
    trait,
} from '../src';

const Position = trait({ x: 0, y: 0 });
const Velocity = trait({ x: 0, y: 0 });
const Inventory = trait(() => ({ items: [] as string[] }));
const IsActive = trait();

describe('Deferred', () => {
    const world = createWorld();

    beforeEach(() => {
        world.reset();
    });

    describe('commands', () => {
        it('spawns entities when flushed', () => {
            const entity = world.deferred.spawn(Position({ x: 1 }), IsActive);

            expect(world.has(entity)).toBe(false);
            expect(world.query(Position)).not.toContain(entity);

            world.deferred.flush();

            expect(world.has(entity)).toBe(true);
            expect([...world.query(Position, IsActive)]).toEqual([entity]);
            expect(entity.get(Position)).toEqual({ x: 1, y: 0 });
        });

        it('destroys entities when flushed', () => {
            const entity = world.spawn(Position);

            world.deferred.destroy(entity);
            expect(world.has(entity)).toBe(true);

            world.deferred.flush();
            expect(world.has(entity)).toBe(false);
            expect([...world.query(Position)]).toEqual([]);
        });

        it('adds and removes traits when flushed', () => {
            const entity = world.spawn(Position);

            world.deferred.add(entity, Velocity({ x: 2 }), IsActive);
            world.deferred.remove(entity, Position);

            expect([...world.query(Velocity)]).toEqual([]);
            expect([...world.query(Position)]).toEqual([entity]);

            world.deferred.flush();

            expect([...world.query(Velocity, IsActive)]).toEqual([entity]);
            expect([...world.query(Position)]).toEqual([]);
            expect(entity.get(Velocity)).toEqual({ x: 2, y: 0 });
        });

        it('adds and removes relation pairs when flushed', () => {
            const Contains = relation({ store: { amount: 0 } });
            const chest = world.spawn();
            const gold = world.spawn();
            const silver = world.spawn();
            chest.add(Contains(silver));

            world.deferred.add(chest, Contains(gold, { amount: 5 }));
            world.deferred.remove(chest, Contains(silver));
            expect([...world.query(Contains(gold))]).toEqual([]);

            world.deferred.flush();

            expect(chest.targetsFor(Contains)).toEqual([gold]);
            expect(chest.get(Contains(gold))).toEqual({ amount: 5 });
        });

        it('executes the commands of updateEach when it exits', () => {
            const entities = [world.spawn(Position), world.spawn(Position), world.spawn(Position)];

            world.query(Position).updateEach(([position], entity) => {
                position.x = 1;
                world.deferred.remove(entity, Position);
                world.deferred.add(entity, IsActive);
                world.deferred.spawn(Velocity);

                // Nothing changes structurally while iterating.
                expect(world.query(Position)).toHaveLength(3);
                expect(world.query(IsActive)).toHaveLength(0);
            });

            expect([...world.query(Position)]).toEqual([]);
            expect([...world.query(IsActive)]).toEqual(entities);
            expect(world.query(Velocity)).toHaveLength(3);
        });

        it('replaces existing relation pairs with addExclusive', () => {
            const Likes = relation({ store: { amount: 0 } });
            const person = world.spawn();
            const apple = world.spawn();
            const banana = world.spawn();
            const cherry = world.spawn();
            person.add(Likes(apple), Likes(banana, { amount: 3 }));

            world.deferred.addExclusive(person, Likes(cherry, { amount: 1 }));
            world.deferred.flush();

            expect(person.targetsFor(Likes)).toEqual([cherry]);
            expect(person.get(Likes(cherry))).toEqual({ amount: 1 });

            // An existing pair is kept along with its data.
            person.add(Likes(banana, { amount: 3 }));
            world.deferred.addExclusive(person, Likes(banana));
            world.deferred.flush();

            expect(person.targetsFor(Likes)).toEqual([banana]);
            expect(person.get(Likes(banana))).toEqual({ amount: 3 });
        });

        it('clears all relation pairs with a wildcard addExclusive', () => {
            const Likes = relation();
            const person = world.spawn();
            const apple = world.spawn();
            const banana = world.spawn();
            person.add(Likes(apple), Likes(banana));

            world.deferred.addExclusive(person, Likes('*'));
            world.deferred.flush();

            expect(person.targetsFor(Likes)).toEqual([]);
            expect(person.has(Likes('*'))).toBe(false);
            expect([...world.query(Likes('*'))]).toEqual([]);
        });

        it('throws when a deferred destruction of the world entity executes', () => {
            const worldEntity = world[$internal].worldEntity;
            const before = world.spawn();
            const after = world.spawn();

            world.deferred.add(before, IsActive);
            expect(() => world.deferred.destroy(worldEntity)).not.toThrow();
            world.deferred.add(after, IsActive);

            expect(() => world.deferred.flush()).toThrow();
            expect(world.has(worldEntity)).toBe(true);
            expect([...world.query(IsActive)]).toEqual([before]);

            // Only the offending command is dropped.
            expect(() => world.deferred.flush()).not.toThrow();
            expect([...world.query(IsActive)]).toEqual([before, after]);
        });

        it('throws when updateEach exits with a deferred destruction of the world entity', () => {
            world.spawn(Position);

            expect(() =>
                world.query(Position).updateEach(() => {
                    world.deferred.destroy(world[$internal].worldEntity);
                })
            ).toThrow();

            expect(world.has(world[$internal].worldEntity)).toBe(true);
            expect(() => world.deferred.flush()).not.toThrow();
        });

        it('executes commands deferred by subscriptions during a flush', () => {
            world.onAdd(Position, (entity) => world.deferred.add(entity, IsActive));
            const entity = world.spawn();

            world.deferred.add(entity, Position);
            world.deferred.flush();

            expect([...world.query(Position, IsActive)]).toEqual([entity]);
        });

        it('reuses recycled entity ids for deferred spawns', () => {
            const destroyed = world.spawn();
            const kept = world.spawn();
            destroyed.destroy();

            const spawned = world.deferred.spawn(Position);
            const other = world.spawn();

            expect(spawned.id()).toBe(destroyed.id());
            expect(spawned.generation()).toBe(destroyed.generation() + 1);
            expect(other.id()).not.toBe(spawned.id());
            expect(world.has(destroyed)).toBe(false);
            expect(world.has(spawned)).toBe(false);

            world.deferred.flush();

            expect(world.has(destroyed)).toBe(false);
            expect(world.has(spawned)).toBe(true);
            expect(world.entities.sort()).toEqual(
                [world[$internal].worldEntity, kept, spawned, other].sort()
            );
        });

        it('discards pending commands on reset', () => {
            const entity = world.spawn();
            world.deferred.add(entity, Position);
            const spawned = world.deferred.spawn(Position);

            world.reset();
            world.deferred.flush();

            expect([...world.query(Position)]).toEqual([]);
            expect(world.has(spawned)).toBe(false);
        });
    });

    describe('ordering', () => {
        it('executes commands in the order they were deferred', () => {
            const a = world.spawn();
            const b = world.spawn();

            world.deferred.add(a, IsActive);
            world.deferred.remove(a, IsActive);
            world.deferred.remove(b, IsActive);
            world.deferred.add(b, IsActive);
            world.deferred.flush();

            expect(a.has(IsActive)).toBe(false);
            expect(b.has(IsActive)).toBe(true);
        });

        it('applies changes in the order they were first deferred', () => {
            const added: Entity[] = [];
            world.onAdd(Position, (entity) => added.push(entity));
            world.query(Position);

            const existing = world.spawn();
            const first = world.deferred.spawn(Position);
            world.deferred.add(existing, Position);
            const last = world.deferred.spawn(Position);
            world.deferred.flush();

            expect(added).toEqual([first, existing, last]);
            expect([...world.query(Position)]).toEqual([first, existing, last]);
        });

        it('replaces earlier values for the same trait with later ones', () => {
            const Contains = relation({ store: { amount: 0 } });
            const entity = world.spawn();
            const gold = world.spawn();
            const items = { items: ['sword'] };

            world.deferred.add(entity, Position({ x: 1, y: 1 }));
            world.deferred.add(entity, Position({ y: 2 }));
            world.deferred.add(entity, Inventory({ items: [] }));
            world.deferred.add(entity, Inventory(items));
            world.deferred.add(entity, Contains(gold, { amount: 5 }));
            world.deferred.add(entity, Contains(gold, { amount: 10 }));
            world.deferred.flush();

            expect(entity.get(Position)).toEqual({ x: 0, y: 2 });
            expect(entity.get(Inventory)).toBe(items);
            expect(entity.get(Contains(gold))).toEqual({ amount: 10 });
        });

        it('replaces values given at spawn with later values', () => {
            const entity = world.deferred.spawn(Position({ x: 1 }));
            world.deferred.add(entity, Position({ x: 2 }));
            world.deferred.flush();

            expect(entity.get(Position)).toEqual({ x: 2, y: 0 });
        });

        it('keeps the earlier value when a later add has no value', () => {
            const entity = world.spawn();

            world.deferred.add(entity, Position({ x: 1 }));
            world.deferred.add(entity, Position);
            world.deferred.flush();

            expect(entity.get(Position)).toEqual({ x: 1, y: 0 });
        });

        it('keeps the value of a trait the entity already has', () => {
            const entity = world.spawn(Position({ x: 1 }));

            world.deferred.add(entity, Position({ x: 5 }));
            world.deferred.flush();

            expect(entity.get(Position)).toEqual({ x: 1, y: 0 });
        });

        it('reinitializes the value of a trait that is removed and added again', () => {
            const entity = world.spawn(Position({ x: 1 }));

            world.deferred.remove(entity, Position);
            world.deferred.add(entity, Position({ x: 5 }));
            world.deferred.flush();

            expect(entity.get(Position)).toEqual({ x: 5, y: 0 });
        });
    });

    describe('execution triggers', () => {
        it('executes pending commands before a non-deferred mutation on the same entity', () => {
            const entity = world.spawn();

            world.deferred.add(entity, IsActive);
            entity.remove(IsActive);
            expect(entity.has(IsActive)).toBe(false);

            world.deferred.flush();
            expect(entity.has(IsActive)).toBe(false);

            world.deferred.add(entity, Position({ x: 1 }));
            entity.set(Position, { x: 2 });
            expect(entity.get(Position)).toEqual({ x: 2, y: 0 });

            world.deferred.add(entity, Velocity);
            entity.add(IsActive);
            expect([...world.query(Velocity, IsActive)]).toEqual([entity]);
        });

        it('executes the whole buffer when triggered', () => {
            const a = world.spawn();
            const b = world.spawn();

            world.deferred.add(a, IsActive);
            world.deferred.add(b, IsActive);
            a.add(Position);

            expect([...world.query(IsActive)]).toEqual([a, b]);
        });

        it('spawns a pending entity before it is mutated', () => {
            const entity = world.deferred.spawn(Position);

            entity.add(IsActive);

            expect(world.has(entity)).toBe(true);
            expect([...world.query(Position, IsActive)]).toEqual([entity]);
        });

        it('executes pending commands before a non-deferred destroy', () => {
            const added = vi.fn();
            const removed = vi.fn();
            world.onAdd(IsActive, added);
            world.onRemove(IsActive, removed);

            const entity = world.spawn();
            world.deferred.add(entity, IsActive);
            entity.destroy();

            expect(added).toHaveBeenCalledTimes(1);
            expect(removed).toHaveBeenCalledTimes(1);
            expect(world.has(entity)).toBe(false);
        });

        it('executes pending world entity commands before world mutations', () => {
            const worldEntity = world[$internal].worldEntity;

            world.deferred.add(worldEntity, Position({ x: 1 }));
            world.set(Position, { x: 2 });

            expect(world.get(Position)).toEqual({ x: 2, y: 0 });
        });

        it('executes outer buffers before a mutation inside a scope', () => {
            const entity = world.spawn(Position);
            world.deferred.add(entity, IsActive);

            world.query(Position).updateEach((_, e) => {
                world.deferred.add(e, Velocity);
                e.remove(IsActive);

                expect(world.query(Velocity)).toContain(e);
            });

            expect(entity.has(IsActive)).toBe(false);
            world.deferred.flush();
            expect(entity.has(IsActive)).toBe(false);
        });

        it('does not execute commands for mutations on other entities', () => {
            const a = world.spawn();
            const b = world.spawn();

            world.deferred.add(a, IsActive);
            b.add(Position);
            world.spawn(Velocity);

            expect([...world.query(IsActive)]).toEqual([]);
        });
    });

    describe('has and get', () => {
        it('return the results they would after a flush', () => {
            const entity = world.spawn(Position({ x: 1 }), Velocity);

            world.deferred.add(entity, IsActive, Inventory);
            world.deferred.remove(entity, Velocity);
            world.deferred.remove(entity, Position);
            world.deferred.add(entity, Position({ x: 3 }));

            const pending = {
                active: entity.has(IsActive),
                velocity: [entity.has(Velocity), entity.get(Velocity)],
                position: [entity.has(Position), entity.get(Position)],
                inventory: entity.get(Inventory),
            };

            world.deferred.flush();

            expect(pending).toEqual({
                active: true,
                velocity: [false, undefined],
                position: [true, { x: 3, y: 0 }],
                inventory: { items: [] },
            });
            expect(pending.active).toBe(entity.has(IsActive));
            expect(pending.velocity).toEqual([entity.has(Velocity), entity.get(Velocity)]);
            expect(pending.position).toEqual([entity.has(Position), entity.get(Position)]);
        });

        it('return the stored instance for AoS traits', () => {
            const entity = world.spawn();
            world.deferred.add(entity, Inventory);

            const inventory = entity.get(Inventory);
            world.deferred.flush();

            expect(entity.get(Inventory)).toBe(inventory);
        });

        it('return copies of pending SoA records', () => {
            const entity = world.spawn();
            world.deferred.add(entity, Position({ x: 1 }));

            entity.get(Position)!.x = 10;
            world.deferred.flush();

            expect(entity.get(Position)).toEqual({ x: 1, y: 0 });
        });

        it('reflect pending spawns and destroys', () => {
            const spawned = world.deferred.spawn(Position({ x: 1 }));
            const destroyed = world.spawn(Position);
            world.deferred.destroy(destroyed);

            expect(spawned.has(Position)).toBe(true);
            expect(spawned.get(Position)).toEqual({ x: 1, y: 0 });
            expect(spawned.has(Velocity)).toBe(false);
            expect(destroyed.has(Position)).toBe(false);
            expect(destroyed.get(Position)).toBeUndefined();

            world.deferred.flush();

            expect(spawned.has(Position)).toBe(true);
            expect(destroyed.has(Position)).toBe(false);
        });

        it('reflect pending relation pairs', () => {
            const Contains = relation({ store: { amount: 0 } });
            const Targeting = relation({ exclusive: true });
            const entity = world.spawn();
            const gold = world.spawn();
            const silver = world.spawn();
            entity.add(Contains(silver), Targeting(silver));

            world.deferred.add(entity, Contains(gold, { amount: 2 }), Targeting(gold));
            world.deferred.remove(entity, Contains(silver));

            expect(entity.has(Contains(gold))).toBe(true);
            expect(entity.get(Contains(gold))).toEqual({ amount: 2 });
            expect(entity.has(Contains(silver))).toBe(false);
            expect(entity.get(Contains(silver))).toBeUndefined();
            expect(entity.has(Targeting(gold))).toBe(true);
            expect(entity.has(Targeting(silver))).toBe(false);

            world.deferred.remove(entity, Contains('*'));
            expect(entity.has(Contains('*'))).toBe(false);

            world.deferred.flush();
            expect(entity.has(Contains('*'))).toBe(false);
            expect(entity.has(Targeting(gold))).toBe(true);
        });

        it('reflect cascades and destroyed relation targets', () => {
            const ChildOf = relation({ autoDestroy: 'orphan' });
            const Likes = relation();
            const parent = world.spawn();
            const child = world.spawn(Position, ChildOf(parent));
            const fan = world.spawn(Likes(parent));

            world.deferred.destroy(parent);

            expect(child.has(Position)).toBe(false);
            expect(fan.has(Likes(parent))).toBe(false);
            expect(fan.has(Likes('*'))).toBe(false);

            world.deferred.flush();

            expect(world.has(child)).toBe(false);
            expect(fan.has(Likes('*'))).toBe(false);
        });

        it('reflect world traits', () => {
            const worldEntity = world[$internal].worldEntity;

            world.deferred.add(worldEntity, Position({ x: 1 }));

            expect(world.has(Position)).toBe(true);
            expect(world.get(Position)).toEqual({ x: 1, y: 0 });
        });
    });

    describe('scopes', () => {
        it('flushes inner scopes independently and preserves outer buffers', () => {
            const outer = world.spawn(Position);
            const inner = world.spawn(Velocity);
            const rootEntity = world.spawn();

            world.deferred.add(rootEntity, IsActive);

            world.query(Position).updateEach((_, entity) => {
                world.deferred.add(entity, Inventory);

                world.query(Velocity).updateEach((_, innerEntity) => {
                    world.deferred.add(innerEntity, IsActive);
                });

                expect([...world.query(IsActive)]).toEqual([inner]);
                expect([...world.query(Inventory)]).toEqual([]);
            });

            expect([...world.query(Inventory)]).toEqual([outer]);
            expect([...world.query(IsActive)]).toEqual([inner]);

            world.deferred.flush();
            expect([...world.query(IsActive)]).toEqual([inner, rootEntity]);
        });

        it('flushes only the current scope with flush', () => {
            const rootEntity = world.spawn();
            const entity = world.spawn(Position);

            world.deferred.add(rootEntity, IsActive);

            world.query(Position).updateEach((_, e) => {
                world.deferred.add(e, Velocity);
                world.deferred.flush();

                expect([...world.query(Velocity)]).toEqual([entity]);
                expect([...world.query(IsActive)]).toEqual([]);
            });

            expect([...world.query(IsActive)]).toEqual([]);
            world.deferred.flush();
            expect([...world.query(IsActive)]).toEqual([rootEntity]);
        });

        it('waits for the outer scope to spawn entities used by an inner scope', () => {
            world.spawn(Position);
            world.spawn(Velocity);
            let spawned!: Entity;

            world.query(Position).updateEach(() => {
                spawned = world.deferred.spawn();

                world.query(Velocity).updateEach(() => {
                    world.deferred.add(spawned, IsActive);
                });

                expect(world.has(spawned)).toBe(false);
                expect(spawned.has(IsActive)).toBe(true);
            });

            expect(world.has(spawned)).toBe(true);
            expect(spawned.has(IsActive)).toBe(true);
            expect([...world.query(IsActive)]).toEqual([spawned]);
        });

        it('keeps the commands of a scope that throws', () => {
            const entity = world.spawn(Position);

            expect(() =>
                world.query(Position).updateEach(() => {
                    world.deferred.add(entity, IsActive);
                    throw new Error('Oops');
                })
            ).toThrow('Oops');

            expect([...world.query(IsActive)]).toEqual([]);
            world.deferred.flush();
            expect([...world.query(IsActive)]).toEqual([entity]);
        });

        it('flushes relation-only query scopes on exit', () => {
            const ChildOf = relation();
            const parent = world.spawn();
            const children = [world.spawn(ChildOf(parent)), world.spawn(ChildOf(parent))];

            world.query(ChildOf(parent)).updateEach((_, child) => {
                world.deferred.destroy(child);
                expect(world.has(child)).toBe(true);
            });

            expect(children.some((child) => world.has(child))).toBe(false);
        });
    });

    describe('destroyed entities', () => {
        it('silently skips commands on destroyed entities', () => {
            const Likes = relation();
            const entity = world.spawn();
            const dead = world.spawn();
            const target = world.spawn();
            dead.destroy();

            world.deferred.destroy(entity);
            world.deferred.add(entity, IsActive);
            world.deferred.destroy(entity);
            world.deferred.add(dead, IsActive);
            world.deferred.destroy(dead);
            world.deferred.destroy(target);
            const spawned = world.deferred.spawn(IsActive, Likes(target));

            expect(() => world.deferred.flush()).not.toThrow();
            expect(world.has(entity)).toBe(false);
            expect([...world.query(IsActive)]).toEqual([spawned]);
            expect(spawned.has(Likes('*'))).toBe(false);
        });

        it('nullifies a spawn and destroy in the same buffer', () => {
            const added = vi.fn();
            const removed = vi.fn();
            const queryAdded = vi.fn();
            world.onAdd(Position, added);
            world.onRemove(Position, removed);
            world.onQueryAdd([Position], queryAdded);
            const Added = createAdded();
            const Removed = createRemoved();
            world.query(Added(Position));
            world.query(Removed(Position));
            const count = world.entities.length;

            const entity = world.deferred.spawn(Position);
            world.deferred.add(entity, Velocity);
            world.deferred.destroy(entity);
            world.deferred.flush();

            expect(world.has(entity)).toBe(false);
            expect(world.entities.length).toBe(count);
            expect(added).not.toHaveBeenCalled();
            expect(removed).not.toHaveBeenCalled();
            expect(queryAdded).not.toHaveBeenCalled();
            expect([...world.query(Added(Position))]).toEqual([]);
            expect([...world.query(Removed(Position))]).toEqual([]);
        });

        it('recycles the entity of a nullified spawn', () => {
            const entity = world.deferred.spawn();
            world.deferred.destroy(entity);
            world.deferred.flush();

            const next = world.spawn();
            expect(next.id()).toBe(entity.id());
            expect(next).not.toBe(entity);
            expect(entity.isAlive()).toBe(false);
        });

        it('cascades autoDestroy relations and nullifies spawned entities', () => {
            const ChildOf = relation({ autoDestroy: 'orphan' });
            const removed: Entity[] = [];
            world.onRemove(Position, (entity) => removed.push(entity));

            const parent = world.spawn(Position);
            const child = world.spawn(Position, ChildOf(parent));
            const spawnedChild = world.deferred.spawn(Position, ChildOf(parent));
            const grandchild = world.deferred.spawn(Position, ChildOf(spawnedChild));
            world.deferred.destroy(parent);
            world.deferred.flush();

            expect(world.has(parent)).toBe(false);
            expect(world.has(child)).toBe(false);
            expect(world.has(spawnedChild)).toBe(false);
            expect(world.has(grandchild)).toBe(false);
            expect(removed.sort()).toEqual([parent, child].sort());
        });

        it('nullifies spawned entities when a spawned parent is destroyed', () => {
            const ChildOf = relation({ autoDestroy: 'orphan' });
            const added = vi.fn();
            world.onAdd(Position, added);
            const count = world.entities.length;

            const parent = world.deferred.spawn(Position);
            world.deferred.spawn(Position, ChildOf(parent));
            world.deferred.destroy(parent);
            world.deferred.flush();

            expect(world.entities.length).toBe(count);
            expect(added).not.toHaveBeenCalled();
        });

        it('cascades to targets with autoDestroy target', () => {
            const Contains = relation({ autoDestroy: 'target' });
            const container = world.spawn();
            const item = world.spawn();
            container.add(Contains(item));
            const spawnedItem = world.deferred.spawn(Position);
            world.deferred.add(container, Contains(spawnedItem));

            world.deferred.destroy(container);
            world.deferred.flush();

            expect(world.has(container)).toBe(false);
            expect(world.has(item)).toBe(false);
            expect(world.has(spawnedItem)).toBe(false);
            expect([...world.query(Position)]).toEqual([]);
        });

        it('cascades using the relations at the time of destruction', () => {
            const ChildOf = relation({ autoDestroy: 'orphan' });
            const Contains = relation({ autoDestroy: 'target' });
            const parent = world.spawn();
            const keptChild = world.spawn(ChildOf(parent));
            const newChild = world.spawn();
            const container = world.spawn();
            const keptItem = world.spawn();
            container.add(Contains(keptItem));

            world.deferred.remove(keptChild, ChildOf(parent));
            world.deferred.add(newChild, ChildOf(parent));
            world.deferred.destroy(parent);
            world.deferred.remove(container, Contains(keptItem));
            world.deferred.destroy(container);
            world.deferred.flush();

            expect(world.has(keptChild)).toBe(true);
            expect(world.has(newChild)).toBe(false);
            expect(world.has(keptItem)).toBe(true);
        });
    });

    describe('subscriptions', () => {
        it('fire once per relation pair based on the state difference', () => {
            const Likes = relation();
            const person = world.spawn();
            const apple = world.spawn();
            const banana = world.spawn();
            const cherry = world.spawn();
            person.add(Likes(apple), Likes(banana));

            const adds: [Entity, Entity | undefined][] = [];
            const removes: [Entity, Entity | undefined][] = [];
            world.onAdd(Likes, (e, t) => adds.push([e, t]));
            world.onRemove(Likes, (e, t) => removes.push([e, t]));

            world.deferred.add(person, Likes(cherry));
            world.deferred.remove(person, Likes(cherry));
            world.deferred.add(person, Likes(cherry));
            world.deferred.remove(person, Likes(apple));
            world.deferred.add(person, Likes(apple));
            world.deferred.flush();

            expect(adds).toEqual([[person, cherry]]);
            expect(removes).toEqual([]);

            world.deferred.addExclusive(person, Likes(banana));
            world.deferred.flush();

            expect(adds).toEqual([[person, cherry]]);
            expect(removes.sort()).toEqual(
                [
                    [person, apple],
                    [person, cherry],
                ].sort()
            );

            removes.length = 0;
            world.deferred.addExclusive(person, Likes('*'));
            world.deferred.flush();

            expect(removes).toEqual([[person, banana]]);
        });

        it('fire trait subscriptions based on the state difference', () => {
            const existing = world.spawn(Position({ x: 1 }));
            const entity = world.spawn();

            const added = vi.fn();
            const removed = vi.fn();
            const changed = vi.fn();
            world.onAdd(Position, added);
            world.onRemove(Position, removed);
            world.onChange(Position, changed);

            world.deferred.add(entity, Position);
            world.deferred.remove(entity, Position);
            world.deferred.remove(existing, Position);
            world.deferred.add(existing, Position({ x: 1 }));
            world.deferred.flush();

            expect(added).not.toHaveBeenCalled();
            expect(removed).not.toHaveBeenCalled();
            expect(changed).not.toHaveBeenCalled();

            world.deferred.remove(existing, Position);
            world.deferred.add(existing, Position({ x: 2 }));
            world.deferred.flush();

            expect(added).not.toHaveBeenCalled();
            expect(removed).not.toHaveBeenCalled();
            expect(changed).toHaveBeenCalledTimes(1);
            expect(existing.get(Position)).toEqual({ x: 2, y: 0 });
        });

        it('fire exclusive relation subscriptions once per pair', () => {
            const Targeting = relation({ exclusive: true });
            const hero = world.spawn();
            const rat = world.spawn();
            const goblin = world.spawn();
            hero.add(Targeting(rat));

            const adds: Entity[] = [];
            const removes: Entity[] = [];
            world.onAdd(Targeting, (_, t) => adds.push(t));
            world.onRemove(Targeting, (_, t) => removes.push(t));

            world.deferred.add(hero, Targeting(goblin));
            world.deferred.add(hero, Targeting(rat));
            world.deferred.add(hero, Targeting(goblin));
            world.deferred.flush();

            expect(adds).toEqual([goblin]);
            expect(removes).toEqual([rat]);
        });

        it('keep queries and tracking modifiers in sync with the net changes', () => {
            const Added = createAdded();
            const Removed = createRemoved();
            const a = world.spawn();
            const b = world.spawn(Position, Velocity);

            const queryAdded = vi.fn();
            const queryRemoved = vi.fn();
            world.onQueryAdd([Position, Velocity], queryAdded);
            world.onQueryRemove([Position, Velocity], queryRemoved);
            world.query(Added(Position));
            world.query(Removed(Position));

            world.deferred.add(a, Position);
            world.deferred.add(a, Velocity);
            world.deferred.remove(b, Position);
            world.deferred.add(b, Position);
            world.deferred.flush();

            expect(queryAdded).toHaveBeenCalledTimes(1);
            expect(queryAdded).toHaveBeenCalledWith(a);
            expect(queryRemoved).not.toHaveBeenCalled();
            expect([...world.query(Added(Position))]).toEqual([a]);
            expect([...world.query(Removed(Position))]).toEqual([]);
        });
    });

    it('keeps ordered relations in sync', () => {
        const ChildOf = relation();
        const OrderedChildren = ordered(ChildOf);
        const parent = world.spawn(OrderedChildren);
        const first = world.spawn();

        world.deferred.add(first, ChildOf(parent));
        const second = world.deferred.spawn(ChildOf(parent));
        world.deferred.flush();

        expect([...parent.get(OrderedChildren)!]).toEqual([first, second]);

        world.deferred.destroy(first);
        world.deferred.flush();

        expect([...parent.get(OrderedChildren)!]).toEqual([second]);
    });
});
