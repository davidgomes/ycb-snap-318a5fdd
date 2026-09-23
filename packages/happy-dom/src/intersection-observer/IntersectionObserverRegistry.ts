/**
 * Notifies active intersection observers when the DOM connection of a node changes.
 */
const refreshes = new Set<() => void>();

/**
 * Subscribes an observer refresh. Returns an unsubscribe function.
 *
 * @param refresh Refresh callback.
 * @returns Unsubscribe.
 */
export function subscribeIntersectionObserver(refresh: () => void): () => void {
	refreshes.add(refresh);
	return () => {
		refreshes.delete(refresh);
	};
}

/**
 * Asks active observers to recompute intersections.
 */
export function notifyIntersectionObserverTargets(): void {
	if (refreshes.size === 0) {
		return;
	}

	for (const refresh of [...refreshes]) {
		refresh();
	}
}
