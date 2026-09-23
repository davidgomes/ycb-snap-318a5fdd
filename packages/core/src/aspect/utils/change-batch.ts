import type { Entity } from '../../entity/types';

type AspectChangeCallback = (entity: Entity) => void;

let batching = false;
let pending: Map<Entity, Set<AspectChangeCallback>> | null = null;

/**
 * Notify an aspect change subscriber. While a batch is open, notifications are
 * deduplicated per entity so a single write touching several constituents fires once.
 */
export function notifyAspectChange(entity: Entity, callback: AspectChangeCallback) {
    if (!batching) return callback(entity);

    if (!pending) pending = new Map();
    let callbacks = pending.get(entity);
    if (!callbacks) {
        callbacks = new Set();
        pending.set(entity, callbacks);
    }
    callbacks.add(callback);
}

export function batchAspectChanges(fn: () => void) {
    if (batching) return fn();

    let batch: Map<Entity, Set<AspectChangeCallback>> | null;
    batching = true;
    try {
        fn();
    } finally {
        batching = false;
        batch = pending;
        pending = null;
    }

    if (!batch) return;
    for (const [entity, callbacks] of batch) {
        for (const callback of callbacks) callback(entity);
    }
}
