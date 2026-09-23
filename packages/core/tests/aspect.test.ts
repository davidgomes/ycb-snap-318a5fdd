import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
    createAdded,
    createAspect,
    createChanged,
    createRemoved,
    createWorld,
    Not,
    Or,
    relation,
    trait,
} from '../src';

const Position = trait({ x: 0, y: 0 });
const Velocity = trait({ x: 1, y: 2 });
const Health = trait({ hp: 10 });
const Shield = trait({ sp: 5 });
const Name = trait({ name: 'anon' });
const IsDead = trait();
const IsFrozen = trait();

describe('Aspect', () => {
    const world = createWorld();

    beforeEach(() => {
        world.reset();
    });

    it('exposes id, traits, and schema and returns a distinct instance each call', () => {
        const Combat = createAspect(Health, Shield, IsDead);
        const CombatAgain = createAspect(Health, Shield, IsDead);

        expect(Combat).not.toBe(CombatAgain);
        expect(Combat.id).not.toBe(CombatAgain.id);
        expect(Combat.traits).toEqual([Health, Shield, IsDead]);
        expect(Combat.schema).toMatchObject({ hp: 10, sp: 5 });
        expect(typeof Combat).toBe('function');
    });

    it('throws when fewer than two traits remain after flattening', () => {
        // @ts-expect-error createAspect requires two or more traits
        expect(() => createAspect()).toThrow(/two or more/);
        // @ts-expect-error createAspect requires two or more traits
        expect(() => createAspect(Health)).toThrow(/two or more/);
    });

    it('throws when constituent fields overlap', () => {
        expect(() => createAspect(Position, Velocity)).toThrow(/"x"/);
    });

    it('throws when a constituent is a relation', () => {
        const ChildOf = relation();
        const Contains = relation({ store: { amount: 0 } });
        // @ts-expect-error aspects cannot contain relations
        expect(() => createAspect(Health, ChildOf)).toThrow(/relation/i);
        // @ts-expect-error aspects cannot contain relations
        expect(() => createAspect(Health, Contains)).toThrow(/relation/i);
    });

    it('accepts tag traits and flattens nested aspects', () => {
        const Movement = createAspect(Position, Name);
        const Body = createAspect(Movement, Health, IsDead);

        expect(Body.traits).toEqual([Position, Name, Health, IsDead]);
        expect(Body.schema).toMatchObject({ x: 0, y: 0, name: 'anon', hp: 10 });
        expect(createAspect(Movement).traits).toEqual([Position, Name]);
        expect(createAspect(Movement).id).not.toBe(Movement.id);
    });

    it('dedupes a trait shared through nesting', () => {
        const Vital = createAspect(Health, IsDead);
        const Body = createAspect(Vital, Health, Shield);
        expect(Body.traits).toEqual([Health, IsDead, Shield]);
    });

    it('has, get, set, add, and remove operate on every constituent', () => {
        const Combat = createAspect(Health, Shield, IsDead);
        const entity = world.spawn();

        expect(entity.has(Combat)).toBe(false);
        expect(entity.get(Combat)).toBeUndefined();

        entity.add(Health({ hp: 4 }));
        entity.add(Combat({ hp: 99, sp: 2 }));

        expect(entity.has(Health)).toBe(true);
        expect(entity.has(Shield)).toBe(true);
        expect(entity.has(IsDead)).toBe(true);
        expect(entity.has(Combat)).toBe(true);
        // Existing constituents keep their values; only missing ones are initialized.
        expect(entity.get(Health)).toMatchObject({ hp: 4 });
        expect(entity.get(Shield)).toMatchObject({ sp: 2 });
        expect(entity.get(Combat)).toMatchObject({ hp: 4, sp: 2 });

        const onHealth = vi.fn();
        const onShield = vi.fn();
        world.onChange(Health, onHealth);
        world.onChange(Shield, onShield);

        entity.set(Combat, { hp: 7 });
        expect(entity.get(Health)).toMatchObject({ hp: 7 });
        expect(entity.get(Shield)).toMatchObject({ sp: 2 });
        expect(onHealth).toHaveBeenCalledTimes(1);
        expect(onShield).not.toHaveBeenCalled();

        entity.set(Combat, (prev) => ({ hp: prev.hp + 1, sp: prev.sp + 3 }));
        expect(entity.get(Combat)).toMatchObject({ hp: 8, sp: 5 });

        entity.remove(Combat);
        expect(entity.has(Health)).toBe(false);
        expect(entity.has(Shield)).toBe(false);
        expect(entity.has(IsDead)).toBe(false);
        expect(entity.has(Combat)).toBe(false);
        expect(entity.get(Combat)).toBeUndefined();
    });

    it('merges AoS fields into the aspect record and writes them back', () => {
        const Vec = trait(() => ({ x: 0, y: 0 }));
        const Label = trait({ label: '' });
        const Thing = createAspect(Vec, Label);
        const entity = world.spawn(Thing({ x: 1, y: 2, label: 'a' }));

        expect(entity.get(Thing)).toMatchObject({ x: 1, y: 2, label: 'a' });
        entity.set(Thing, { x: 8, label: 'b' });
        expect(entity.get(Vec)).toMatchObject({ x: 8, y: 2 });
        expect(entity.get(Label)).toMatchObject({ label: 'b' });
    });

    it('queries require every constituent and read/update a merged record', () => {
        const Combat = createAspect(Health, Shield, IsDead);
        const ready = world.spawn(Combat({ hp: 3, sp: 4 }), Name({ name: 'ada' }));
        const partial = world.spawn(Health, Shield, Name({ name: 'skip' }));

        const matched = world.query(Combat, Name);
        expect(matched).toContain(ready);
        expect(matched).not.toContain(partial);

        const seen: { hp: number; sp: number; name: string }[] = [];
        matched.readEach(([combat, name]) => {
            seen.push({ hp: combat.hp, sp: combat.sp, name: name.name });
            combat.hp = 100;
        });
        expect(seen).toEqual([{ hp: 3, sp: 4, name: 'ada' }]);
        expect(ready.get(Health)!.hp).toBe(3);

        world.query(Combat, Name).updateEach(([combat, name]) => {
            combat.hp += 1;
            combat.sp += 1;
            name.name = 'updated';
        });
        expect(ready.get(Health)).toMatchObject({ hp: 4 });
        expect(ready.get(Shield)).toMatchObject({ sp: 5 });
        expect(ready.get(Name)).toMatchObject({ name: 'updated' });
    });

    it('composes with Not, Or, Added, Removed, and Changed', () => {
        const Combat = createAspect(Health, Shield, IsDead);
        const complete = world.spawn(Combat({ hp: 1, sp: 1 }));
        const partial = world.spawn(Health);
        const named = world.spawn(Name);
        const empty = world.spawn();

        const excluded = world.query(Not(Combat));
        expect(excluded).not.toContain(complete);
        expect(excluded).toContain(partial);
        expect(excluded).toContain(named);
        expect(excluded).toContain(empty);

        const either = world.query(Or(Combat, Name));
        expect(either).toContain(complete);
        expect(either).toContain(named);
        expect(either).not.toContain(partial);

        const withFilter = world.query(Combat, Not(IsFrozen));
        expect(withFilter).toContain(complete);
        complete.add(IsFrozen);
        expect(world.query(Combat, Not(IsFrozen))).not.toContain(complete);
        complete.remove(IsFrozen);

        const Added = createAdded();
        const late = world.spawn(Health, Shield);
        expect(world.query(Added(Combat))).toHaveLength(0);
        late.add(IsDead);
        expect(world.query(Added(Combat))).toContain(late);

        const Removed = createRemoved();
        const going = world.spawn(Health, Shield, IsDead);
        expect(world.query(Removed(Combat))).toHaveLength(0);
        going.remove(Shield);
        expect(going.has(Health)).toBe(true);
        expect(going.has(IsDead)).toBe(true);
        expect(world.query(Removed(Combat))).toContain(going);

        const Changed = createChanged();
        const edited = world.spawn(Combat);
        const half = world.spawn(Health);
        expect(world.query(Changed(Combat))).toHaveLength(0);
        half.set(Health, { hp: 2 });
        expect(world.query(Changed(Combat))).not.toContain(half);
        edited.set(Shield, { sp: 9 });
        expect(world.query(Changed(Combat))).toContain(edited);
        expect(world.query(Changed(Combat))).not.toContain(half);
    });

    it('Added ignores entities that were already complete before the cursor', () => {
        const Combat = createAspect(Health, Shield);
        world.spawn(Health, Shield);
        const Added = createAdded();
        expect(world.query(Added(Combat))).toHaveLength(0);
    });

    it('backfills entities that were complete before the aspect existed', () => {
        const entity = world.spawn(Health({ hp: 6 }), Shield({ sp: 7 }));
        const Combat = createAspect(Health, Shield);
        expect(entity.has(Combat)).toBe(true);
        expect(world.query(Combat)).toContain(entity);
        expect(entity.get(Combat)).toMatchObject({ hp: 6, sp: 7 });
    });

    it('fires onAdd, onRemove, and onChange on completeness transitions', () => {
        const Combat = createAspect(Health, Shield, IsDead);
        const added = vi.fn();
        const removed = vi.fn();
        const changed = vi.fn();

        world.onAdd(Combat, (entity) => {
            expect(entity.has(Combat)).toBe(true);
            added(entity, entity.get(Combat));
        });
        world.onRemove(Combat, (entity) => {
            expect(entity.has(Combat)).toBe(true);
            expect(entity.get(Combat)?.hp).toBe(3);
            removed(entity);
        });
        world.onChange(Combat, changed);

        const entity = world.spawn();
        entity.add(Health({ hp: 3 }));
        entity.add(Shield);
        expect(added).not.toHaveBeenCalled();
        entity.add(IsDead);
        expect(added).toHaveBeenCalledTimes(1);
        expect(added).toHaveBeenCalledWith(entity, expect.objectContaining({ hp: 3, sp: 5 }));

        entity.set(Health, { hp: 3 });
        expect(changed).toHaveBeenCalledTimes(1);
        changed.mockClear();

        const partial = world.spawn(Health);
        partial.set(Health, { hp: 1 });
        expect(changed).not.toHaveBeenCalled();

        entity.remove(IsDead);
        expect(removed).toHaveBeenCalledTimes(1);
        entity.remove(Health);
        expect(removed).toHaveBeenCalledTimes(1);

        world.query(Combat).updateEach(() => {});
        const watched = world.spawn(Combat({ hp: 1, sp: 1 }));
        changed.mockClear();
        world.query(Combat).updateEach(([combat]) => {
            combat.hp = 4;
            combat.sp = 6;
        });
        expect(watched.get(Health)!.hp).toBe(4);
        expect(watched.get(Shield)!.sp).toBe(6);
        expect(changed).toHaveBeenCalled();
    });
});
