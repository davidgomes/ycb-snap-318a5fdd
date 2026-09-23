import type { Brand } from '../../common';
import { $aspect } from '../symbols';
import type { Aspect } from '../types';

/**
 * Check if a value is an Aspect
 */
export /* @inline @pure */ function isAspect(value: unknown): value is Aspect<any> {
    return (value as Brand<typeof $aspect> | null | undefined)?.[$aspect] as unknown as boolean;
}
