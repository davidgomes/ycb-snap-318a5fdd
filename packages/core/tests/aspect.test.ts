import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createAdded, createAspect, createChanged, createRemoved, createWorld, Not, Or, relation, trait } from '../src';

const Position = trait({ x: 0, y: 0 });
const Health = trait({ hp: 10 });
const Name = trait({ name: 'entity' });
const IsPlayer = trait();
const IsEnemy = trait();

describe('Aspect', () => {
    const world = createWorld();

    beforeEach(() => {
        world.reset();
    });

    it('exposes id, traits, and schema and flattens nested aspects', () => {
        const Movement = createAspect(Position, Name);
        const Unit = createAspect(Movement, Health, IsPlayer);

        expect(typeof Unit.id).toBe('number');
        expect(Unit.traits).toEqual([Position, Name, Health, IsPlayer]);
        expect(Unit.schema).toEqual({ x: 0, y: 0, name: 'entity', hp: 10 });
        expect(createAspect(Position, Health)).not.toBe(createAspect(Position, Health));
        expect(createAspect(Position, Health).id).not.toBe(createAspect(Position, Health).id);
    });

    it('rejects overlapping fields, relations, AoS traits, and fewer than two constituents', () => {
        const Velocity = trait({ x: 0, y: 1 });
        expect(() => createAspect(Position, Velocity)).toThrow(/overlapping|multiple constituents/i);
        expect(() => createAspect(relation(), Position)).toThrow(/relation/i);
        expect(() => createAspect(trait(() => ({ a: 1 })), Position)).toThrow(/AoS/i);
        expect(() => createAspect(Position)).toThrow(/at least two/i);
    });

    it('has, get, set, add, and remove operate on the whole group', () => {
        const Unit = createAspect(Position, Health, IsPlayer);
        const entity = world.spawn();

        expect(entity.has(Unit)).toBe(false);
        expect(entity.get(Unit)).toBeUndefined();

        entity.add(Position({ x: 4, y: 5 }));
        entity.add(Unit({ hp: 3 }));

        expect(entity.has(Position)).toBe(true);
        expect(entity.has(Health)).toBe(true);
        expect(entity.has(IsPlayer)).toBe(true);
        expect(entity.get(Position)).toMatchObject({ x: 4, y: 5 });
        expect(entity.get(Unit)).toEqual({ x: 4, y: 5, hp: 3 });

        entity.set(Unit, { x: 1, hp: 8 });
        expect(entity.get(Position)).toMatchObject({ x: 1, y: 5 });
        expect(entity.get(Health)).toMatchObject({ hp: 8 });

        entity.remove(Unit);
        expect(entity.has(Position)).toBe(false);
        expect(entity.has(Health)).toBe(false);
        expect(entity.has(IsPlayer)).toBe(false);
        expect(entity.get(Unit)).toBeUndefined();
    });

    it('add skips constituents the entity already has', () => {
        const Unit = createAspect(Position, Health);
        const entity = world.spawn(Position({ x: 9, y: 8 }));

        entity.add(Unit({ x: 1, y: 2, hp: 4 }));

        expect(entity.get(Position)).toMatchObject({ x: 9, y: 8 });
        expect(entity.get(Health)).toMatchObject({ hp: 4 });
    });

    it('requires every constituent in a query and merges read/update', () => {
        const Unit = createAspect(Position, Health);
        const full = world.spawn(Unit({ x: 2, y: 3, hp: 6 }));
        world.spawn(Position);
        world.spawn(Health);

        const results = world.query(Unit);
        expect([...results]).toEqual([full]);

        const seen: Record<string, unknown>[] = [];
        results.readEach((state) => {
            seen.push({ ...state[0] });
        });
        expect(seen).toEqual([{ x: 2, y: 3, hp: 6 }]);

        results.updateEach(([unit]) => {
            const data = unit as { x: number; y: number; hp: number };
            data.x += 1;
            data.hp += 1;
        });

        expect(full.get(Position)).toMatchObject({ x: 3, y: 3 });
        expect(full.get(Health)).toMatchObject({ hp: 7 });
    });

    it('composes with Not, Or, and tracking modifiers', () => {
        const Unit = createAspect(Position, Health);
        const withBoth = world.spawn(Unit);
        const positionOnly = world.spawn(Position);
        const enemy = world.spawn(IsEnemy);
        world.spawn();

        const missing = world.query(Not(Unit));
        expect(missing).not.toContain(withBoth);
        expect(missing).toContain(positionOnly);
        expect(missing).toContain(enemy);

        expect([...world.query(Or(Unit, IsEnemy))].sort()).toEqual([withBoth, enemy].sort());
        expect(world.query(Or(Unit, IsEnemy))).not.toContain(positionOnly);

        const Added = createAdded();
        const partial = world.spawn(Position);
        partial.add(Health);
        expect(world.query(Added(Unit))).toContain(partial);
        expect(world.query(Added(Unit))).not.toContain(withBoth);

        const Changed = createChanged();
        withBoth.set(Position, { x: 5 });
        expect(world.query(Changed(Unit))).toContain(withBoth);
        expect(world.query(Changed(Unit))).not.toContain(positionOnly);

        const present = world.spawn(Unit);
        const Removed = createRemoved();
        present.remove(Health);
        expect([...world.query(Removed(Unit))]).toContain(present);
        expect(present.has(Unit)).toBe(false);
    });

    it('fires onAdd, onRemove, and onChange on completeness transitions', () => {
        const Unit = createAspect(Position, Health, IsPlayer);
        const onAdd = vi.fn();
        const onRemove = vi.fn();
        const onChange = vi.fn();

        world.onAdd(Unit, onAdd);
        world.onRemove(Unit, onRemove);
        world.onChange(Unit, onChange);

        const entity = world.spawn(Position, Health);
        expect(onAdd).not.toHaveBeenCalled();

        entity.add(IsPlayer);
        expect(onAdd).toHaveBeenCalledTimes(1);
        expect(onAdd).toHaveBeenCalledWith(entity);

        entity.set(Position, { x: 1 });
        expect(onChange).toHaveBeenCalledTimes(1);

        entity.remove(Health);
        expect(onRemove).toHaveBeenCalledTimes(1);
        expect(onRemove).toHaveBeenCalledWith(entity);

        entity.set(Position, { x: 2 });
        expect(onChange).toHaveBeenCalledTimes(1);

        entity.remove(Position);
        expect(onRemove).toHaveBeenCalledTimes(1);

        entity.add(Health);
        expect(onAdd).toHaveBeenCalledTimes(1);
        entity.add(Position);
        expect(onAdd).toHaveBeenCalledTimes(2);
    });
});
