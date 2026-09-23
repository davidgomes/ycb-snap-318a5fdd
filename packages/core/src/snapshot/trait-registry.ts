import { $internal } from '../common';
import type { Relation } from '../relation/types';
import { isRelation } from '../relation/utils/is-relation';
import type { Trait } from '../trait/types';
import type { TraitRegistry } from './types';

type RegistryValue = Trait | Relation<Trait>;

function isTrait(value: unknown): value is Trait {
    if (typeof value !== 'function') return false;
    const internal = (value as { [$internal]?: { type?: unknown } })[$internal];
    return internal?.type === 'tag' || internal?.type === 'soa' || internal?.type === 'aos';
}

/**
 * Build a registry that names the traits and relations a snapshot is allowed to read
 * and write. Keys, trait identities, and relation identities must all be unique.
 */
export function createTraitRegistry(...entries: Array<[string, RegistryValue]>): TraitRegistry {
    const byKey = new Map<string, RegistryValue>();
    const traitKeys = new Map<Trait, string>();
    const relationKeys = new Map<Relation<Trait>, string>();

    for (const entry of entries) {
        if (!Array.isArray(entry) || entry.length !== 2) {
            throw new Error('Koota: Registry entries must be [string, Trait | Relation] tuples.');
        }

        const [key, value] = entry;
        if (typeof key !== 'string') {
            throw new Error('Koota: Registry keys must be strings.');
        }
        if (byKey.has(key)) {
            throw new Error(`Koota: Duplicate registry key "${key}".`);
        }

        if (isRelation(value)) {
            if (relationKeys.has(value)) {
                throw new Error(`Koota: Duplicate relation "${key}".`);
            }
            relationKeys.set(value, key);
        } else if (isTrait(value)) {
            if (traitKeys.has(value)) {
                throw new Error(`Koota: Duplicate trait "${key}".`);
            }
            traitKeys.set(value, key);
        } else {
            throw new Error(`Koota: Registry entry "${key}" is not a trait or relation.`);
        }

        byKey.set(key, value);
    }

    return {
        get(key) {
            return byKey.get(key);
        },
        keyForTrait(trait) {
            return traitKeys.get(trait);
        },
        keyForRelation(relation) {
            return relationKeys.get(relation);
        },
    };
}
