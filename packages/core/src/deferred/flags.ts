let suppressDepth = 0;

export function areSubscriptionsSuppressed(): boolean {
    return suppressDepth > 0;
}

export function pushSubscriptionSuppress(): void {
    suppressDepth++;
}

export function popSubscriptionSuppress(): void {
    suppressDepth--;
}
