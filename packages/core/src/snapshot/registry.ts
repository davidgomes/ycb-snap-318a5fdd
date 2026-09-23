import { $internal } from '../common';
import type { Relation } from '../relation/types';
import { isRelation } from '../relation/utils/is-relation';
import type { Trait } from '../trait/types';
import type { TraitRegistry, TraitRegistryEntry } from './types';

function isTrait(value: unknown): value is Trait {
    return typeof value === 'function' && $internal in value && 'schema' in value;
}

export function createTraitRegistry(...entries: TraitRegistryEntry[]): TraitRegistry {
    const byKey = new Map<string, Trait | Relation<Trait>>();
    const traitKeys = new Map<Trait, string>();
    const relationKeys = new Map<Relation<Trait>, string>();

    for (const entry of entries) {
        if (!Array.isArray(entry) || entry.length !== 2) {
            throw new Error('Koota: Trait registry entries must be [key, trait] tuples.');
        }

        const [key, value] = entry;

        if (typeof key !== 'string') {
            throw new Error('Koota: Trait registry keys must be strings.');
        }
        if (byKey.has(key)) {
            throw new Error(`Koota: Duplicate trait registry key "${key}".`);
        }

        if (isRelation(value)) {
            if (relationKeys.has(value)) {
                throw new Error(
                    `Koota: Relation registered as "${key}" is already registered as "${relationKeys.get(value)}".`
                );
            }
            relationKeys.set(value, key);
        } else if (isTrait(value)) {
            if (traitKeys.has(value)) {
                throw new Error(
                    `Koota: Trait registered as "${key}" is already registered as "${traitKeys.get(value)}".`
                );
            }
            traitKeys.set(value, key);
        } else {
            throw new Error(`Koota: Trait registry entry "${key}" is not a trait or relation.`);
        }

        byKey.set(key, value);
    }

    return {
        entries: [...byKey],
        byKey,
        traitKeys,
        relationKeys,
    };
}
