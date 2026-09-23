export function deepClone<T>(value: T, seen: Map<object, unknown> = new Map()): T {
    if (value === null || typeof value !== 'object') return value;

    const source = value as unknown as object;
    if (seen.has(source)) return seen.get(source) as T;

    if (Array.isArray(source)) {
        const copy: unknown[] = [];
        seen.set(source, copy);
        for (let i = 0; i < source.length; i++) copy.push(deepClone(source[i], seen));
        return copy as T;
    }

    if (source instanceof Date) return new Date(source.getTime()) as T;

    if (source instanceof Map) {
        const copy = new Map();
        seen.set(source, copy);
        for (const [k, v] of source) copy.set(deepClone(k, seen), deepClone(v, seen));
        return copy as T;
    }

    if (source instanceof Set) {
        const copy = new Set();
        seen.set(source, copy);
        for (const v of source) copy.add(deepClone(v, seen));
        return copy as T;
    }

    if (source instanceof DataView) {
        return new DataView(
            source.buffer.slice(source.byteOffset, source.byteOffset + source.byteLength)
        ) as T;
    }

    if (ArrayBuffer.isView(source)) {
        return (source as unknown as { slice(): T }).slice();
    }

    if (source instanceof ArrayBuffer) return source.slice(0) as T;

    const copy = Object.create(Object.getPrototypeOf(source)) as Record<string, unknown>;
    seen.set(source, copy);
    for (const key of Object.keys(source)) {
        copy[key] = deepClone((source as Record<string, unknown>)[key], seen);
    }
    return copy as T;
}
