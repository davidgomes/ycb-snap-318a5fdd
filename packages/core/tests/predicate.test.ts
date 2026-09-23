import { beforeEach, describe, expect, it } from 'vitest';
import {
    $internal,
    createAdded,
    createChanged,
    createPredicate,
    createRemoved,
    createWorld,
    Not,
    Or,
    relation,
    trait,
} from '../src';

const Velocity = trait({ x: 0, y: 0 });
const Position = trait({ x: 0, y: 0 });
const IsNpc = trait();
const ChildOf = relation();

describe('createPredicate', () => {
    const world = createWorld();
    world.init();

    beforeEach(() => {
        world.reset();
    });

    it('returns a distinct instance on each call', () => {
        const isFast = createPredicate([Velocity], ([velocity]) => velocity.x > 10);
        const alsoFast = createPredicate([Velocity], ([velocity]) => velocity.x > 10);
        expect(isFast).not.toBe(alsoFast);

        const entity = world.spawn(Velocity({ x: 20, y: 0 }));
        expect(world.query(isFast)).toContain(entity);
        expect(world.query(alsoFast)).toContain(entity);
        expect(world[$internal].queriesHashMap.size).toBe(2);
    });

    it('throws when a dependency is a tag or a relation', () => {
        expect(() => createPredicate([IsNpc], () => true)).toThrow(/tag/);
        expect(() => createPredicate([ChildOf as never], () => true)).toThrow(/relation/);
        const Likes = relation({ store: { amount: 0 } });
        expect(() => createPredicate([Likes as never], () => true)).toThrow(/relation/);
    });

    it('matches on dependency values in order and re-evaluates on set and add', () => {
        const isAhead = createPredicate([Position, Velocity], ([position, velocity]) => {
            return position.x + velocity.x > 10;
        });

        const entity = world.spawn();
        expect(world.query(isAhead)).not.toContain(entity);

        entity.add(Position({ x: 4, y: 0 }), Velocity({ x: 3, y: 0 }));
        expect(world.query(isAhead)).not.toContain(entity);

        entity.set(Velocity, { x: 8, y: 0 });
        expect(world.query(isAhead)).toContain(entity);

        entity.set(Position, { x: 0, y: 0 });
        expect(world.query(isAhead)).not.toContain(entity);
    });

    it('adds no data to the callback tuple', () => {
        const isFast = createPredicate([Velocity], ([velocity]) => velocity.x > 10);
        const parent = world.spawn();
        world.spawn(Position({ x: 1, y: 2 }), Velocity({ x: 12, y: 0 }), ChildOf(parent));

        world.query(Position, isFast, ChildOf(parent)).updateEach((state) => {
            expect(state).toHaveLength(1);
            expect(state[0].x).toBe(1);
        });
    });

    it('Not matches entities missing a dependency or failing the predicate', () => {
        const isFast = createPredicate([Velocity], ([velocity]) => velocity.x > 10);
        const bare = world.spawn();
        const slow = world.spawn(Velocity({ x: 1, y: 0 }));
        const fast = world.spawn(Velocity({ x: 20, y: 0 }));

        let entities = world.query(Not(isFast));
        expect(entities).toContain(bare);
        expect(entities).toContain(slow);
        expect(entities).not.toContain(fast);

        fast.set(Velocity, { x: 0, y: 0 });
        entities = world.query(Not(isFast));
        expect(entities).toContain(fast);

        slow.set(Velocity, { x: 30, y: 0 });
        entities = world.query(Not(isFast));
        expect(entities).not.toContain(slow);
    });

    it('Or accepts predicates and traits', () => {
        const isFast = createPredicate([Velocity], ([velocity]) => velocity.x > 10);
        const isTall = createPredicate([Position], ([position]) => position.y > 5);

        const fast = world.spawn(Velocity({ x: 12, y: 0 }));
        const tall = world.spawn(Position({ x: 0, y: 9 }));
        const npc = world.spawn(IsNpc);
        const neither = world.spawn(Velocity({ x: 0, y: 0 }), Position({ x: 0, y: 0 }));

        const byPredicate = world.query(Or(isFast, isTall));
        expect(byPredicate).toContain(fast);
        expect(byPredicate).toContain(tall);
        expect(byPredicate).not.toContain(npc);
        expect(byPredicate).not.toContain(neither);

        const mixed = world.query(Or(isFast, IsNpc));
        expect(mixed).toContain(fast);
        expect(mixed).toContain(npc);
        expect(mixed).not.toContain(tall);
        expect(mixed).not.toContain(neither);
    });

    it('tracks Added, Removed, and Changed against the previous result', () => {
        const isFast = createPredicate([Velocity], ([velocity]) => velocity.x > 10);
        const Added = createAdded();
        const Removed = createRemoved();
        const Changed = createChanged();

        const entity = world.spawn(Velocity({ x: 0, y: 0 }));

        expect(world.query(Added(isFast))).toHaveLength(0);
        expect(world.query(Removed(isFast))).toHaveLength(0);
        expect(world.query(Changed(isFast))).toHaveLength(0);

        entity.set(Velocity, { x: 20, y: 0 });
        expect(world.query(Added(isFast))).toContain(entity);
        expect(world.query(Added(isFast))).toHaveLength(0);
        expect(world.query(Changed(isFast))).toContain(entity);
        expect(world.query(Changed(isFast))).toHaveLength(0);

        entity.set(Velocity, { x: 25, y: 0 });
        expect(world.query(Added(isFast))).toHaveLength(0);
        expect(world.query(Changed(isFast))).toHaveLength(0);
        expect(world.query(Removed(isFast))).toHaveLength(0);

        entity.set(Velocity, { x: 0, y: 0 });
        expect(world.query(Removed(isFast))).toContain(entity);
        expect(world.query(Removed(isFast))).toHaveLength(0);
        expect(world.query(Changed(isFast))).toContain(entity);
        expect(world.query(Added(isFast))).toHaveLength(0);

        entity.remove(Velocity);
        entity.add(Velocity({ x: 40, y: 0 }));
        expect(world.query(Added(isFast))).toContain(entity);
    });

    it('includes entities that already satisfy Added on the first result', () => {
        const isFast = createPredicate([Velocity], ([velocity]) => velocity.x > 10);
        const Added = createAdded();
        const entity = world.spawn(Velocity({ x: 40, y: 0 }));

        expect(world.query(Added(isFast))).toContain(entity);
        expect(world.query(Added(isFast))).toHaveLength(0);
    });

    it('defers predicate re-evaluation until updateEach finishes', () => {
        const isFast = createPredicate([Velocity], ([velocity]) => velocity.x > 10);
        const entity = world.spawn(Position({ x: 0, y: 0 }), Velocity({ x: 0, y: 0 }));
        expect(world.query(isFast)).toHaveLength(0);

        world.query(Position, Velocity).updateEach(([position, velocity]) => {
            velocity.x = 50;
            position.x = 1;
            expect(world.query(isFast)).toHaveLength(0);
        });

        expect(world.query(isFast)).toContain(entity);

        const other = world.spawn(Position);
        world.query(Position).updateEach((state, current) => {
            if (current !== other) return;
            current.add(Velocity({ x: 80, y: 0 }));
            expect(world.query(isFast)).not.toContain(other);
        });
        expect(world.query(isFast)).toContain(other);
    });

    it('composes with relation pairs', () => {
        const isFast = createPredicate([Velocity], ([velocity]) => velocity.x > 10);
        const parent = world.spawn();
        const child = world.spawn(ChildOf(parent), Velocity({ x: 15, y: 0 }));
        const slowChild = world.spawn(ChildOf(parent), Velocity({ x: 1, y: 0 }));
        const fastStranger = world.spawn(Velocity({ x: 15, y: 0 }));

        const entities = world.query(ChildOf(parent), isFast);
        expect(entities).toContain(child);
        expect(entities).not.toContain(slowChild);
        expect(entities).not.toContain(fastStranger);

        slowChild.set(Velocity, { x: 30, y: 0 });
        expect(world.query(ChildOf(parent), isFast)).toContain(slowChild);
    });
});
