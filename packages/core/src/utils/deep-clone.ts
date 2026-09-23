/**
 * Deep clones plain objects, arrays, Maps, Sets, Dates, RegExps and binary data.
 * Class instances keep their prototype and functions are copied by reference.
 */
export function deepClone<T>(value: T, seen = new Map<object, unknown>()): T {
    if (typeof value !== 'object' || value === null) return value;
    if (seen.has(value)) return seen.get(value) as T;

    if (value instanceof Date) return new Date(value.getTime()) as T;
    if (value instanceof RegExp) return new RegExp(value.source, value.flags) as T;
    if (value instanceof ArrayBuffer) return value.slice(0) as T;
    if (value instanceof DataView) {
        const end = value.byteOffset + value.byteLength;
        return new DataView(value.buffer.slice(value.byteOffset, end)) as T;
    }
    if (ArrayBuffer.isView(value)) return (value as unknown as Uint8Array).slice() as T;

    if (Array.isArray(value)) {
        const copy: unknown[] = [];
        seen.set(value, copy);
        for (let i = 0; i < value.length; i++) copy[i] = deepClone(value[i], seen);
        return copy as T;
    }

    if (value instanceof Map) {
        const copy = new Map();
        seen.set(value, copy);
        for (const [key, entry] of value) copy.set(deepClone(key, seen), deepClone(entry, seen));
        return copy as T;
    }

    if (value instanceof Set) {
        const copy = new Set();
        seen.set(value, copy);
        for (const entry of value) copy.add(deepClone(entry, seen));
        return copy as T;
    }

    const copy = Object.create(Object.getPrototypeOf(value)) as Record<string, unknown>;
    seen.set(value, copy);
    for (const key of Object.keys(value)) {
        copy[key] = deepClone((value as Record<string, unknown>)[key], seen);
    }
    return copy as T;
}
