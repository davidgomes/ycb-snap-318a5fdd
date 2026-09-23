type ActiveBodyReader = {
	cancel: (reason?: unknown) => Promise<void> | void;
};

const activeReaders = new WeakMap<object, ActiveBodyReader>();

/**
 * Tracks the reader currently consuming a request or response body.
 *
 * @param owner Request or response.
 * @param reader Active reader.
 */
export function trackActiveReader(owner: object, reader: ActiveBodyReader): void {
	activeReaders.set(owner, reader);
}

/**
 * Cancels the reader currently consuming a request or response body.
 *
 * Reader cancellation resolves a pending read instead of rejecting it, so callers still need to
 * observe the aborted flag and reject with AbortError.
 *
 * @param owner Request or response.
 */
export function cancelActiveReader(owner: object): void {
	const reader = activeReaders.get(owner);
	if (!reader) {
		return;
	}

	activeReaders.delete(owner);

	try {
		const result = reader.cancel();
		if (result && typeof (<Promise<void>>result).catch === 'function') {
			(<Promise<void>>result).catch(() => undefined);
		}
	} catch {
		// The reader may already be closed.
	}
}

/**
 * Drops the active reader without cancelling it.
 *
 * @param owner Request or response.
 */
export function releaseActiveReader(owner: object): void {
	activeReaders.delete(owner);
}
