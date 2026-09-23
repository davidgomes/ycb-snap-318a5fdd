import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
    $internal,
    createAdded,
    createChanged,
    createRemoved,
    createWorld,
    getStore,
    Not,
    Or,
    relation,
    trait,
} from '../src';

const Position = trait({ x: 0, y: 0 });
const IsActive = trait();
const Foo = trait();
const Bar = trait();

describe('Query modifiers', () => {
    const world = createWorld();
    world.init();

    beforeEach(() => {
        world.reset();
    });

    it('should correctly populate Not queries when traits are added and removed', () => {
        const entityA = world.spawn();
        const entityB = world.spawn();
        const entityC = world.spawn();

        let entities: any = world.query(Foo);
        expect(entities.length).toBe(0);

        entities = world.query(Not(Foo));
        expect(entities[0]).toBe(entityA);
        expect(entities[1]).toBe(entityB);
        expect(entities[2]).toBe(entityC);

        // Add
        entityA.add(Foo);
        entityB.add(Bar);
        entityC.add(Foo, Bar);

        entities = world.query(Foo);
        expect(entities[0]).toBe(entityA);
        expect(entities[1]).toBe(entityC);

        entities = world.query(Foo, Bar);
        expect(entities[0]).toBe(entityC);

        entities = world.query(Not(Foo));
        expect(entities[0]).toBe(entityB);

        // Remove
        entityA.remove(Foo);

        entities = world.query(Foo);
        expect(entities[0]).toBe(entityC);

        entities = world.query(Not(Foo));
        expect(entities[0]).toBe(entityB);
        expect(entities[1]).toBe(entityA);

        entities = world.query(Not(Foo), Not(Bar));
        expect(entities[0]).toBe(entityA);

        // Remove more so entity A and C have no traits
        entityC.remove(Foo);
        entityC.remove(Bar);

        entities = world.query(Not(Foo), Not(Bar));
        expect(entities.length).toBe(2);

        entities = world.query(Not(Foo));
        expect(entities.length).toBe(3);
    });

    it('modifiers can be added as one call or separately', () => {
        const ctx = world[$internal];
        const entity = world.spawn();
        entity.add(Position, IsActive);

        let entities: any = world.query(Not(Foo), Not(Bar));
        expect(entities.length).toBe(1);

        entities = world.query(Not(Foo, Bar));
        expect(entities.length).toBe(1);

        // These queries should be hashed the same.
        expect(ctx.queriesHashMap.size).toBe(1);
    });

    it('should correctly populate Added queries when traits are added', () => {
        const Added = createAdded();

        const entityA = world.spawn();
        const entityB = world.spawn();
        const entityC = world.spawn();

        let entities: readonly number[] = [];

        entities = world.query(Added(Foo));
        expect(entities.length).toBe(0);

        entityA.add(Foo);
        entities = world.query(Added(Foo));
        expect(entities[0]).toBe(entityA);

        // The query gets drained and should be empty when run again.
        entities = world.query(Added(Foo));
        expect(entities.length).toBe(0);

        entityB.add(Foo);
        entities = world.query(Added(Foo));
        expect(entities[0]).toBe(entityB);

        // And a static query should give both entities.
        entities = world.query(Foo);
        expect(entities[0]).toBe(entityA);
        expect(entities[1]).toBe(entityB);

        // Should not be added to the query if the trait is removed before it is read.
        entityC.add(Foo);
        entityC.remove(Foo);
        entities = world.query(Added(Foo));
        expect(entities.length).toBe(0);

        // But if it is removed and added again in the same frame it should be recorded.
        entityA.remove(Foo);
        entityA.add(Foo);
        entities = world.query(Added(Foo));
        expect(entities[0]).toBe(entityA);

        // Should only populate the query if tracked trait is added,
        // even if it matches the query otherwise.
        entityA.remove(Foo, Bar); // Quick reset
        entityA.add(Foo);
        world.query(Added(Foo)); // Drain query

        entityA.add(Bar);
        entities = world.query(Added(Foo));
        expect(entities.length).toBe(0); // Fails for Added
        entities = world.query(Foo, Bar);
        expect(entities[0]).toBe(entityA); // But matches static query
    });

    it('should properly populate Added queries with mulitple tracked traits', () => {
        const Added = createAdded();

        const entityA = world.spawn();
        const entityB = world.spawn();

        let entities = world.query(Added(Foo, Bar));
        expect(entities.length).toBe(0);

        entityA.add(Foo);
        entities = world.query(Added(Foo, Bar));
        expect(entities.length).toBe(0);

        entityA.add(Bar);
        entities = world.query(Added(Foo, Bar));
        expect(entities[0]).toBe(entityA);

        entityB.add(Foo);
        entities = world.query(Added(Foo, Bar));
        expect(entities.length).toBe(0);

        entityB.add(Bar);
        entities = world.query(Added(Foo, Bar));
        expect(entities[0]).toBe(entityB);
    });

    it('should track multiple Added modifiers independently', () => {
        const Added = createAdded();
        const Added2 = createAdded();

        const entityA = world.spawn();
        const entityB = world.spawn();

        let entities = world.query(Added(Foo));
        expect(entities.length).toBe(0);

        let entities2 = world.query(Added2(Foo));
        expect(entities2.length).toBe(0);

        entityA.add(Foo);
        entities = world.query(Added(Foo));
        expect(entities.length).toBe(1);

        entityB.remove(Foo);
        entityB.add(Foo);
        entities = world.query(Added(Foo));
        entities2 = world.query(Added2(Foo));

        expect(entities.length).toBe(1);
        expect(entities2.length).toBe(2);
    });

    it('should populate Added queries even if they are registered after the trait is added', () => {
        const Added = createAdded();

        const entityA = world.spawn(Foo);
        const entityB = world.spawn(Foo, Bar);

        let entities: any = world.query(Added(Foo));
        expect(entities[0]).toBe(entityA);
        expect(entities[1]).toBe(entityB);

        entities = world.query(Added(Foo, Bar));
        expect(entities[0]).toBe(entityB);

        const LaterAdded = createAdded();

        let entities2 = world.query(LaterAdded(Foo));
        expect(entities2.length).toBe(0);

        entityA.remove(Foo); // Reset
        entityA.add(Foo);
        entities = world.query(Added(Foo));
        entities2 = world.query(LaterAdded(Foo));

        expect(entities.length).toBe(1);
        expect(entities2.length).toBe(1);
    });

    it('should combine Not and Added modifiers with logical AND', () => {
        const Added = createAdded();

        const entityA = world.spawn();
        const entityB = world.spawn();

        // No entities should match this query since while Not will match
        // all empty entities, Added will only match entities that have Foo.
        let entities = world.query(Added(Foo), Not(Bar));
        expect(entities.length).toBe(0);

        // Adding Foo to entityA should match the query as it has Foo added and not Bar.
        entityA.add(Foo);
        entities = world.query(Added(Foo), Not(Bar));
        expect(entities[0]).toBe(entityA);

        // Adding Foo and Bar to entityB should not match the query as it has Bar.
        entityB.add(Foo, Bar);
        entities = world.query(Added(Foo), Not(Bar));
        expect(entities.length).toBe(0);
    });

    it('should properly populate Removed queries when traits are removed', () => {
        const Removed = createRemoved();

        const entityA = world.spawn();
        const entityB = world.spawn();

        let entities = world.query(Removed(Foo));
        expect(entities.length).toBe(0);

        entityA.add(Foo);
        entityB.add(Foo);
        entities = world.query(Removed(Foo));
        expect(entities.length).toBe(0);

        entityA.remove(Foo);
        entities = world.query(Removed(Foo));
        expect(entities[0]).toBe(entityA);

        // Should work with traits added and removed in the same frame.
        entityA.add(Foo);
        entityA.remove(Foo);
        entities = world.query(Removed(Foo));
        expect(entities[0]).toBe(entityA);
        // Should track between Removed modifiers independently.
        const Removed2 = createRemoved();

        let entities2 = world.query(Removed2(Foo));
        expect(entities2.length).toBe(0);

        entityA.add(Foo);
        entityA.remove(Foo);
        entities = world.query(Removed(Foo));
        expect(entities.length).toBe(1);

        entityB.add(Foo);
        entityB.remove(Foo);
        entities = world.query(Removed(Foo));
        entities2 = world.query(Removed2(Foo));

        expect(entities.length).toBe(1);
        expect(entities2.length).toBe(2);
    });

    it('should populate Removed queries even if they are registered after the trait is removed', () => {
        const Removed = createRemoved();

        const entity = world.spawn(Foo);
        entity.remove(Foo);

        let entities = world.query(Removed(Foo));
        expect(entities[0]).toBe(entity);

        entity.add(Foo); // Reset

        const LaterRemoved = createRemoved();

        let entities2 = world.query(LaterRemoved(Foo));
        expect(entities2.length).toBe(0);

        entity.remove(Foo);
        entities = world.query(Removed(Foo));
        entities2 = world.query(LaterRemoved(Foo));

        expect(entities.length).toBe(1);
        expect(entities2.length).toBe(1);
    });

    it('should combine Not and Removed modifiers with logical AND', () => {
        const Removed = createRemoved();

        const entityA = world.spawn();
        const entityB = world.spawn();

        // Initially, no entities should match the query because no entities
        // have Foo removed even though Not matches all empty entities.
        let entities = world.query(Removed(Foo), Not(Bar));
        expect(entities.length).toBe(0);

        // Add Foo to entityA, then it should not match as it hasn't been removed yet.
        entityA.add(Foo);
        entities = world.query(Removed(Foo), Not(Bar));
        expect(entities.length).toBe(0);

        // Add Foo and Bar to entityB, it also should not match as
        // Foo hasn't been removed and it has Bar.
        entityB.add(Foo, Bar);
        entities = world.query(Removed(Foo), Not(Bar));
        expect(entities.length).toBe(0);

        // Remove Foo from entityA, it should now match as Foo is removed and
        // it does not have Bar.
        entityA.remove(Foo);
        entities = world.query(Removed(Foo), Not(Bar));
        expect(entities[0]).toBe(entityA);

        // Remove Foo from entityB, it should still not match as it has Bar.
        entityB.remove(Foo);
        entities = world.query(Removed(Foo), Not(Bar));
        expect(entities.length).toBe(0);
    });

    it('should combine Added and Removed modifiers with logical AND', () => {
        const Added = createAdded();
        const Removed = createRemoved();

        const entityA = world.spawn();
        const entityB = world.spawn();

        let entities = world.query(Added(Foo), Removed(Bar));
        expect(entities.length).toBe(0);

        // Add Foo to entityA and Bar to entityB.
        // Neither entity should match the query.
        entityA.add(Foo);
        entityB.add(Bar);
        entities = world.query(Added(Foo), Removed(Bar));
        expect(entities.length).toBe(0);

        // Remove Foo from entityA and remove Bar from entityB.
        // Neither entity should match the query.
        entityA.remove(Foo);
        entityB.remove(Bar);
        entities = world.query(Added(Foo), Removed(Bar));
        expect(entities.length).toBe(0);

        // Add Foo and Bar to entityA, then remove Bar.
        // This entity should now match the query.
        entityA.add(Foo, Bar);
        entityA.remove(Bar);
        entities = world.query(Added(Foo), Removed(Bar));
        expect(entities[0]).toBe(entityA);

        // Resets and can fill again.
        entityA.remove(Foo);
        entityA.add(Foo);
        entities = world.query(Added(Foo), Removed(Bar));
        expect(entities.length).toBe(0);

        // Add Foo to entityB and remove Bar.
        // This entity should now match the query.
        entityB.add(Foo, Bar);
        entityB.remove(Bar);
        entities = world.query(Added(Foo), Removed(Bar));
        expect(entities[0]).toBe(entityB);

        // Make sure changes in one entity do not leak to the other.
        const entityC = world.spawn();
        const entityD = world.spawn();

        entityC.add(Foo);
        entityD.add(Bar);
        entityD.remove(Bar);

        entities = world.query(Added(Foo), Removed(Bar));
        expect(entities.length).toBe(0);
    });

    it('should properly populate Changed queries when traits are changed', () => {
        const Changed = createChanged();

        const entityA = world.spawn();

        let entities = world.query(Changed(Position));
        expect(entities.length).toBe(0);

        entityA.add(Position);
        entities = world.query(Changed(Position));
        expect(entities.length).toBe(0);

        const positions = getStore(world, Position);
        positions.x[entityA] = 10;
        positions.y[entityA] = 20;

        // Set changed should populate the query.
        entityA.changed(Position);
        entities = world.query(Changed(Position));
        expect(entities[0]).toBe(entityA);

        // Querying again should not return the entity.
        entities = world.query(Changed(Position));
        expect(entities.length).toBe(0);

        // Should not populate the query if the trait is removed.
        entityA.remove(Position);
        entityA.changed(Position);
        entities = world.query(Changed(Position));
        expect(entities.length).toBe(0);
    });

    it('should populate Changed queries even if they are registered after the trait is changed', () => {
        const Changed = createChanged();

        const entity = world.spawn(Position);

        const positions = getStore(world, Position);
        positions.x[entity] = 10;
        positions.y[entity] = 20;
        entity.changed(Position);

        let entities = world.query(Changed(Position));
        // expect(entities).toEqual([entity]);

        const LaterChanged = createChanged();

        let entities2 = world.query(LaterChanged(Position));
        expect(entities2.length).toBe(0);

        positions.x[entity] = 30;
        positions.y[entity] = 40;
        entity.changed(Position);

        entities = world.query(Changed(Position));
        entities2 = world.query(LaterChanged(Position));

        expect(entities.length).toBe(1);
        expect(entities2.length).toBe(1);
    });

    it('should only update a Changed query when the tracked trait is changed', () => {
        const entity = world.spawn(Foo, Bar);

        const Changed = createChanged();

        expect(world.queryFirst(Changed(Foo), Changed(Bar))).toBeUndefined();

        entity.changed(Foo);
        entity.changed(Bar);
        expect(world.queryFirst(Changed(Foo), Changed(Bar))).toBe(entity);

        entity.changed(Foo);
        expect(world.queryFirst(Changed(Foo), Changed(Bar))).toBeUndefined();
    });

    // @see https://github.com/pmndrs/koota/issues/115
    it('should not trigger Changed query when removing a different trait', () => {
        const Changed = createChanged();
        const entity = world.spawn(Position);

        // Initial state - no changes
        expect(world.queryFirst(Changed(Position), Not(Foo))).toBeUndefined();

        // Change Position
        entity.changed(Position);
        expect(world.queryFirst(Changed(Position), Not(Foo))).toBe(entity);

        // Query again - should be empty
        expect(world.queryFirst(Changed(Position), Not(Foo))).toBeUndefined();

        // Add and remove Foo - should be empty
        entity.add(Foo);
        entity.remove(Foo);
        expect(world.queryFirst(Changed(Position), Not(Foo))).toBeUndefined();
    });

    it('should correctly populate Changed query when trait changes happen before query initialization', () => {
        // Create change modifier and spawn an entity
        const Changed = createChanged();
        const entity = world.spawn(Foo, Bar);

        // Mark Bar as changed
        entity.changed(Bar);

        // Even if the query wasn't executed before,
        // it should pick up the trait change
        expect(world.queryFirst(Changed(Bar))).toBe(entity);
    });

    it('updateEach should work with Added modifier', () => {
        const Added = createAdded();
        const entity = world.spawn(Position({ x: 10, y: 20 }));

        world.query(Added(Position)).updateEach(([position]) => {
            expect(position).toHaveProperty('x', 10);
            expect(position).toHaveProperty('y', 20);
            position.x = 100;
        });

        expect(entity.get(Position)!.x).toBe(100);
    });

    it('updateEach should work with Added modifier combined with other traits', () => {
        const Added = createAdded();
        const Name = trait({ name: '' });
        const entity = world.spawn(Position({ x: 5, y: 15 }), Name({ name: 'test' }));

        world.query(Added(Position), Name).updateEach(([position, name]) => {
            expect(position).toHaveProperty('x', 5);
            expect(position).toHaveProperty('y', 15);
            expect(name).toHaveProperty('name', 'test');
            position.x = 50;
            name.name = 'updated';
        });

        expect(entity.get(Position)!.x).toBe(50);
        expect(entity.get(Name)!.name).toBe('updated');
    });

    it('updateEach should work with Changed modifier', () => {
        const Changed = createChanged();
        const entity = world.spawn(Position({ x: 1, y: 2 }));

        entity.changed(Position);

        world.query(Changed(Position)).updateEach(([position]) => {
            expect(position).toHaveProperty('x', 1);
            expect(position).toHaveProperty('y', 2);
            position.x = 10;
        });

        expect(entity.get(Position)!.x).toBe(10);
    });

    it('updateEach should work with Removed modifier', () => {
        const Removed = createRemoved();
        const Name = trait({ name: '' });
        const entity = world.spawn(Position({ x: 7, y: 8 }), Name({ name: 'keep' }));

        entity.remove(Position);

        // Removed modifier includes the removed trait in stores, plus any other queried traits
        world.query(Removed(Position), Name).updateEach(([position, name]) => {
            // Position data may still be accessible (stale) even after removal
            expect(position).toHaveProperty('x');
            expect(name).toHaveProperty('name', 'keep');
            name.name = 'modified';
        });

        expect(entity.get(Name)!.name).toBe('modified');
    });

    it('should combine Or with Changed modifiers to match ANY changed trait', () => {
        const Changed = createChanged();

        const entityA = world.spawn(Position, Foo);
        const entityB = world.spawn(Position, Foo);
        const entityC = world.spawn(Position, Foo);

        // No changes yet
        let entities = world.query(Or(Changed(Position), Changed(Foo)));
        expect(entities.length).toBe(0);

        // Change only Position on entityA
        entityA.changed(Position);
        entities = world.query(Or(Changed(Position), Changed(Foo)));
        expect(entities).toContain(entityA);
        expect(entities.length).toBe(1);

        // Change only Foo on entityB
        entityB.changed(Foo);
        entities = world.query(Or(Changed(Position), Changed(Foo)));
        expect(entities).toContain(entityB);
        expect(entities.length).toBe(1);

        // Change both on entityC - should still match
        entityC.changed(Position);
        entityC.changed(Foo);
        entities = world.query(Or(Changed(Position), Changed(Foo)));
        expect(entities).toContain(entityC);
        expect(entities.length).toBe(1);
    });

    it('should combine Or with Added modifiers to match ANY added trait', () => {
        const Added = createAdded();

        const entityA = world.spawn();
        const entityB = world.spawn();
        const entityC = world.spawn();

        // No additions yet
        let entities = world.query(Or(Added(Position), Added(Foo)));
        expect(entities.length).toBe(0);

        // Add only Position to entityA
        entityA.add(Position);
        entities = world.query(Or(Added(Position), Added(Foo)));
        expect(entities).toContain(entityA);
        expect(entities.length).toBe(1);

        // Add only Foo to entityB
        entityB.add(Foo);
        entities = world.query(Or(Added(Position), Added(Foo)));
        expect(entities).toContain(entityB);
        expect(entities.length).toBe(1);

        // Add both to entityC - should still match
        entityC.add(Position, Foo);
        entities = world.query(Or(Added(Position), Added(Foo)));
        expect(entities).toContain(entityC);
        expect(entities.length).toBe(1);
    });

    it('should combine Or with Removed modifiers to match ANY removed trait', () => {
        const Removed = createRemoved();

        const entityA = world.spawn(Position, Foo);
        const entityB = world.spawn(Position, Foo);
        const entityC = world.spawn(Position, Foo);

        // No removals yet
        let entities = world.query(Or(Removed(Position), Removed(Foo)));
        expect(entities.length).toBe(0);

        // Remove only Position from entityA
        entityA.remove(Position);
        entities = world.query(Or(Removed(Position), Removed(Foo)));
        expect(entities).toContain(entityA);
        expect(entities.length).toBe(1);

        // Remove only Foo from entityB
        entityB.remove(Foo);
        entities = world.query(Or(Removed(Position), Removed(Foo)));
        expect(entities).toContain(entityB);
        expect(entities.length).toBe(1);

        // Remove both from entityC - should still match
        entityC.remove(Position);
        entityC.remove(Foo);
        entities = world.query(Or(Removed(Position), Removed(Foo)));
        expect(entities).toContain(entityC);
        expect(entities.length).toBe(1);
    });

    it('should track Changed on a relation', () => {
        const ChildOf = relation({ store: { order: 0 } });
        const Changed = createChanged();

        const parentA = world.spawn();
        const parentB = world.spawn();
        const childA = world.spawn(ChildOf(parentA));
        const childB = world.spawn(ChildOf(parentB));

        // No changes yet
        expect(world.query(Changed(ChildOf))).toHaveLength(0);

        // Change only childA
        childA.set(ChildOf(parentA), { order: 1 });
        let changed = world.query(Changed(ChildOf));
        expect(changed).toHaveLength(1);
        expect(changed).toContain(childA);

        // Change both, query filtered by parentA pair
        childA.set(ChildOf(parentA), { order: 2 });
        childB.set(ChildOf(parentB), { order: 3 });
        const filteredA = world.query(Changed(ChildOf), ChildOf(parentA));
        expect(filteredA).toHaveLength(1);
        expect(filteredA).toContain(childA);
    });

    it('should track Added on a relation', () => {
        const ChildOf = relation();
        const Added = createAdded();

        const parentA = world.spawn();
        const parentB = world.spawn();

        const childA = world.spawn(ChildOf(parentA));
        const childB = world.spawn(ChildOf(parentB));
        const childC = world.spawn(ChildOf(parentA));

        // Filtered by parentA: only childA and childC target parentA
        const filteredA = world.query(Added(ChildOf), ChildOf(parentA));
        expect(filteredA).toHaveLength(2);
        expect(filteredA).toContain(childA);
        expect(filteredA).toContain(childC);
        expect(filteredA).not.toContain(childB);
    });

    it('should track Removed on a relation', () => {
        const ChildOf = relation();
        const Removed = createRemoved();

        const parentA = world.spawn();
        const parentB = world.spawn();
        const childA = world.spawn(ChildOf(parentA));
        const childB = world.spawn(ChildOf(parentB));

        // No removals yet
        expect(world.query(Removed(ChildOf))).toHaveLength(0);

        // Remove childA's relation
        childA.remove(ChildOf(parentA));
        let removed = world.query(Removed(ChildOf));
        expect(removed).toHaveLength(1);
        expect(removed).toContain(childA);

        // Remove childB
        childB.remove(ChildOf(parentB));
        removed = world.query(Removed(ChildOf));
        expect(removed).toHaveLength(1);
        expect(removed).toContain(childB);
    });

    it('updateEach should work with Removed modifier for relations', () => {
        const Removed = createRemoved();
        const Contains = relation({ store: { amount: 0 } });

        const inventory = world.spawn();
        const gold = world.spawn();

        inventory.add(Contains(gold, { amount: 42 }));
        inventory.remove(Contains(gold));

        world.query(Removed(Contains), Contains(gold)).updateEach(([contains], entity) => {
            // Removed relation queries should still expose the removed pair's store data.
            expect(contains).toHaveProperty('amount', 42);
            // And its target
            expect(entity.targetFor(Contains)).toBe(gold);
        });
    });

    it('should not add newly spawned entities to tracking queries', () => {
        const Added = createAdded();
        const Removed = createRemoved();

        expect(world.query(Added(Foo))).toHaveLength(0);
        expect(world.query(Removed(Foo))).toHaveLength(0);

        world.spawn();

        expect(world.query(Added(Foo))).toHaveLength(0);
        expect(world.query(Removed(Foo))).toHaveLength(0);
    });

    it('should apply every query constraint when a tracking query is first populated', () => {
        const Added = createAdded();
        const Removed = createRemoved();

        const entityA = world.spawn(Foo, Position);
        world.spawn(Foo);
        world.spawn(Foo, Position, Bar);

        expect(world.query(Added(Foo), Position, Not(Bar)).slice()).toEqual([entityA]);

        // Both tracking groups must match, not just one of them.
        const entityD = world.spawn(Foo, Bar);
        entityD.remove(Bar);
        expect(world.query(Added(Foo), Removed(Bar)).slice()).toEqual([entityD]);
    });

    it('should hash tracking modifiers nested in Or', () => {
        const ctx = world[$internal];
        const Added = createAdded();
        const Changed = createChanged();

        world.query(Or(Added(Foo)));
        world.query(Or(Changed(Foo)));
        world.query(Or(Added(Bar)));

        expect(ctx.queriesHashMap.size).toBe(3);
        expect(ctx.queriesHashMap.has('')).toBe(false);
    });

    // @internal Tests internal implementation edge case with generation overflow
    it('[internal] should handle Changed modifier when trait registration causes generation overflow', () => {
        // Create a fresh world to control trait registration count
        const testWorld = createWorld();
        testWorld.init();

        // IsExcluded is already registered (bitflag=2 after), register 29 more to get bitflag=2^30
        const fillerTraits = Array.from({ length: 29 }, () => trait());
        for (const t of fillerTraits) {
            testWorld.spawn(t);
        }

        // Create Changed modifier - snapshots entityMasks with 1 generation
        const Changed = createChanged();

        // Spawn an entity so entityIndex.dense is not empty (required to trigger the bug)
        const entity = testWorld.spawn();

        // Register the 31st trait to trigger overflow (bitflag 2^30 -> 2^31 -> overflow)
        const Trait31 = trait();
        entity.add(Trait31);

        // Now entityMasks has 2 generations, but changedMask only has 1

        // Create the 32nd trait - will be in generation 1
        const NewTrait = trait();

        // This should not throw "cannot read properties of undefined (reading '0')"
        // when accessing changedMask[generationId][eid] where generationId is 1 but changedMask only has index 0
        expect(() => {
            testWorld.query(Changed(NewTrait));
        }).not.toThrow();
    });
});

