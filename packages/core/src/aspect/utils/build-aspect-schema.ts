import { $internal } from '../../common';
import type { Trait } from '../../trait/types';

function getTraitFieldKeys(trait: Trait): string[] {
    const traitCtx = trait[$internal];

    if (traitCtx.type === 'tag') return [];

    if (traitCtx.type === 'aos') {
        const defaults =
            typeof trait.schema === 'function' ? (trait.schema() as Record<string, unknown>) : null;
        return defaults && typeof defaults === 'object' ? Object.keys(defaults) : [];
    }

    return Object.keys(trait.schema);
}

function getTraitFieldDefault(trait: Trait, key: string): unknown {
    const traitCtx = trait[$internal];

    if (traitCtx.type === 'aos') {
        const defaults =
            typeof trait.schema === 'function' ? (trait.schema() as Record<string, unknown>) : null;
        return defaults?.[key];
    }

    const schema = trait.schema as Record<string, unknown>;
    const value = schema[key];
    return typeof value === 'function' ? (value as () => unknown)() : value;
}

export function buildAspectSchema(traits: Trait[]) {
    const fieldMap = new Map<string, Trait>();
    const schema: Record<string, unknown> = {};

    for (let i = 0; i < traits.length; i++) {
        const trait = traits[i];
        const keys = getTraitFieldKeys(trait);

        for (let j = 0; j < keys.length; j++) {
            const key = keys[j];

            if (fieldMap.has(key)) {
                throw new Error(`Koota: overlapping field name "${key}" in aspect constituents.`);
            }

            fieldMap.set(key, trait);
            schema[key] = getTraitFieldDefault(trait, key);
        }
    }

    const dataTraits = traits.filter((trait) => trait[$internal].type !== 'tag');

    return {
        fieldMap,
        schema: Object.freeze(schema),
        dataTraits: Object.freeze(dataTraits),
    };
}
