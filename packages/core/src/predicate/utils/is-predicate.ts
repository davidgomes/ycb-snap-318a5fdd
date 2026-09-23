import type { Brand } from '../../common';
import type { Predicate } from '../types';
import { $predicate } from '../symbols';

/**
 * Check if a value is a Predicate
 */
export /* @inline @pure */ function isPredicate(value: unknown): value is Predicate {
    return (value as Brand<typeof $predicate> | null | undefined)?.[$predicate] as unknown as boolean;
}
