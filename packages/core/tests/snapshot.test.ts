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
    universe,
} from '../src';

const Position = trait({ x: 0, y: 0 });
const Velocity = trait({ x: 0, y: 0 });
const Health = trait(() => ({ hp: 1, gear: { name: 'sword' } }));
const IsPlayer = trait();
const IsEnemy = trait();
const ChildOf = relation({ exclusive: true });
const Contains = relation({ store: { amount: 0 } });
const Likes = relation();

function registry() {
    return createTraitRegistry(
        ['Position', Position],
        ['Velocity', Velocity],
        ['Health', Health],
        ['IsPlayer', IsPlayer],
        ['IsEnemy', IsEnemy],
        ['ChildOf', ChildOf],
        ['Contains', Contains],
        ['Likes', Likes]
    );
}

describe('Snapshot', () => {
    beforeEach(() => {
        universe.reset();
    });

    describe('createTraitRegistry', () => {
        it('throws on duplicate keys, traits, and relations', () => {
            const A = trait();
            const B = trait({ n: 0 });
            const Rel = relation();

            expect(() => createTraitRegistry(['A', A], ['A', B])).toThrow(Error);
            expect(() => createTraitRegistry(['A', A], ['B', A])).toThrow(Error);
            expect(() => createTraitRegistry(['R', Rel], ['S', Rel])).toThrow(Error);
        });
    });

    describe('snapshotEntity', () => {
        it('stores tags as true and data traits as deep copies', () => {
            const world = createWorld();
            const player = world.spawn(IsPlayer, Position({ x: 3, y: 4 }), Health);
            player.get(Health)!.gear.name = 'bow';

            const snapshot = snapshotEntity(world, player, registry());

            expect(snapshot.id).toBe(player.id());
            expect(snapshot.traits.IsPlayer).toBe(true);
            expect(snapshot.traits.Position).toEqual({ x: 3, y: 4 });
            expect(snapshot.traits.Health).toEqual({ hp: 1, gear: { name: 'bow' } });
            expect(snapshot.traits.Health).not.toBe(player.get(Health));
            expect((snapshot.traits.Health as { gear: { name: string } }).gear).not.toBe(
                player.get(Health)!.gear
            );
            expect(snapshot).not.toHaveProperty('relations');

            (snapshot.traits.Position as { x: number }).x = 99;
            (snapshot.traits.Health as { gear: { name: string } }).gear.name = 'staff';
            expect(player.get(Position)!.x).toBe(3);
            expect(player.get(Health)!.gear.name).toBe('bow');
        });

        it('copies relation targets and store data', () => {
            const world = createWorld();
            const parent = world.spawn(IsPlayer);
            const gold = world.spawn();
            const silver = world.spawn();
            const child = world.spawn(
                ChildOf(parent),
                Contains(gold, { amount: 5 }),
                Contains(silver, { amount: 2 }),
                Likes(parent)
            );

            const snapshot = snapshotEntity(world, child, registry());

            expect(snapshot.relations?.ChildOf).toEqual([{ targetId: parent.id() }]);
            expect(snapshot.relations?.ChildOf?.[0]).not.toHaveProperty('data');
            expect(snapshot.relations?.Likes).toEqual([{ targetId: parent.id() }]);
            expect(snapshot.relations?.Contains).toEqual([
                { targetId: gold.id(), data: { amount: 5 } },
                { targetId: silver.id(), data: { amount: 2 } },
            ]);

            (snapshot.relations!.Contains[0]!.data as { amount: number }).amount = 50;
            expect(child.get(Contains(gold))!.amount).toBe(5);
        });

        it('throws for destroyed entities and unregistered traits or relations', () => {
            const world = createWorld();
            const Secret = trait({ n: 1 });
            const SecretRel = relation();
            const player = world.spawn(IsPlayer, Secret);
            const other = world.spawn(SecretRel(player));

            expect(() => snapshotEntity(world, player, registry())).toThrow(Error);
            player.remove(Secret);
            expect(() => snapshotEntity(world, other, registry())).toThrow(Error);

            player.destroy();
            expect(() => snapshotEntity(world, player, registry())).toThrow(Error);
        });
    });

    describe('snapshotWorld', () => {
        it('excludes the internal world entity and sorts by id', () => {
            const world = createWorld();
            world.add(IsPlayer);
            const a = world.spawn(IsEnemy);
            const b = world.spawn(Position({ x: 1, y: 2 }));

            const snapshot = world.snapshot(registry());

            expect(snapshot.entities.map((entity) => entity.id)).toEqual([a.id(), b.id()]);
            expect(snapshot.entities.some((entity) => entity.traits.IsPlayer)).toBe(false);
            expect(snapshotWorld(world, registry())).toEqual(snapshot);
        });
    });

    describe('rollbackEntity', () => {
        it('removes missing state and restores traits and relations', () => {
            const world = createWorld();
            const reg = registry();
            const parent = world.spawn(IsPlayer);
            const other = world.spawn(IsEnemy);
            const child = world.spawn(
                IsPlayer,
                Position({ x: 1, y: 2 }),
                ChildOf(parent),
                Contains(parent, { amount: 4 })
            );

            const snapshot = child.snapshot(reg);

            child.remove(IsPlayer);
            child.add(IsEnemy, Velocity({ x: 8, y: 9 }));
            child.set(Position, { x: 50, y: 60 });
            child.remove(ChildOf(parent));
            child.add(ChildOf(other));
            child.set(Contains(parent), { amount: 1 });
            child.add(Likes(other));

            rollbackEntity(world, child, reg, snapshot);

            expect(child.has(IsPlayer)).toBe(true);
            expect(child.has(IsEnemy)).toBe(false);
            expect(child.has(Velocity)).toBe(false);
            expect(child.get(Position)).toEqual({ x: 1, y: 2 });
            expect(child.targetFor(ChildOf)).toBe(parent);
            expect(child.has(Likes(other))).toBe(false);
            expect(child.get(Contains(parent))!.amount).toBe(4);
            expect(world.has(parent)).toBe(true);
            expect(world.has(other)).toBe(true);
        });

        it('throws for destroyed entities, unknown keys, and missing targets without mutating', () => {
            const world = createWorld();
            const reg = registry();
            const player = world.spawn(IsPlayer, Position({ x: 1, y: 1 }));
            const snapshot = player.snapshot(reg);

            player.destroy();
            expect(() => player.rollback(reg, snapshot)).toThrow(Error);

            const alive = world.spawn(IsPlayer, Position({ x: 2, y: 3 }));
            expect(() =>
                rollbackEntity(world, alive, reg, {
                    id: alive.id(),
                    traits: { Missing: true },
                })
            ).toThrow(Error);
            expect(alive.has(IsPlayer)).toBe(true);
            expect(alive.get(Position)).toEqual({ x: 2, y: 3 });

            expect(() =>
                alive.rollback(reg, {
                    id: alive.id(),
                    traits: {},
                    relations: { ChildOf: [{ targetId: 999 }] },
                })
            ).toThrow(Error);
            expect(alive.has(IsPlayer)).toBe(true);
            expect(alive.has(ChildOf('*'))).toBe(false);
        });

        it('can target the world entity', () => {
            const world = createWorld();
            const reg = registry();
            const worldEntity = world.entities[0]!;
            const player = world.spawn(ChildOf(worldEntity));

            const snapshot = snapshotEntity(world, player, reg);
            expect(snapshot.relations?.ChildOf).toEqual([{ targetId: worldEntity.id() }]);

            player.remove(ChildOf(worldEntity));
            player.rollback(reg, snapshot);
            expect(player.targetFor(ChildOf)).toBe(worldEntity);

            const checkpoint = snapshotWorld(world, reg);
            world.reset();
            rollbackWorld(world, reg, checkpoint);
            const restored = world.entities.find((entity) => entity.id() === player.id());
            expect(restored?.targetFor(ChildOf)?.id()).toBe(0);
        });
    });

    describe('rollbackWorld', () => {
        it('recreates the same ids and restores references', () => {
            const world = createWorld();
            const reg = registry();
            const parent = world.spawn(IsPlayer, Position({ x: 4, y: 5 }));
            const dropped = world.spawn(IsEnemy);
            const child = world.spawn(Health, ChildOf(parent), Contains(parent, { amount: 7 }));
            dropped.destroy();

            child.get(Health)!.hp = 9;
            child.get(Health)!.gear.name = 'bow';

            const checkpoint = snapshotWorld(world, reg);
            parent.set(Position, { x: 0, y: 0 });
            child.destroy();
            world.spawn(Velocity);

            world.rollback(reg, checkpoint);

            expect(world.has(parent)).toBe(true);
            expect(world.has(child)).toBe(true);
            expect(world.has(dropped)).toBe(false);
            expect(parent.get(Position)).toEqual({ x: 4, y: 5 });
            expect(parent.has(IsPlayer)).toBe(true);
            expect(child.get(Health)).toEqual({ hp: 9, gear: { name: 'bow' } });
            expect(child.targetFor(ChildOf)).toBe(parent);
            expect(child.get(Contains(parent))!.amount).toBe(7);
            expect(world.query(Velocity).length).toBe(0);
            expect(world.query(Position).length).toBe(1);

            child.get(Health)!.gear.name = 'axe';
            expect(
                (
                    checkpoint.entities.find((entity) => entity.id === child.id())!.traits.Health as {
                        gear: { name: string };
                    }
                ).gear.name
            ).toBe('bow');
        });

        it('replaces the world with an empty checkpoint', () => {
            const world = createWorld();
            world.spawn(IsPlayer);
            rollbackWorld(world, registry(), { entities: [] });
            expect(world.entities.length).toBe(1);
        });

        it('throws for unknown keys and dangling targets without replacing state', () => {
            const world = createWorld();
            const reg = registry();
            const player = world.spawn(IsPlayer);
            const before = world.entities.length;

            expect(() =>
                rollbackWorld(world, reg, {
                    entities: [{ id: player.id(), traits: { Nope: true } }],
                })
            ).toThrow(Error);

            expect(() =>
                world.rollback(reg, {
                    entities: [
                        {
                            id: player.id(),
                            traits: {},
                            relations: { Likes: [{ targetId: player.id() + 50 }] },
                        },
                    ],
                })
            ).toThrow(Error);

            expect(world.has(player)).toBe(true);
            expect(player.has(IsPlayer)).toBe(true);
            expect(world.entities.length).toBe(before);
        });
    });

    describe('diffEntitySnapshots', () => {
        it('returns sorted trait changes using shallow equality', () => {
            const nested = { z: 1 };
            const diff = diffEntitySnapshots(
                {
                    id: 1,
                    traits: {
                        b: { x: 1 },
                        a: true,
                        c: { x: 1, nested },
                    },
                },
                {
                    id: 1,
                    traits: {
                        c: { x: 1, nested: { z: 1 } },
                        d: true,
                        b: { x: 1 },
                    },
                }
            );

            expect(diff).toEqual({
                addedTraits: ['d'],
                removedTraits: ['a'],
                changedTraits: ['c'],
            });
        });

        it('throws when either snapshot is null or undefined', () => {
            const snapshot = { id: 1, traits: {} };
            expect(() => diffEntitySnapshots(null, snapshot)).toThrow(Error);
            expect(() => diffEntitySnapshots(snapshot, undefined)).toThrow(Error);
        });
    });

    describe('diffWorldSnapshots', () => {
        it('diffs entities independent of key and target order', () => {
            const diff = diffWorldSnapshots(
                {
                    entities: [
                        {
                            id: 2,
                            traits: { b: { n: 1 }, a: true },
                            relations: {
                                Likes: [{ targetId: 3, data: { amount: 1 } }, { targetId: 1 }],
                            },
                        },
                        { id: 10, traits: { a: { n: 1 } } },
                        { id: 4, traits: {} },
                    ],
                },
                {
                    entities: [
                        { id: 4, traits: {}, relations: {} },
                        {
                            id: 2,
                            traits: { a: true, b: { n: 2 } },
                            relations: {
                                Likes: [{ targetId: 1 }, { targetId: 3, data: { amount: 1 } }],
                            },
                        },
                        { id: 7, traits: { a: true } },
                    ],
                }
            );

            expect(diff).toEqual({ added: [7], removed: [10], changed: [2] });
        });

        it('marks an entity changed when only relation data changes', () => {
            const diff = diffWorldSnapshots(
                {
                    entities: [
                        {
                            id: 1,
                            traits: {},
                            relations: { Contains: [{ targetId: 2, data: { amount: 1 } }] },
                        },
                    ],
                },
                {
                    entities: [
                        {
                            id: 1,
                            traits: {},
                            relations: { Contains: [{ targetId: 2, data: { amount: 2 } }] },
                        },
                    ],
                }
            );
            expect(diff.changed).toEqual([1]);
        });

        it('treats an empty relations object as no relations', () => {
            const diff = diffWorldSnapshots(
                { entities: [{ id: 1, traits: { a: true } }] },
                { entities: [{ id: 1, traits: { a: true }, relations: {} }] }
            );
            expect(diff).toEqual({ added: [], removed: [], changed: [] });
        });

        it('throws when a snapshot is missing or has no entities array', () => {
            expect(() => diffWorldSnapshots(null, { entities: [] })).toThrow(Error);
            expect(() => diffWorldSnapshots({ entities: [] }, undefined)).toThrow(Error);
            expect(() => diffWorldSnapshots({ entities: [] }, {} as { entities: never })).toThrow(
                Error
            );
        });
    });
});
