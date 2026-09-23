import type { Aspect } from './types';

export const $aspect = Symbol.for('koota.aspect');

/** Hash offset so aspect ids never collide with trait or modifier hashes. */
export const ASPECT_HASH_BIAS = 1e15;

export function isAspect(value: unknown): value is Aspect {
    return (
        typeof value === 'function' &&
        (value as { [$aspect]?: boolean } | null | undefined)?.[$aspect] === true
    );
}
