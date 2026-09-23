import { $internal } from '../common';
import type { Relation } from '../relation/types';
import { isRelation } from '../relation/utils/is-relation';
import type { Trait } from '../trait/types';
import type { TraitRegistry, TraitRegistryEntry } from './types';

function isTrait(value: unknown): value is Trait {
    return typeof (value as Trait | undefined)?.[$internal]?.createStore === 'function';
}

/**
 * Creates a registry that maps stable string keys to traits and relations so that
 * snapshots can be stored, serialized and rolled back.
 */
export function createTraitRegistry(...entries: TraitRegistryEntry[]): TraitRegistry {
    const byKey = new Map<string, Trait | Relation<Trait>>();
    const byTrait = new Map<Trait | Relation<Trait>, string>();

    for (const entry of entries) {
        if (!Array.isArray(entry) || typeof entry[0] !== 'string') {
            throw new Error('Koota: Trait registry entries must be [key, trait] tuples.');
        }

        const [key, value] = entry;
        const kind = isRelation(value) ? 'relation' : isTrait(value) ? 'trait' : undefined;

        if (!kind) throw new Error(`Koota: Trait registry key "${key}" is not a trait or relation.`);
        if (byKey.has(key)) throw new Error(`Koota: Duplicate trait registry key "${key}".`);
        if (byTrait.has(value)) {
            throw new Error(
                `Koota: Duplicate ${kind} in trait registry: "${key}" is already registered as "${byTrait.get(value)}".`
            );
        }

        byKey.set(key, value);
        byTrait.set(value, key);
    }

    return { [$internal]: { entries: byKey, keys: byTrait } };
}
