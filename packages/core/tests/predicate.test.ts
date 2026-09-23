import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
    createAdded,
    createChanged,
    createPredicate,
    createQuery,
    createRemoved,
    createWorld,
    type Entity,
    Not,
    Or,
    relation,
    trait,
    type Trait,
} from '../src';

const Health = trait({ value: 100 });
const Armor = trait({ value: 0 });
const Position = trait({ x: 0, y: 0 });
const Inventory = trait(() => ({ items: [] as string[] }));
const IsPlayer = trait();

// Query results carry helper methods, so compare their entities as plain arrays.
const list = (entities: readonly Entity[]) => [...entities];

describe('Predicates', () => {
    const world = createWorld();

    beforeEach(() => {
        world.reset();
    });

    it('should match entities whose dependency data satisfies the predicate', () => {
        const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);

        const low = world.spawn(Health({ value: 10 }));
        world.spawn(Health({ value: 50 }));
        world.spawn(Position);

        expect(list(world.query(IsLowHealth))).toEqual([low]);
    });

    it('should pass each dependency data in order as a single array', () => {
        const fn = vi.fn(([health, armor]) => health.value + armor.value > 100);
        const IsTough = createPredicate([Health, Armor], fn);

        const entity = world.spawn(Health({ value: 90 }), Armor({ value: 20 }));

        expect(list(world.query(IsTough))).toEqual([entity]);
        expect(fn).toHaveBeenLastCalledWith([{ value: 90 }, { value: 20 }]);
    });

    it('should pass AoS instances to the predicate', () => {
        const HasItems = createPredicate([Inventory], ([inventory]) => inventory.items.length > 0);

        const entity = world.spawn(Inventory({ items: ['sword'] }));
        world.spawn(Inventory);

        expect(list(world.query(HasItems))).toEqual([entity]);
    });

    it('should return a distinct predicate on every call', () => {
        const fn = ([health]: { value: number }[]) => health.value < 20;
        const A = createPredicate([Health], fn);
        const B = createPredicate([Health], fn);

        expect(A).not.toBe(B);
        expect(createQuery(A).hash).not.toBe(createQuery(B).hash);

        const entity = world.spawn(Health({ value: 10 }));
        expect(list(world.query(A))).toEqual([entity]);
        expect(list(world.query(B))).toEqual([entity]);
    });

    it('should throw when a dependency is a tag', () => {
        expect(() => createPredicate([IsPlayer], () => true)).toThrow();
        expect(() => createPredicate([Health, IsPlayer], () => true)).toThrow();
    });

    it('should throw when a dependency is a relation', () => {
        const ChildOf = relation();
        const Contains = relation({ store: { amount: 0 } });
        const parent = world.spawn();

        expect(() => createPredicate([ChildOf as unknown as Trait], () => true)).toThrow();
        expect(() => createPredicate([Contains as unknown as Trait], () => true)).toThrow();
        expect(() => createPredicate([Contains(parent) as unknown as Trait], () => true)).toThrow();
    });

    it('should re-evaluate when a dependency is set', () => {
        const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);
        const entity = world.spawn(Health);

        expect(world.query(IsLowHealth)).toHaveLength(0);

        entity.set(Health, { value: 5 });
        expect(list(world.query(IsLowHealth))).toEqual([entity]);

        entity.set(Health, { value: 50 });
        expect(world.query(IsLowHealth)).toHaveLength(0);

        // Setting without flagging a change still re-evaluates.
        entity.set(Health, { value: 1 }, false);
        expect(list(world.query(IsLowHealth))).toEqual([entity]);

        // Callback setters also re-evaluate.
        entity.set(Health, (prev) => ({ value: prev.value + 100 }));
        expect(world.query(IsLowHealth)).toHaveLength(0);
    });

    it('should re-evaluate when a dependency is added or removed', () => {
        const IsTough = createPredicate(
            [Health, Armor],
            ([health, armor]) => health.value + armor.value > 100
        );
        const entity = world.spawn(Health);

        expect(world.query(IsTough)).toHaveLength(0);

        entity.add(Armor({ value: 10 }));
        expect(list(world.query(IsTough))).toEqual([entity]);

        entity.remove(Armor);
        expect(world.query(IsTough)).toHaveLength(0);

        entity.add(Armor({ value: 0 }));
        expect(world.query(IsTough)).toHaveLength(0);
    });

    it('should re-evaluate when a dependency is flagged as changed', () => {
        const HasItems = createPredicate([Inventory], ([inventory]) => inventory.items.length > 0);
        const entity = world.spawn(Inventory);

        expect(world.query(HasItems)).toHaveLength(0);

        entity.get(Inventory)!.items.push('shield');
        entity.changed(Inventory);
        expect(list(world.query(HasItems))).toEqual([entity]);
    });

    it('should evaluate entities spawned after the predicate is used', () => {
        const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);
        expect(world.query(IsLowHealth)).toHaveLength(0);

        const entity = world.spawn(Health({ value: 5 }));
        world.spawn(Health({ value: 25 }));
        expect(list(world.query(IsLowHealth))).toEqual([entity]);
    });

    it('should drop destroyed entities', () => {
        const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);
        const entity = world.spawn(Health({ value: 5 }));
        expect(list(world.query(IsLowHealth))).toEqual([entity]);

        entity.destroy();
        expect(world.query(IsLowHealth)).toHaveLength(0);
    });

    it('should combine with traits and not add data to the callback tuple', () => {
        const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);

        const entity = world.spawn(Health({ value: 5 }), Position({ x: 1, y: 2 }));
        world.spawn(Health({ value: 50 }), Position);

        const reads: { x: number; value: number; length: number }[] = [];
        world.query(Position, IsLowHealth, Health).readEach((state, e) => {
            const [position, health] = state;
            expect(e).toBe(entity);
            reads.push({ x: position.x, value: health.value, length: state.length });
        });
        expect(reads).toEqual([{ x: 1, value: 5, length: 2 }]);

        world.query(IsLowHealth, Position).updateEach(([position]) => {
            position.x = 10;
        });
        expect(entity.get(Position)!.x).toBe(10);

        world.query(IsLowHealth).useStores((stores, entities) => {
            expect(stores).toHaveLength(0);
            expect(list(entities)).toEqual([entity]);
        });
    });

    it('should match entities missing a dependency or failing the predicate with Not', () => {
        const IsTough = createPredicate(
            [Health, Armor],
            ([health, armor]) => health.value + armor.value > 100
        );

        const tough = world.spawn(Health, Armor({ value: 50 }));
        const weak = world.spawn(Health, Armor);
        const noArmor = world.spawn(Health);
        const empty = world.spawn();

        let entities = world.query(Not(IsTough));
        expect(entities).toHaveLength(3);
        expect(entities).toContain(weak);
        expect(entities).toContain(noArmor);
        expect(entities).toContain(empty);
        expect(entities).not.toContain(tough);

        weak.set(Armor, { value: 90 });
        tough.remove(Armor);
        entities = world.query(Not(IsTough));
        expect(entities).toContain(tough);
        expect(entities).not.toContain(weak);

        // Newly spawned entities without dependencies match Not.
        const spawned = world.spawn();
        expect(world.query(Not(IsTough))).toContain(spawned);

        // Not composes with other traits.
        const withHealth = world.query(Health, Not(IsTough));
        expect(withHealth).toHaveLength(2);
        expect(withHealth).toContain(tough);
        expect(withHealth).toContain(noArmor);
    });

    it('should accept predicates in Or', () => {
        const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);
        const IsArmored = createPredicate([Armor], ([armor]) => armor.value > 50);

        const low = world.spawn(Health({ value: 5 }));
        const armored = world.spawn(Armor({ value: 60 }));
        const player = world.spawn(IsPlayer);
        world.spawn(Health, Armor);

        let entities = world.query(Or(IsLowHealth, IsArmored));
        expect(entities).toHaveLength(2);
        expect(entities).toContain(low);
        expect(entities).toContain(armored);

        entities = world.query(Or(IsLowHealth, IsPlayer));
        expect(entities).toHaveLength(2);
        expect(entities).toContain(low);
        expect(entities).toContain(player);

        low.set(Health, { value: 100 });
        expect(list(world.query(Or(IsLowHealth, IsArmored)))).toEqual([armored]);
    });

    it('should track entities that start satisfying the predicate with Added', () => {
        const Added = createAdded();
        const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);

        const a = world.spawn(Health({ value: 5 }));
        const b = world.spawn(Health({ value: 50 }));

        // Nothing was in the previous result, so every match is new.
        expect(list(world.query(Added(IsLowHealth)))).toEqual([a]);
        expect(world.query(Added(IsLowHealth))).toHaveLength(0);

        // Staying true is not an addition.
        a.set(Health, { value: 1 });
        expect(world.query(Added(IsLowHealth))).toHaveLength(0);

        b.set(Health, { value: 10 });
        expect(list(world.query(Added(IsLowHealth)))).toEqual([b]);

        // Becoming true and false again before the query runs is not an addition.
        const c = world.spawn(Health({ value: 50 }));
        c.set(Health, { value: 10 });
        c.set(Health, { value: 50 });
        expect(world.query(Added(IsLowHealth))).toHaveLength(0);

        // Gaining a missing dependency can satisfy the predicate.
        const d = world.spawn();
        d.add(Health({ value: 3 }));
        expect(list(world.query(Added(IsLowHealth)))).toEqual([d]);
    });

    it('should track entities that stop satisfying the predicate with Removed', () => {
        const Removed = createRemoved();
        const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);

        const a = world.spawn(Health({ value: 5 }));
        const b = world.spawn(Health({ value: 5 }));
        const c = world.spawn(Health({ value: 50 }));

        expect(world.query(Removed(IsLowHealth))).toHaveLength(0);

        a.set(Health, { value: 50 });
        expect(list(world.query(Removed(IsLowHealth)))).toEqual([a]);
        expect(world.query(Removed(IsLowHealth))).toHaveLength(0);

        // Losing a dependency is a transition to false.
        b.remove(Health);
        expect(list(world.query(Removed(IsLowHealth)))).toEqual([b]);

        // Staying false is not a removal.
        c.set(Health, { value: 60 });
        expect(world.query(Removed(IsLowHealth))).toHaveLength(0);

        // Becoming false and true again before the query runs is not a removal.
        c.set(Health, { value: 1 });
        world.query(Removed(IsLowHealth));
        c.set(Health, { value: 50 });
        c.set(Health, { value: 1 });
        expect(world.query(Removed(IsLowHealth))).toHaveLength(0);
    });

    it('should track any truthiness transition with Changed', () => {
        const Changed = createChanged();
        const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);

        const a = world.spawn(Health({ value: 50 }));
        const b = world.spawn(Health({ value: 5 }));

        world.query(Changed(IsLowHealth)); // Drain matches from setup

        // Changes that do not flip the predicate are ignored.
        a.set(Health, { value: 60 });
        b.set(Health, { value: 1 });
        expect(world.query(Changed(IsLowHealth))).toHaveLength(0);

        // Both directions count.
        a.set(Health, { value: 10 });
        b.set(Health, { value: 90 });
        const entities = world.query(Changed(IsLowHealth));
        expect(entities).toHaveLength(2);
        expect(entities).toContain(a);
        expect(entities).toContain(b);
        expect(world.query(Changed(IsLowHealth))).toHaveLength(0);

        // A round trip still transitioned.
        a.set(Health, { value: 90 });
        a.set(Health, { value: 10 });
        expect(list(world.query(Changed(IsLowHealth)))).toEqual([a]);

        // Losing a dependency while true is a transition.
        a.remove(Health);
        expect(list(world.query(Changed(IsLowHealth)))).toEqual([a]);
    });

    it('should combine predicate Changed with trait Changed', () => {
        const Changed = createChanged();
        const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);

        const entity = world.spawn(Health({ value: 50 }), Position);
        world.query(Changed(IsLowHealth, Position));
        world.query(Or(Changed(IsLowHealth), Changed(Position)));

        entity.set(Health, { value: 10 });
        expect(world.query(Changed(IsLowHealth, Position))).toHaveLength(0);

        entity.set(Health, { value: 50 });
        entity.set(Position, { x: 1 });
        expect(list(world.query(Changed(IsLowHealth, Position)))).toEqual([entity]);

        world.query(Or(Changed(IsLowHealth), Changed(Position)));
        entity.set(Health, { value: 10 });
        expect(list(world.query(Or(Changed(IsLowHealth), Changed(Position))))).toEqual([entity]);
    });

    it('should compose with relation pairs', () => {
        const ChildOf = relation();
        const Added = createAdded();
        const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);

        const parentA = world.spawn();
        const parentB = world.spawn();
        const childA = world.spawn(ChildOf(parentA), Health({ value: 5 }));
        const childB = world.spawn(ChildOf(parentB), Health({ value: 5 }));
        const childC = world.spawn(ChildOf(parentA), Health({ value: 50 }));

        expect(list(world.query(IsLowHealth, ChildOf(parentA)))).toEqual([childA]);
        expect(list(world.query(Added(IsLowHealth), ChildOf(parentA)))).toEqual([childA]);

        childC.set(Health, { value: 1 });
        expect(world.query(IsLowHealth, ChildOf(parentA))).toHaveLength(2);
        expect(list(world.query(Added(IsLowHealth), ChildOf(parentA)))).toEqual([childC]);

        childB.remove(ChildOf(parentB));
        childB.add(ChildOf(parentA));
        expect(world.query(IsLowHealth, ChildOf(parentA))).toContain(childB);
        expect(world.query(Not(IsLowHealth), ChildOf(parentA))).toHaveLength(0);
    });

    it('should notify query subscriptions when the predicate flips', () => {
        const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);
        const entity = world.spawn(Health);

        const added: Entity[] = [];
        const removed: Entity[] = [];
        world.onQueryAdd([IsLowHealth], (e) => added.push(e));
        world.onQueryRemove([IsLowHealth], (e) => removed.push(e));

        entity.set(Health, { value: 5 });
        entity.set(Health, { value: 50 });

        expect(added).toEqual([entity]);
        expect(removed).toEqual([entity]);
    });

    describe('updateEach', () => {
        it('should re-evaluate dependencies changed in updateEach once iteration ends', () => {
            const fn = vi.fn(([health]) => health.value < 20);
            const IsLowHealth = createPredicate([Health], fn);

            const a = world.spawn(Health({ value: 50 }));
            const b = world.spawn(Health({ value: 50 }));

            expect(world.query(IsLowHealth)).toHaveLength(0);
            fn.mockClear();

            world.query(Health).updateEach(([health]) => {
                health.value = 10;
                // Not evaluated mid-iteration.
                expect(fn).not.toHaveBeenCalled();
                expect(world.query(IsLowHealth)).toHaveLength(0);
            });

            const entities = world.query(IsLowHealth);
            expect(entities).toHaveLength(2);
            expect(entities).toContain(a);
            expect(entities).toContain(b);
        });

        it('should defer sets made inside updateEach and use committed values', () => {
            const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);
            const entity = world.spawn(Health({ value: 50 }), Position);
            world.query(IsLowHealth);

            world.query(Health, Position).updateEach((_, e) => {
                // The committed state overwrites this set when the callback returns.
                e.set(Health, { value: 1 });
                expect(world.query(IsLowHealth)).toHaveLength(0);
            });

            expect(entity.get(Health)!.value).toBe(50);
            expect(world.query(IsLowHealth)).toHaveLength(0);

            world.query(Position).updateEach((_, e) => {
                e.set(Health, { value: 1 });
                expect(world.query(IsLowHealth)).toHaveLength(0);
            });

            expect(list(world.query(IsLowHealth))).toEqual([entity]);
        });

        it('should settle a predicate first used inside updateEach once iteration ends', () => {
            const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);
            const entity = world.spawn(Health({ value: 50 }), Position);

            world.query(Health, Position).updateEach((_, e) => {
                e.set(Health, { value: 1 });
                world.query(IsLowHealth);
            });

            expect(entity.get(Health)!.value).toBe(50);
            expect(world.query(IsLowHealth)).toHaveLength(0);
        });

        it('should defer re-evaluation for every change detection mode', () => {
            const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);
            const entity = world.spawn(Health({ value: 50 }));
            world.query(IsLowHealth);

            for (const changeDetection of ['auto', 'always', 'never'] as const) {
                world.query(Health).updateEach(
                    ([health]) => {
                        health.value = health.value < 20 ? 50 : 10;
                    },
                    { changeDetection }
                );

                const expected = entity.get(Health)!.value < 20 ? [entity] : [];
                expect(list(world.query(IsLowHealth))).toEqual(expected);
            }
        });

        it('should defer until the outermost updateEach ends', () => {
            const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);
            const entity = world.spawn(Health({ value: 50 }), Position);
            world.query(IsLowHealth);

            world.query(Position).updateEach(() => {
                world.query(Health).updateEach(([health]) => {
                    health.value = 5;
                });
                expect(world.query(IsLowHealth)).toHaveLength(0);
            });

            expect(list(world.query(IsLowHealth))).toEqual([entity]);
        });

        it('should resume re-evaluation when updateEach throws', () => {
            const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);
            const entity = world.spawn(Health({ value: 50 }));
            world.query(IsLowHealth);

            expect(() =>
                world.query(Health).updateEach((_, e) => {
                    e.set(Health, { value: 5 });
                    throw new Error('boom');
                })
            ).toThrow('boom');

            expect(list(world.query(IsLowHealth))).toEqual([entity]);

            entity.set(Health, { value: 50 });
            expect(world.query(IsLowHealth)).toHaveLength(0);
        });

        it('should track predicate transitions from updateEach', () => {
            const Added = createAdded();
            const Removed = createRemoved();
            const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);
            const entity = world.spawn(Health({ value: 50 }));

            world.query(Added(IsLowHealth));
            world.query(Removed(IsLowHealth));

            world.query(Health).updateEach(([health]) => {
                health.value = 5;
            });
            expect(list(world.query(Added(IsLowHealth)))).toEqual([entity]);

            world.query(Health).updateEach(([health]) => {
                health.value = 50;
            });
            expect(list(world.query(Removed(IsLowHealth)))).toEqual([entity]);
        });
    });

    it('should work independently across worlds and after reset', () => {
        const IsLowHealth = createPredicate([Health], ([health]) => health.value < 20);
        const otherWorld = createWorld();

        const a = world.spawn(Health({ value: 5 }));
        const b = otherWorld.spawn(Health({ value: 50 }));

        expect(list(world.query(IsLowHealth))).toEqual([a]);
        expect(otherWorld.query(IsLowHealth)).toHaveLength(0);

        b.set(Health, { value: 1 });
        expect(list(otherWorld.query(IsLowHealth))).toEqual([b]);
        expect(list(world.query(IsLowHealth))).toEqual([a]);

        world.reset();
        const c = world.spawn(Health({ value: 5 }));
        expect(list(world.query(IsLowHealth))).toEqual([c]);

        otherWorld.destroy();
    });
});
