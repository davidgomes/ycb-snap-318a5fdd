/**
 * Deep-copy a snapshot value so later mutations of the world or the snapshot
 * cannot alias each other. Prototype methods are preserved; functions are copied
 * by reference because they are behavior, not state.
 */
export function deepCopy<T>(value: T, seen = new WeakMap<object, unknown>()): T {
    if (typeof value !== 'object' || value === null) return value;

    const cached = seen.get(value);
    if (cached !== undefined) return cached as T;

    if (value instanceof Date) {
        const copy = new Date(value.getTime());
        seen.set(value, copy);
        return copy as T;
    }

    if (value instanceof Map) {
        const copy = new Map();
        seen.set(value, copy);
        for (const [key, entry] of value) {
            copy.set(deepCopy(key, seen), deepCopy(entry, seen));
        }
        return copy as T;
    }

    if (value instanceof Set) {
        const copy = new Set();
        seen.set(value, copy);
        for (const entry of value) copy.add(deepCopy(entry, seen));
        return copy as T;
    }

    if (Array.isArray(value)) {
        const copy: unknown[] = [];
        seen.set(value, copy);
        for (let i = 0; i < value.length; i++) copy[i] = deepCopy(value[i], seen);
        return copy as T;
    }

    const copy: Record<PropertyKey, unknown> = Object.create(Object.getPrototypeOf(value));
    seen.set(value, copy);

    for (const key of Reflect.ownKeys(value)) {
        const descriptor = Object.getOwnPropertyDescriptor(value, key);
        if (!descriptor) continue;
        if ('value' in descriptor) {
            Object.defineProperty(copy, key, {
                configurable: descriptor.configurable,
                enumerable: descriptor.enumerable,
                writable: descriptor.writable,
                value: deepCopy(descriptor.value, seen),
            });
        } else {
            Object.defineProperty(copy, key, descriptor);
        }
    }

    return copy as T;
}
