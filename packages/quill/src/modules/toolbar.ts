import Delta from 'quill-delta';
import { EmbedBlot, Scope } from 'parchment';
import Quill from '../core/quill.js';
import logger from '../core/logger.js';
import Module from '../core/module.js';
import type { Range } from '../core/selection.js';

const debug = logger('quill:toolbar');

type Handler = (this: Toolbar, value: any) => void;

export type ToolbarConfig = Array<
  string[] | Array<string | Record<string, unknown>>
>;
export interface ToolbarProps {
  container?: HTMLElement | ToolbarConfig | null;
  handlers?: Record<string, Handler>;
  option?: number;
  module?: boolean;
  theme?: boolean;
}

const groups = new WeakMap<HTMLElement, ToolbarGroup>();

class Toolbar extends Module<ToolbarProps> {
  static DEFAULTS: ToolbarProps;
  static events = {
    UPDATE: 'toolbar-update',
  } as const;

  container?: HTMLElement | null;
  controls: [string, HTMLElement][];
  handlers: Record<string, Handler>;
  group?: ToolbarGroup;

  constructor(quill: Quill, options: Partial<ToolbarProps>) {
    super(quill, options);
    if (Array.isArray(this.options.container)) {
      const container = document.createElement('div');
      container.setAttribute('role', 'toolbar');
      addControls(container, this.options.container);
      quill.container?.parentNode?.insertBefore(container, quill.container);
      this.container = container;
    } else if (typeof this.options.container === 'string') {
      this.container = document.querySelector(this.options.container);
    } else {
      this.container = this.options.container;
    }
    if (!(this.container instanceof HTMLElement)) {
      debug.error('Container required for toolbar', this.options);
      return;
    }
    this.container.classList.add('ql-toolbar');
    this.controls = [];
    this.handlers = {};
    const group = ToolbarGroup.get(this.container);
    this.group = group;
    if (this.options.handlers) {
      Object.keys(this.options.handlers).forEach((format) => {
        const handler = this.options.handlers?.[format];
        if (handler) {
          this.addHandler(format, handler);
        }
      });
    }
    Array.from(this.container.querySelectorAll('button, select')).forEach(
      (input) => {
        // @ts-expect-error
        this.attach(input);
      },
    );
    group.add(this);
    this.quill.on(Quill.events.EDITOR_CHANGE, (type, eventRange) => {
      if (type === Quill.events.SELECTION_CHANGE && eventRange != null) {
        group.activate(this);
      }
      if (!this.isActive()) return;
      const [range] = this.quill.selection.getRange(); // quill.getSelection triggers update
      this.update(range);
    });
    this.quill.on(Quill.events.ENABLE_CHANGE, () => {
      if (this.isActive()) group.refresh();
    });
    this.quill.root.addEventListener('focusin', () => {
      group.activate(this);
    });
  }

  addHandler(format: string, handler: Handler) {
    this.handlers[format] = handler;
  }

  attach(input: HTMLElement) {
    const format = getControlFormat(input);
    if (!format) return;
    if (input.tagName === 'BUTTON') {
      input.setAttribute('type', 'button');
    }
    if (
      this.handlers[format] == null &&
      this.quill.scroll.query(format) == null
    ) {
      debug.warn('ignoring attaching to nonexistent format', format, input);
      return;
    }
    if (this.controls.some(([, control]) => control === input)) return;
    this.controls.push([format, input]);
    this.group?.bind(input);
  }

  handle(input: HTMLElement, format: string, e: Event) {
    if (
      this.handlers[format] == null &&
      this.quill.scroll.query(format) == null
    ) {
      return;
    }
    let value;
    if (input.tagName === 'SELECT') {
      // @ts-expect-error
      if (input.selectedIndex < 0) return;
      // @ts-expect-error
      const selected = input.options[input.selectedIndex];
      if (selected.hasAttribute('selected')) {
        value = false;
      } else {
        value = selected.value || false;
      }
    } else {
      if (input.classList.contains('ql-active')) {
        value = false;
      } else {
        // @ts-expect-error
        value = input.value || !input.hasAttribute('value');
      }
      e.preventDefault();
    }
    this.quill.focus();
    const [range] = this.quill.selection.getRange();
    if (this.handlers[format] != null) {
      this.handlers[format].call(this, value);
    } else if (
      // @ts-expect-error
      this.quill.scroll.query(format).prototype instanceof EmbedBlot
    ) {
      value = prompt(`Enter ${format}`); // eslint-disable-line no-alert
      if (!value) return;
      this.quill.updateContents(
        new Delta()
          // @ts-expect-error Fix me later
          .retain(range.index)
          // @ts-expect-error Fix me later
          .delete(range.length)
          .insert({ [format]: value }),
        Quill.sources.USER,
      );
    } else {
      this.quill.format(format, value, Quill.sources.USER);
    }
    this.update(range);
  }