describe('Relation pair tracking modifiers', () => {
    const world = createWorld();

    // Modifier factories are long-lived and reused across world resets.
    const Added = createAdded();
    const Removed = createRemoved();
    const Changed = createChanged();

    const ChildOf = relation();
    const Contains = relation({ store: { amount: 0 } });
    const Targeting = relation({ exclusive: true });

    beforeEach(() => {
        world.reset();
    });

    it('should track added pairs per target', () => {
        const parentA = world.spawn();
        const parentB = world.spawn();

        expect(world.query(Added(ChildOf(parentA)))).toHaveLength(0);

        const childA = world.spawn(ChildOf(parentA));
        world.spawn(ChildOf(parentB));

        expect(world.query(Added(ChildOf(parentA))).slice()).toEqual([childA]);
        expect(world.query(Added(ChildOf(parentA)))).toHaveLength(0);
    });

    it('should populate pair tracking queries registered after the events', () => {
        const parent = world.spawn();
        const child = world.spawn(ChildOf(parent), Contains(parent));
        const orphan = world.spawn(ChildOf(parent));

        // Starts observing while the orphan still has its pair.
        const LaterRemoved = createRemoved();

        orphan.remove(ChildOf(parent));
        child.set(Contains(parent), { amount: 1 });

        expect(world.query(Added(ChildOf(parent))).slice()).toEqual([child]);
        expect(world.query(LaterRemoved(ChildOf(parent))).slice()).toEqual([orphan]);
        // The orphan gained and lost the pair since the world reset, which cancels out.
        expect(world.query(Removed(ChildOf(parent)))).toHaveLength(0);
        expect(world.query(Changed(Contains(parent))).slice()).toEqual([child]);
    });

    it('should detect non-first pair additions and non-last pair removals', () => {
        const parentA = world.spawn();
        const parentB = world.spawn();
        const child = world.spawn(ChildOf(parentA));

        world.query(Added(ChildOf));
        world.query(Added(ChildOf(parentB)));
        world.query(Removed(ChildOf));
        world.query(Removed(ChildOf(parentA)));

        // The relation trait is already on the entity, so only the pair was added.
        child.add(ChildOf(parentB));
        expect(world.query(Added(ChildOf))).toHaveLength(0);
        expect(world.query(Added(ChildOf(parentB))).slice()).toEqual([child]);

        // Another target remains, so only the pair was removed.
        child.remove(ChildOf(parentA));
        expect(world.query(Removed(ChildOf))).toHaveLength(0);
        expect(world.query(Removed(ChildOf(parentA))).slice()).toEqual([child]);
    });

    it('should treat a * target as a wildcard', () => {
        const parentA = world.spawn();
        const parentB = world.spawn();
        const child = world.spawn(ChildOf(parentA));

        expect(world.query(Added(ChildOf('*'))).slice()).toEqual([child]);

        child.add(ChildOf(parentB));
        expect(world.query(Added(ChildOf('*'))).slice()).toEqual([child]);
        expect(world.query(Removed(ChildOf('*')))).toHaveLength(0);

        child.remove(ChildOf(parentA));
        expect(world.query(Removed(ChildOf('*'))).slice()).toEqual([child]);
    });

    it('should produce a removal and an addition for exclusive replacement', () => {
        const hero = world.spawn();
        const rat = world.spawn();
        const goblin = world.spawn();

        hero.add(Targeting(rat));
        world.query(Added(Targeting(goblin)));
        world.query(Removed(Targeting(rat)));
        world.query(Added(Targeting('*')));
        world.query(Removed(Targeting('*')));

        hero.add(Targeting(goblin));

        expect(world.query(Removed(Targeting(rat))).slice()).toEqual([hero]);
        expect(world.query(Added(Targeting(goblin))).slice()).toEqual([hero]);
        expect(world.query(Removed(Targeting('*'))).slice()).toEqual([hero]);
        expect(world.query(Added(Targeting('*'))).slice()).toEqual([hero]);
    });

    it('should cancel opposite pair events on the same target within an observation window', () => {
        const parentA = world.spawn();
        const parentB = world.spawn();
        const child = world.spawn(ChildOf(parentA));

        world.query(Added(ChildOf('*')));
        world.query(Removed(ChildOf('*')));

        // Added then removed.
        child.add(ChildOf(parentB));
        child.remove(ChildOf(parentB));
        expect(world.query(Added(ChildOf('*')))).toHaveLength(0);
        expect(world.query(Removed(ChildOf('*')))).toHaveLength(0);

        // Removed then added.
        child.remove(ChildOf(parentA));
        child.add(ChildOf(parentA));
        expect(world.query(Added(ChildOf('*')))).toHaveLength(0);
        expect(world.query(Removed(ChildOf('*')))).toHaveLength(0);

        // Events on different targets do not cancel.
        child.add(ChildOf(parentB));
        child.remove(ChildOf(parentA));
        expect(world.query(Added(ChildOf('*'))).slice()).toEqual([child]);
        expect(world.query(Removed(ChildOf('*'))).slice()).toEqual([child]);
    });

    it('should fire pair removals for all active pairs when an entity is destroyed', () => {
        const parentA = world.spawn();
        const parentB = world.spawn();
        const child = world.spawn(ChildOf(parentA), ChildOf(parentB));

        world.query(Removed(ChildOf(parentA)));
        world.query(Removed(ChildOf(parentB)));
        world.query(Removed(ChildOf('*')));

        child.destroy();

        expect(world.query(Removed(ChildOf(parentA))).slice()).toEqual([child]);
        expect(world.query(Removed(ChildOf(parentB))).slice()).toEqual([child]);
        expect(world.query(Removed(ChildOf('*'))).slice()).toEqual([child]);
    });

    it('should fire pair removals on sources when a target is destroyed', () => {
        const parent = world.spawn();
        const child = world.spawn(ChildOf(parent));

        world.query(Removed(ChildOf(parent)));
        parent.destroy();

        expect(world.query(Removed(ChildOf(parent))).slice()).toEqual([child]);
        expect(world.has(child)).toBe(true);
    });

    it('should compose pair modifiers with Or', () => {
        const parentA = world.spawn();
        const parentB = world.spawn();
        const parentC = world.spawn();

        const eitherAdded = () => world.query(Or(Added(ChildOf(parentA)), Added(ChildOf(parentB))));
        const mixed = () => world.query(Or(Removed(ChildOf(parentA)), Added(ChildOf(parentC))));
        expect(eitherAdded()).toHaveLength(0);
        expect(mixed()).toHaveLength(0);

        const childA = world.spawn(ChildOf(parentA));
        const childB = world.spawn(ChildOf(parentB));
        const childC = world.spawn(ChildOf(parentC));

        const entities = eitherAdded();
        expect(entities).toHaveLength(2);
        expect(entities).toContain(childA);
        expect(entities).toContain(childB);

        // Mixed event types.
        expect(mixed().slice()).toEqual([childC]);
        childA.remove(ChildOf(parentA));
        expect(mixed().slice()).toEqual([childA]);
    });

    it('should cache a distinct query per pair target', () => {
        const ctx = world[$internal];
        const parentA = world.spawn();
        const parentB = world.spawn();
        const child = world.spawn(ChildOf(parentA));

        const before = ctx.queriesHashMap.size;
        expect(world.query(Added(ChildOf(parentA))).slice()).toEqual([child]);
        expect(world.query(Added(ChildOf(parentB)))).toHaveLength(0);
        expect(world.query(Added(ChildOf('*'))).slice()).toEqual([child]);
        expect(world.query(Added(ChildOf)).slice()).toEqual([child]);
        expect(world.query(Or(Added(ChildOf(parentA)))).slice()).toEqual([child]);
        expect(world.query(Or(Added(ChildOf(parentB))))).toHaveLength(0);
        expect(ctx.queriesHashMap.size).toBe(before + 6);

        // The same pair maps to the same cached query.
        world.query(Added(ChildOf(parentA)));
        expect(ctx.queriesHashMap.size).toBe(before + 6);
    });

    it('should satisfy every constraint when combined with regular trait parameters', () => {
        const parent = world.spawn();
        const otherParent = world.spawn();

        world.query(Added(ChildOf(parent)), Position, Not(Foo));

        const child = world.spawn(Position, ChildOf(parent));
        world.spawn(ChildOf(parent));
        world.spawn(Position, Foo, ChildOf(parent));
        world.spawn(Position, ChildOf(otherParent));

        expect(world.query(Added(ChildOf(parent)), Position, Not(Foo)).slice()).toEqual([child]);

        // Also when the query is first created after the events.
        expect(world.query(Added(ChildOf(otherParent)), Not(Position))).toHaveLength(0);
        expect(world.query(Added(ChildOf(otherParent)), Position)).toHaveLength(1);
    });

    it('should satisfy relation filters combined with pair modifiers', () => {
        const parent = world.spawn();
        const gold = world.spawn();

        const child = world.spawn(ChildOf(parent), Contains(gold));
        world.spawn(ChildOf(parent));
        expect(world.query(Added(ChildOf(parent)), Contains(gold)).slice()).toEqual([child]);

        // Adding an unrelated pair must not re-admit an already observed entity.
        child.add(Contains(world.spawn()));
        expect(world.query(Added(ChildOf(parent)), Contains(gold))).toHaveLength(0);
    });

    it('should track changed pairs per target', () => {
        const gold = world.spawn();
        const silver = world.spawn();
        const inventory = world.spawn(Contains(gold), Contains(silver));

        expect(world.query(Changed(Contains(gold)))).toHaveLength(0);
        expect(world.query(Changed(Contains('*')))).toHaveLength(0);

        inventory.set(Contains(silver), { amount: 5 });
        expect(world.query(Changed(Contains(gold)))).toHaveLength(0);
        expect(world.query(Changed(Contains('*'))).slice()).toEqual([inventory]);

        inventory.set(Contains(gold), { amount: 10 });
        expect(world.query(Changed(Contains(gold))).slice()).toEqual([inventory]);

        // A removed pair no longer counts as changed.
        inventory.set(Contains(gold), { amount: 20 });
        inventory.remove(Contains(gold));
        expect(world.query(Changed(Contains(gold)))).toHaveLength(0);
    });

    it('should flag pair changes manually with entity.changed', () => {
        const gold = world.spawn();
        const silver = world.spawn();
        const inventory = world.spawn(Contains(gold), Contains(silver));

        const onChange = vi.fn();
        world.onChange(Contains(gold), onChange);

        world.query(Changed(Contains(gold)));
        world.query(Changed(Contains(silver)));

        inventory.changed(Contains(gold));
        expect(world.query(Changed(Contains(gold))).slice()).toEqual([inventory]);
        expect(world.query(Changed(Contains(silver)))).toHaveLength(0);
        expect(onChange).toHaveBeenCalledWith(inventory, gold);

        inventory.changed(Contains('*'));
        expect(world.query(Changed(Contains(gold))).slice()).toEqual([inventory]);
        expect(world.query(Changed(Contains(silver))).slice()).toEqual([inventory]);

        // Pairs the entity does not have are ignored.
        expect(world.query(Changed(Contains('*'))).slice()).toEqual([inventory]);
        inventory.changed(Contains(world.spawn()));
        expect(world.query(Changed(Contains('*')))).toHaveLength(0);
    });

    it('should keep tracking pairs across world resets', () => {
        const parent = world.spawn();
        const child = world.spawn(ChildOf(parent));
        expect(world.query(Added(ChildOf(parent))).slice()).toEqual([child]);

        world.reset();

        const newParent = world.spawn();
        const newChild = world.spawn(ChildOf(newParent), Contains(newParent));
        expect(world.query(Added(ChildOf(newParent))).slice()).toEqual([newChild]);
        expect(world.query(Removed(ChildOf(newParent)))).toHaveLength(0);

        newChild.set(Contains(newParent), { amount: 1 });
        expect(world.query(Changed(Contains(newParent))).slice()).toEqual([newChild]);

        newChild.remove(ChildOf(newParent));
        expect(world.query(Removed(ChildOf(newParent))).slice()).toEqual([newChild]);
    });

    it('should resolve per-target relation data when iterating pair tracked traits', () => {
        const gold = world.spawn();
        const silver = world.spawn();
        const inventory = world.spawn(Contains(gold, { amount: 1 }), Contains(silver, { amount: 2 }));

        world.query(Changed(Contains(silver)));
        inventory.set(Contains(silver), { amount: 3 });

        const amounts: number[] = [];
        world.query(Changed(Contains(silver))).readEach(([contains]) => {
            amounts.push(contains.amount);
        });
        expect(amounts).toEqual([3]);

        world.query(Added(Contains(gold))).readEach(([contains], entity) => {
            expect(entity).toBe(inventory);
            expect(contains.amount).toBe(1);
        });
    });

    it('should resolve the matched target data when iterating wildcard pair tracked traits', () => {
        const gold = world.spawn();
        const silver = world.spawn();
        const inventory = world.spawn(Contains(gold, { amount: 1 }), Contains(silver, { amount: 2 }));

        world.query(Changed(Contains('*')));
        inventory.set(Contains(silver), { amount: 3 });

        world.query(Changed(Contains('*'))).updateEach(([contains]) => {
            expect(contains.amount).toBe(3);
            contains.amount = 30;
        });

        expect(inventory.get(Contains(silver))!.amount).toBe(30);
        expect(inventory.get(Contains(gold))!.amount).toBe(1);

        // The write is flagged for the matched target only.
        expect(world.query(Changed(Contains(gold)))).toHaveLength(0);
        const changed = world.query(Changed(Contains('*')));
        expect(changed.slice()).toEqual([inventory]);

        const amounts: number[] = [];
        changed.readEach(([contains]) => amounts.push(contains.amount));
        expect(amounts).toEqual([30]);
    });

    it('should write per-target relation data back when updating pair tracked traits', () => {
        const gold = world.spawn();
        const silver = world.spawn();
        const inventory = world.spawn(Contains(gold, { amount: 1 }), Contains(silver, { amount: 2 }));

        const onChange = vi.fn();
        world.onChange(Contains, onChange);

        world.query(Changed(Contains(gold)));
        world.query(Changed(Contains(silver)));

        world.query(Added(Contains(silver))).updateEach(([contains]) => {
            contains.amount = 20;
        });

        expect(inventory.get(Contains(silver))!.amount).toBe(20);
        expect(inventory.get(Contains(gold))!.amount).toBe(1);
        expect(onChange).toHaveBeenCalledTimes(1);
        expect(onChange).toHaveBeenCalledWith(inventory, silver);

        // Changes made while iterating are flagged for their target only.
        expect(world.query(Changed(Contains(gold)))).toHaveLength(0);
        expect(world.query(Changed(Contains(silver))).slice()).toEqual([inventory]);

        inventory.set(Contains(gold), { amount: 5 });
        world.query(Changed(Contains(gold))).updateEach(([contains]) => {
            expect(contains.amount).toBe(5);
            contains.amount = 50;
        });
        expect(inventory.get(Contains(gold))!.amount).toBe(50);
        expect(world.query(Changed(Contains(gold))).slice()).toEqual([inventory]);
        expect(world.query(Changed(Contains(silver)))).toHaveLength(0);
    });
});
