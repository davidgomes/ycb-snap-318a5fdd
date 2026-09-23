import { beforeEach, describe, expect, it } from 'vitest';
import {
    createTraitRegistry,
    createWorld,
    diffEntitySnapshots,
    diffWorldSnapshots,
    relation,
    rollbackEntity,
    rollbackWorld,
    snapshotEntity,
    snapshotWorld,
    trait,
} from '../src';

const Position = trait({ x: 0, y: 0 });
const Inventory = trait(() => ({ items: [] as string[] }));
const IsPlayer = trait();
const Likes = relation();
const Damage = relation({ store: { amount: 0 } });
const Unregistered = trait();

const registry = createTraitRegistry(
    ['position', Position],
    ['inventory', Inventory],
    ['isPlayer', IsPlayer],
    ['likes', Likes],
    ['damage', Damage]
);

describe('Snapshot', () => {
    const world = createWorld();

    beforeEach(() => {
        world.reset();
    });

    describe('createTraitRegistry', () => {
        it('throws on duplicate keys, traits and relations', () => {
            expect(() => createTraitRegistry(['a', Position], ['a', IsPlayer])).toThrow(Error);
            expect(() => createTraitRegistry(['a', Position], ['b', Position])).toThrow(Error);
            expect(() => createTraitRegistry(['a', Likes], ['b', Likes])).toThrow(Error);
        });
    });

    describe('snapshotEntity', () => {
        it('snapshots tags, data and relations as deep copies', () => {
            const target = world.spawn();
            const entity = world.spawn(
                Position({ x: 1, y: 2 }),
                Inventory({ items: ['sword'] }),
                IsPlayer,
                Likes(target),
                Damage(target, { amount: 5 })
            );

            const snapshot = snapshotEntity(world, entity, registry);
            expect(snapshot).toEqual({
                id: entity.id(),
                traits: {
                    position: { x: 1, y: 2 },
                    inventory: { items: ['sword'] },
                    isPlayer: true,
                },
                relations: {
                    likes: [{ targetId: target.id() }],
                    damage: [{ targetId: target.id(), data: { amount: 5 } }],
                },
            });
            expect(snapshot.relations!.likes[0]).not.toHaveProperty('data');

            entity.get(Inventory)!.items.push('shield');
            expect((snapshot.traits.inventory as { items: string[] }).items).toEqual(['sword']);
        });

        it('omits relations when the entity has none', () => {
            const entity = world.spawn(IsPlayer);
            expect(entity.snapshot(registry)).not.toHaveProperty('relations');
        });

        it('throws for destroyed entities and unregistered traits', () => {
            const entity = world.spawn(Unregistered);
            expect(() => snapshotEntity(world, entity, registry)).toThrow(Error);
            entity.destroy();
            expect(() => snapshotEntity(world, entity, registry)).toThrow(Error);

            const Other = relation();
            const e = world.spawn(Other(world.spawn()));
            expect(() => snapshotEntity(world, e, registry)).toThrow(Error);
        });
    });

    describe('snapshotWorld', () => {
        it('excludes the world entity', () => {
            const a = world.spawn(IsPlayer);
            const b = world.spawn(Position);
            const snapshot = snapshotWorld(world, registry);
            expect(snapshot.entities.map((e) => e.id).sort()).toEqual([a.id(), b.id()].sort());
            expect(world.snapshot(registry)).toEqual(snapshot);
        });
    });

    describe('rollbackEntity', () => {
        it('restores traits and relations exactly', () => {
            const t1 = world.spawn();
            const t2 = world.spawn();
            const entity = world.spawn(
                Position({ x: 1, y: 1 }),
                IsPlayer,
                Likes(t1),
                Damage(t1, { amount: 3 })
            );
            const snapshot = snapshotEntity(world, entity, registry);

            entity.set(Position, { x: 9, y: 9 });
            entity.remove(IsPlayer, Damage(t1));
            entity.add(Inventory, Likes(t2), Damage(t2, { amount: 7 }));

            rollbackEntity(world, entity, registry, snapshot);

            expect(entity.get(Position)).toEqual({ x: 1, y: 1 });
            expect(entity.has(IsPlayer)).toBe(true);
            expect(entity.has(Inventory)).toBe(false);
            expect(entity.targetsFor(Likes)).toEqual([t1]);
            expect(entity.targetsFor(Damage)).toEqual([t1]);
            expect(entity.get(Damage(t1))).toEqual({ amount: 3 });
            expect(snapshotEntity(world, entity, registry)).toEqual(snapshot);
        });

        it('does not share data with the snapshot', () => {
            const entity = world.spawn(Inventory({ items: ['a'] }));
            const snapshot = entity.snapshot(registry);
            entity.rollback(registry, snapshot);
            entity.get(Inventory)!.items.push('b');
            expect((snapshot.traits.inventory as { items: string[] }).items).toEqual(['a']);
        });

        it('throws for destroyed entities, unknown keys and missing targets', () => {
            const entity = world.spawn();
            expect(() =>
                rollbackEntity(world, entity, registry, {
                    id: 0,
                    traits: { nope: true },
                })
            ).toThrow(Error);
            expect(() =>
                rollbackEntity(world, entity, registry, {
                    id: 0,
                    traits: {},
                    relations: { likes: [{ targetId: 9999 }] },
                })
            ).toThrow(Error);
            entity.destroy();
            expect(() => rollbackEntity(world, entity, registry, { id: 0, traits: {} })).toThrow(
                Error
            );
        });
    });

    describe('rollbackWorld', () => {
        it('replaces world state and recreates entities with the same IDs', () => {
            const a = world.spawn(Position({ x: 1, y: 2 }));
            const b = world.spawn(IsPlayer, Damage(a, { amount: 4 }));
            const checkpoint = snapshotWorld(world, registry);

            b.destroy();
            a.set(Position, { x: 5, y: 5 });
            const c = world.spawn(Inventory);

            rollbackWorld(world, registry, checkpoint);

            expect(c.isAlive()).toBe(false);
            expect(world.entities.length).toBe(3); // world entity + a + b
            expect(snapshotWorld(world, registry)).toEqual(checkpoint);
            expect(world.query(Position).length).toBe(1);
            expect(world.query(IsPlayer).length).toBe(1);
        });

        it('keeps queries in sync and old handles stale', () => {
            const a = world.spawn(Position);
            expect([...world.query(Position)]).toEqual([a]);
            const checkpoint = world.snapshot(registry);

            world.rollback(registry, checkpoint);

            const [restored] = world.query(Position);
            expect(restored.id()).toBe(a.id());
            expect(a.isAlive()).toBe(false);
            expect(restored.isAlive()).toBe(true);
            expect(world.spawn().id()).not.toBe(a.id());
        });

        it('recreates sparse IDs never used in the world', () => {
            rollbackWorld(world, registry, {
                entities: [
                    {
                        id: 5,
                        traits: { isPlayer: true },
                        relations: { likes: [{ targetId: 7 }] },
                    },
                    { id: 7, traits: {} },
                ],
            });
            const ids = world.query(IsPlayer).map((e) => e.id());
            expect(ids).toEqual([5]);
            expect(world.query(IsPlayer)[0].targetFor(Likes)!.id()).toBe(7);
            const spawned = world.spawn();
            expect([5, 7]).not.toContain(spawned.id());
        });

        it('throws on unknown keys or dangling targets without mutating', () => {
            const a = world.spawn(IsPlayer);
            expect(() =>
                rollbackWorld(world, registry, {
                    entities: [{ id: 3, traits: { nope: true } }],
                })
            ).toThrow(Error);
            expect(() =>
                rollbackWorld(world, registry, {
                    entities: [{ id: 3, traits: {}, relations: { likes: [{ targetId: 4 }] } }],
                })
            ).toThrow(Error);
            expect(a.isAlive()).toBe(true);
        });
    });

    describe('diffEntitySnapshots', () => {
        it('reports sorted added, removed and changed traits', () => {
            const diff = diffEntitySnapshots(
                {
                    id: 1,
                    traits: { b: true, a: { x: 1 }, c: { x: 1 }, keep: { x: 1 } },
                },
                {
                    id: 1,
                    traits: { z: true, y: true, a: { x: 2 }, c: true, keep: { x: 1 } },
                }
            );
            expect(diff).toEqual({
                addedTraits: ['y', 'z'],
                removedTraits: ['b'],
                changedTraits: ['a', 'c'],
            });
        });

        it('throws on missing arguments', () => {
            expect(() => diffEntitySnapshots(null as any, { id: 1, traits: {} })).toThrow(Error);
            expect(() => diffEntitySnapshots({ id: 1, traits: {} }, undefined as any)).toThrow(Error);
        });
    });

    describe('diffWorldSnapshots', () => {
        it('reports sorted added, removed and changed entities', () => {
            const diff = diffWorldSnapshots(
                {
                    entities: [
                        { id: 3, traits: { a: { x: 1 } } },
                        { id: 1, traits: {} },
                        { id: 2, traits: { a: true } },
                    ],
                },
                {
                    entities: [
                        { id: 3, traits: { a: { x: 2 } } },
                        { id: 5, traits: {} },
                        { id: 4, traits: {} },
                        { id: 2, traits: { a: true } },
                    ],
                }
            );
            expect(diff).toEqual({ added: [4, 5], removed: [1], changed: [3] });
        });

        it('ignores key and target ordering and empty relations', () => {
            const diff = diffWorldSnapshots(
                {
                    entities: [
                        {
                            id: 1,
                            traits: { a: true, b: { x: 1 } },
                            relations: {
                                r: [{ targetId: 2 }, { targetId: 3, data: { v: 1 } }],
                                s: [{ targetId: 2 }],
                            },
                        },
                        { id: 2, traits: {}, relations: {} },
                    ],
                },
                {
                    entities: [
                        { id: 2, traits: {} },
                        {
                            id: 1,
                            traits: { b: { x: 1 }, a: true },
                            relations: {
                                s: [{ targetId: 2 }],
                                r: [{ targetId: 3, data: { v: 1 } }, { targetId: 2 }],
                            },
                        },
                    ],
                }
            );
            expect(diff).toEqual({ added: [], removed: [], changed: [] });
        });

        it('detects relation changes', () => {
            const diff = diffWorldSnapshots(
                {
                    entities: [
                        {
                            id: 1,
                            traits: {},
                            relations: { r: [{ targetId: 2, data: { v: 1 } }] },
                        },
                    ],
                },
                {
                    entities: [
                        {
                            id: 1,
                            traits: {},
                            relations: { r: [{ targetId: 2, data: { v: 2 } }] },
                        },
                    ],
                }
            );
            expect(diff.changed).toEqual([1]);
        });

        it('throws on invalid arguments', () => {
            expect(() => diffWorldSnapshots(null as any, { entities: [] })).toThrow(Error);
            expect(() => diffWorldSnapshots({ entities: [] }, {} as any)).toThrow(Error);
        });
    });
});
