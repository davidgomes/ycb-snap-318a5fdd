import type { Brand } from '../../common';
import { $aspect } from '../symbols';
import type { Aspect } from '../types';

export /* @inline @pure */ function isAspect(value: unknown): value is Aspect<any> {
    return (value as Brand<typeof $aspect> | null | undefined)?.[$aspect] as unknown as boolean;
}
