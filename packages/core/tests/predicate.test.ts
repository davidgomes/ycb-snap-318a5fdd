import { beforeEach, describe, expect, it } from 'vitest';
import {
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

const Position = trait({ x: 0, y: 0 });
const Velocity = trait({ x: 0, y: 0 });
const Health = trait({ value: 100 });
const IsActive = trait();
const ChildOf = relation();

describe('createPredicate', () => {
    const world = createWorld();
    world.init();

    beforeEach(() => {
        world.reset();
    });

    it('filters entities by trait values', () => {
        const isFast = createPredicate([Velocity], ([vel]) => vel.x > 10);

        const slow = world.spawn(Velocity({ x: 1, y: 0 }));
        const fast = world.spawn(Velocity({ x: 20, y: 0 }));

        expect(world.query(isFast)).toContain(fast);
        expect(world.query(isFast)).not.toContain(slow);
    });

    it('returns distinct instances on each call', () => {
        const a = createPredicate([Velocity], ([vel]) => vel.x > 0);
        const b = createPredicate([Velocity], ([vel]) => vel.x > 0);

        expect(a).not.toBe(b);
        expect(a.id).not.toBe(b.id);
    });

    it('throws when tags are used as dependencies', () => {
        expect(() => createPredicate([IsActive], () => true)).toThrow(
            'Tags cannot be used as predicate dependencies'
        );
    });

    it('throws when relations are used as dependencies', () => {
        expect(() => createPredicate([ChildOf as never], () => true)).toThrow(
            'Relations cannot be used as predicate dependencies'
        );
    });

    it('re-evaluates when a dependency is set', () => {
        const isHealthy = createPredicate([Health], ([health]) => health.value > 50);
        const entity = world.spawn(Health({ value: 30 }));

        expect(world.query(isHealthy)).toHaveLength(0);

        entity.set(Health, { value: 80 });
        expect(world.query(isHealthy)).toContain(entity);
    });

    it('re-evaluates when a dependency is added', () => {
        const needsBoth = createPredicate([Position, Velocity], ([pos, vel]) => pos.x + vel.x > 10);
        const entity = world.spawn(Position({ x: 8, y: 0 }));

        expect(world.query(needsBoth)).toHaveLength(0);

        entity.add(Velocity({ x: 5, y: 0 }));
        expect(world.query(needsBoth)).toContain(entity);
    });

    it('does not add predicate dependencies to updateEach callback tuple', () => {
        const isFast = createPredicate([Velocity], ([vel]) => vel.x > 10);
        const entity = world.spawn(Velocity({ x: 20, y: 0 }), Position({ x: 1, y: 2 }));

        world.query(isFast, Position).updateEach(([position]) => {
            expect(position).toEqual({ x: 1, y: 2 });
        });
    });

    it('supports Not(predicate) for missing dependencies or false predicate', () => {
        const isFast = createPredicate([Velocity], ([vel]) => vel.x > 10);

        const noVelocity = world.spawn();
        const slow = world.spawn(Velocity({ x: 1, y: 0 }));
        const fast = world.spawn(Velocity({ x: 20, y: 0 }));

        const entities = world.query(Not(isFast));
        expect(entities).toContain(noVelocity);
        expect(entities).toContain(slow);
        expect(entities).not.toContain(fast);
    });

    it('supports Or with predicates', () => {
        const isFast = createPredicate([Velocity], ([vel]) => vel.x > 10);

        const tagged = world.spawn(IsActive);
        const slow = world.spawn(Velocity({ x: 1, y: 0 }));
        const fast = world.spawn(Velocity({ x: 20, y: 0 }));

        const entities = world.query(Or(isFast, IsActive));
        expect(entities).toContain(tagged);
        expect(entities).toContain(fast);
        expect(entities).not.toContain(slow);
    });

    it('supports Added(predicate)', () => {
        const Added = createAdded();
        const isFast = createPredicate([Velocity], ([vel]) => vel.x > 10);
        const entity = world.spawn(Velocity({ x: 1, y: 0 }));

        expect(world.query(Added(isFast))).toHaveLength(0);

        entity.set(Velocity, { x: 20, y: 0 });
        expect(world.query(Added(isFast))).toContain(entity);

        expect(world.query(Added(isFast))).toHaveLength(0);
    });

    it('supports Removed(predicate)', () => {
        const Removed = createRemoved();
        const isFast = createPredicate([Velocity], ([vel]) => vel.x > 10);
        const entity = world.spawn(Velocity({ x: 20, y: 0 }));

        expect(world.query(Removed(isFast))).toHaveLength(0);

        entity.set(Velocity, { x: 1, y: 0 });
        expect(world.query(Removed(isFast))).toContain(entity);

        expect(world.query(Removed(isFast))).toHaveLength(0);
    });

    it('supports Changed(predicate) on truthiness transitions', () => {
        const Changed = createChanged();
        const isFast = createPredicate([Velocity], ([vel]) => vel.x > 10);
        const entity = world.spawn(Velocity({ x: 1, y: 0 }));

        expect(world.query(Changed(isFast))).toHaveLength(0);

        entity.set(Velocity, { x: 20, y: 0 });
        expect(world.query(Changed(isFast))).toContain(entity);
        expect(world.query(Changed(isFast))).toHaveLength(0);

        entity.set(Velocity, { x: 1, y: 0 });
        expect(world.query(Changed(isFast))).toContain(entity);
    });

    it('defers predicate re-evaluation during updateEach until iteration ends', () => {
        const isFast = createPredicate([Velocity], ([vel]) => vel.x > 10);
        const a = world.spawn(Velocity({ x: 1, y: 0 }));
        const b = world.spawn(Velocity({ x: 1, y: 0 }));

        world.query(Velocity).updateEach(
            ([vel], entity) => {
                vel.x = 20;
                expect(world.query(isFast)).not.toContain(entity);
            },
            { changeDetection: 'always' }
        );

        expect(world.query(isFast)).toContain(a);
        expect(world.query(isFast)).toContain(b);
    });

    it('composes predicates with relation pairs', () => {
        const parent = world.spawn();
        const child = world.spawn(Velocity({ x: 20, y: 0 }), ChildOf(parent));
        const other = world.spawn(Velocity({ x: 20, y: 0 }));

        const isFast = createPredicate([Velocity], ([vel]) => vel.x > 10);

        expect(world.query(isFast, ChildOf(parent))).toContain(child);
        expect(world.query(isFast, ChildOf(parent))).not.toContain(other);
    });
});
