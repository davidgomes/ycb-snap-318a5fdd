import { beforeEach, describe, expect, it } from 'vitest';
import {
    createTraitRegistry,
    diffEntitySnapshots,
    diffWorldSnapshots,
    relation,
    snapshotEntity,
    snapshotWorld,
    trait,
    universe,
    createWorld,
    type EntitySnapshot,
} from '../src';

describe('snapshots', () => {
    beforeEach(() => universe.reset());

    it('snapshots and rolls back entity traits and relations', () => {
        const Position = trait({ x: 0, y: 0 });
        const Selected = trait();
        const Links = relation({ store: { weight: 0 } });
        const registry = createTraitRegistry(
            ['position', Position],
            ['selected', Selected],
            ['links', Links]
        );
        const world = createWorld();
        const target = world.spawn();
        const entity = world.spawn(Position({ x: 1, y: 2 }), Selected, Links(target, { weight: 3 }));

        const checkpoint = snapshotEntity(world, entity, registry);
        entity.set(Position, { x: 9, y: 9 });
        entity.remove(Selected, Links(target));
        entity.rollback(registry, checkpoint);

        expect(snapshotEntity(world, entity, registry)).toEqual(checkpoint);
        expect(entity.get(Position)).toEqual({ x: 1, y: 2 });
        expect(entity.get(Links(target))).toEqual({ weight: 3 });
    });

    it('restores a world with stable logical IDs', () => {
        const Name = trait({ value: '' });
        const ChildOf = relation({ exclusive: true });
        const registry = createTraitRegistry(['name', Name], ['parent', ChildOf]);
        const world = createWorld();
        const parent = world.spawn(Name({ value: 'parent' }));
        world.spawn(Name({ value: 'child' }), ChildOf(parent));
        const checkpoint = snapshotWorld(world, registry);

        parent.destroy();
        world.rollback(registry, checkpoint);

        expect(world.entities.map((entity) => entity.id())).toEqual([0, 1, 2]);
        expect(
            world.entities
                .find((entity) => entity.get(Name)?.value === 'child')
                ?.targetFor(ChildOf)
                ?.id()
        ).toBe(parent.id());
        expect(snapshotWorld(world, registry)).toEqual(checkpoint);
    });

    it('diffs snapshots without depending on key or relation order', () => {
        const a: EntitySnapshot = {
            id: 1,
            traits: { first: { value: 1 }, tag: true },
            relations: { links: [{ targetId: 3, data: { value: 2 } }, { targetId: 2 }] },
        };
        const b: EntitySnapshot = {
            id: 1,
            traits: { tag: true, first: { value: 1 } },
            relations: { links: [{ targetId: 2 }, { targetId: 3, data: { value: 2 } }] },
        };

        expect(diffEntitySnapshots(a, b)).toEqual({
            addedTraits: [],
            removedTraits: [],
            changedTraits: [],
        });
        expect(diffWorldSnapshots({ entities: [a] }, { entities: [b] })).toEqual({
            added: [],
            removed: [],
            changed: [],
        });
    });
});
