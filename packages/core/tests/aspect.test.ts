import { beforeEach, describe, expect, it } from 'vitest';
import {
    createAdded,
    createAspect,
    createChanged,
    createRemoved,
    createWorld,
    Not,
    relation,
    trait,
} from '../src';

const Position = trait({ x: 0, y: 0 });
const Velocity = trait({ vx: 0, vy: 0 });
const Mass = trait({ mass: 1 });
const IsActive = trait();
const Meta = trait(() => ({ label: 'none' }));

describe('Aspect', () => {
    const world = createWorld();

    beforeEach(() => {
        world.reset();
    });

    it('creates distinct aspects with id, traits and schema', () => {
        const a = createAspect(Position, Velocity);
        const b = createAspect(Position, Velocity);
        expect(a).not.toBe(b);
        expect(a.id).not.toBe(b.id);
        expect(a.traits).toEqual([Position, Velocity]);
        expect(a.schema).toEqual({ x: 0, y: 0, vx: 0, vy: 0 });
    });

    it('throws on overlapping fields and relations', () => {
        const Other = trait({ x: 0 });
        expect(() => createAspect(Position, Other)).toThrow();
        const ChildOf = relation();
        expect(() => createAspect(Position, ChildOf as any)).toThrow();
    });

    it('accepts tags and flattens nested aspects', () => {
        const Motion = createAspect(Position, Velocity);
        const Body = createAspect(Motion, Mass, IsActive);
        expect(Body.traits).toEqual([Position, Velocity, Mass, IsActive]);
    });

    it('supports has, get, set, add and remove', () => {
        const Motion = createAspect(Position, Velocity, IsActive);
        const e = world.spawn(Position({ x: 5 }));

        expect(e.has(Motion)).toBe(false);
        expect(e.get(Motion)).toBeUndefined();

        e.add(Motion({ x: 99, vx: 2 }));
        expect(e.has(Motion)).toBe(true);
        // Existing constituent is untouched.
        expect(e.get(Motion)).toEqual({ x: 5, y: 0, vx: 2, vy: 0 });

        e.set(Motion, { y: 3, vy: 4 });
        expect(e.get(Position)).toEqual({ x: 5, y: 3 });
        expect(e.get(Velocity)).toEqual({ vx: 2, vy: 4 });

        e.set(Motion, (prev) => ({ x: prev.x + 1 }));
        expect(e.get(Position)!.x).toBe(6);

        e.remove(Motion);
        expect(e.has(Position)).toBe(false);
        expect(e.has(Velocity)).toBe(false);
        expect(e.has(IsActive)).toBe(false);
    });

    it('works with AoS constituents', () => {
        const Labeled = createAspect(Position, Meta);
        const e = world.spawn(Labeled({ label: 'hi', x: 1 }));
        expect(e.get(Labeled)).toEqual({ x: 1, y: 0, label: 'hi' });
        e.set(Labeled, { label: 'yo' });
        expect(e.get(Meta)!.label).toBe('yo');
    });

    it('queries with readEach and updateEach', () => {
        const Motion = createAspect(Position, Velocity);
        const a = world.spawn(Position, Velocity({ vx: 1, vy: 2 }));
        world.spawn(Position);

        const result = world.query(Motion, Mass);
        expect(result.length).toBe(0);

        a.add(Mass);
        const entities = world.query(Motion);
        expect([...entities]).toEqual([a]);

        entities.updateEach(([m]) => {
            m.x += m.vx;
            m.y += m.vy;
        });
        expect(a.get(Position)).toEqual({ x: 1, y: 2 });

        world.query(Motion, Mass).readEach(([m, mass]) => {
            expect(m).toEqual({ x: 1, y: 2, vx: 1, vy: 2 });
            expect(mass.mass).toBe(1);
        });
    });

    it('Not matches entities missing at least one constituent', () => {
        const Motion = createAspect(Position, Velocity);
        const a = world.spawn(Position, Velocity);
        const b = world.spawn(Position);
        const c = world.spawn(Mass);

        const result = world.query(Mass, Not(Motion));
        expect([...result]).toEqual([c]);
        const result2 = world.query(Position, Not(Motion));
        expect([...result2]).toEqual([b]);
        a.remove(Velocity);
        expect([...world.query(Position, Not(Motion))]).toContain(a);
    });

    it('Added, Removed and Changed track aspect transitions', () => {
        const Motion = createAspect(Position, Velocity);
        const Added = createAdded();
        const Removed = createRemoved();
        const Changed = createChanged();

        const e = world.spawn(Position);
        expect([...world.query(Added(Motion))]).toEqual([]);

        e.add(Velocity);
        expect([...world.query(Added(Motion))]).toEqual([e]);
        expect([...world.query(Added(Motion))]).toEqual([]);

        expect([...world.query(Changed(Motion))]).toEqual([]);
        e.set(Velocity, { vx: 1 });
        const changed = world.query(Changed(Motion));
        expect([...changed]).toEqual([e]);
        e.set(Position, { x: 1 });
        world.query(Changed(Motion)).readEach(([m]) => {
            expect(m).toEqual({ x: 1, y: 0, vx: 1, vy: 0 });
        });

        expect([...world.query(Removed(Motion))]).toEqual([]);
        e.remove(Position);
        expect([...world.query(Removed(Motion))]).toEqual([e]);
    });

    it('fires onAdd, onRemove and onChange hooks', () => {
        const Motion = createAspect(Position, Velocity);
        const events: string[] = [];
        world.onAdd(Motion, () => events.push('add'));
        world.onRemove(Motion, () => events.push('remove'));
        world.onChange(Motion, () => events.push('change'));

        const e = world.spawn(Position);
        e.set(Position, { x: 1 });
        expect(events).toEqual([]);

        e.add(Velocity);
        expect(events).toEqual(['add']);

        e.set(Motion, { x: 2, vx: 3 });
        expect(events).toEqual(['add', 'change', 'change']);

        e.remove(Velocity);
        expect(events).toEqual(['add', 'change', 'change', 'remove']);
    });

    it('backfills entities that existed before the aspect was created', () => {
        const e = world.spawn(Position, Mass);
        const Late = createAspect(Position, Mass);
        expect([...world.query(Late)]).toEqual([e]);
        expect([...world.query(Position, Not(Late))]).toEqual([]);
    });
});
