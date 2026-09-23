import type { Entity } from '../entity/types';
import type { Trait } from '../trait/types';
import type { World } from '../world/types';

export type AspectWatcher = {
    traits: readonly Trait[];
    completeness: Trait;
};

const watchersByTraitId: (AspectWatcher[] | undefined)[] = [];

export function registerAspectWatcher(watcher: AspectWatcher) {
    for (let i = 0; i < watcher.traits.length; i++) {
        const traitId = watcher.traits[i].id;
        let bucket = watchersByTraitId[traitId];
        if (!bucket) {
            bucket = [];
            watchersByTraitId[traitId] = bucket;
        }
        bucket.push(watcher);
    }
}

export function getAspectWatchers(traitId: number): AspectWatcher[] | undefined {
    return watchersByTraitId[traitId];
}

type AspectEventHandler = (world: World, entity: Entity, trait: Trait) => void;

let addedHandler: AspectEventHandler | null = null;
let removingHandler: AspectEventHandler | null = null;
let changedHandler: AspectEventHandler | null = null;

export function setAspectAddedHandler(handler: AspectEventHandler) {
    addedHandler = handler;
}

export function setAspectRemovingHandler(handler: AspectEventHandler) {
    removingHandler = handler;
}

export function setAspectChangedHandler(handler: AspectEventHandler) {
    changedHandler = handler;
}

export function notifyTraitAdded(world: World, entity: Entity, trait: Trait) {
    if (!watchersByTraitId[trait.id]) return;
    addedHandler?.(world, entity, trait);
}

export function notifyTraitRemoving(world: World, entity: Entity, trait: Trait) {
    if (!watchersByTraitId[trait.id]) return;
    removingHandler?.(world, entity, trait);
}

export function notifyTraitChanged(world: World, entity: Entity, trait: Trait) {
    if (!watchersByTraitId[trait.id]) return;
    changedHandler?.(world, entity, trait);
}
