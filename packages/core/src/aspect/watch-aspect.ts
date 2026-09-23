import { $internal } from '../common';
import type { Entity } from '../entity/types';
import { hasTrait, registerTrait } from '../trait/trait';
import { getTraitInstance, hasTraitInstance } from '../trait/trait-instance';
import type { Trait } from '../trait/types';
import type { World } from '../world';
import { type Aspect, type AspectSubscriber } from './aspect';

type Watchers = {
    add: AspectSubscriber;
    remove: AspectSubscriber;
    change: AspectSubscriber & { aspectWatcher?: true };
};

const watched = new WeakMap<World, Map<Aspect, Watchers>>();
const resetHooked = new WeakSet<World>();

function entityHasAspect(world: World, entity: Entity, aspect: Aspect): boolean {
    const traits = aspect.traits;
    for (let i = 0; i < traits.length; i++) {
        if (!hasTrait(world, entity, traits[i])) return false;
    }
    return true;
}

function hasExternalChangeSub(subs: Set<(entity: Entity, target?: Entity) => void>): boolean {
    for (const sub of subs) {
        if (!(sub as Watchers['change']).aspectWatcher) return true;
    }
    return false;
}

function otherAspectTracks(world: World, trait: Trait, except: Aspect): boolean {
    const map = watched.get(world);
    if (!map) return false;

    for (const [aspect, _watchers] of map) {
        if (aspect === except) continue;
        if (aspect[$internal].changeSubscriptions.size === 0) continue;
        if (aspect.traits.includes(trait)) return true;
    }

    return false;
}

export function syncAspectChangeTracking(world: World, aspect: Aspect) {
    const ctx = world[$internal];
    const needed = aspect[$internal].changeSubscriptions.size > 0;

    for (let i = 0; i < aspect.traits.length; i++) {
        const trait = aspect.traits[i];
        if (needed) {
            ctx.trackedTraits.add(trait);
            continue;
        }

        if (otherAspectTracks(world, trait, aspect)) continue;

        const instance = getTraitInstance(ctx.traitInstances, trait);
        if (instance && hasExternalChangeSub(instance.changeSubscriptions)) continue;

        ctx.trackedTraits.delete(trait);
    }
}

export function watchAspect(world: World, aspect: Aspect) {
    if (!resetHooked.has(world)) {
        resetHooked.add(world);
        world[$internal].resetSubscriptions.add((resetWorld) => {
            const map = watched.get(resetWorld);
            if (!map) return;
            const aspects = [...map.keys()];
            map.clear();
            for (let i = 0; i < aspects.length; i++) {
                const current = aspects[i];
                const internal = current[$internal];
                if (
                    internal.addSubscriptions.size > 0 ||
                    internal.removeSubscriptions.size > 0 ||
                    internal.changeSubscriptions.size > 0
                ) {
                    watchAspect(resetWorld, current);
                }
            }
        });
    }

    let map = watched.get(world);
    if (!map) {
        map = new Map();
        watched.set(world, map);
    }

    if (!map.has(aspect)) {
        const ctx = world[$internal];

        for (let i = 0; i < aspect.traits.length; i++) {
            const trait = aspect.traits[i];
            if (!hasTraitInstance(ctx.traitInstances, trait)) registerTrait(world, trait);
        }

        const add: AspectSubscriber = (entity) => {
            if (!entityHasAspect(world, entity, aspect)) return;
            for (const sub of aspect[$internal].addSubscriptions) sub(entity);
        };

        // Remove callbacks run before the constituent bit is cleared.
        const remove: AspectSubscriber = (entity) => {
            if (!entityHasAspect(world, entity, aspect)) return;
            for (const sub of aspect[$internal].removeSubscriptions) sub(entity);
        };

        const change = ((entity: Entity) => {
            if (aspect[$internal].changeSubscriptions.size === 0) return;
            if (!entityHasAspect(world, entity, aspect)) return;
            for (const sub of aspect[$internal].changeSubscriptions) sub(entity);
        }) as Watchers['change'];
        change.aspectWatcher = true;

        for (let i = 0; i < aspect.traits.length; i++) {
            const instance = getTraitInstance(ctx.traitInstances, aspect.traits[i])!;
            instance.addSubscriptions.add(add);
            instance.removeSubscriptions.add(remove);
            instance.changeSubscriptions.add(change);
        }

        map.set(aspect, { add, remove, change });
    }

    syncAspectChangeTracking(world, aspect);
}
