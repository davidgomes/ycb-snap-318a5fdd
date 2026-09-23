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
const Health = trait({ value: 0 });
const Inventory = trait(() => ({ items: [] as string[] }));
const Static = trait();
const Likes = relation();
const Owns = relation({ store: { amount: 0 } });
const ChildOf = relation({ exclusive: true, store: { order: 0 } });

describe('snapshot', () => {
    const world = createWorld();

    beforeEach(() => {
        world.reset();
    });

    it('throws on duplicate registry entries', () => {
        expect(() => createTraitRegistry(['a', Position], ['a', Health])).toThrow(Error);
        expect(() => createTraitRegistry(['a', Position], ['b', Position])).toThrow(Error);
        expect(() => createTraitRegistry(['a', Likes], ['b', Likes])).toThrow(Error);
    });

    it('snapshots tags, data, and relations', () => {
        const registry = createTraitRegistry(
            ['position', Position],
            ['static', Static],
            ['likes', Likes],
            ['owns', Owns]
        );
        const parent = world.spawn(Static);
        const child = world.spawn(Position({ x: 1, y: 2 }));
        child.add(Likes(parent));
        child.add(Owns(parent, { amount: 4 }));

        const snapshot = snapshotEntity(world, child, registry);
        expect(snapshot.id).toBe(child.id());
        expect(snapshot.traits.static).toBeUndefined();
        expect(snapshot.traits.position).toEqual({ x: 1, y: 2 });
        expect(snapshot.relations?.likes).toEqual([{ targetId: parent.id() }]);
        expect(snapshot.relations?.owns).toEqual([{ targetId: parent.id(), data: { amount: 4 } }]);

        (snapshot.traits.position as { x: number }).x = 99;
        expect(child.get(Position)?.x).toBe(1);
    });

    it('omits relations when the entity has none', () => {
        const registry = createTraitRegistry(['static', Static]);
        const entity = world.spawn(Static);
        const snapshot = snapshotEntity(world, entity, registry);
        expect(snapshot.traits.static).toBe(true);
        expect(snapshot).not.toHaveProperty('relations');
    });

    it('throws for destroyed entities and unregistered traits', () => {
        const registry = createTraitRegistry(['static', Static]);
        const entity = world.spawn(Static, Position);
        expect(() => snapshotEntity(world, entity, registry)).toThrow(Error);
        entity.remove(Position);
        const snapshot = snapshotEntity(world, entity, registry);
        entity.destroy();
        expect(() => snapshotEntity(world, entity, registry)).toThrow(Error);
        expect(() => rollbackEntity(world, entity, registry, snapshot)).toThrow(Error);
    });

    it('rolls an entity back to a snapshot', () => {
        const registry = createTraitRegistry(
            ['position', Position],
            ['health', Health],
            ['static', Static],
            ['likes', Likes]
        );
        const target = world.spawn();
        const other = world.spawn();
        const entity = world.spawn(Position({ x: 1, y: 1 }), Static);
        entity.add(Likes(target));
        const snapshot = snapshotEntity(world, entity, registry);

        entity.set(Position, { x: 8, y: 9 });
        entity.add(Health({ value: 3 }));
        entity.remove(Static);
        entity.remove(Likes(target));
        entity.add(Likes(other));

        rollbackEntity(world, entity, registry, snapshot);

        expect(entity.has(Static)).toBe(true);
        expect(entity.has(Health)).toBe(false);
        expect(entity.get(Position)).toEqual({ x: 1, y: 1 });
        expect(entity.targetsFor(Likes).map((entry) => entry.id())).toEqual([target.id()]);
    });

    it('throws when a relation target is missing or a key is unknown', () => {
        const registry = createTraitRegistry(['likes', Likes], ['position', Position]);
        const entity = world.spawn();
        const missing = world.spawn();
        missing.destroy();
        expect(() =>
            rollbackEntity(world, entity, registry, {
                id: entity.id(),
                traits: {},
                relations: { likes: [{ targetId: missing.id() }] },
            })
        ).toThrow(Error);
        expect(() =>
            rollbackEntity(world, entity, registry, {
                id: entity.id(),
                traits: { nope: true },
            })
        ).toThrow(Error);
    });

    it('snapshots and restores a world, preserving ids and skipping the world entity', () => {
        const registry = createTraitRegistry(
            ['position', Position],
            ['inventory', Inventory],
            ['childOf', ChildOf]
        );
        const parent = world.spawn(Position({ x: 2, y: 3 }));
        const dropped = world.spawn();
        dropped.destroy();
        const child = world.spawn(Inventory);
        child.set(Inventory, { items: ['gem'] });
        child.add(ChildOf(parent, { order: 1 }));
        const checkpoint = snapshotWorld(world, registry);

        expect(checkpoint.entities.map((entry) => entry.id)).toEqual([parent.id(), child.id()]);
        expect(checkpoint.entities.some((entry) => entry.id === world.entities[0].id())).toBe(false);

        child.get(Inventory)!.items.push('mutated');
        expect(checkpoint.entities[1].traits.inventory).toEqual({ items: ['gem'] });

        parent.set(Position, { x: 0, y: 0 });
        child.destroy();
        world.spawn(Position);

        rollbackWorld(world, registry, checkpoint);

        const restoredParent = world.entities.find((entry) => entry.id() === parent.id())!;
        const restoredChild = world.entities.find((entry) => entry.id() === child.id())!;
        expect(restoredParent.get(Position)).toEqual({ x: 2, y: 3 });
        expect(restoredChild.get(Inventory)).toEqual({ items: ['gem'] });
        expect(restoredChild.targetFor(ChildOf)?.id()).toBe(parent.id());
        expect(restoredChild.get(ChildOf(restoredParent))).toEqual({ order: 1 });
        expect(world.entities.filter((entry) => entry.id() !== 0)).toHaveLength(2);
    });

    it('throws for dangling world relation targets', () => {
        const registry = createTraitRegistry(['likes', Likes]);
        expect(() =>
            rollbackWorld(world, registry, {
                entities: [{ id: 1, traits: {}, relations: { likes: [{ targetId: 4 }] } }],
            })
        ).toThrow(Error);
    });

    it('diffs entity and world snapshots', () => {
        expect(() => diffEntitySnapshots(undefined as never, { id: 1, traits: {} })).toThrow(Error);
        expect(() => diffWorldSnapshots(undefined as never, { entities: [] })).toThrow(Error);
        expect(() => diffWorldSnapshots({} as never, { entities: [] })).toThrow(Error);

        const diff = diffEntitySnapshots(
            { id: 1, traits: { a: true, b: { x: 1 }, c: { y: 1 } } },
            { id: 1, traits: { b: { x: 1 }, c: { y: 2 }, d: true } }
        );
        expect(diff).toEqual({
            addedTraits: ['d'],
            removedTraits: ['a'],
            changedTraits: ['c'],
        });

        const changed = diffWorldSnapshots(
            {
                entities: [
                    { id: 2, traits: { a: true }, relations: { likes: [{ targetId: 1 }, { targetId: 3 }] } },
                    { id: 3, traits: { a: { n: 1 } } },
                    { id: 5, traits: {}, relations: {} },
                ],
            },
            {
                entities: [
                    { id: 1, traits: {} },
                    {
                        id: 2,
                        traits: { a: true },
                        relations: { likes: [{ targetId: 3 }, { targetId: 1 }] },
                    },
                    { id: 3, traits: { a: { n: 2 } } },
                    { id: 5, traits: {} },
                ],
            }
        );
        expect(changed).toEqual({ added: [1], removed: [], changed: [3] });
    });

    it('exposes world and entity convenience methods', () => {
        const registry = createTraitRegistry(['position', Position]);
        const entity = world.spawn(Position({ x: 4, y: 5 }));
        const checkpoint = world.snapshot(registry);
        entity.set(Position, { x: 0, y: 0 });
        entity.rollback(registry, checkpoint.entities[0]);
        expect(entity.get(Position)).toEqual({ x: 4, y: 5 });
        entity.set(Position, { x: 1, y: 1 });
        world.rollback(registry, checkpoint);
        const restored = world.entities.find((entry) => entry.id() === entity.id())!;
        expect(restored.get(Position)).toEqual({ x: 4, y: 5 });
    });
});