  isActive() {
    return this.group != null && this.group.active === this;
  }

  update(range: Range | null) {
    if (range != null && this.group != null) {
      // Shared controls only reflect the active editor, and stay neutral while it is disabled
      if (this.group.active !== this) return;
      if (this.group.isDisabled()) range = null;
    }
    const formats = range == null ? {} : this.quill.getFormat(range);
    this.controls.forEach((pair) => {
      const [format, input] = pair;
      if (input.tagName === 'SELECT') {
        let option: HTMLOptionElement | null = null;
        if (range == null) {
          option = null;
        } else if (formats[format] == null) {
          option = input.querySelector('option[selected]');
        } else if (!Array.isArray(formats[format])) {
          let value = formats[format];
          if (typeof value === 'string') {
            value = value.replace(/"/g, '\\"');
          }
          option = input.querySelector(`option[value="${value}"]`);
        }
        if (option == null) {
          // @ts-expect-error TODO fix me later
          input.value = ''; // TODO make configurable?
          // @ts-expect-error TODO fix me later
          input.selectedIndex = -1;
        } else {
          option.selected = true;
        }
      } else if (range == null) {
        input.classList.remove('ql-active');
        input.setAttribute('aria-pressed', 'false');
      } else if (input.hasAttribute('value')) {
        // both being null should match (default values)
        // '1' should match with 1 (headers)
        const value = formats[format] as boolean | number | string | object;
        const isActive =
          value === input.getAttribute('value') ||
          (value != null && value.toString() === input.getAttribute('value')) ||
          (value == null && !input.getAttribute('value'));
        input.classList.toggle('ql-active', isActive);
        input.setAttribute('aria-pressed', isActive.toString());
      } else {
        const isActive = formats[format] != null;
        input.classList.toggle('ql-active', isActive);
        input.setAttribute('aria-pressed', isActive.toString());
      }
    });
  }
}
Toolbar.DEFAULTS = {};

// Owns the DOM wiring of a toolbar container, which may be shared by several
// editors. Actions are routed to whichever editor was most recently active.
class ToolbarGroup {
  static get(container: HTMLElement) {
    let group = groups.get(container);
    if (group == null) {
      group = new ToolbarGroup(container);
      groups.set(container, group);
    }
    return group;
  }

  container: HTMLElement;
  toolbars: Toolbar[] = [];
  active: Toolbar | null = null;
  protected attached = new WeakSet<Element>();
  protected external = new WeakSet<Element>();
  protected disabledControls = new WeakSet<Element>();
  protected handledEvents = new WeakSet<Event>();
  protected removalObserver: MutationObserver | null = null;

  constructor(container: HTMLElement) {
    this.container = container;
    // Capture so non-bubbling change events dispatched by pickers are seen too
    const listener = (event: Event) => {
      const input = this.findControl(event);
      if (input != null) this.dispatch(input, event);
    };
    container.addEventListener('click', listener, true);
    container.addEventListener('change', listener, true);
    new MutationObserver((records) => {
      const inputs: HTMLElement[] = [];
      records.forEach((record) => {
        record.addedNodes.forEach((node) => {
          if (!(node instanceof Element)) return;
          if (node.matches('button, select')) inputs.push(node as HTMLElement);
          inputs.push(...node.querySelectorAll<HTMLElement>('button, select'));
        });
      });
      const changed = this.toolbars.reduce(
        (result, toolbar) => this.syncControls(toolbar, inputs) || result,
        false,
      );
      if (changed) this.refresh();
    }).observe(container, { childList: true, subtree: true });
  }

  add(toolbar: Toolbar) {
    if (this.toolbars.includes(toolbar)) return;
    this.toolbars.push(toolbar);
    if (this.toolbars.length === 1) {
      this.active = toolbar;
      return;
    }
    this.observeRemovals();
    this.refresh();
  }

