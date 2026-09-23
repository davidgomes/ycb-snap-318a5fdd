import type { Brand } from '../common';
import { $aspect } from './symbols';
import type { Aspect } from './types';

export function isAspect(value: unknown): value is Aspect {
    return (value as Brand<typeof $aspect> | null | undefined)?.[$aspect] === true;
}
