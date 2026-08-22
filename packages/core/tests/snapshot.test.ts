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

describe('Entity snapshot and rollback', () => {
    beforeEach(() => {
        universe.reset();
    });

    const Position = trait({ x: 0, y: 0 });
    const Velocity = trait({ x: 0, y: 0 });
    const IsPlayer = trait();
    const ChildOf = relation({ store: { weight: 0 } });
    const Likes = relation();

    const registry = createTraitRegistry(
        ['position', Position],
        ['velocity', Velocity],
        ['isPlayer', IsPlayer],
        ['childOf', ChildOf],
        ['likes', Likes]
    );

    it('createTraitRegistry throws on duplicate keys', () => {
        expect(() => createTraitRegistry(['position', Position], ['position', Velocity])).toThrow(
            Error
        );
    });

    it('createTraitRegistry throws on duplicate traits', () => {
        expect(() => createTraitRegistry(['position', Position], ['pos', Position])).toThrow(
            Error
        );
    });

    it('createTraitRegistry throws on duplicate relations', () => {
        expect(() => createTraitRegistry(['childOf', ChildOf], ['parent', ChildOf])).toThrow(Error);
    });

    it('snapshotEntity captures tags, data traits, and relation data', () => {
        const world = createWorld();
        const parent = world.spawn(Position);
        const player = world.spawn(
            [Position, { x: 1, y: 2 }],
            Velocity,
            IsPlayer,
            ChildOf(parent, { weight: 3 })
        );
        player.add(Likes(parent));

        const snapshot = snapshotEntity(world, player, registry);

        expect(snapshot).toEqual({
            id: player.id(),
            traits: {
                position: { x: 1, y: 2 },
                velocity: { x: 0, y: 0 },
                isPlayer: true,
            },
            relations: {
                childOf: [{ targetId: parent.id(), data: { weight: 3 } }],
                likes: [{ targetId: parent.id() }],
            },
        });
        expect(snapshot.traits.position).not.toBe(player.get(Position));
    });

    it('snapshotEntity omits relations when none exist', () => {
        const world = createWorld();
        const entity = world.spawn(Position);

        expect(snapshotEntity(world, entity, registry)).toEqual({
            id: entity.id(),
            traits: { position: { x: 0, y: 0 } },
        });
    });

    it('snapshotEntity throws for destroyed entities', () => {
        const world = createWorld();
        const entity = world.spawn(Position);
        entity.destroy();

        expect(() => snapshotEntity(world, entity, registry)).toThrow(Error);
    });

    it('snapshotEntity throws for unregistered traits', () => {
        const world = createWorld();
        const Hidden = trait({ secret: 0 });
        const entity = world.spawn(Position, Hidden);

        expect(() => snapshotEntity(world, entity, registry)).toThrow(Error);
    });

    it('snapshotWorld excludes the internal world entity', () => {
        const world = createWorld();
        const entity = world.spawn(Position);

        const snapshot = snapshotWorld(world, registry);

        expect(snapshot.entities).toHaveLength(1);
        expect(snapshot.entities[0]!.id).toBe(entity.id());
    });

    it('rollbackEntity restores entity state', () => {
        const world = createWorld();
        const parent = world.spawn(Position);
        const player = world.spawn(
            [Position, { x: 1, y: 2 }],
            Velocity,
            IsPlayer,
            ChildOf(parent, { weight: 3 })
        );
        player.add(Likes(parent));

        const checkpoint = snapshotEntity(world, player, registry);

        player.set(Position, { x: 9, y: 9 });
        player.remove(Velocity);
        player.remove(Likes(parent));
        player.add(IsPlayer);

        rollbackEntity(world, player, registry, checkpoint);

        expect(snapshotEntity(world, player, registry)).toEqual(checkpoint);
    });

    it('rollbackEntity throws when relation target is missing', () => {
        const world = createWorld();
        const entity = world.spawn(Position);
        const snapshot = {
            id: entity.id(),
            traits: { position: { x: 0, y: 0 } },
            relations: { likes: [{ targetId: 999 }] },
        };

        expect(() => rollbackEntity(world, entity, registry, snapshot)).toThrow(Error);
    });

    it('rollbackWorld fully replaces world state with same ids', () => {
        const world = createWorld();
        const parent = world.spawn([Position, { x: 1, y: 1 }]);
        const child = world.spawn([Position, { x: 2, y: 2 }], ChildOf(parent, { weight: 5 }));

        const checkpoint = snapshotWorld(world, registry);

        world.spawn([Position, { x: 99, y: 99 }]);
        child.destroy();

        rollbackWorld(world, registry, checkpoint);

        expect(snapshotWorld(world, registry)).toEqual(checkpoint);
        expect(world.entities).toHaveLength(3);
    });

    it('rollbackWorld throws for dangling relation targets', () => {
        const world = createWorld();

        expect(() =>
            rollbackWorld(world, registry, {
                entities: [
                    {
                        id: 1,
                        traits: { position: { x: 0, y: 0 } },
                        relations: { likes: [{ targetId: 2 }] },
                    },
                ],
            })
        ).toThrow(Error);
    });

    it('entity and world convenience methods work', () => {
        const world = createWorld();
        const entity = world.spawn([Position, { x: 4, y: 5 }]);
        const entitySnapshot = entity.snapshot(registry);
        const worldSnapshot = world.snapshot(registry);

        entity.set(Position, { x: 0, y: 0 });
        entity.rollback(registry, entitySnapshot);

        expect(entity.snapshot(registry)).toEqual(entitySnapshot);
        expect(world.snapshot(registry)).toEqual(worldSnapshot);
    });

    it('diffEntitySnapshots reports trait changes', () => {
        const before = {
            id: 1,
            traits: {
                position: { x: 0, y: 0 },
                velocity: { x: 1, y: 1 },
            },
        };
        const after = {
            id: 1,
            traits: {
                position: { x: 1, y: 0 },
                isPlayer: true,
            },
        };

        expect(diffEntitySnapshots(before, after)).toEqual({
            addedTraits: ['isPlayer'],
            removedTraits: ['velocity'],
            changedTraits: ['position'],
        });
    });

    it('diffEntitySnapshots throws for null snapshots', () => {
        expect(() => diffEntitySnapshots(null as any, { id: 1, traits: {} })).toThrow(Error);
    });

    it('diffWorldSnapshots ignores relation ordering and empty relations', () => {
        const before = {
            entities: [
                {
                    id: 1,
                    traits: { position: { x: 0, y: 0 } },
                    relations: { likes: [{ targetId: 2 }] },
                },
                { id: 2, traits: { position: { x: 1, y: 1 } } },
            ],
        };
        const after = {
            entities: [
                {
                    id: 1,
                    traits: { position: { x: 0, y: 0 } },
                    relations: { likes: [{ targetId: 2 }] },
                },
                { id: 2, traits: { position: { x: 1, y: 1 } }, relations: {} },
            ],
        };

        expect(diffWorldSnapshots(before, after)).toEqual({
            added: [],
            removed: [],
            changed: [],
        });
    });

    it('diffWorldSnapshots reports added, removed, and changed entities', () => {
        const before = {
            entities: [
                { id: 1, traits: { position: { x: 0, y: 0 } } },
                { id: 2, traits: { position: { x: 1, y: 1 } } },
            ],
        };
        const after = {
            entities: [
                { id: 1, traits: { position: { x: 5, y: 0 } } },
                { id: 3, traits: { position: { x: 2, y: 2 } } },
            ],
        };

        expect(diffWorldSnapshots(before, after)).toEqual({
            added: [3],
            removed: [2],
            changed: [1],
        });
    });

    it('diffWorldSnapshots throws for invalid snapshots', () => {
        expect(() => diffWorldSnapshots(null as any, { entities: [] })).toThrow(Error);
        expect(() => diffWorldSnapshots({ entities: [] }, undefined as any)).toThrow(Error);
    });
});