  activate(toolbar: Toolbar) {
    if (!this.toolbars.includes(toolbar)) {
      // Rejoining after having been detached from the document
      this.syncControls(
        toolbar,
        Array.from(this.container.querySelectorAll('button, select')),
      );
      this.toolbars.push(toolbar);
      if (this.toolbars.length > 1) this.observeRemovals();
    } else if (this.active === toolbar) {
      return;
    }
    const previous = this.active;
    this.active = toolbar;
    this.refresh();
    // Drop the previous editor's stale range now rather than on the next
    // selectionchange, so selecting that same range again reactivates it.
    if (previous != null && this.toolbars.includes(previous)) {
      previous.quill.selection.update(Quill.sources.USER);
    }
  }

  bind(input: HTMLElement) {
    this.attached.add(input);
    if (this.container.contains(input) || this.external.has(input)) return;
    this.external.add(input);
    const eventName = input.tagName === 'SELECT' ? 'change' : 'click';
    input.addEventListener(eventName, (event) => {
      this.dispatch(input, event);
    });
  }

  isDisabled() {
    return (
      this.toolbars.length > 1 &&
      this.active != null &&
      !this.active.quill.isEnabled()
    );
  }

  prune() {
    const removed = this.toolbars.filter(
      ({ quill }) => !quill.container.isConnected,
    );
    if (removed.length === 0) return;
    this.toolbars = this.toolbars.filter(
      (toolbar) => !removed.includes(toolbar),
    );
    if (this.active != null && removed.includes(this.active)) {
      this.active = null;
    }
    if (this.toolbars.length === 0 && this.removalObserver != null) {
      this.removalObserver.disconnect();
      this.removalObserver = null;
    }
    this.refresh(removed);
  }

  refresh(removed: Toolbar[] = []) {
    const { active } = this;
    const disabled = this.isDisabled();
    const toolbars = this.toolbars.concat(removed);
    toolbars.forEach((toolbar) => {
      toolbar.controls.forEach(([, input]) => {
        this.setDisabled(input, disabled);
      });
      if (toolbar !== active || disabled) toolbar.update(null);
    });
    if (active != null && !disabled) {
      active.update(active.quill.selection.getRange()[0]);
    }
    toolbars.forEach(({ quill }) => {
      quill.emitter.emit(Toolbar.events.UPDATE);
    });
  }

  protected dispatch(input: HTMLElement, event: Event) {
    if (this.handledEvents.has(event)) return;
    this.handledEvents.add(event);
    const format = getControlFormat(input);
    const toolbar = this.resolveActive();
    if (!format || toolbar == null || this.isDisabled()) return;
    toolbar.handle(input, format, event);
  }

  protected findControl(event: Event) {
    let node = event.target instanceof Element ? event.target : null;
    while (node != null && node !== this.container) {
      if (node.tagName === 'SELECT') {
        return event.type === 'change' ? (node as HTMLElement) : null;
      }
      if (
        event.type === 'click' &&
        (node.tagName === 'BUTTON' || this.attached.has(node))
      ) {
        return node as HTMLElement;
      }
      node = node.parentElement;
    }
    return null;
  }

  protected observeRemovals() {
    if (this.removalObserver != null) return;
    this.removalObserver = new MutationObserver((records) => {
      if (records.some((record) => record.removedNodes.length > 0)) {
        this.prune();
      }
    });
    this.removalObserver.observe(document, { childList: true, subtree: true });
  }

  // The event based activation can miss an editor whose selection was restored
  // to an unchanged range, so trust where the document selection and focus are.
  protected resolveActive() {
    this.prune();
    const selection = document.getSelection();
    const anchor =
      selection != null && selection.rangeCount > 0
        ? selection.anchorNode
        : null;
    const current =
      this.toolbars.find(
        ({ quill }) => anchor != null && quill.root.contains(anchor),
      ) || this.toolbars.find(({ quill }) => quill.hasFocus());
    if (current != null) this.activate(current);
    return this.active;
  }

  protected setDisabled(input: HTMLElement, disabled: boolean) {
    if (!('disabled' in input)) return;
    const control = input as HTMLButtonElement | HTMLSelectElement;
    if (disabled && !control.disabled) {
      control.disabled = true;
      this.disabledControls.add(control);
    } else if (!disabled && this.disabledControls.has(control)) {
      control.disabled = false;
      this.disabledControls.delete(control);
    }
  }

