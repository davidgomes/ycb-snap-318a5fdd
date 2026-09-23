import type { Entity } from '../entity/types';

type Subscriber = (entity: Entity, target?: Entity) => void;

export type EventSource = {
    addSubscriptions: Set<Subscriber>;
    removeSubscriptions: Set<Subscriber>;
    changeSubscriptions?: Set<Subscriber>;
};

type LoggedEvent = {
    source: EventSource;
    entity: Entity;
    target: Entity | undefined;
    first: 'add' | 'remove' | null;
    last: 'add' | 'remove' | null;
    changed: boolean;
};

type EventLog = {
    lookup: Map<EventSource, Map<string, LoggedEvent>>;
    order: LoggedEvent[];
};

let activeLog: EventLog | null = null;

function notify(subs: Set<Subscriber>, entity: Entity, target: Entity | undefined) {
    if (target === undefined) for (const sub of subs) sub(entity);
    else for (const sub of subs) sub(entity, target);
}

function record(
    log: EventLog,
    source: EventSource,
    entity: Entity,
    target: Entity | undefined,
    type: 'add' | 'remove' | 'change'
) {
    let events = log.lookup.get(source);
    if (!events) {
        events = new Map();
        log.lookup.set(source, events);
    }

    const key = target === undefined ? `${entity}` : `${entity}:${target}`;
    let event = events.get(key);
    if (!event) {
        event = { source, entity, target, first: null, last: null, changed: false };
        events.set(key, event);
        log.order.push(event);
    }

    if (type === 'change') {
        event.changed = true;
    } else {
        if (event.first === null) event.first = type;
        event.last = type;
    }
}

export function emitAdd(source: EventSource, entity: Entity, target?: Entity) {
    if (source.addSubscriptions.size === 0 && !activeLog) return;
    if (activeLog) record(activeLog, source, entity, target, 'add');
    else notify(source.addSubscriptions, entity, target);
}

export function emitRemove(source: EventSource, entity: Entity, target?: Entity) {
    if (source.removeSubscriptions.size === 0 && !activeLog) return;
    if (activeLog) record(activeLog, source, entity, target, 'remove');
    else notify(source.removeSubscriptions, entity, target);
}

export function emitChange(source: EventSource, entity: Entity, target?: Entity) {
    if (activeLog) record(activeLog, source, entity, target, 'change');
    else notify(source.changeSubscriptions!, entity, target);
}

/**
 * Collects subscription events until the returned function is called, then fires
 * one event per (source, entity, target) based on the net state difference.
 */
export function beginEventLog(): () => void {
    const previous = activeLog;
    const log: EventLog = { lookup: new Map(), order: [] };
    activeLog = log;

    return () => {
        activeLog = previous;

        for (let i = 0; i < log.order.length; i++) {
            const { source, entity, target, first, last, changed } = log.order[i];

            if (first !== null) {
                const before = first === 'remove';
                const after = last === 'add';
                if (!before && after) notify(source.addSubscriptions, entity, target);
                else if (before && !after) notify(source.removeSubscriptions, entity, target);
            }

            if (changed && last !== 'remove' && source.changeSubscriptions) {
                notify(source.changeSubscriptions, entity, target);
            }
        }
    };
}
