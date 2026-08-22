import { Brand } from '../../common';
import type { Aspect } from '../types';
import { $aspect } from '../symbols';

export /* @inline @pure */ function isAspect(value: unknown): value is Aspect {
    return (value as Brand<typeof $aspect> | null | undefined)?.[$aspect] as unknown as boolean;
}