  protected syncControls(toolbar: Toolbar, inputs: HTMLElement[]) {
    const { length } = toolbar.controls;
    for (let i = toolbar.controls.length - 1; i >= 0; i -= 1) {
      const [, input] = toolbar.controls[i];
      if (!this.external.has(input) && !this.container.contains(input)) {
        toolbar.controls.splice(i, 1);
      }
    }
    let changed = toolbar.controls.length !== length;
    inputs.forEach((input) => {
      if (!this.container.contains(input)) return;
      const count = toolbar.controls.length;
      toolbar.attach(input);
      changed = changed || toolbar.controls.length !== count;
    });
    return changed;
  }
}

function getControlFormat(input: Element) {
  const format = Array.from(input.classList).find((className) => {
    return className.indexOf('ql-') === 0;
  });
  return format == null ? null : format.slice('ql-'.length);
}

function addButton(container: HTMLElement, format: string, value?: string) {
  const input = document.createElement('button');
  input.setAttribute('type', 'button');
  input.classList.add(`ql-${format}`);
  input.setAttribute('aria-pressed', 'false');
  if (value != null) {
    input.value = value;
    input.setAttribute('aria-label', `${format}: ${value}`);
  } else {
    input.setAttribute('aria-label', format);
  }
  container.appendChild(input);
}

function addControls(
  container: HTMLElement,
  groups:
    | (string | Record<string, unknown>)[][]
    | (string | Record<string, unknown>)[],
) {
  if (!Array.isArray(groups[0])) {
    // @ts-expect-error
    groups = [groups];
  }
  groups.forEach((controls: any) => {
    const group = document.createElement('span');
    group.classList.add('ql-formats');
    controls.forEach((control: any) => {
      if (typeof control === 'string') {
        addButton(group, control);
      } else {
        const format = Object.keys(control)[0];
        const value = control[format];
        if (Array.isArray(value)) {
          addSelect(group, format, value);
        } else {
          addButton(group, format, value);
        }
      }
    });
    container.appendChild(group);
  });
}

function addSelect(
  container: HTMLElement,
  format: string,
  values: Array<string | boolean>,
) {
  const input = document.createElement('select');
  input.classList.add(`ql-${format}`);
  values.forEach((value) => {
    const option = document.createElement('option');
    if (value !== false) {
      option.setAttribute('value', String(value));
    } else {
      option.setAttribute('selected', 'selected');
    }
    input.appendChild(option);
  });
  container.appendChild(input);
}

Toolbar.DEFAULTS = {
  container: null,
  handlers: {
    clean() {
      const range = this.quill.getSelection();
      if (range == null) return;
      if (range.length === 0) {
        const formats = this.quill.getFormat();
        Object.keys(formats).forEach((name) => {
          // Clean functionality in existing apps only clean inline formats
          if (this.quill.scroll.query(name, Scope.INLINE) != null) {
            this.quill.format(name, false, Quill.sources.USER);
          }
        });
      } else {
        this.quill.removeFormat(range.index, range.length, Quill.sources.USER);
      }
    },
    direction(value) {
      const { align } = this.quill.getFormat();
      if (value === 'rtl' && align == null) {
        this.quill.format('align', 'right', Quill.sources.USER);
      } else if (!value && align === 'right') {
        this.quill.format('align', false, Quill.sources.USER);
      }
      this.quill.format('direction', value, Quill.sources.USER);
    },
    indent(value) {
      const range = this.quill.getSelection();
      // @ts-expect-error
      const formats = this.quill.getFormat(range);
      // @ts-expect-error
      const indent = parseInt(formats.indent || 0, 10);
      if (value === '+1' || value === '-1') {
        let modifier = value === '+1' ? 1 : -1;
        if (formats.direction === 'rtl') modifier *= -1;
        this.quill.format('indent', indent + modifier, Quill.sources.USER);
      }
    },
    link(value) {
      if (value === true) {
        value = prompt('Enter link URL:'); // eslint-disable-line no-alert
      }
      this.quill.format('link', value, Quill.sources.USER);
    },
    list(value) {
      const range = this.quill.getSelection();
      // @ts-expect-error
      const formats = this.quill.getFormat(range);
      if (value === 'check') {
        if (formats.list === 'checked' || formats.list === 'unchecked') {
          this.quill.format('list', false, Quill.sources.USER);
        } else {
          this.quill.format('list', 'unchecked', Quill.sources.USER);
        }
      } else {
        this.quill.format('list', value, Quill.sources.USER);
      }
    },
  },
};

export { Toolbar as default, ToolbarGroup, addControls };
