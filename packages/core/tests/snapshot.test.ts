import { beforeEach, describe, expect, it } from 'vitest';
import {
    createTraitRegistry,
    createWorld,
    diffEntitySnapshots,
    diffWorldSnapshots,
    type EntitySnapshot,
    relation,
    rollbackEntity,
    rollbackWorld,
    snapshotEntity,
    snapshotWorld,
    trait,
    universe,
    type WorldSnapshot,
} from '../src';

describe('Snapshots', () => {
    beforeEach(() => {
        universe.reset();
    });

    describe('createTraitRegistry', () => {
        it('throws on duplicate keys, traits, or relations', () => {
            const A = trait();
            const B = trait({ x: 0 });
            const R = relation();

            expect(() => createTraitRegistry(['a', A], ['a', B])).toThrow(Error);
            expect(() => createTraitRegistry(['a', A], ['b', A])).toThrow(Error);
            expect(() => createTraitRegistry(['r1', R], ['r2', R])).toThrow(Error);
            expect(() => createTraitRegistry(['a', A], ['b', B], ['r', R])).not.toThrow();
        });
    });

    describe('snapshotEntity', () => {
        it('stores tags as true and data traits as deep copies', () => {
            const Tag = trait();
            const Position = trait({ x: 0, y: 0 });
            const Inventory = trait({ items: () => [] as string[] });
            const registry = createTraitRegistry(
                ['tag', Tag],
                ['position', Position],
                ['inventory', Inventory]
            );
            const world = createWorld();
            const entity = world.spawn(Tag, Position({ x: 1, y: 2 }), Inventory({ items: ['a'] }));

            const snapshot = snapshotEntity(world, entity, registry);

            expect(snapshot).toEqual({
                id: entity.id(),
                traits: { tag: true, position: { x: 1, y: 2 }, inventory: { items: ['a'] } },
            });
            expect('relations' in snapshot).toBe(false);

            entity.get(Inventory)!.items.push('b');
            expect((snapshot.traits.inventory as { items: string[] }).items).toEqual(['a']);
        });

        it('stores relations with deep copied data', () => {
            const Likes = relation();
            const Owes = relation({ store: { amount: 0 } });
            const registry = createTraitRegistry(['likes', Likes], ['owes', Owes]);
            const world = createWorld();
            const a = world.spawn();
            const b = world.spawn();
            const entity = world.spawn(Likes(a), Likes(b), Owes(a, { amount: 5 }));

            expect(snapshotEntity(world, entity, registry)).toEqual({
                id: entity.id(),
                traits: {},
                relations: {
                    likes: [{ targetId: a.id() }, { targetId: b.id() }],
                    owes: [{ targetId: a.id(), data: { amount: 5 } }],
                },
            });
        });

        it('throws for destroyed entities and unregistered traits', () => {
            const A = trait();
            const B = trait();
            const R = relation();
            const registry = createTraitRegistry(['a', A]);
            const world = createWorld();

            const withB = world.spawn(A, B);
            expect(() => snapshotEntity(world, withB, registry)).toThrow(Error);

            const target = world.spawn();
            const withR = world.spawn(A, R(target));
            expect(() => snapshotEntity(world, withR, registry)).toThrow(Error);

            const destroyed = world.spawn(A);
            destroyed.destroy();
            expect(() => snapshotEntity(world, destroyed, registry)).toThrow(Error);
        });

        it('is available as an entity method', () => {
            const A = trait({ v: 1 });
            const registry = createTraitRegistry(['a', A]);
            const world = createWorld();
            const entity = world.spawn(A);
            expect(entity.snapshot(registry)).toEqual(snapshotEntity(world, entity, registry));
        });
    });

    describe('snapshotWorld', () => {
        it('excludes the world entity', () => {
            const A = trait();
            const registry = createTraitRegistry(['a', A]);
            const world = createWorld();
            world.add(A);
            const e1 = world.spawn(A);
            const e2 = world.spawn();

            const snapshot = snapshotWorld(world, registry);
            expect(snapshot.entities.map((e) => e.id)).toEqual([e1.id(), e2.id()]);
            expect(world.snapshot(registry)).toEqual(snapshot);
        });
    });

    describe('rollbackEntity', () => {
        it('restores traits and relations exactly', () => {
            const Tag = trait();
            const Other = trait();
            const Position = trait({ x: 0, y: 0 });
            const Links = relation({ store: { weight: 0 } });
            const registry = createTraitRegistry(
                ['tag', Tag],
                ['other', Other],
                ['position', Position],
                ['links', Links]
            );
            const world = createWorld();
            const a = world.spawn();
            const b = world.spawn();
            const entity = world.spawn(Tag, Position({ x: 1, y: 2 }), Links(a, { weight: 3 }));

            const snapshot = snapshotEntity(world, entity, registry);

            entity.remove(Tag);
            entity.add(Other);
            entity.set(Position, { x: 9, y: 9 });
            entity.set(Links(a), { weight: 100 });
            entity.add(Links(b, { weight: 7 }));

            rollbackEntity(world, entity, registry, snapshot);

            expect(entity.has(Tag)).toBe(true);
            expect(entity.has(Other)).toBe(false);
            expect(entity.get(Position)).toEqual({ x: 1, y: 2 });
            expect(entity.targetsFor(Links)).toEqual([a]);
            expect(entity.get(Links(a))).toEqual({ weight: 3 });
            expect(snapshotEntity(world, entity, registry)).toEqual(snapshot);
        });

        it('restores relation target order', () => {
            const Likes = relation();
            const registry = createTraitRegistry(['likes', Likes]);
            const world = createWorld();
            const a = world.spawn();
            const b = world.spawn();
            const c = world.spawn();
            const entity = world.spawn(Likes(a), Likes(b), Likes(c));
            const snapshot = entity.snapshot(registry);

            entity.remove(Likes(a));
            entity.rollback(registry, snapshot);

            expect(entity.targetsFor(Likes)).toEqual([a, b, c]);
        });

        it('removes relations when the snapshot has none', () => {
            const Likes = relation();
            const registry = createTraitRegistry(['likes', Likes]);
            const world = createWorld();
            const target = world.spawn();
            const entity = world.spawn();
            const snapshot = entity.snapshot(registry);

            entity.add(Likes(target));
            entity.rollback(registry, snapshot);

            expect(entity.has(Likes('*'))).toBe(false);
        });

        it('does not share data with the snapshot', () => {
            const Inventory = trait({ items: () => [] as string[] });
            const registry = createTraitRegistry(['inventory', Inventory]);
            const world = createWorld();
            const entity = world.spawn(Inventory({ items: ['a'] }));
            const snapshot = entity.snapshot(registry);

            entity.rollback(registry, snapshot);
            entity.get(Inventory)!.items.push('b');

            expect(snapshot.traits.inventory).toEqual({ items: ['a'] });
        });

        it('throws for missing targets, destroyed entities, and unknown keys', () => {
            const A = trait();
            const Likes = relation();
            const registry = createTraitRegistry(['a', A], ['likes', Likes]);
            const world = createWorld();
            const target = world.spawn();
            const entity = world.spawn(A, Likes(target));
            const snapshot = entity.snapshot(registry);

            target.destroy();
            expect(() => rollbackEntity(world, entity, registry, snapshot)).toThrow(Error);
            // Validation happens before mutation.
            expect(entity.has(A)).toBe(true);

            expect(() =>
                rollbackEntity(world, entity, registry, { id: entity.id(), traits: { nope: true } })
            ).toThrow(Error);
            expect(() =>
                rollbackEntity(world, entity, registry, {
                    id: entity.id(),
                    traits: {},
                    relations: { nope: [] },
                })
            ).toThrow(Error);

            entity.destroy();
            expect(() =>
                rollbackEntity(world, entity, registry, { id: entity.id(), traits: {} })
            ).toThrow(Error);
        });
    });

    describe('rollbackWorld', () => {
        it('replaces world state and restores entity IDs', () => {
            const Name = trait({ value: '' });
            const ChildOf = relation({ exclusive: true });
            const registry = createTraitRegistry(['name', Name], ['childOf', ChildOf]);
            const world = createWorld();
            const parent = world.spawn(Name({ value: 'parent' }));
            const child = world.spawn(Name({ value: 'child' }), ChildOf(parent));
            const checkpoint = snapshotWorld(world, registry);

            parent.destroy();
            world.spawn(Name({ value: 'extra' }));
            world.spawn(Name({ value: 'extra2' }));

            rollbackWorld(world, registry, checkpoint);

            expect(snapshotWorld(world, registry)).toEqual(checkpoint);
            expect(world.entities.map((e) => e.id()).sort()).toEqual([0, parent.id(), child.id()]);

            const restoredChild = world.entities.find((e) => e.get(Name)?.value === 'child')!;
            expect(restoredChild.targetFor(ChildOf)!.id()).toBe(parent.id());
        });

        it('recreates entities with arbitrary IDs', () => {
            const A = trait({ v: 0 });
            const Likes = relation();
            const registry = createTraitRegistry(['a', A], ['likes', Likes]);
            const world = createWorld();
            const checkpoint: WorldSnapshot = {
                entities: [
                    { id: 10, traits: { a: { v: 1 } }, relations: { likes: [{ targetId: 5 }] } },
                    { id: 5, traits: {} },
                ],
            };

            world.rollback(registry, checkpoint);

            const snapshot = world.snapshot(registry);
            expect(diffWorldSnapshots(snapshot, checkpoint)).toEqual({
                added: [],
                removed: [],
                changed: [],
            });

            // New spawns still work after restoring sparse IDs.
            const spawned = world.spawn(A);
            expect(world.has(spawned)).toBe(true);
            expect(world.entities.length).toBe(4);
        });

        it('throws for unknown keys and dangling targets without mutating', () => {
            const A = trait();
            const Likes = relation();
            const registry = createTraitRegistry(['a', A], ['likes', Likes]);
            const world = createWorld();
            const existing = world.spawn(A);

            expect(() =>
                rollbackWorld(world, registry, { entities: [{ id: 1, traits: { nope: true } }] })
            ).toThrow(Error);
            expect(() =>
                rollbackWorld(world, registry, {
                    entities: [{ id: 1, traits: {}, relations: { likes: [{ targetId: 99 }] } }],
                })
            ).toThrow(Error);
            expect(world.has(existing)).toBe(true);
        });
    });

    describe('diffEntitySnapshots', () => {
        it('reports added, removed, and changed traits sorted', () => {
            const a: EntitySnapshot = {
                id: 1,
                traits: { z: true, b: { x: 1 }, c: { x: 1 }, same: { x: 1 } },
            };
            const b: EntitySnapshot = {
                id: 1,
                traits: { b: { x: 2 }, c: { x: 1, y: 2 }, same: { x: 1 }, y: true, a: { x: 0 } },
            };

            expect(diffEntitySnapshots(a, b)).toEqual({
                addedTraits: ['a', 'y'],
                removedTraits: ['z'],
                changedTraits: ['b', 'c'],
            });
        });

        it('throws for null or undefined', () => {
            const snapshot: EntitySnapshot = { id: 0, traits: {} };
            expect(() => diffEntitySnapshots(null as any, snapshot)).toThrow(Error);
            expect(() => diffEntitySnapshots(snapshot, undefined as any)).toThrow(Error);
        });
    });

    describe('diffWorldSnapshots', () => {
        it('reports added, removed, and changed entities sorted', () => {
            const before: WorldSnapshot = {
                entities: [
                    { id: 3, traits: { a: true } },
                    { id: 1, traits: { a: { x: 1 } } },
                    { id: 2, traits: {} },
                ],
            };
            const after: WorldSnapshot = {
                entities: [
                    { id: 5, traits: {} },
                    { id: 1, traits: { a: { x: 2 } } },
                    { id: 4, traits: {} },
                    { id: 2, traits: {} },
                ],
            };

            expect(diffWorldSnapshots(before, after)).toEqual({
                added: [4, 5],
                removed: [3],
                changed: [1],
            });
        });

        it('ignores key and target ordering and empty relations', () => {
            const before: WorldSnapshot = {
                entities: [
                    {
                        id: 1,
                        traits: { a: true, b: { x: 1 } },
                        relations: {
                            r: [{ targetId: 2 }, { targetId: 3, data: { w: 1 } }],
                            s: [{ targetId: 2 }],
                        },
                    },
                    { id: 2, traits: {}, relations: {} },
                ],
            };
            const after: WorldSnapshot = {
                entities: [
                    { id: 2, traits: {} },
                    {
                        id: 1,
                        traits: { b: { x: 1 }, a: true },
                        relations: {
                            s: [{ targetId: 2 }],
                            r: [{ targetId: 3, data: { w: 1 } }, { targetId: 2 }],
                        },
                    },
                ],
            };

            expect(diffWorldSnapshots(before, after)).toEqual({
                added: [],
                removed: [],
                changed: [],
            });

            const changedData = structuredClone(after);
            changedData.entities[1].relations!.r[0].data = { w: 2 };
            expect(diffWorldSnapshots(before, changedData).changed).toEqual([1]);

            const changedTarget = structuredClone(after);
            changedTarget.entities[1].relations!.s[0].targetId = 3;
            expect(diffWorldSnapshots(before, changedTarget).changed).toEqual([1]);
        });

        it('throws for invalid input', () => {
            const snapshot: WorldSnapshot = { entities: [] };
            expect(() => diffWorldSnapshots(null as any, snapshot)).toThrow(Error);
            expect(() => diffWorldSnapshots(snapshot, undefined as any)).toThrow(Error);
            expect(() => diffWorldSnapshots({} as any, snapshot)).toThrow(Error);
            expect(() => diffWorldSnapshots(snapshot, { entities: 'x' } as any)).toThrow(Error);
        });
    });
});
