import { $internal } from '../common';
import type { Relation } from '../relation/types';
import { isRelation } from '../relation/utils/is-relation';
import type { Trait } from '../trait/types';
import type { TraitRegistry, TraitRegistryEntry } from './types';

export function createTraitRegistry(...entries: [string, TraitRegistryEntry][]): TraitRegistry {
    const registryEntries = new Map<string, TraitRegistryEntry>();
    const traitKeys = new Map<Trait, string>();
    const relationKeys = new Map<Relation<Trait>, string>();
    const seenKeys = new Set<string>();

    for (const [key, entry] of entries) {
        if (seenKeys.has(key)) {
            throw new Error(`Koota: Duplicate registry key "${key}".`);
        }
        seenKeys.add(key);

        if (isRelation(entry)) {
            if (relationKeys.has(entry)) {
                throw new Error('Koota: Duplicate relation in trait registry.');
            }

            const relationTrait = entry[$internal].trait;
            if (traitKeys.has(relationTrait)) {
                throw new Error('Koota: Duplicate trait in trait registry.');
            }

            relationKeys.set(entry, key);
            traitKeys.set(relationTrait, key);
        } else {
            if (traitKeys.has(entry)) {
                throw new Error('Koota: Duplicate trait in trait registry.');
            }

            if (entry[$internal].relation && relationKeys.has(entry[$internal].relation)) {
                throw new Error('Koota: Duplicate relation in trait registry.');
            }

            traitKeys.set(entry, key);
        }

        registryEntries.set(key, entry);
    }

    return {
        entries: registryEntries,
        traitKeys,
        relationKeys,
    };
}
