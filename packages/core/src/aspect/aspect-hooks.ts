import type { Entity } from '../entity/types';
import type { Trait } from '../trait/types';
import type { World } from '../world';

export type AspectHook = (world: World, entity: Entity, trait: Trait) => void;

/**
 * Trait add/remove/change notifies these hooks.
 * `aspect.ts` installs them. Keeping the slots here avoids an import cycle.
 */
export const aspectHooks: {
    /** Nonzero while an aspect is distributing constituent change events. */
    depth: number;
    added: AspectHook | null;
    removing: AspectHook | null;
    removed: AspectHook | null;
    changed: AspectHook | null;
} = {
    depth: 0,
    added: null,
    removing: null,
    removed: null,
    changed: null,
};
